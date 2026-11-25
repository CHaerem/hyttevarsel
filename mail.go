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
	"slices"
	"strings"
	"time"
	"unicode"
)

// Message er en utgående e-post: ren tekst og HTML med samme innhold.
type Message struct {
	To        []string
	Subject   string
	Text      string
	HTML      string
	InReplyTo string // Message-ID vi svarer på
	ReplyTo   string // der svaret skal gå, se watchAddr
}

type Sender interface {
	Send(ctx context.Context, m Message) error
}

// Mailer lager e-postene. Tonen er nøktern, og utseendet følger
// oppskriftssiden (docs/index.html): samme farger, én knapp per handling.
type Mailer struct {
	Sender  Sender
	Address string   // varseladressen, f.eks. hyttevarsel@gmail.com
	To      []string // mottakere av varsler og oppsummering
	PageURL string   // oppskriftssiden, valgfri
}

// watchAddr er svaradressen for én overvåking: varseladressen med «+h<id>».
// Gmail leverer pluss-adresser til samme innboks, så et svar forteller
// hvilken hytte det gjelder uten at et nummer må stå i emnet.
func (m *Mailer) watchAddr(w Watch) string {
	local, domain, ok := strings.Cut(m.Address, "@")
	if !ok {
		return m.Address
	}
	return fmt.Sprintf("%s+h%d@%s", local, w.ID, domain)
}

func mailto(addr, subject, body string) string {
	// «+» i adressen skrives %2B, så ingen e-postklient leser det som mellomrom.
	return "mailto:" + strings.ReplaceAll(addr, "+", "%2B") + "?" + strings.ReplaceAll(url.Values{"subject": {subject}, "body": {body}}.Encode(), "+", "%20")
}

func (m *Mailer) stopURL(w Watch) string {
	return mailto(m.watchAddr(w), "Stopp", "Slutt å følge med.")
}

// page er oppskriftssiden med varseladressen i ankeret. Siden selv er uten
// adresse; den leser ankeret og fyller den inn for den som kom herfra.
func (m *Mailer) page() string {
	if m.PageURL == "" {
		return ""
	}
	return m.PageURL + "#til=" + m.Address
}

type mailLink struct{ Label, URL string }

type mailRow struct{ Title, Detail, Status, LinkLabel, LinkURL string }

type mailData struct {
	Heading string
	Lines   []string
	Rows    []mailRow
	Buttons []mailLink // store knapper
	Links   []mailLink // små lenker under
	Footer  string
}

var mailHTML = template.Must(template.New("mail").Parse(`<!doctype html>
<html lang="nb"><body style="margin:0;padding:0;background:#f3f0eb;font-family:-apple-system,'Segoe UI',Roboto,Arial,sans-serif;color:#26221f">
<div style="max-width:520px;margin:0 auto;padding:24px 18px">
<h1 style="font-size:21px;line-height:1.25;margin:0 0 12px">{{.Heading}}</h1>
{{range .Lines}}<p style="font-size:16px;line-height:1.5;margin:0 0 10px">{{.}}</p>
{{end}}{{range .Rows}}<div style="border-top:1px solid #e3ddd4;padding:12px 0"><div style="font-size:16px;font-weight:600">{{.Title}}</div><div style="font-size:15px;line-height:1.45;color:#6a625b">{{.Detail}}</div><div style="font-size:15px;margin-top:2px">{{.Status}}{{if and .Status .LinkURL}} · {{end}}{{if .LinkURL}}<a href="{{.LinkURL}}" style="color:#2d5a45">{{.LinkLabel}}</a>{{end}}</div></div>
{{end}}{{range .Buttons}}<p style="margin:14px 0"><a href="{{.URL}}" style="display:block;background:#2d5a45;color:#ffffff;text-decoration:none;font-weight:700;font-size:17px;text-align:center;padding:14px 18px;border-radius:12px">{{.Label}}</a></p>
{{end}}{{if .Links}}<p style="font-size:15px;margin:16px 0 0;text-align:center">{{range $i, $l := .Links}}{{if $i}} &nbsp;·&nbsp; {{end}}<a href="{{$l.URL}}" style="color:#2d5a45">{{$l.Label}}</a>{{end}}</p>
{{end}}{{with .Footer}}<p style="font-size:13px;line-height:1.45;color:#6a625b;margin:24px 0 0;border-top:1px solid #e3ddd4;padding-top:12px">{{.}}</p>
{{end}}</div></body></html>`))

func render(d mailData) (text, html string) {
	var t strings.Builder
	for _, l := range d.Lines {
		t.WriteString(l + "\n\n")
	}
	for _, r := range d.Rows {
		if strings.HasPrefix(r.LinkURL, "https://") {
			// Samme form som knappene («Bestill …:» og lenken på neste linje),
			// så et svar som siterer e-posten ikke blir lest som nye lenker.
			fmt.Fprintf(&t, "%s %s:\n%s\n\n", r.LinkLabel, strings.ToLower(r.Title), r.LinkURL)
			continue
		}
		t.WriteString(r.Title + "\n")
		for _, l := range []string{r.Detail, r.Status} {
			if l != "" {
				t.WriteString(l + "\n")
			}
		}
		t.WriteString("\n")
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

func (m *Mailer) send(ctx context.Context, to []string, subject, inReplyTo, replyTo string, d mailData) error {
	text, html := render(d)
	return m.Sender.Send(ctx, Message{To: to, Subject: subject, Text: text, HTML: html, InReplyTo: inReplyTo, ReplyTo: replyTo})
}

func replySubject(subject string) string {
	if replyPrefix.MatchString(subject) {
		return subject
	}
	return "Re: " + subject
}

// Welcome er den første e-posten. Den forteller hva adressen er til, og er
// samtidig testen på at e-post herfra kommer fram (og ikke i søppelpost).
func (m *Mailer) Welcome(ctx context.Context) error {
	d := mailData{
		Heading: "Hyttevarsel er klart",
		Lines: []string{
			"Hyttevarsel følger med på fulle DNT-hytter og sender deg en e-post når noen avbestiller.",
			"Slik legger du til en hytte: velg datoer og antall personer på hyttebestilling.dnt.no, trykk Del, og send lenken til denne adressen.",
			"Lagre avsenderen som kontakt og slå på varsler for den, så ser du varselet med en gang. Lå denne e-posten i søppelpost, marker den som «ikke søppelpost».",
		},
		Footer: "Varsler kommer fra " + m.Address + ". Tjenesten er privat og svarer bare deg.",
	}
	if p := m.page(); p != "" {
		d.Buttons = []mailLink{{"Slik bruker du Hyttevarsel", p}}
	}
	return m.send(ctx, m.To, "Hyttevarsel er klart", "", "", d)
}

// Available varsler om startdatoer som nettopp ble ledige. Kommer varselet
// rett etter at hytta ble lagt inn, var datoene ledige fra før.
func (m *Mailer) Available(ctx context.Context, w Watch, starts []string) error {
	return m.available(ctx, w, starts, "", time.Since(w.CreatedAt) < 10*time.Minute)
}

// AvailableNow er svaret når hytta hun nettopp la inn allerede er ledig:
// én e-post i stedet for bekreftelse og varsel rett etter hverandre.
func (m *Mailer) AvailableNow(ctx context.Context, w Watch, starts []string, inReplyTo string) error {
	return m.available(ctx, w, starts, inReplyTo, true)
}

func (m *Mailer) available(ctx context.Context, w Watch, starts []string, inReplyTo string, justAdded bool) error {
	d := mailData{Heading: "Ledig nå: " + w.Title()}
	switch {
	case w.IsRoute():
		d.Lines = append(d.Lines, fmt.Sprintf("Hele turen har plass til %s. Alle hyttene må bestilles for at turen skal gå opp.", personer(w.Guests)))
	case justAdded && len(starts) > 1:
		d.Lines = append(d.Lines, fmt.Sprintf("Disse datoene har allerede plass til %s.", personer(w.Guests)))
	case justAdded:
		d.Lines = append(d.Lines, fmt.Sprintf("Det er allerede plass til %s, så du kan bestille med en gang.", personer(w.Guests)))
	case len(starts) > 1:
		d.Lines = append(d.Lines, fmt.Sprintf("Disse datoene har fått plass til %s. Andre ser de ledige plassene også, så det lønner seg å bestille med en gang.", personer(w.Guests)))
	default:
		d.Lines = append(d.Lines, fmt.Sprintf("Det er blitt plass til %s. Andre ser den ledige plassen også, så det lønner seg å bestille med en gang.", personer(w.Guests)))
	}
	if len(starts) == 1 && !w.IsRoute() {
		from, _ := w.Legs[0].Stay(starts[0])
		d.Lines = append(d.Lines, fmt.Sprintf("%s, %s.", capitalize(norskDato(from)), netter(w.Legs[0].Nights)))
	}
	if justAdded && len(starts) < len(w.Starts) {
		d.Lines = append(d.Lines, "Hyttevarsel følger med på de andre datoene.")
	}
	for _, s := range starts {
		for i, l := range w.Legs {
			from, to := l.Stay(s)
			link := BookingURL(l.CabinID, from, to, w.Guests)
			label := "Bestill nå"
			switch {
			case w.IsRoute():
				label = fmt.Sprintf("%d av %d: %s, %s", i+1, len(w.Legs), l.Name(), kortDato(from))
			case len(starts) > 3:
				// Mange ledige datoer: en liste i stedet for en vegg av knapper.
				d.Rows = append(d.Rows, mailRow{Title: capitalize(norskDato(from)), LinkLabel: "Bestill", LinkURL: link})
				continue
			case len(starts) > 1:
				label = "Bestill " + kortDato(from)
			}
			d.Buttons = append(d.Buttons, mailLink{label, link})
		}
	}
	d.Links = []mailLink{
		{"Jeg har booket", mailto(m.watchAddr(w), "Stopp", "Jeg har booket.")},
		{"Slutt å følge med", m.stopURL(w)},
	}
	d.Footer = "Du kan også svare «stopp» på denne e-posten. Blir plassen tatt før du rekker det, følger Hyttevarsel med videre."
	if len(w.Starts) > 1 {
		// Flere datoer: en bestilling skal ikke stoppe resten uten at hun ber om det.
		d.Links = []mailLink{{"Slutt å følge med på " + w.Title(), m.stopURL(w)}}
		d.Footer = "Hyttevarsel følger med på alle datoene til du trykker lenken over eller svarer «stopp» på denne e-posten."
	}
	first, _ := w.Legs[0].Stay(starts[0])
	subject := fmt.Sprintf("Ledig: %s %s", w.Title(), kortDato(first))
	if len(starts) > 1 {
		subject += fmt.Sprintf(" og %d til", len(starts)-1)
	}
	return m.send(ctx, m.recipients(w), subject, inReplyTo, m.watchAddr(w), d)
}

// Confirm bekrefter en ny overvåking og sier hvordan e-posten ble tolket.
// checking er sann når datoene fortsatt sjekkes (mange datoer).
func (m *Mailer) Confirm(ctx context.Context, w Watch, inReplyTo string, checking bool) error {
	d := mailData{Heading: "Følger med: " + w.Title(), Lines: []string{Describe(w)}}
	if checking {
		d.Lines = append(d.Lines, "Datoene sjekkes nå. Er noe ledig allerede, får du beskjed om et par minutter.")
	} else {
		d.Lines = append(d.Lines, "Det er fullt nå. Du får e-post når noen avbestiller.")
	}
	d.Lines = append(d.Lines, "Stemmer det ikke, svar «stopp» og send lenken på nytt med datoen i emnefeltet.")
	d.Links = []mailLink{{"Slutt å følge med", m.stopURL(w)}}
	if p := m.page(); p != "" {
		d.Links = append(d.Links, mailLink{"Slik bruker du Hyttevarsel", p})
	}
	return m.send(ctx, m.recipients(w), "Følger med: "+w.Title()+", "+Short(w), inReplyTo, m.watchAddr(w), d)
}

// ConfirmMany bekrefter flere hytter lagt inn i samme beskjed.
func (m *Mailer) ConfirmMany(ctx context.Context, added []Watch, failed []string, inReplyTo string) error {
	d := mailData{Heading: fmt.Sprintf("Følger med på %d hytter", len(added))}
	if same := Describe(added[0]); allSame(added) {
		d.Lines = append(d.Lines, same)
	}
	d.Lines = append(d.Lines, "Datoene sjekkes nå. Er noe ledig allerede, får du beskjed om et par minutter.")
	for _, f := range failed {
		d.Lines = append(d.Lines, "Ble ikke lagt inn: "+f+".")
	}
	for _, w := range added {
		r := mailRow{Title: w.Title(), LinkLabel: "Stopp", LinkURL: m.stopURL(w)}
		if !allSame(added) {
			r.Detail = Short(w) + ", " + personer(w.Guests)
		}
		d.Rows = append(d.Rows, r)
	}
	d.Footer = "Du får én e-post per hytte som blir ledig. Svar «stopp alt» for å stoppe alle."
	return m.send(ctx, m.recipients(added[0]), fmt.Sprintf("Følger med på %d hytter", len(added)), inReplyTo, "", d)
}

// allSame er sann når hyttene følges for de samme datoene og personene.
func allSame(ws []Watch) bool {
	for _, w := range ws[1:] {
		if w.Guests != ws[0].Guests || !slices.Equal(w.Starts, ws[0].Starts) || w.Legs[0].Nights != ws[0].Legs[0].Nights || w.IsRoute() != ws[0].IsRoute() {
			return false
		}
	}
	return !ws[0].IsRoute()
}

// Stopped bekrefter at en overvåking er stoppet.
func (m *Mailer) Stopped(ctx context.Context, to string, w Watch, subject, inReplyTo string) error {
	d := mailData{Heading: "Stoppet: " + w.Title(), Lines: []string{
		fmt.Sprintf("Hyttevarsel følger ikke lenger med på %s (%s).", w.Title(), Short(w)),
		"Send lenken på nytt hvis du vil følge med igjen.",
	}}
	return m.send(ctx, []string{to}, replySubject(subject), inReplyTo, "", d)
}

func (m *Mailer) rows(watches []Watch, now time.Time) []mailRow {
	var rows []mailRow
	for _, w := range watches {
		if w.Expired(now) {
			continue
		}
		status := "Fullt"
		if len(w.FreeStarts(now)) > 0 {
			status = "Ledig nå"
		}
		rows = append(rows, mailRow{Title: w.Title(), Detail: Short(w) + ", " + personer(w.Guests), Status: status, LinkLabel: "Stopp", LinkURL: m.stopURL(w)})
	}
	return rows
}

// WhichToStop spør hvilken hun mente når «stopp» kan gjelde flere.
func (m *Mailer) WhichToStop(ctx context.Context, to string, watches []Watch, subject, inReplyTo string) error {
	d := mailData{
		Heading: "Hvilken skal stoppes?",
		Lines:   []string{"Du følger med på flere hytter. Velg den du vil slutte med."},
		Rows:    m.rows(watches, time.Now()),
		Footer:  "Svar «stopp alt» for å stoppe alle.",
	}
	return m.send(ctx, []string{to}, replySubject(subject), inReplyTo, "", d)
}

// Summary sender oversikten (ukentlig, og når hun ber om den).
func (m *Mailer) Summary(ctx context.Context, to []string, watches []Watch, inReplyTo, subject string) error {
	d := mailData{Heading: "Dette følger du med på", Rows: m.rows(watches, time.Now())}
	if len(d.Rows) == 0 {
		d.Lines = []string{"Du følger ikke med på noen hytter nå."}
	} else {
		d.Footer = "Trykk Stopp ved en hytte, eller svar «stopp alt» for å stoppe alle."
	}
	if subject == "" {
		subject = "Hyttevarsel: oversikt"
	}
	return m.send(ctx, to, subject, inReplyTo, "", d)
}

// Reply svarer kort på en beskjed (feil ved innlegging o.l.).
func (m *Mailer) Reply(ctx context.Context, to, subject, inReplyTo string, lines ...string) error {
	return m.send(ctx, []string{to}, replySubject(subject), inReplyTo, "", mailData{Heading: "Hyttevarsel", Lines: lines})
}

// Help er svaret når e-posten ikke ga mening.
func (m *Mailer) Help(ctx context.Context, to, subject, inReplyTo string) error {
	d := mailData{Heading: "Fant ingen hyttelenke", Lines: []string{
		"Send lenken til hytta fra hyttebestilling.dnt.no, med datoene valgt.",
		"Du kan også svare «oversikt» for å se hva du følger med på, eller «stopp» for å slutte.",
	}}
	if p := m.page(); p != "" {
		d.Links = []mailLink{{"Slik bruker du Hyttevarsel", p}}
	}
	return m.send(ctx, []string{to}, replySubject(subject), inReplyTo, "", d)
}

// Ask stiller et spørsmål med et eget emne (svaret hennes går til samme emne).
func (m *Mailer) Ask(ctx context.Context, to, subject, inReplyTo string, lines ...string) error {
	return m.send(ctx, []string{to}, subject, inReplyTo, "", mailData{Heading: "Hvilken dato?", Lines: lines})
}

// Booked sier fra at en ordrebekreftelse stoppet overvåkingen.
func (m *Mailer) Booked(ctx context.Context, booked []Watch) error {
	var names []string
	to := m.To
	for _, w := range booked {
		names = append(names, w.Title())
		to = m.recipients(w)
	}
	d := mailData{Heading: "Stoppet: " + strings.Join(names, ", "), Lines: []string{
		"Ordrebekreftelsen fra DNT kom, så Hyttevarsel følger ikke lenger med på " + strings.Join(names, " og ") + ".",
		"Var det feil, send lenken på nytt.",
	}}
	return m.send(ctx, to, "Stoppet: "+strings.Join(names, ", "), "", "", d)
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

func capitalize(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

func sameNights(w Watch) bool {
	for _, l := range w.Legs {
		if l.Nights != w.Legs[0].Nights {
			return false
		}
	}
	return true
}

// Describe forteller i en setning hva det følges med på.
func Describe(w Watch) string {
	nights := netter(w.Legs[0].Nights)
	if w.IsRoute() {
		nights += " på hver hytte"
	}
	if len(w.Starts) == 1 {
		if !w.IsRoute() {
			from, _ := w.Legs[0].Stay(w.Starts[0])
			return fmt.Sprintf("%s, %s, %s.", capitalize(norskDato(from)), nights, personer(w.Guests))
		}
		var parts []string
		for _, l := range w.Legs {
			from, _ := l.Stay(w.Starts[0])
			part := l.Name() + " " + kortDato(from)
			if !sameNights(w) {
				part += " (" + netter(l.Nights) + ")"
			}
			parts = append(parts, part)
		}
		s := strings.Join(parts, ", ") + ". "
		if sameNights(w) {
			s += capitalize(nights) + ", "
		}
		return s + personer(w.Guests) + "."
	}
	if isConsecutive(w.Starts) {
		last := w.Legs[len(w.Legs)-1]
		_, end := last.Stay(w.Starts[len(w.Starts)-1])
		return fmt.Sprintf("%s, %s, med ankomst hvilken som helst dag fra %s til %s (siste avreise %s). Du får varsel for hver dato som blir ledig.",
			capitalize(nights), personer(w.Guests), kortDato(parseDate(w.Starts[0])), kortDato(parseDate(w.Starts[len(w.Starts)-1])), kortDato(end))
	}
	if len(w.Starts) > 6 {
		what := fmt.Sprintf("%d datoer", len(w.Starts))
		if days := weekdaySummary(w.Starts); days != "" {
			what = days
		}
		return fmt.Sprintf("%s, %s, %s fra %s til %s (%d datoer). Du får varsel for hver dato som blir ledig.",
			capitalize(nights), personer(w.Guests), what, kortDato(parseDate(w.Starts[0])), kortDato(parseDate(w.Starts[len(w.Starts)-1])), len(w.Starts))
	}
	var ds []string
	for _, s := range w.Starts {
		ds = append(ds, kortDato(parseDate(s)))
	}
	return fmt.Sprintf("%s, %s, med ankomst %s. Du får varsel for hver dato som blir ledig.", capitalize(nights), personer(w.Guests), strings.Join(ds, ", "))
}

var flertall = []string{"søndager", "mandager", "tirsdager", "onsdager", "torsdager", "fredager", "lørdager"}

// weekdaySummary gir «fredager og lørdager» når datoene bare faller på
// én eller to ukedager, ellers tom streng.
func weekdaySummary(starts []string) string {
	seen := map[time.Weekday]bool{}
	for _, s := range starts {
		seen[parseDate(s).Weekday()] = true
	}
	if len(seen) > 2 {
		return ""
	}
	var names []string
	for _, d := range []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday, time.Sunday} {
		if seen[d] {
			names = append(names, flertall[d])
		}
	}
	return "ankomst " + strings.Join(names, " og ")
}

// Short er datoen i kortform, til emner og lister.
func Short(w Watch) string {
	first := kortDato(parseDate(w.Starts[0]))
	switch {
	case len(w.Starts) == 1:
		return first
	case isConsecutive(w.Starts):
		return fmt.Sprintf("%s til %s", first, kortDato(parseDate(w.Starts[len(w.Starts)-1])))
	default:
		return fmt.Sprintf("%d datoer fra %s", len(w.Starts), first)
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
	if m.ReplyTo != "" {
		h = append(h, "Reply-To: "+m.ReplyTo)
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
