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

	interval, err := time.ParseDuration(cmp.Or(os.Getenv("POLL_INTERVAL"), "5m"))
	if err != nil || interval < time.Minute {
		slog.Error("POLL_INTERVAL må være minst 1m", "value", os.Getenv("POLL_INTERVAL"))
		os.Exit(1)
	}
	store, err := OpenStore(cmp.Or(os.Getenv("DATA_DIR"), "data"))
	if err != nil {
		slog.Error("kan ikke åpne lagringen", "err", err)
		os.Exit(1)
	}

	var notifier Notifier = LogNotifier{}
	if host := os.Getenv("SMTP_HOST"); host != "" {
		notifier = &SMTPNotifier{
			Host:     host,
			Port:     cmp.Or(os.Getenv("SMTP_PORT"), "587"),
			User:     os.Getenv("SMTP_USER"),
			Password: os.Getenv("SMTP_PASSWORD"),
			From:     cmp.Or(os.Getenv("MAIL_FROM"), os.Getenv("SMTP_USER")),
			To:       splitList(os.Getenv("MAIL_TO")),
			BaseURL:  os.Getenv("BASE_URL"),
		}
	} else {
		slog.Warn("SMTP_HOST er ikke satt; varsler skrives bare til loggen")
	}

	poller := &Poller{Store: store, DNT: NewDNT(), Notifier: notifier, Interval: interval, Spacing: 3 * time.Second}
	srv := &http.Server{
		Addr:              cmp.Or(os.Getenv("LISTEN"), ":8080"),
		Handler:           (&Server{Store: store, Poller: poller}).Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go poller.Run(ctx)
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}()
	slog.Info("lytter", "addr", srv.Addr, "interval", interval)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		slog.Error("server", "err", err)
		os.Exit(1)
	}
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
