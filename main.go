// hyttevarsel følger med på fullbookede DNT-hytter og sender e-post når
// en avbestilling gjør plass. Se README.md.
package main

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/base64"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // Europe/Oslo uten tzdata i imaget
)

var oslo = mustLoadLocation("Europe/Oslo")

func mustLoadLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

	store, err := OpenStore(cmp.Or(os.Getenv("DATA_DIR"), "data"))
	if err != nil {
		slog.Error("kan ikke åpne lagringen", "err", err)
		os.Exit(1)
	}

	user, password := os.Getenv("SMTP_USER"), os.Getenv("SMTP_PASSWORD")
	// Uten navn viser innboksen hennes bare adressen.
	from := cmp.Or(os.Getenv("MAIL_FROM"), "Hyttevarsel <"+user+">")
	address := addrOnly(from)
	mailer := &Mailer{
		Sender:  LogSender{},
		Address: address,
		To:      splitList(os.Getenv("MAIL_TO")),
		PageURL: cmp.Or(os.Getenv("PAGE_URL"), "https://chaerem.github.io/hyttevarsel/"),
	}
	if host := os.Getenv("SMTP_HOST"); host != "" {
		mailer.Sender = &SMTPSender{
			Host: host, Port: cmp.Or(os.Getenv("SMTP_PORT"), "587"),
			User: user, Password: password,
			From: from,
		}
	} else {
		slog.Warn("SMTP_HOST er ikke satt; e-post skrives bare til loggen")
	}

	cadence := DefaultCadence
	if s := os.Getenv("POLL_CADENCE"); s != "" {
		if cadence, err = parseCadence(s); err != nil {
			slog.Error("POLL_CADENCE", "err", err)
			os.Exit(1)
		}
	}
	dnt := NewDNT()
	cabins := OpenDirectory(cmp.Or(os.Getenv("DATA_DIR"), "data"), dnt)
	poller := &Poller{Store: store, DNT: dnt, Notifier: mailer, Spacing: 3 * time.Second, Cadence: cadence, Names: cabins}
	if dir := os.Getenv("VARSEL_AGENT_DIR"); dir != "" {
		poller.Alarm = &VarselAgent{URLFile: dir + "/url", TokenFile: dir + "/token", Client: &http.Client{Timeout: 30 * time.Second}}
	}

	// SECRET_LINK=on legger siden bak en hemmelig personlig lenke (web.go).
	// Lenken lages her første gang og ligger i volumet, så den aldri går
	// gjennom git, chat eller loggen. SEND_LINK=on sender den til MAIL_TO
	// når siden svarer på BASE_URL.
	token, appURL := "", ""
	if os.Getenv("SECRET_LINK") == "on" {
		if token, err = loadOrCreateToken(cmp.Or(os.Getenv("DATA_DIR"), "data")); err != nil {
			slog.Error("kan ikke lage den hemmelige lenken", "err", err)
			os.Exit(1)
		}
		if base := strings.TrimRight(os.Getenv("BASE_URL"), "/"); base != "" {
			appURL = base + "/" + token + "/"
			mailer.AppURL = func() string {
				if store.LinkSent() {
					return appURL
				}
				return ""
			}
		} else {
			slog.Warn("BASE_URL mangler; lenken til Mine varsler kan ikke sendes på e-post")
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if appURL != "" && os.Getenv("SEND_LINK") == "on" && os.Getenv("SMTP_HOST") != "" && len(mailer.To) > 0 {
		health := strings.TrimRight(os.Getenv("BASE_URL"), "/") + "/healthz"
		go RunLink(ctx, store, mailer, appURL, health, &http.Client{Timeout: 15 * time.Second})
	}
	go poller.Run(ctx)
	go cabins.Run(ctx)
	go RunSummary(ctx, store, mailer)
	if os.Getenv("SMTP_HOST") != "" && len(mailer.To) > 0 && os.Getenv("WELCOME") != "off" {
		go RunWelcome(ctx, store, mailer)
	}
	if host := os.Getenv("IMAP_HOST"); host != "" {
		inbox := &Inbox{
			Addr: host + ":" + cmp.Or(os.Getenv("IMAP_PORT"), "993"),
			User: user, Password: password,
			AuthServ: cmp.Or(os.Getenv("IMAP_AUTHSERV"), "mx.google.com"),
		}
		allowed := splitList(cmp.Or(os.Getenv("ALLOWED_SENDERS"), os.Getenv("MAIL_TO")))
		bookingDomains := splitList(cmp.Or(os.Getenv("BOOKING_DOMAINS"), "dnt.no,visbook.com"))
		go RunInbox(ctx, inbox, &MailHandler{Store: store, Poller: poller, Mailer: mailer, Allowed: allowed, BookingDomains: bookingDomains})
	} else {
		slog.Warn("IMAP_HOST er ikke satt; innboksen leses ikke")
	}

	srv := &http.Server{
		Addr:              cmp.Or(os.Getenv("LISTEN"), ":8080"),
		Handler:           (&Server{Store: store, Poller: poller, Cabins: cabins, Address: address, Token: token}).Routes(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      90 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    16 << 10,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}()
	slog.Info("lytter", "addr", srv.Addr, "cadence", cadence, "overvåkinger", len(store.List()), "hemmelig lenke", token != "")
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		slog.Error("server", "err", err)
		os.Exit(1)
	}
}

// loadOrCreateToken gir den hemmelige delen av lenken: 32 tilfeldige byte.
func loadOrCreateToken(dir string) (string, error) {
	path := filepath.Join(dir, "lenke")
	if b, err := os.ReadFile(path); err == nil {
		if t := strings.TrimSpace(string(b)); len(t) >= 40 {
			return t, nil
		}
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	t := base64.RawURLEncoding.EncodeToString(raw)
	return t, os.WriteFile(path, []byte(t+"\n"), 0o600)
}

// parseCadence leser «2m,5m,15m» (≤10 dager, ≤30 dager, lenger fram).
func parseCadence(s string) (Cadence, error) {
	parts := splitList(s)
	var d [3]time.Duration
	if len(parts) != 3 {
		return Cadence{}, errInvalidCadence
	}
	for i, p := range parts {
		v, err := time.ParseDuration(p)
		if err != nil || v < time.Minute {
			return Cadence{}, errInvalidCadence
		}
		d[i] = v
	}
	return Cadence{Near: d[0], Mid: d[1], Far: d[2]}, nil
}

var errInvalidCadence = errorString("POLL_CADENCE må være tre varigheter på minst 1m, f.eks. 2m,5m,15m")

type errorString string

func (e errorString) Error() string { return string(e) }

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
