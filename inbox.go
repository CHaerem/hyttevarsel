package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	_ "github.com/emersion/go-message/charset" // ISO-8859-1 o.l. fra eldre klienter
	"github.com/emersion/go-message/mail"
)

// Incoming er det vi trenger fra en innkommende e-post.
type Incoming struct {
	From      string // adressen alene
	Subject   string
	Text      string
	MessageID string
	Verified  bool // Gmail har bekreftet avsenderen (DMARC eller DKIM)
	WatchID   int  // fra «+h<id>» i mottakeradressen (Mailer.watchAddr), ellers 0
}

// Inbox henter uleste e-poster over IMAP og merker dem lest når de er
// behandlet.
type Inbox struct {
	Addr, User, Password string // imap.gmail.com:993
	AuthServ             string // den som skriver Authentication-Results, f.eks. mx.google.com
}

func (in *Inbox) Fetch(ctx context.Context, handle func(Incoming)) error {
	c, err := imapclient.DialTLS(in.Addr, &imapclient.Options{Dialer: &net.Dialer{Timeout: 30 * time.Second}})
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.Login(in.User, in.Password).Wait(); err != nil {
		return fmt.Errorf("IMAP-innlogging: %w", err)
	}
	if _, err := c.Select("INBOX", nil).Wait(); err != nil {
		return err
	}
	res, err := c.UIDSearch(&imap.SearchCriteria{NotFlag: []imap.Flag{imap.FlagSeen}}, nil).Wait()
	if err != nil {
		return err
	}
	uids := res.AllUIDs()
	if len(uids) == 0 {
		return c.Logout().Wait()
	}
	section := &imap.FetchItemBodySection{Peek: true}
	msgs, err := c.Fetch(imap.UIDSetNum(uids...), &imap.FetchOptions{UID: true, BodySection: []*imap.FetchItemBodySection{section}}).Collect()
	if err != nil {
		return err
	}
	for _, m := range msgs {
		if ctx.Err() != nil {
			break
		}
		raw := m.FindBodySection(section)
		msg, err := ParseIncoming(raw, in.AuthServ)
		if err != nil {
			slog.Warn("kunne ikke lese e-post", "uid", m.UID, "err", err)
		} else {
			handle(msg)
		}
		// Merk lest også ved feil, så samme e-post ikke behandles hver runde.
		store := &imap.StoreFlags{Op: imap.StoreFlagsAdd, Flags: []imap.Flag{imap.FlagSeen}, Silent: true}
		if err := c.Store(imap.UIDSetNum(m.UID), store, nil).Close(); err != nil {
			return err
		}
	}
	return c.Logout().Wait()
}

var (
	dmarcPass = regexp.MustCompile(`(?i)\bdmarc=pass\b`)
	plusTag   = regexp.MustCompile(`\+h(\d+)@`)
	dkimPass  = regexp.MustCompile(`(?i)\bdkim=pass\b[^;]*header\.(?:i|d)=@?([^\s;]+)`)
)

// ParseIncoming leser en rå e-post. Verified krever at den øverste
// Authentication-Results fra vår egen e-posttjener sier dmarc=pass, eller
// dkim=pass for avsenderens domene. Uten det kan hvem som helst skrive
// Kristines adresse i From.
func ParseIncoming(raw []byte, authServ string) (Incoming, error) {
	mr, err := mail.CreateReader(bytes.NewReader(raw))
	if err != nil && mr == nil {
		return Incoming{}, err
	}
	var in Incoming
	h := mr.Header
	if from, err := h.AddressList("From"); err == nil && len(from) > 0 {
		in.From = strings.ToLower(from[0].Address)
	}
	in.Subject, _ = h.Subject()
	in.MessageID = h.Get("Message-Id")
	for _, k := range []string{"To", "Cc", "Delivered-To", "X-Original-To"} {
		if m := plusTag.FindStringSubmatch(strings.Join(h.Values(k), " ")); m != nil && in.WatchID == 0 {
			in.WatchID, _ = strconv.Atoi(m[1])
		}
	}
	for _, ar := range h.Values("Authentication-Results") {
		if !strings.HasPrefix(strings.TrimSpace(strings.ToLower(ar)), strings.ToLower(authServ)) {
			continue
		}
		domain := in.From[strings.LastIndex(in.From, "@")+1:]
		if dmarcPass.MatchString(ar) {
			in.Verified = true
		}
		for _, m := range dkimPass.FindAllStringSubmatch(ar, -1) {
			if d := strings.ToLower(m[1]); d == domain || strings.HasSuffix(d, "@"+domain) {
				in.Verified = true
			}
		}
		break // bare den øverste, lagt til av vår egen tjener
	}

	var plain, htmlText string
	for {
		p, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			break
		}
		if _, ok := p.Header.(*mail.InlineHeader); !ok {
			continue
		}
		ct, _, _ := mime.ParseMediaType(p.Header.Get("Content-Type"))
		b, _ := io.ReadAll(io.LimitReader(p.Body, 256<<10))
		switch {
		case ct == "text/plain" && plain == "":
			plain = string(b)
		case ct == "text/html" && htmlText == "":
			htmlText = HTMLToText(string(b))
		}
	}
	in.Text = plain
	if strings.TrimSpace(plain) == "" || (!linkPattern.MatchString(plain) && linkPattern.MatchString(htmlText)) {
		// Noen klienter legger den delte lenken bare i HTML-en.
		in.Text = htmlText
	}
	return in, nil
}

// MailHandler gjør om innkommende e-post til handlinger.
type MailHandler struct {
	Store   *Store
	Poller  *Poller
	Mailer  *Mailer
	Allowed []string // avsendere som kan styre tjenesten
	// Domenene ordrebekreftelsene kommer fra. De kan bare stoppe en
	// overvåking, aldri legge inn eller styre noe annet.
	BookingDomains []string
}

var dntSenderLine = regexp.MustCompile(`(?i)@(dnt\.no|visbook\.com)\b`)

func domainIn(addr string, domains []string) bool {
	d := strings.ToLower(addr[strings.LastIndex(addr, "@")+1:])
	for _, x := range domains {
		x = strings.ToLower(strings.TrimSpace(x))
		if d == x || strings.HasSuffix(d, "."+x) {
			return true
		}
	}
	return false
}

func (h *MailHandler) Handle(ctx context.Context, in Incoming) {
	log := slog.With("from", in.From, "subject", in.Subject)
	fromDNT := domainIn(in.From, h.BookingDomains)
	if !containsFold(h.Allowed, in.From) && !fromDNT {
		log.Info("ignorerer e-post fra ukjent avsender")
		return
	}
	if !in.Verified {
		log.Warn("ignorerer e-post uten bekreftet avsender (DMARC/DKIM)")
		return
	}
	now := time.Now()

	// En ordrebekreftelse: fra DNT selv (et filter hos henne sender den hit),
	// eller videresendt for hånd, med DNTs avsenderlinje i teksten.
	forwardedDNT := dntSenderLine.MatchString(in.Text)
	if fromDNT || forwardedDNT {
		if booked := MatchBooking(h.Store.List(), in.Subject, in.Text, now); len(booked) > 0 {
			for _, w := range booked {
				h.Store.Delete(w.ID)
				log.Info("stoppet etter ordrebekreftelse", "watch", w.ID, "title", w.Title())
			}
			if err := h.Mailer.Booked(ctx, booked); err != nil {
				log.Error("melding om booking feilet", "err", err)
			}
			return
		}
		// Annen e-post fra DNT (nyhetsbrev, kvitteringer) skal aldri gi svar
		// eller bli tolket som en hyttelenke.
		log.Info("e-post fra DNT uten treff på en overvåking")
		return
	}

	req, err := ParseMessage(in.Subject, in.Text, in.WatchID, now)
	reply := func(lines ...string) {
		if err := h.Mailer.Reply(ctx, in.From, in.Subject, in.MessageID, lines...); err != nil {
			log.Error("svar feilet", "err", err)
		}
	}
	switch {
	case errors.Is(err, ErrIgnore):
		return
	case errors.Is(err, ErrNoDate):
		h.askDate(ctx, in, req.Watch.Legs)
		return
	case errors.Is(err, ErrHelp):
		if err := h.Mailer.Help(ctx, in.From, in.Subject, in.MessageID); err != nil {
			log.Error("svar feilet", "err", err)
		}
		return
	case err != nil:
		reply("Hytta ble ikke lagt inn: " + err.Error() + ".")
		return
	}

	switch req.Kind {
	case ReqStop:
		h.stop(ctx, in, req, now)
	case ReqStopAll:
		n := 0
		for _, w := range h.Store.List() {
			h.Store.Delete(w.ID)
			n++
		}
		log.Info("stoppet alle", "count", n)
		reply(fmt.Sprintf("Hyttevarsel følger ikke lenger med på noe (%d stoppet).", n))
	case ReqList:
		if err := h.Mailer.Summary(ctx, []string{in.From}, h.Store.List(), in.MessageID, replySubject(in.Subject)); err != nil {
			log.Error("oversikt feilet", "err", err)
		}
	case ReqAdd:
		w := req.Watch
		w.Requester = in.From
		w, err := AddWatch(ctx, h.Store, h.Poller, w)
		if err != nil {
			reply("Hytta ble ikke lagt inn: " + err.Error() + ".")
			return
		}
		log.Info("ny overvåking fra e-post", "watch", w.ID, "title", w.Title())
		Announce(ctx, h.Store, h.Poller, h.Mailer, w, in.MessageID)
	}
}

// stop finner hvilken overvåking «stopp» gjelder: den svaret kom til, den
// hun nevner ved navn, eller den eneste hun har. Ellers spør vi.
func (h *MailHandler) stop(ctx context.Context, in Incoming, req Request, now time.Time) {
	log := slog.With("from", in.From, "subject", in.Subject)
	reply := func(lines ...string) {
		if err := h.Mailer.Reply(ctx, in.From, in.Subject, in.MessageID, lines...); err != nil {
			log.Error("svar feilet", "err", err)
		}
	}
	var target *Watch
	if req.WatchID > 0 {
		w, ok := h.Store.Get(req.WatchID)
		if !ok {
			reply("Den er allerede stoppet.")
			return
		}
		target = &w
	} else {
		var active, named []Watch
		text := normalize(req.Text)
		for _, w := range h.Store.List() {
			if w.Expired(now) {
				continue
			}
			active = append(active, w)
			for _, l := range w.Legs {
				if l.CabinName != "" && containsAny(text, nameForms(l.CabinName)) {
					named = append(named, w)
					break
				}
			}
		}
		switch {
		case len(active) == 0:
			reply("Du følger ikke med på noen hytter nå.")
			return
		case len(named) == 1:
			target = &named[0]
		case len(active) == 1:
			target = &active[0]
		default:
			if err := h.Mailer.WhichToStop(ctx, in.From, active, in.Subject, in.MessageID); err != nil {
				log.Error("svar feilet", "err", err)
			}
			return
		}
	}
	h.Store.Delete(target.ID)
	log.Info("stoppet", "watch", target.ID, "title", target.Title())
	if err := h.Mailer.Stopped(ctx, in.From, *target, in.Subject, in.MessageID); err != nil {
		log.Error("svar feilet", "err", err)
	}
}

// Announce sender den første e-posten for en ny overvåking. Få datoer
// sjekkes først, så hun får ett svar: enten «følger med» eller «ledig nå».
// Mange datoer bekreftes med en gang og sjekkes i bakgrunnen.
func Announce(ctx context.Context, store *Store, p *Poller, m *Mailer, w Watch, inReplyTo string) {
	const quick = 6 // kall mot DNT vi lar henne vente på
	if len(w.Starts)*len(w.Legs) > quick {
		if err := m.Confirm(ctx, w, inReplyTo, true); err != nil {
			slog.Error("bekreftelse feilet", "watch", w.ID, "err", err)
		}
		go func() {
			bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Minute)
			defer cancel()
			p.CheckWatch(bg, w.ID)
			p.Evaluate(bg, w.ID)
		}()
		return
	}
	p.CheckWatch(ctx, w.ID)
	w, ok := store.Get(w.ID)
	if !ok {
		return
	}
	var free []string
	for _, s := range w.ActiveStarts(time.Now()) {
		if w.OptionFree(s) {
			free = append(free, s)
		}
	}
	if len(free) == 0 {
		if err := m.Confirm(ctx, w, inReplyTo, false); err != nil {
			slog.Error("bekreftelse feilet", "watch", w.ID, "err", err)
		}
		return
	}
	if err := m.AvailableNow(ctx, w, free, inReplyTo); err != nil {
		// Ikke merket som varslet, så neste runde prøver igjen.
		slog.Error("varsel feilet", "watch", w.ID, "err", err)
		return
	}
	p.MarkNotified(w.ID, free)
}

// AddWatch slår opp navnene og lagrer. Announce sjekker og svarer. Felles
// for nettsiden og e-post.
func AddWatch(ctx context.Context, store *Store, p *Poller, w Watch) (Watch, error) {
	now := time.Now()
	if err := w.Init(now); err != nil {
		return Watch{}, err
	}
	for i, l := range w.Legs {
		from, to := l.Stay(w.Starts[0])
		if _, err := p.DNT.Check(ctx, l.CabinID, from, to); err != nil {
			if strings.Contains(err.Error(), "web products") {
				return Watch{}, fmt.Errorf("hytte %d kan ikke bestilles på nett, så den kan ikke overvåkes", l.CabinID)
			}
			return Watch{}, fmt.Errorf("DNT svarte ikke som ventet for hytte %d", l.CabinID)
		}
		name, err := p.DNT.CabinName(ctx, l.CabinID)
		if err != nil {
			slog.Warn("fant ikke hyttenavnet", "cabin", l.CabinID, "err", err)
		}
		w.Legs[i].CabinName = name
	}
	return store.Add(w)
}

// RunInbox sjekker innboksen hvert halve minutt.
func RunInbox(ctx context.Context, in *Inbox, h *MailHandler) {
	for {
		if err := in.Fetch(ctx, func(m Incoming) { h.Handle(ctx, m) }); err != nil {
			slog.Error("innboksen", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Second):
		}
	}
}

// RunWelcome sender velkomst-e-posten én gang, og prøver igjen til den er
// sendt.
func RunWelcome(ctx context.Context, store *Store, m *Mailer) {
	for !store.WelcomeSent() {
		if err := m.Welcome(ctx); err != nil {
			slog.Error("velkomst-e-posten feilet", "err", err)
		} else {
			slog.Info("velkomst-e-post sendt", "to", m.To)
			store.SetWelcomeSent()
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(10 * time.Minute):
		}
	}
}

// RunSummary sender oversikten søndag kveld når hun følger med på noe.
func RunSummary(ctx context.Context, store *Store, m *Mailer) {
	for {
		now := time.Now().In(oslo)
		if now.Weekday() == time.Sunday && now.Hour() >= 18 {
			var last time.Time
			store.Meta(func(ls, _ *time.Time) { last = *ls })
			if now.Sub(last) > 24*time.Hour {
				active := 0
				for _, w := range store.List() {
					if !w.Expired(now) {
						active++
					}
				}
				var err error
				if active > 0 {
					err = m.Summary(ctx, m.To, store.List(), "", "")
				}
				if err != nil {
					slog.Error("ukesoppsummering feilet", "err", err)
				} else {
					store.Meta(func(ls, _ *time.Time) { *ls = now })
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(10 * time.Minute):
		}
	}
}

// askDate spør etter datoen når hun delte en lenke uten datoer. Hytta står
// i emnet, så svaret hennes trenger bare datoen.
func (h *MailHandler) askDate(ctx context.Context, in Incoming, legs []Leg) {
	var names []string
	links := make([]cabinLink, len(legs))
	for i, l := range legs {
		links[i] = cabinLink{id: l.CabinID}
		name, err := h.Poller.DNT.CabinName(ctx, l.CabinID)
		if err != nil {
			name = fmt.Sprintf("hytte %d", l.CabinID)
		}
		names = append(names, name)
	}
	subject := fmt.Sprintf("Hvilken dato for %s? %s", strings.Join(names, " → "), HytteTag(links))
	err := h.Mailer.Ask(ctx, in.From, subject, in.MessageID,
		"Lenken til "+strings.Join(names, " → ")+" hadde ingen datoer.",
		"Svar på denne e-posten med datoen, for eksempel «12. juli», «12.–14. juli, 2 personer» eller «en natt mellom 10. og 20. juli».",
		"Velger du datoene på DNT-siden før du deler lenken, følger de med av seg selv.")
	if err != nil {
		slog.Error("spørsmål om dato feilet", "err", err)
	}
}
