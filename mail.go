package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"html/template"
	"log/slog"
	"mime"
	"mime/multipart"
	"net"
	"net/smtp"
	"net/textproto"
	"net/url"
	"strings"
	"time"
)

// Message er en utgående e-post: ren tekst og HTML med samme innhold.
type Message struct {
	To        []string
	Subject   string
	Text      string
	HTML      string
	InReplyTo string // Message-ID vi svarer på
}

type Sender interface {
	Send(ctx context.Context, m Message) error
}

// Mailer lager e-postene. Address er varseladressen hun svarer til.
type Mailer struct {
	Sender  Sender
	Address string   // f.eks. hyttevarsel@gmail.com
	To      []string // mottakere av varsler og oppsummering
	BaseURL string   // nettsiden på tailnettet, valgfri
}

func (m *Mailer) mailto(subject, body string) string {
	return "mailto:" + m.Address + "?" + strings.ReplaceAll(url.Values{"subject": {subject}, "body": {body}}.Encode(), "+", "%20")
}

type mailLink struct{ Label, URL string }

type mailData struct {
	Heading string
	Lines   []string
	Buttons []mailLink // store knapper
	Links   []mailLink // små lenker under
	Footer  string
}

var mailHTML = template.Must(template.New("mail").Parse(`<!doctype html>
<html lang="nb"><body style="margin:0;padding:0;background:#f6f5f1;font-family:-apple-system,'Segoe UI',sans-serif;color:#1d2321">
<div style="max-width:560px;margin:0 auto;padding:20px 16px">
<h1 style="font-size:20px;margin:0 0 12px">{{.Heading}}</h1>
{{range .Lines}}<p style="font-size:16px;line-height:1.45;margin:0 0 10px">{{.}}</p>{{end}}
{{range .Buttons}}<p style="margin:14px 0"><a href="{{.URL}}" style="display:block;background:#c8102e;color:#ffffff;text-decoration:none;font-weight:600;font-size:17px;text-align:center;padding:14px 18px;border-radius:10px">{{.Label}}</a></p>{{end}}
{{if .Links}}<p style="font-size:15px;margin:18px 0 0">{{range $i, $l := .Links}}{{if $i}} · {{end}}<a href="{{$l.URL}}" style="color:#5f6b66">{{$l.Label}}</a>{{end}}</p>{{end}}
{{with .Footer}}<p style="font-size:13px;color:#5f6b66;margin:22px 0 0">{{.}}</p>{{end}}
</div></body></html>`))

func render(d mailData) (text, html string) {
	var t strings.Builder
	for _, l := range d.Lines {
		t.WriteString(l + "\n\n")
	}
	for _, b := range append(append([]mailLink{}, d.Buttons...), d.Links...) {
		fmt.Fprintf(&t, "%s:\n%s\n\n", b.Label, b.URL)
	}
	if d.Footer != "" {
		t.WriteString(d.Footer + "\n")
	}
	var h bytes.Buffer
	if err := mailHTML.Execute(&h, d); err != nil {
		slog.Error("e-postmal", "err", err)
	}
	return t.String(), h.String()
}

func tag(w Watch) string { return fmt.Sprintf("[#%d]", w.ID) }

func (m *Mailer) stopLinks(w Watch) []mailLink {
	return []mailLink{
		{"Jeg har booket", m.mailto("Stopp "+tag(w), "Jeg har booket, du kan slutte å følge med.")},
		{"Slutt å følge med", m.mailto("Stopp "+tag(w), "Slutt å følge med.")},
	}
}

func (m *Mailer) footer() string {
	f := "Svar «stopp» på denne e-posten for å slutte å følge med. Ny hytte: del lenken fra hyttebestilling.dnt.no til " + m.Address + "."
	if m.BaseURL != "" {
		f += " Oversikt: " + m.BaseURL
	}
	return f
}

// Available varsler om startdatoer som nettopp ble ledige.
func (m *Mailer) Available(ctx context.Context, w Watch, starts []string) error {
	d := mailData{Heading: "Ledig nå: " + w.Title()}
	if w.IsRoute() {
		d.Lines = append(d.Lines, fmt.Sprintf("Hele ruta har plass til %s. Bestill hver hytte, helst med en gang:", personer(w.Guests)))
	} else {
		d.Lines = append(d.Lines, fmt.Sprintf("Det er blitt plass til %s. Andre ser det samme, så det lønner seg å være rask.", personer(w.Guests)))
	}
	for _, s := range starts {
		for _, l := range w.Legs {
			from, to := l.Stay(s)
			label := "Bestill nå"
			if w.IsRoute() || len(starts) > 1 {
				label = fmt.Sprintf("Bestill %s, %s", l.Name(), kortDato(from))
			}
			d.Buttons = append(d.Buttons, mailLink{label, BookingURL(l.CabinID, from, to, w.Guests)})
		}
	}
	if len(starts) == 1 {
		d.Lines = append(d.Lines, StayText(w, starts[0]))
	}
	d.Links = m.stopLinks(w)
	d.Footer = m.footer()
	text, html := render(d)
	subject := fmt.Sprintf("Ledig: %s %s %s", w.Title(), kortDato(parseDate(starts[0])), tag(w))
	return m.Sender.Send(ctx, Message{To: m.recipients(w), Subject: subject, Text: text, HTML: html})
}

// Confirm bekrefter en ny overvåking og sier hvordan den ble tolket.
func (m *Mailer) Confirm(ctx context.Context, w Watch, inReplyTo string) error {
	d := mailData{Heading: "Følger med på " + w.Title()}
	d.Lines = append(d.Lines, Describe(w))
	free := false
	for _, s := range w.ActiveStarts(time.Now()) {
		free = free || w.OptionFree(s)
	}
	if free {
		d.Lines = append(d.Lines, "Det er faktisk ledig allerede nå. Du får et eget varsel med bestillingslenke.")
	} else {
		d.Lines = append(d.Lines, "Det er fullt nå. Du får e-post så snart noen avbestiller.")
	}
	d.Lines = append(d.Lines, "Stemmer ikke dette? Svar «stopp» og send lenken på nytt med datoen skrevet i emnefeltet.")
	d.Links = m.stopLinks(w)[1:]
	d.Footer = m.footer()
	text, html := render(d)
	return m.Sender.Send(ctx, Message{To: m.recipients(w), Subject: "Følger med: " + w.Title() + " " + tag(w), Text: text, HTML: html, InReplyTo: inReplyTo})
}

// Reply svarer kort på en beskjed (stopp, feil, hjelp).
func (m *Mailer) Reply(ctx context.Context, to, subject, inReplyTo string, lines ...string) error {
	d := mailData{Heading: "Hyttevarsel", Lines: lines, Footer: m.footer()}
	text, html := render(d)
	if !strings.HasPrefix(strings.ToLower(subject), "re:") {
		subject = "Re: " + subject
	}
	return m.Sender.Send(ctx, Message{To: []string{to}, Subject: subject, Text: text, HTML: html, InReplyTo: inReplyTo})
}

var helpLines = []string{
	"Slik legger du til en hytte: åpne hytta på hyttebestilling.dnt.no, velg datoene og antall personer, trykk Del og send lenken hit.",
	"Du kan også skrive datoen i emnefeltet: «12. juli», «12.–14. juli» (ankomst og avreise), «en natt mellom 10. og 20. juli» eller «lørdager i august». Skriv «2 personer» hvis dere er flere.",
	"Flere lenker i samme e-post blir en rute med én natt på hver hytte, i rekkefølgen du limte dem inn.",
	"Svar «stopp» på et varsel for å slutte å følge med på den hytta, «stopp alt» for alle, og «oversikt» for å se hva du følger med på.",
}

// Summary sender oversikten (ukentlig, og når hun ber om den).
func (m *Mailer) Summary(ctx context.Context, to []string, watches []Watch, inReplyTo, subject string) error {
	d := mailData{Heading: "Dette følger du med på"}
	now := time.Now()
	active := 0
	for _, w := range watches {
		if w.Expired(now) {
			continue
		}
		active++
		status := "fullt"
		if free := w.FreeStarts(now); len(free) > 0 {
			status = "LEDIG nå"
		}
		d.Lines = append(d.Lines, fmt.Sprintf("%s %s: %s (%s)", tag(w), w.Title(), Describe(w), status))
	}
	if active == 0 {
		d.Lines = []string{"Du følger ikke med på noen hytter nå."}
	}
	d.Lines = append(d.Lines, "Svar «stopp» og nummeret, for eksempel «stopp [#3]», for å fjerne en.")
	d.Footer = m.footer()
	text, html := render(d)
	if subject == "" {
		subject = "Hyttevarsel: ukens oversikt"
	}
	return m.Sender.Send(ctx, Message{To: to, Subject: subject, Text: text, HTML: html, InReplyTo: inReplyTo})
}

func (m *Mailer) recipients(w Watch) []string {
	if w.Requester != "" && !containsFold(m.To, w.Requester) {
		return append([]string{w.Requester}, m.To...)
	}
	return m.To
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(addrOnly(x), addrOnly(s)) {
			return true
		}
	}
	return false
}

// StayText beskriver ett opphold, med alle hyttene i en rute.
func StayText(w Watch, start string) string {
	var parts []string
	for _, l := range w.Legs {
		from, _ := l.Stay(start)
		part := fmt.Sprintf("%s fra %s, %s", l.Name(), norskDato(from), netter(l.Nights))
		parts = append(parts, part)
	}
	return strings.Join(parts, "; ") + ", " + personer(w.Guests) + "."
}

// Describe forteller hvordan overvåkingen ble tolket.
func Describe(w Watch) string {
	nights := netter(w.Legs[0].Nights)
	if w.IsRoute() {
		nights += " på hver hytte"
	}
	switch {
	case len(w.Starts) == 1:
		return StayText(w, w.Starts[0])
	case isConsecutive(w.Starts):
		return fmt.Sprintf("%s, %s, med ankomst en hvilken som helst dag fra %s til %s. Du får varsel for hver dato som blir ledig.",
			nights, personer(w.Guests), kortDato(parseDate(w.Starts[0])), kortDato(parseDate(w.Starts[len(w.Starts)-1])))
	default:
		var ds []string
		for _, s := range w.Starts {
			ds = append(ds, kortDato(parseDate(s)))
		}
		return fmt.Sprintf("%s, %s, med ankomst %s. Du får varsel for hver dato som blir ledig.", nights, personer(w.Guests), strings.Join(ds, ", "))
	}
}

func isConsecutive(starts []string) bool {
	for i := 1; i < len(starts); i++ {
		if !parseDate(starts[i]).Equal(parseDate(starts[i-1]).AddDate(0, 0, 1)) {
			return false
		}
	}
	return true
}

// SMTPSender sender med STARTTLS (Gmail: smtp.gmail.com:587 og et app-passord).
type SMTPSender struct {
	Host, Port, User, Password, From string
}

func (s *SMTPSender) Send(_ context.Context, m Message) error {
	raw, err := buildMIME(s.From, m)
	if err != nil {
		return err
	}
	auth := smtp.PlainAuth("", s.User, s.Password, s.Host)
	return smtp.SendMail(net.JoinHostPort(s.Host, s.Port), auth, addrOnly(s.From), addrsOnly(m.To), raw)
}

func buildMIME(from string, m Message) ([]byte, error) {
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	h := []string{
		"From: " + from,
		"To: " + strings.Join(m.To, ", "),
		"Subject: " + mime.QEncoding.Encode("utf-8", m.Subject),
		"Date: " + time.Now().Format(time.RFC1123Z),
		"Message-ID: " + newMessageID(from),
		"MIME-Version: 1.0",
		"Content-Type: multipart/alternative; boundary=" + mw.Boundary(),
	}
	if m.InReplyTo != "" {
		h = append(h, "In-Reply-To: "+m.InReplyTo, "References: "+m.InReplyTo)
	}
	var out bytes.Buffer
	out.WriteString(strings.Join(h, "\r\n") + "\r\n\r\n")
	for _, part := range []struct{ typ, body string }{{"text/plain", m.Text}, {"text/html", m.HTML}} {
		pw, err := mw.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {part.typ + "; charset=utf-8"},
			"Content-Transfer-Encoding": {"8bit"},
		})
		if err != nil {
			return nil, err
		}
		pw.Write([]byte(strings.ReplaceAll(part.body, "\n", "\r\n")))
	}
	mw.Close()
	out.Write(b.Bytes())
	return out.Bytes(), nil
}

func newMessageID(from string) string {
	r := make([]byte, 12)
	rand.Read(r)
	domain := "hyttevarsel.local"
	if a := addrOnly(from); strings.Contains(a, "@") {
		domain = a[strings.LastIndex(a, "@")+1:]
	}
	return "<" + hex.EncodeToString(r) + "@" + domain + ">"
}

// addrOnly henter adressen ut av «Navn <adresse>».
func addrOnly(s string) string {
	if i, j := strings.LastIndex(s, "<"), strings.LastIndex(s, ">"); i >= 0 && j > i {
		return s[i+1 : j]
	}
	return strings.TrimSpace(s)
}

func addrsOnly(list []string) []string {
	out := make([]string, len(list))
	for i, s := range list {
		out[i] = addrOnly(s)
	}
	return out
}

// LogSender brukes når SMTP ikke er satt opp (lokalt).
type LogSender struct{}

func (LogSender) Send(_ context.Context, m Message) error {
	slog.Info("E-POST (ingen SMTP satt opp)", "to", m.To, "subject", m.Subject, "text", m.Text)
	return nil
}

var (
	ukedager = []string{"søndag", "mandag", "tirsdag", "onsdag", "torsdag", "fredag", "lørdag"}
	måneder  = []string{"januar", "februar", "mars", "april", "mai", "juni", "juli", "august", "september", "oktober", "november", "desember"}
)

func norskDato(t time.Time) string {
	return fmt.Sprintf("%s %d. %s %d", ukedager[t.Weekday()], t.Day(), måneder[t.Month()-1], t.Year())
}

func kortDato(t time.Time) string {
	return fmt.Sprintf("%s %d. %s", string([]rune(ukedager[t.Weekday()])[:3]), t.Day(), måneder[t.Month()-1])
}

func netter(n int) string {
	if n == 1 {
		return "1 natt"
	}
	return fmt.Sprintf("%d netter", n)
}

func personer(n int) string {
	if n == 1 {
		return "1 person"
	}
	return fmt.Sprintf("%d personer", n)
}
