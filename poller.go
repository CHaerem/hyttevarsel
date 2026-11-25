package main

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"sync/atomic"
	"time"
)

// Hvor lenge et nytt varsel for samme overvåking holdes tilbake. En plass
// som ligger i noens handlekurv (30 min hos DNT) kan forsvinne og komme
// tilbake; ett varsel per halvtime er nok.
const notifyCooldown = 30 * time.Minute

type Checker interface {
	Check(ctx context.Context, cabinID int, from, to time.Time) (Availability, error)
	CabinName(ctx context.Context, cabinID int) (string, error)
}

type Notifier interface {
	Notify(ctx context.Context, w Watch, a Availability) error
}

type Poller struct {
	Store    *Store
	DNT      Checker
	Notifier Notifier
	Interval time.Duration
	Spacing  time.Duration // pause mellom to kall mot DNT

	lastRound atomic.Int64 // unix-sekunder for siste fullførte runde
}

// Evaluate avgjør ny tilstand etter en vellykket sjekk, og om det skal varsles.
func Evaluate(w Watch, a Availability, now time.Time) (Watch, bool) {
	w.LastChecked = now
	w.LastError = ""
	w.Free = a.Free
	if a.Free < w.Guests {
		w.Status = StatusFull
		return w, false
	}
	notify := w.Status != StatusFree && now.Sub(w.LastNotified) >= notifyCooldown
	w.Status = StatusFree
	return w, notify
}

// CheckOne sjekker én overvåking, lagrer resultatet og varsler ved behov.
func (p *Poller) CheckOne(ctx context.Context, w Watch) {
	now := time.Now()
	a, err := p.DNT.Check(ctx, w.CabinID, w.FromDate(), w.ToDate())
	if err != nil {
		slog.Warn("sjekk feilet", "watch", w.ID, "cabin", w.CabinID, "err", err)
		p.Store.Update(w.ID, func(s *Watch) { s.LastChecked = now; s.LastError = err.Error() })
		return
	}
	next, notify := Evaluate(w, a, now)
	if notify {
		if err := p.Notifier.Notify(ctx, next, a); err != nil {
			// Status forblir den gamle, så neste runde prøver igjen.
			slog.Error("varsel feilet", "watch", w.ID, "err", err)
			p.Store.Update(w.ID, func(s *Watch) { s.LastChecked = now; s.Free = a.Free; s.LastError = "e-post feilet: " + err.Error() })
			return
		}
		next.LastNotified = now
		slog.Info("varslet", "watch", w.ID, "cabin", next.CabinName, "from", w.From, "free", a.Free)
	}
	p.Store.Update(w.ID, func(s *Watch) {
		s.Status, s.Free, s.LastChecked, s.LastError, s.LastNotified =
			next.Status, next.Free, next.LastChecked, next.LastError, next.LastNotified
	})
}

func (p *Poller) Round(ctx context.Context) {
	now := time.Now()
	for i, w := range p.Store.List() {
		if w.Expired(now) {
			if now.Sub(w.FromDate()) > 14*24*time.Hour {
				p.Store.Delete(w.ID)
			}
			continue
		}
		if i > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(p.Spacing):
			}
		}
		p.CheckOne(ctx, w)
	}
	p.lastRound.Store(time.Now().Unix())
}

func (p *Poller) Run(ctx context.Context) {
	for {
		p.Round(ctx)
		// ±20 % slik at kallene ikke treffer på faste klokkeslett.
		wait := time.Duration(float64(p.Interval) * (0.8 + 0.4*rand.Float64()))
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// Healthy er sann når en runde er fullført nylig nok.
func (p *Poller) Healthy(now time.Time) bool {
	last := p.lastRound.Load()
	if last == 0 {
		return true // første runde pågår
	}
	return now.Sub(time.Unix(last, 0)) < 3*p.Interval+10*time.Minute
}
