package main

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sync/atomic"
	"time"
)

// Hvor lenge et nytt varsel for samme dato holdes tilbake. En plass som
// ligger i noens handlekurv (30 min hos DNT) kan forsvinne og komme
// tilbake; ett varsel per halvtime er nok.
const notifyCooldown = 30 * time.Minute

type Checker interface {
	Check(ctx context.Context, cabinID int, from, to time.Time) (Availability, error)
	CabinName(ctx context.Context, cabinID int) (string, error)
}

// Notifier er det poller og innboks trenger av e-posten (mail.go).
type Notifier interface {
	Available(ctx context.Context, w Watch, starts []string) error
}

// Alarm sier fra til plattformen når sjekkene har sluttet å virke.
type Alarm interface {
	Raise(ctx context.Context, title, text string) error
}

type Poller struct {
	Store    *Store
	DNT      Checker
	Notifier Notifier
	Alarm    Alarm         // valgfri
	Spacing  time.Duration // pause mellom to kall mot DNT
	Cadence  Cadence

	lastRound   atomic.Int64 // unix-sekunder for siste gjennomgang
	lastSuccess atomic.Int64 // unix-sekunder for siste vellykkede kall
	failing     atomic.Bool
	started     time.Time // når pollerens første runde gikk
	lastNameTry time.Time
}

// Cadence bestemmer hvor ofte et opphold sjekkes, etter dager til ankomst.
// De fleste avbestillingene kommer rett før den gratis fristen (5 dager
// på selvbetjente hytter, 8 på betjente), så der sjekkes det tett.
type Cadence struct {
	Near, Mid, Far time.Duration
}

var DefaultCadence = Cadence{Near: 2 * time.Minute, Mid: 5 * time.Minute, Far: 15 * time.Minute}

func (c Cadence) For(arrival, now time.Time) time.Duration {
	days := int(arrival.Sub(today(now)).Hours() / 24)
	switch {
	case days <= 10:
		return c.Near
	case days <= 30:
		return c.Mid
	default:
		return c.Far
	}
}

type dueCheck struct {
	watchID int
	key     string
	leg     Leg
	start   string
	next    time.Time
}

func (p *Poller) due(now time.Time) []dueCheck {
	var out []dueCheck
	for _, w := range p.Store.List() {
		for _, s := range w.ActiveStarts(now) {
			for _, l := range w.Legs {
				k := checkKey(l, s)
				c := w.Checks[k]
				if c == nil || !c.NextCheck.After(now) {
					var next time.Time
					if c != nil {
						next = c.NextCheck
					}
					out = append(out, dueCheck{w.ID, k, l, s, next})
				}
			}
		}
	}
	slices.SortFunc(out, func(a, b dueCheck) int { return a.next.Compare(b.next) })
	return out
}

// CheckWatch sjekker alle oppholdene i én overvåking med en gang (brukes når
// den legges inn). Varsler sendes først av Evaluate, etter bekreftelsen.
func (p *Poller) CheckWatch(ctx context.Context, id int) {
	w, ok := p.Store.Get(id)
	if !ok {
		return
	}
	for i, s := range w.ActiveStarts(time.Now()) {
		for j, l := range w.Legs {
			if i+j > 0 && !p.sleep(ctx) {
				return
			}
			p.checkOne(ctx, dueCheck{watchID: id, key: checkKey(l, s), leg: l, start: s})
		}
	}
}

func (p *Poller) sleep(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(p.Spacing):
		return true
	}
}

func (p *Poller) checkOne(ctx context.Context, d dueCheck) {
	now := time.Now()
	from, to := d.leg.Stay(d.start)
	a, err := p.DNT.Check(ctx, d.leg.CabinID, from, to)
	next := now.Add(p.Cadence.For(from, now))
	if err != nil {
		slog.Warn("sjekk feilet", "watch", d.watchID, "cabin", d.leg.CabinID, "from", from.Format(dateLayout), "err", err)
	} else {
		p.lastSuccess.Store(now.Unix())
	}
	p.Store.Update(d.watchID, func(w *Watch) {
		c := w.Checks[d.key]
		if c == nil {
			c = &Check{}
			w.Checks[d.key] = c
		}
		c.LastChecked, c.NextCheck = now, next
		if err != nil {
			c.LastError = err.Error()
			return
		}
		c.LastError, c.Free, c.Known = "", a.Free, true
	})
}

// MarkNotified setter startdatoene som ledige og varslet, når varselet er
// sendt utenom Evaluate (svaret på en hytte som alt var ledig).
func (p *Poller) MarkNotified(id int, starts []string) {
	now := time.Now()
	p.Store.Update(id, func(w *Watch) {
		for _, s := range starts {
			w.Options[s] = &Option{Free: true, LastNotified: now}
		}
	})
}

// Transition avgjør om en startdato har blitt ledig, og om det skal varsles.
func Transition(o Option, free bool, now time.Time) (Option, bool) {
	if !free {
		o.Free = false
		return o, false
	}
	notify := !o.Free && now.Sub(o.LastNotified) >= notifyCooldown
	o.Free = true
	return o, notify
}

// evaluate oppdaterer startdatoene i en overvåking og varsler om dem som
// nettopp ble ledige, samlet i én e-post.
func (p *Poller) Evaluate(ctx context.Context, id int) {
	now := time.Now()
	w, ok := p.Store.Get(id)
	if !ok {
		return
	}
	var newly []string
	next := map[string]Option{}
	for _, s := range w.ActiveStarts(now) {
		// En dato der en av hyttene ikke kunne sjekkes nå, står som før.
		unknown := false
		for _, l := range w.Legs {
			if c := w.Checks[checkKey(l, s)]; c == nil || !c.Known || c.LastError != "" {
				unknown = true
			}
		}
		if unknown {
			continue
		}
		o := Option{}
		if w.Options[s] != nil {
			o = *w.Options[s]
		}
		o, notify := Transition(o, w.OptionFree(s), now)
		next[s] = o
		if notify {
			newly = append(newly, s)
		}
	}
	notified := false
	if len(newly) > 0 {
		if err := p.Notifier.Available(ctx, w, newly); err != nil {
			// Datoene forblir «ikke ledig», så neste runde prøver igjen.
			slog.Error("varsel feilet", "watch", id, "err", err)
			for _, s := range newly {
				delete(next, s)
			}
		} else {
			notified = true
			slog.Info("varslet", "watch", id, "title", w.Title(), "starts", newly)
		}
	}
	p.Store.Update(id, func(w *Watch) {
		for s, o := range next {
			if notified && slices.Contains(newly, s) {
				o.LastNotified = now
			}
			oo := o
			w.Options[s] = &oo
		}
	})
}

// Round gjør alle kallene som er forfalt, ett om gangen.
func (p *Poller) Round(ctx context.Context) {
	now := time.Now()
	if p.started.IsZero() {
		p.started = now
	}
	p.prune(now)
	touched := map[int]bool{}
	for i, d := range p.due(now) {
		if i > 0 && !p.sleep(ctx) {
			return
		}
		p.checkOne(ctx, d)
		touched[d.watchID] = true
	}
	for id := range touched {
		p.Evaluate(ctx, id)
	}
	p.fillNames(ctx)
	p.lastRound.Store(time.Now().Unix())
	p.watchdog(ctx, time.Now(), len(touched) > 0)
}

// fillNames prøver igjen å finne navn som manglet da hytta ble lagt inn.
func (p *Poller) fillNames(ctx context.Context) {
	if time.Since(p.lastNameTry) < time.Hour {
		return // en hyttesides er ~400 kB; ikke hver halve minutt
	}
	p.lastNameTry = time.Now()
	for _, w := range p.Store.List() {
		for i, l := range w.Legs {
			if l.CabinName != "" {
				continue
			}
			name, err := p.DNT.CabinName(ctx, l.CabinID)
			if err != nil {
				continue
			}
			p.Store.Update(w.ID, func(w *Watch) { w.Legs[i].CabinName = name })
		}
	}
}

// prune fjerner overvåkinger to uker etter siste mulige startdato.
func (p *Poller) prune(now time.Time) {
	for _, w := range p.Store.List() {
		if w.Expired(now) && now.Sub(w.LastStart()) > 14*24*time.Hour {
			p.Store.Delete(w.ID)
		}
	}
}

// watchdog sier fra til varselagenten når ingen kall har lyktes på en
// time, og én gang til når det virker igjen. Høyst én alarm per tolv
// timer, som de andre vaktene.
func (p *Poller) watchdog(ctx context.Context, now time.Time, active bool) {
	if p.Alarm == nil || !active {
		return
	}
	since := p.started
	if ls := p.lastSuccess.Load(); ls != 0 {
		since = time.Unix(ls, 0)
	}
	broken := now.Sub(since) > time.Hour
	if broken == p.failing.Load() {
		return
	}
	if !broken {
		p.failing.Store(false)
		slog.Info("DNT-sjekkene virker igjen")
		return
	}
	var lastAlarm time.Time
	p.Store.Meta(func(_, la *time.Time) { lastAlarm = *la })
	if now.Sub(lastAlarm) < 12*time.Hour {
		p.failing.Store(true)
		return
	}
	text := fmt.Sprintf("Ingen kall mot hyttebestilling.dnt.no har lyktes siden %s. "+
		"Kristine får ikke varsler før dette er fikset. Se loggen til deploymenten "+
		"hyttevarsel i namespacet hyttevarsel; endepunktet er beskrevet i README-en i CHaerem/hyttevarsel.",
		since.In(oslo).Format("02.01. kl. 15:04"))
	if err := p.Alarm.Raise(ctx, "Hyttevarselet når ikke DNT", text); err != nil {
		slog.Error("alarm feilet", "err", err)
		return
	}
	p.failing.Store(true)
	p.Store.Meta(func(_, la *time.Time) { *la = now })
}

func (p *Poller) Run(ctx context.Context) {
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		p.Round(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// Healthy er sann når løkka har gått nylig. Feil hos DNT gjør den ikke
// usunn (en omstart hjelper ikke); de går til varselagenten.
func (p *Poller) Healthy(now time.Time) bool {
	last := p.lastRound.Load()
	return last == 0 || now.Sub(time.Unix(last, 0)) < 15*time.Minute
}
