package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeDNT svarer med ledige plasser per «hytte/fra-dato», og med navnet i
// <title> på hyttesiden.
type fakeDNT struct {
	mu    sync.Mutex
	free  map[string]int
	calls int
}

func (f *fakeDNT) set(cabin int, from string, n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.free[fmt.Sprintf("%d/%s", cabin, from)] = n
}

func (f *fakeDNT) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id, ok := strings.CutPrefix(r.URL.Path, "/hytte/"); ok {
		fmt.Fprintf(w, `<html><head><title>Hytte nr %s | DNT Hyttebestilling</title></head></html>`, id)
		return
	}
	f.calls++
	q := r.URL.Query()
	if q.Get("cabinId") == "999" {
		w.Write([]byte(`{"error":"Couldn't fetch web products"}`))
		return
	}
	n := f.free[q.Get("cabinId")+"/"+q.Get("fromDate")]
	fmt.Fprintf(w, `{"data":{"availability":[%d,0],"maxTotalUnitsThatCanBeAvailable":12}}`, n)
}

type sentMail struct {
	mu   sync.Mutex
	msgs []Message
}

func (s *sentMail) Send(_ context.Context, m Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.msgs = append(s.msgs, m)
	return nil
}

func (s *sentMail) subjects() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, m := range s.msgs {
		out = append(out, m.Subject)
	}
	return out
}

func setup(t *testing.T) (*fakeDNT, *Store, *Poller, *sentMail, *Mailer) {
	f := &fakeDNT{free: map[string]int{}}
	ts := httptest.NewServer(f)
	t.Cleanup(ts.Close)
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sent := &sentMail{}
	m := &Mailer{Sender: sent, Address: "varsel@example.com", To: []string{"kristine@example.com"}}
	p := &Poller{Store: store, DNT: &DNT{Base: ts.URL, Client: ts.Client()}, Notifier: m, Cadence: DefaultCadence}
	return f, store, p, sent, m
}

func future(days int) string { return time.Now().In(oslo).AddDate(0, 0, days).Format(dateLayout) }

// expire gjør alle sjekkene forfalt, som om tiden har gått.
func expire(store *Store, id int) {
	store.Update(id, func(w *Watch) {
		for _, c := range w.Checks {
			c.NextCheck = time.Time{}
		}
	})
}

func TestNotifyOnceOnTransition(t *testing.T) {
	f, store, p, sent, m := setup(t)
	ctx := context.Background()
	w, err := AddWatch(ctx, store, p, Watch{Guests: 2, Legs: []Leg{{CabinID: 1, Nights: 1}}, Starts: []string{future(40)}})
	if err != nil {
		t.Fatal(err)
	}
	if w.Legs[0].CabinName != "Hytte nr 1" {
		t.Errorf("navn %q", w.Legs[0].CabinName)
	}
	Announce(ctx, store, p, m, w, "")
	if got := sent.subjects(); len(got) != 1 || !strings.HasPrefix(got[0], "Følger med: Hytte nr 1") {
		t.Fatalf("fullt skal gi én bekreftelse: %v", got)
	}
	sent.msgs = nil

	f.set(1, future(40), 1) // én plass, men de er to
	expire(store, w.ID)
	p.Round(ctx)
	if len(sent.msgs) != 0 {
		t.Fatalf("for få plasser skal ikke varsle: %v", sent.subjects())
	}

	f.set(1, future(40), 2)
	expire(store, w.ID)
	p.Round(ctx)
	expire(store, w.ID)
	p.Round(ctx)
	if got := sent.subjects(); len(got) != 1 || !strings.HasPrefix(got[0], "Ledig: Hytte nr 1") || strings.Contains(got[0], "#") {
		t.Fatalf("vil ha ett varsel, fikk %v", got)
	}
	msg := sent.msgs[0]
	stop := fmt.Sprintf("mailto:varsel%%2Bh%d@example.com?", w.ID)
	if !strings.Contains(msg.HTML, "Bestill nå") || !strings.Contains(msg.HTML, stop) || msg.ReplyTo != fmt.Sprintf("varsel+h%d@example.com", w.ID) {
		t.Errorf("varselet mangler knappene eller svaradressen (%q):\n%s", msg.ReplyTo, msg.HTML)
	}
	if !strings.Contains(msg.Text, "hyttebestilling.dnt.no/hytte/1?adults=2&fromDate="+future(40)) {
		t.Errorf("varselet mangler bestillingslenken:\n%s", msg.Text)
	}
}

func TestRouteNeedsAllLegs(t *testing.T) {
	f, store, p, sent, _ := setup(t)
	ctx := context.Background()
	start := future(50)
	w, err := AddWatch(ctx, store, p, Watch{Guests: 1, Legs: []Leg{{CabinID: 1, Nights: 1}, {CabinID: 2, Offset: 1, Nights: 1}}, Starts: []string{start}})
	if err != nil {
		t.Fatal(err)
	}
	f.set(1, start, 3)
	expire(store, w.ID)
	p.Round(ctx)
	if len(sent.msgs) != 0 {
		t.Fatal("halve ruta skal ikke varsle")
	}
	f.set(2, future(51), 1)
	expire(store, w.ID)
	p.Round(ctx)
	if len(sent.msgs) != 1 || strings.Count(sent.msgs[0].Text, "hyttebestilling.dnt.no/hytte/") != 2 {
		t.Fatalf("vil ha ett varsel med to bestillingslenker: %+v", sent.msgs)
	}
}

func TestFlexibleNotifiesPerDate(t *testing.T) {
	f, store, p, sent, _ := setup(t)
	ctx := context.Background()
	w, err := AddWatch(ctx, store, p, Watch{Guests: 1, Flexible: true, Legs: []Leg{{CabinID: 1, Nights: 1}}, Starts: []string{future(60), future(61), future(62)}})
	if err != nil {
		t.Fatal(err)
	}
	f.set(1, future(61), 1)
	expire(store, w.ID)
	p.Round(ctx)
	f.set(1, future(62), 1)
	expire(store, w.ID)
	p.Round(ctx)
	if len(sent.msgs) != 2 {
		t.Fatalf("vil ha ett varsel per dato, fikk %v", sent.subjects())
	}
}

func TestCadenceOnlyCallsDue(t *testing.T) {
	f, store, p, _, _ := setup(t)
	ctx := context.Background()
	if _, err := AddWatch(ctx, store, p, Watch{Guests: 1, Legs: []Leg{{CabinID: 1, Nights: 1}}, Starts: []string{future(100)}}); err != nil {
		t.Fatal(err)
	}
	p.Round(ctx)
	before := f.calls
	p.Round(ctx) // nettopp sjekket; neste om 15 min
	if f.calls != before {
		t.Fatalf("sjekket for tidlig: %d kall", f.calls-before)
	}
	now := time.Now()
	for days, want := range map[int]time.Duration{5: 2 * time.Minute, 10: 2 * time.Minute, 20: 5 * time.Minute, 90: 15 * time.Minute} {
		if got := DefaultCadence.For(today(now).AddDate(0, 0, days), now); got != want {
			t.Errorf("%d dager: %v, vil ha %v", days, got, want)
		}
	}
}

func TestRejectsCabinWithoutWebBooking(t *testing.T) {
	_, store, p, _, _ := setup(t)
	_, err := AddWatch(context.Background(), store, p, Watch{Guests: 1, Legs: []Leg{{CabinID: 999, Nights: 1}}, Starts: []string{future(10)}})
	if err == nil || !strings.Contains(err.Error(), "ikke bestilles på nett") {
		t.Fatalf("err %v", err)
	}
	if len(store.List()) != 0 {
		t.Fatal("skulle ikke lagres")
	}
}

type recordingAlarm struct{ titles []string }

func (a *recordingAlarm) Raise(_ context.Context, title, _ string) error {
	a.titles = append(a.titles, title)
	return nil
}

func TestWatchdogRaisesOnce(t *testing.T) {
	_, store, p, _, _ := setup(t)
	alarm := &recordingAlarm{}
	p.Alarm = alarm
	now := time.Now()
	p.started = now.Add(-2 * time.Hour)
	p.watchdog(context.Background(), now, true)
	p.watchdog(context.Background(), now.Add(time.Minute), true)
	if len(alarm.titles) != 1 {
		t.Fatalf("vil ha én alarm, fikk %v", alarm.titles)
	}
	// Virker igjen, så brudd på nytt innen tolv timer: ingen ny alarm.
	p.lastSuccess.Store(now.Unix())
	p.watchdog(context.Background(), now.Add(2*time.Minute), true)
	p.watchdog(context.Background(), now.Add(3*time.Hour), true)
	if len(alarm.titles) != 1 {
		t.Fatalf("alarm innen tolv timer: %v", alarm.titles)
	}
	_ = store
}

func TestWebAddAndList(t *testing.T) {
	_, store, p, sent, m := setup(t)
	h := (&Server{Store: store, Poller: p, Mailer: m, Address: m.Address}).Routes()
	form := url.Values{"hytter": {"https://hyttebestilling.dnt.no/hytte/5"}, "dato": {future(30)}, "til": {future(32)}, "netter": {"1"}, "personer": {"2"}}
	req := httptest.NewRequest("POST", "/watches", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "ok=") {
		t.Fatalf("svar %d, Location %q", rec.Code, rec.Header().Get("Location"))
	}
	ws := store.List()
	if len(ws) != 1 || len(ws[0].Starts) != 3 || !ws[0].Flexible {
		t.Fatalf("overvåkinger: %+v", ws)
	}
	if got := sent.subjects(); len(got) != 1 || !strings.HasPrefix(got[0], "Følger med: Hytte nr 5") {
		t.Fatalf("vil ha bekreftelse, fikk %v", got)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if body := rec.Body.String(); !strings.Contains(body, "Hytte nr 5") || !strings.Contains(body, "Fullt") {
		t.Fatalf("forsiden viser ikke overvåkingen:\n%s", body)
	}
}
