// hyttevarsel følger med på fullbookede DNT-hytter og sender e-post når
// en avbestilling gjør plass. Se README.md.
package main

import (
	"cmp"
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
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
	address := cmp.Or(addrOnly(os.Getenv("MAIL_FROM")), user)
	mailer := &Mailer{
		Sender:  LogSender{},
		Address: address,
		To:      splitList(os.Getenv("MAIL_TO")),
		BaseURL: os.Getenv("BASE_URL"),
	}
	if host := os.Getenv("SMTP_HOST"); host != "" {
		mailer.Sender = &SMTPSender{
			Host: host, Port: cmp.Or(os.Getenv("SMTP_PORT"), "587"),
			User: user, Password: password,
			From: cmp.Or(os.Getenv("MAIL_FROM"), user),
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
	poller := &Poller{Store: store, DNT: NewDNT(), Notifier: mailer, Spacing: 3 * time.Second, Cadence: cadence}
	if dir := os.Getenv("VARSEL_AGENT_DIR"); dir != "" {
		poller.Alarm = &VarselAgent{URLFile: dir + "/url", TokenFile: dir + "/token", Client: &http.Client{Timeout: 30 * time.Second}}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go poller.Run(ctx)
	go RunSummary(ctx, store, mailer)
	if host := os.Getenv("IMAP_HOST"); host != "" {
		inbox := &Inbox{
			Addr: host + ":" + cmp.Or(os.Getenv("IMAP_PORT"), "993"),
			User: user, Password: password,
			AuthServ: cmp.Or(os.Getenv("IMAP_AUTHSERV"), "mx.google.com"),
		}
		allowed := splitList(cmp.Or(os.Getenv("ALLOWED_SENDERS"), os.Getenv("MAIL_TO")))
		go RunInbox(ctx, inbox, &MailHandler{Store: store, Poller: poller, Mailer: mailer, Allowed: allowed})
	} else {
		slog.Warn("IMAP_HOST er ikke satt; innboksen leses ikke")
	}

	srv := &http.Server{
		Addr:              cmp.Or(os.Getenv("LISTEN"), ":8080"),
		Handler:           (&Server{Store: store, Poller: poller, Mailer: mailer, Address: address}).Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}()
	slog.Info("lytter", "addr", srv.Addr, "cadence", cadence)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		slog.Error("server", "err", err)
		os.Exit(1)
	}
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
