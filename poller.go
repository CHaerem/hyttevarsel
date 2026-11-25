package main

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
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
	Calendar(ctx context.Context, cabinID int, from, to time.Time) (map[string]int, error)
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
	Names    *Directory    // hyttenavn uten å hente hyttesiden; valgfri
	Spacing  time.Duration // pause mellom to kall mot DNT
	Cadence  Cadence

	lastRound   atomic.Int64 // unix-sekunder for siste gjennomgang
	lastSuccess atomic.Int64 // unix-sekunder for siste vellykkede kall
	failing     atomic.Bool
	started     time.Time // når pollerens første runde gikk
	lastNameTry time.Time
	cal         map[int]*calState
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
	known   bool // siste sjekk ga et svar
}

// calendarMin er hvor mange opphold som må følges for samme hytte før
// kalenderen brukes: ett kall per hytte i stedet for ett per dato.
const calendarMin = 4

// calState er det vi husker om en hyttes kalender mellom rundene.
type calState struct {
	next time.Time
	sig  map[string]string // sjekknøkkel → ledige enheter per natt sist
}

// plan deler det som skal sjekkes i to: enkeltkall som er forfalt, og
// hytter med mange datoer der kalenderen er forfalt.
func (p *Poller) plan(now time.Time) (direct []dueCheck, calendars map[int][]dueCheck) {
	byCabin := map[int][]dueCheck{}
	for _, w := range p.Store.List() {
		for _, s := range w.ActiveStarts(now) {
			for _, l := range w.Legs {
				d := dueCheck{watchID: w.ID, key: checkKey(l, s), leg: l, start: s}
				if c := w.Checks[d.key]; c != nil {
					d.next, d.known = c.NextCheck, c.Known && c.LastError == ""
				}
				byCabin[l.CabinID] = append(byCabin[l.CabinID], d)
			}
		}
	}
	calendars = map[int][]dueCheck{}
	for cabin, checks := range byCabin {
		keys := map[string]bool{}
		for _, d := range checks {
			keys[d.key] = true
		}
		if len(keys) >= calendarMin {
			if st := p.cal[cabin]; st == nil || !st.next.After(now) {
				calendars[cabin] = checks
			}
			continue
		}
		for _, d := range checks {
			if !d.next.After(now) {
				direct = append(direct, d)
			}
		}
	}
	slices.SortFunc(direct, func(a, b dueCheck) int { return a.next.Compare(b.next) })
	return direct, calendars
}

// screen henter kalenderen for én hytte. Netter uten ledige enheter settes
// som fulle uten flere kall. Opphold der noe er ledig og bildet har endret
// seg siden sist, returneres for et nøyaktig kall (Check), som teller
// sengeplasser.
func (p *Poller) screen(ctx context.Context, cabin int, checks []dueCheck) (exact []dueCheck) {
	now := time.Now()
	if p.cal == nil {
		p.cal = map[int]*calState{}
	}
	st := p.cal[cabin]
	if st == nil {
		st = &calState{}
		p.cal[cabin] = st
	}
	lo, hi := checks[0].leg.Stay(checks[0].start)
	for _, d := range checks {
		from, to := d.leg.Stay(d.start)
		if from.Before(lo) {
			lo = from
		}
		if to.After(hi) {
			hi = to
		}
	}
	st.next = now.Add(p.Cadence.For(lo, now))
	days, err := p.DNT.Calendar(ctx, cabin, lo, hi)
	if err != nil {
		slog.Warn("kalenderen feilet", "cabin", cabin, "err", err)
		return nil
	}
	p.lastSuccess.Store(now.Unix())

	sigs := map[string]string{}
	full := map[int][]string{} // overvåking → sjekknøkler som er fulle
	for _, d := range checks {
		from, to := d.leg.Stay(d.start)
		var sig strings.Builder
		covered, free := true, true
		for n := from; n.Before(to); n = n.AddDate(0, 0, 1) {
			units, ok := days[n.Format(dateLayout)]
			if !ok {
				covered = false
				break
			}
			fmt.Fprintf(&sig, "%d,", units)
			free = free && units > 0
		}
		switch {
		case !covered: // kalenderen rakk ikke så langt
			exact = append(exact, d)
		case !free:
			full[d.watchID] = append(full[d.watchID], d.key)
			sigs[d.key] = sig.String()
		default:
			if !d.known || st.sig[d.key] != sig.String() {
				exact = append(exact, d)
			}
			sigs[d.key] = sig.String()
		}
	}
	st.sig = sigs
	slog.Info("kalender", "cabin", cabin, "opphold", len(checks), "fulle", len(checks)-len(exact), "sjekkes", len(exact))
	for id, keys := range full {
		p.Store.Update(id, func(w *Watch) {
			for _, k := range keys {
				c := w.Checks[k]
				if c == nil {
					c = &Check{}
					w.Checks[k] = c
				}
				c.Free, c.Known, c.LastError, c.LastChecked, c.NextCheck = 0, true, "", now, st.next
			}
		})
	}
	return exact
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

// Round gjør alle kallene som er forfalt, ett om gangen: først kalenderne
// for hytter med mange datoer, så enkeltkallene.
func (p *Poller) Round(ctx context.Context) {
	now := time.Now()
	if p.started.IsZero() {
		p.started = now
	}
	p.prune(now)
	direct, calendars := p.plan(now)
	touched := map[int]bool{}
	calls := 0
	pause := func() bool { // ett kall om gangen, med pause mellom
		calls++
		return calls == 1 || p.sleep(ctx)
	}
	for cabin, checks := range calendars {
		if !pause() {
			return
		}
		direct = append(direct, p.screen(ctx, cabin, checks)...)
		for _, d := range checks {
			touched[d.watchID] = true
		}
	}
	for _, d := range direct {
		if !pause() {
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

// cabinName gir navnet fra hyttelista, eller henter det fra hyttesiden.
func (p *Poller) cabinName(ctx context.Context, id int) (string, error) {
	if name := p.Names.Name(id); name != "" {
		return name, nil
	}
	return p.DNT.CabinName(ctx, id)
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
			name, err := p.cabinName(ctx, l.CabinID)
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
