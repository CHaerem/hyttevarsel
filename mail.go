package main

import (
	"context"
	"fmt"
	"log/slog"
	"mime"
	"net"
	"net/smtp"
	"strings"
	"time"
)

type SMTPNotifier struct {
	Host     string // f.eks. smtp.gmail.com
	Port     string // 587, STARTTLS
	User     string
	Password string
	From     string
	To       []string
	BaseURL  string // lenke til oversikten i e-posten, valgfri
}

func (n *SMTPNotifier) Notify(_ context.Context, w Watch, a Availability) error {
	name := w.CabinName
	if name == "" {
		name = fmt.Sprintf("hytte %d", w.CabinID)
	}
	subject := fmt.Sprintf("Ledig: %s %s", name, norskDato(w.FromDate()))
	var body strings.Builder
	fmt.Fprintf(&body, "Det er blitt ledig plass på %s.\n\n", name)
	fmt.Fprintf(&body, "Ankomst: %s, %s\n", norskDato(w.FromDate()), netter(w.Nights))
	fmt.Fprintf(&body, "Ledige plasser nå: %d (du følger med for %s)\n\n", a.Free, personer(w.Guests))
	fmt.Fprintf(&body, "Bestill her:\n%s\n\n", BookingURL(w.CabinID, w.FromDate(), w.ToDate(), w.Guests))
	body.WriteString("Plassen kan bli tatt av andre fort, så det lønner seg å være rask.\n")
	if n.BaseURL != "" {
		fmt.Fprintf(&body, "\nDine overvåkinger: %s\n", n.BaseURL)
	}

	msg := strings.Join([]string{
		"From: " + n.From,
		"To: " + strings.Join(n.To, ", "),
		"Subject: " + mime.QEncoding.Encode("utf-8", subject),
		"Date: " + time.Now().Format(time.RFC1123Z),
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=utf-8",
		"Content-Transfer-Encoding: 8bit",
		"",
		body.String(),
	}, "\r\n")
	auth := smtp.PlainAuth("", n.User, n.Password, n.Host)
	return smtp.SendMail(net.JoinHostPort(n.Host, n.Port), auth, addrOnly(n.From), n.To, []byte(msg))
}

// addrOnly henter adressen ut av «Navn <adresse>».
func addrOnly(s string) string {
	if i, j := strings.LastIndex(s, "<"), strings.LastIndex(s, ">"); i >= 0 && j > i {
		return s[i+1 : j]
	}
	return s
}

// LogNotifier brukes når SMTP ikke er satt opp (lokalt og i tester).
type LogNotifier struct{}

func (LogNotifier) Notify(_ context.Context, w Watch, a Availability) error {
	slog.Info("VARSEL (ingen SMTP satt opp)", "cabin", w.CabinName, "from", w.From, "free", a.Free,
		"url", BookingURL(w.CabinID, w.FromDate(), w.ToDate(), w.Guests))
	return nil
}

var (
	ukedager = []string{"søndag", "mandag", "tirsdag", "onsdag", "torsdag", "fredag", "lørdag"}
	måneder  = []string{"januar", "februar", "mars", "april", "mai", "juni", "juli", "august", "september", "oktober", "november", "desember"}
)

func norskDato(t time.Time) string {
	return fmt.Sprintf("%s %d. %s %d", ukedager[t.Weekday()], t.Day(), måneder[t.Month()-1], t.Year())
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
