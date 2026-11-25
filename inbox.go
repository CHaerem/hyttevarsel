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
}

func (h *MailHandler) Handle(ctx context.Context, in Incoming) {
	log := slog.With("from", in.From, "subject", in.Subject)
	if !containsFold(h.Allowed, in.From) {
		log.Info("ignorerer e-post fra ukjent avsender")
		return
	}
	if !in.Verified {
		log.Warn("ignorerer e-post uten bekreftet avsender (DMARC/DKIM)")
		return
	}
	now := time.Now()
	req, err := ParseMessage(in.Subject, in.Text, now)
	reply := func(lines ...string) {
		if err := h.Mailer.Reply(ctx, in.From, in.Subject, in.MessageID, lines...); err != nil {
			log.Error("svar feilet", "err", err)
		}
	}
	switch {
	case errors.Is(err, ErrIgnore):
		return
	case errors.Is(err, ErrHelp):
		reply(append([]string{"Jeg fant ingen hyttelenke i e-posten din."}, helpLines...)...)
		return
	case err != nil:
		reply("Jeg fikk ikke lagt inn hytta: "+err.Error()+".", helpLines[1])
		return
	}

	switch req.Kind {
	case ReqStop:
		w, ok := h.Store.Get(req.WatchID)
		if !ok {
			reply(fmt.Sprintf("Jeg følger ikke med på %s lenger, så det er allerede stoppet.", fmt.Sprintf("[#%d]", req.WatchID)))
			return
		}
		h.Store.Delete(w.ID)
		log.Info("stoppet", "watch", w.ID)
		reply("Ok, jeg slutter å følge med på " + w.Title() + " " + tag(w) + ". God tur!")
	case ReqStopAll:
		n := 0
		for _, w := range h.Store.List() {
			h.Store.Delete(w.ID)
			n++
		}
		reply(fmt.Sprintf("Ok, jeg har stoppet alle (%d).", n))
	case ReqList:
		if err := h.Mailer.Summary(ctx, []string{in.From}, h.Store.List(), in.MessageID, "Re: "+in.Subject); err != nil {
			log.Error("oversikt feilet", "err", err)
		}
	case ReqAdd:
		w := req.Watch
		w.Requester = in.From
		w, err := AddWatch(ctx, h.Store, h.Poller, w)
		if err != nil {
			reply("Jeg fikk ikke lagt inn hytta: " + err.Error() + ".")
			return
		}
		log.Info("ny overvåking fra e-post", "watch", w.ID, "title", w.Title())
		if err := h.Mailer.Confirm(ctx, w, in.MessageID); err != nil {
			log.Error("bekreftelse feilet", "err", err)
		}
		h.Poller.Evaluate(ctx, w.ID)
	}
}

// AddWatch slår opp navnene, lagrer og sjekker med en gang. Felles for
// nettsiden og e-post.
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
	w, err := store.Add(w)
	if err != nil {
		return Watch{}, err
	}
	p.CheckWatch(ctx, w.ID)
	w, _ = store.Get(w.ID)
	return w, nil
}

// RunInbox sjekker innboksen hvert minutt.
func RunInbox(ctx context.Context, in *Inbox, h *MailHandler) {
	for {
		if err := in.Fetch(ctx, func(m Incoming) { h.Handle(ctx, m) }); err != nil {
			slog.Error("innboksen", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Minute):
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
