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
	mu       sync.Mutex
	free     map[string]int
	calls    int // nøyaktige kall (cabin-availability)
	calCalls int // kalenderkall
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
	q := r.URL.Query()
	if r.URL.Path == "/api/booking/availability-calendar" {
		// Én enhet per ledig plass, for hver natt i det spurte tidsrommet.
		f.calCalls++
		from, _ := time.Parse(dateLayout, q.Get("fromDate"))
		to, _ := time.Parse(dateLayout, q.Get("toDate"))
		var days []string
		for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
			n := f.free[q.Get("cabinId")+"/"+d.Format(dateLayout)]
			days = append(days, fmt.Sprintf(`{"date":"%sT00:00:00.000Z","products":[{"available":%d},{"available":0}]}`, d.Format(dateLayout), min(n, 1)))
		}
		fmt.Fprintf(w, `{"data":{"availabilityList":[%s]}}`, strings.Join(days, ","))
		return
	}
	f.calls++
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
	h := (&Server{Store: store, Poller: p, Address: m.Address}).Routes()
	form := url.Values{"hytte": {"https://hyttebestilling.dnt.no/hytte/5"}, "dato": {future(30)}, "til": {future(32)}, "netter": {"1"}, "personer": {"2"}}
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
	if len(sent.msgs) != 0 {
		t.Fatalf("siden viser resultatet, så ingen e-post: %v", sent.subjects())
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if body := rec.Body.String(); !strings.Contains(body, "Hytte nr 5") || !strings.Contains(body, "Fullt") {
		t.Fatalf("forsiden viser ikke overvåkingen:\n%s", body)
	}
}

// Mange datoer for samme hytte: ett kalenderkall per runde, og et nøyaktig
// kall bare der kalenderen viser noe ledig som er nytt.
func TestCalendarScreening(t *testing.T) {
	f, store, p, sent, _ := setup(t)
	ctx := context.Background()
	var starts []string
	for i := 40; i < 48; i++ {
		starts = append(starts, future(i))
	}
	w, err := AddWatch(ctx, store, p, Watch{Guests: 2, Flexible: true, Legs: []Leg{{CabinID: 50, Nights: 1}}, Starts: starts})
	if err != nil {
		t.Fatal(err)
	}
	due := func() { p.cal[50].next = time.Time{} }

	f.calls = 0
	p.Round(ctx)
	if f.calCalls != 1 || f.calls != 0 {
		t.Fatalf("fullt: vil ha 1 kalenderkall og 0 nøyaktige, fikk %d og %d", f.calCalls, f.calls)
	}
	got, _ := store.Get(w.ID)
	if c := got.Checks[checkKey(got.Legs[0], starts[3])]; c == nil || !c.Known || c.Free != 0 {
		t.Fatalf("fulle datoer skal være kjent som fulle: %+v", c)
	}
	p.Round(ctx)
	if f.calCalls != 1 {
		t.Fatalf("kalenderen ble hentet før den var forfalt: %d", f.calCalls)
	}

	f.set(50, starts[2], 1) // én seng ledig, men de er to
	due()
	p.Round(ctx)
	if f.calCalls != 2 || f.calls != 1 || len(sent.msgs) != 0 {
		t.Fatalf("én seng: kalender %d, nøyaktige %d, e-poster %v", f.calCalls, f.calls, sent.subjects())
	}
	due()
	p.Round(ctx)
	if f.calls != 1 {
		t.Fatalf("uendret kalender skal ikke gi nytt nøyaktig kall: %d", f.calls)
	}

	f.set(50, starts[2], 2)
	f.set(50, starts[5], 4)
	due()
	p.Round(ctx)
	// starts[2] har samme kalenderbilde (én enhet ledig), så bare starts[5] sjekkes.
	if f.calls != 2 || len(sent.msgs) != 1 || !strings.Contains(sent.msgs[0].Text, "fromDate="+starts[5]) {
		t.Fatalf("vil ha ett varsel for %s: nøyaktige %d, %v", starts[5], f.calls, sent.subjects())
	}
}
