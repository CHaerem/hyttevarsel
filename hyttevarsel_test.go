package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestParseCabinID(t *testing.T) {
	for in, want := range map[string]int{
		"https://hyttebestilling.dnt.no/hytte/101075912":                         101075912,
		"https://hyttebestilling.dnt.no/hytte/101075912?fromDate=2026-12-01&x=9": 101075912,
		" 101022071 ": 101022071,
		"hyttebestilling.dnt.no/hytte/101022071/": 101022071,
	} {
		if got, ok := ParseCabinID(in); !ok || got != want {
			t.Errorf("ParseCabinID(%q) = %d, %v; vil ha %d", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "Skåla", "https://ut.no/", "https://hyttebestilling.dnt.no/kart?value=1234"} {
		if _, ok := ParseCabinID(in); ok {
			t.Errorf("ParseCabinID(%q) skulle feilet", in)
		}
	}
}

func TestEvaluate(t *testing.T) {
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, oslo)
	w := Watch{Guests: 2}

	w, notify := Evaluate(w, Availability{Free: 1}, now)
	if notify || w.Status != StatusFull {
		t.Fatalf("1 ledig for 2 personer: status %q, varsel %v", w.Status, notify)
	}
	w, notify = Evaluate(w, Availability{Free: 2}, now)
	if !notify || w.Status != StatusFree {
		t.Fatalf("full → ledig skal varsle: status %q, varsel %v", w.Status, notify)
	}
	w.LastNotified = now
	if _, notify = Evaluate(w, Availability{Free: 3}, now.Add(time.Hour)); notify {
		t.Fatal("fortsatt ledig skal ikke varsle igjen")
	}
	// Plassen forsvinner (noens handlekurv) og kommer tilbake raskt.
	w, _ = Evaluate(w, Availability{Free: 0}, now.Add(5*time.Minute))
	if _, notify = Evaluate(w, Availability{Free: 2}, now.Add(10*time.Minute)); notify {
		t.Fatal("ny ledighet innen nedkjølingen skal ikke varsle")
	}
	if _, notify = Evaluate(w, Availability{Free: 2}, now.Add(time.Hour)); !notify {
		t.Fatal("ny ledighet etter nedkjølingen skal varsle")
	}
	// Første sjekk av en overvåking som allerede er ledig, varsler.
	if _, notify = Evaluate(Watch{Guests: 1}, Availability{Free: 1}, now); !notify {
		t.Fatal("ukjent → ledig skal varsle")
	}
}

func TestExpired(t *testing.T) {
	w := Watch{From: "2026-11-25", Nights: 1}
	if w.Expired(time.Date(2026, 11, 25, 23, 0, 0, 0, oslo)) {
		t.Error("ankomstdagen selv er ikke passert")
	}
	if !w.Expired(time.Date(2026, 11, 26, 0, 30, 0, 0, oslo)) {
		t.Error("dagen etter er passert")
	}
}

// Datoen ligger alltid fram i tid, så testene ikke går ut på dato.
var testFrom = time.Now().In(oslo).AddDate(0, 0, 30).Format(dateLayout)
var testTo = time.Now().In(oslo).AddDate(0, 0, 32).Format(dateLayout)

func fakeDNT(t *testing.T, availability string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/hytte/101075912" {
			w.Write([]byte(`<html><head><meta charset="utf-8"/><title>Eigerøy fyr | DNT Hyttebestilling</title></head></html>`))
			return
		}
		q := r.URL.Query()
		if r.URL.Path != "/api/booking/cabin-availability" || q.Get("cabinId") != "101075912" ||
			q.Get("fromDate") != testFrom || q.Get("toDate") != testTo {
			t.Errorf("uventet kall: %s", r.URL)
		}
		w.Write([]byte(`{"data":{"availability":` + availability + `,"maxTotalUnitsThatCanBeAvailable":12}}`))
	}))
}

type recordingNotifier struct{ got []Watch }

func (n *recordingNotifier) Notify(_ context.Context, w Watch, _ Availability) error {
	n.got = append(n.got, w)
	return nil
}

func TestCheckOneNotifiesOnce(t *testing.T) {
	ts := fakeDNT(t, "[0,2,0]")
	defer ts.Close()
	store, _ := OpenStore(t.TempDir())
	notifier := &recordingNotifier{}
	p := &Poller{Store: store, DNT: &DNT{Base: ts.URL, Client: ts.Client()}, Notifier: notifier, Interval: time.Minute}

	w, _ := store.Add(Watch{CabinID: 101075912, CabinName: "Eigerøy fyr", From: testFrom, Nights: 2, Guests: 2})
	p.CheckOne(context.Background(), w)
	p.CheckOne(context.Background(), store.List()[0])
	if len(notifier.got) != 1 {
		t.Fatalf("vil ha ett varsel, fikk %d", len(notifier.got))
	}
	got := store.List()[0]
	if got.Status != StatusFree || got.CabinName != "Eigerøy fyr" || got.LastNotified.IsZero() {
		t.Fatalf("lagret tilstand: %+v", got)
	}
	// Tilstanden overlever en omstart.
	reopened, _ := OpenStore(store.path[:strings.LastIndex(store.path, "/")])
	if reopened.List()[0].Status != StatusFree {
		t.Fatal("tilstanden ble ikke lagret")
	}
}

func TestAddViaForm(t *testing.T) {
	ts := fakeDNT(t, "[0,0,0]")
	defer ts.Close()
	store, _ := OpenStore(t.TempDir())
	p := &Poller{Store: store, DNT: &DNT{Base: ts.URL, Client: ts.Client()}, Notifier: &recordingNotifier{}, Interval: 5 * time.Minute}
	h := (&Server{Store: store, Poller: p}).Routes()

	form := url.Values{"hytte": {"https://hyttebestilling.dnt.no/hytte/101075912"}, "dato": {testFrom}, "netter": {"2"}, "personer": {"1"}}
	req := httptest.NewRequest("POST", "/watches", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "ok=") {
		t.Fatalf("svar %d, Location %q", rec.Code, rec.Header().Get("Location"))
	}
	if ws := store.List(); len(ws) != 1 || ws[0].Status != StatusFull {
		t.Fatalf("overvåkinger: %+v", ws)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if body := rec.Body.String(); !strings.Contains(body, "Eigerøy fyr") || !strings.Contains(body, "Fullt") {
		t.Fatalf("forsiden viser ikke overvåkingen:\n%s", body)
	}
}
