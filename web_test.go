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

type fakeList []Cabin

func (f fakeList) CabinList(context.Context) ([]Cabin, error) { return f, nil }

func post(h http.Handler, path string, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func page(h http.Handler, target string) string {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", target, nil))
	return rec.Body.String()
}

func TestMineVarsler(t *testing.T) {
	f, store, p, sent, m := setup(t)
	p.Spacing = 0
	dir := OpenDirectory(t.TempDir(), fakeList{
		{ID: 71, Name: "Lille Tømtehytta", Area: "Nordmarka"},
		{ID: 72, Name: "Tømtehytta", Area: "Nordmarka"},
		{ID: 73, Name: "Skardstøl", Area: "Jotunheimen"},
		{ID: 74, Name: "Skardstøl", Area: "Rondane"},
	})
	if err := dir.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	p.Names = dir
	h := (&Server{Store: store, Poller: p, Cabins: dir, Address: m.Address}).Routes()

	if body := page(h, "/"); !strings.Contains(body, "Du følger ikke med på noen hytter ennå") || !strings.Contains(body, `<option value="Lille Tømtehytta, Nordmarka">`) {
		t.Fatalf("tom side uten hyttelista:\n%s", body)
	}

	// Navn fra lista, én dato. Hytta er ledig, og det vises på siden uten e-post.
	f.set(71, future(20), 5)
	rec := post(h, "/watches", url.Values{"hytte": {"Lille Tømtehytta, Nordmarka"}, "dato": {future(20)}, "personer": {"2"}})
	if loc := rec.Header().Get("Location"); rec.Code != http.StatusSeeOther || !strings.Contains(loc, "ok=") {
		t.Fatalf("svar %d %q", rec.Code, loc)
	}
	body := page(h, "/")
	if !strings.Contains(body, "Lille Tømtehytta") || !strings.Contains(body, "Ledig nå") || !strings.Contains(body, "hytte/71?adults=2&amp;fromDate="+future(20)) {
		t.Fatalf("siden viser ikke ledig hytte med bestillingslenke:\n%s", body)
	}
	if len(sent.msgs) != 0 {
		t.Fatalf("ingen e-post når hun la den inn på siden: %v", sent.subjects())
	}
	// Datoene blir stående til neste hytte.
	if !strings.Contains(body, `name="dato" type="date" required min="`+time.Now().In(oslo).Format(dateLayout)) || !strings.Contains(body, `value="`+future(20)+`"`) {
		t.Fatalf("datoen fra forrige hytte står ikke i skjemaet")
	}

	// Bare navnet, og helger i en periode.
	rec = post(h, "/watches", url.Values{"hytte": {"tømtehytta"}, "dato": {future(30)}, "til": {future(60)}, "dager": {"helg"}, "personer": {"2"}})
	if !strings.Contains(rec.Header().Get("Location"), "ok=") {
		t.Fatalf("eksakt navn skal vinne over delvis treff: %q", rec.Header().Get("Location"))
	}
	ws := store.List()
	last := ws[len(ws)-1]
	if last.Legs[0].CabinID != 72 || len(last.Starts) < 8 || !isWeekendSet(last.Starts) || last.Legs[0].CabinName != "Tømtehytta" {
		t.Fatalf("helger: %+v", last)
	}
	if body := page(h, "/"); !strings.Contains(body, "ankomst fredager og lørdager") || !strings.Contains(body, `value="helg" checked`) {
		t.Fatalf("siden beskriver ikke helgene:\n%s", body)
	}

	// Samme en gang til gir ingen kopi.
	post(h, "/watches", url.Values{"hytte": {"tømtehytta"}, "dato": {future(30)}, "til": {future(60)}, "dager": {"helg"}, "personer": {"2"}})
	if len(store.List()) != 2 {
		t.Fatalf("duplikat: %d", len(store.List()))
	}

	// Flertydig og ukjent navn: tilbake til skjemaet med det hun skrev.
	for query, want := range map[string]string{"Skardstøl": "passer på flere hytter", "Finnesikke": "Fant ingen hytte"} {
		rec = post(h, "/watches", url.Values{"hytte": {query}, "dato": {future(20)}, "personer": {"3"}})
		loc := rec.Header().Get("Location")
		body := page(h, loc)
		if !strings.Contains(body, want) || !strings.Contains(body, `value="`+query+`"`) || !strings.Contains(body, `name="personer" type="number" inputmode="numeric" min="1" max="20" value="3"`) {
			t.Fatalf("%s: vil ha feilen %q og skjemaet fylt ut (%s)", query, want, loc)
		}
	}
	if len(store.List()) != 2 {
		t.Fatalf("feil skal ikke legge inn noe")
	}

	// Fjern.
	rec = post(h, "/watches/1/delete", nil)
	if _, ok := store.Get(1); ok || !strings.Contains(rec.Header().Get("Location"), "Fjernet") {
		t.Fatalf("fjern virket ikke: %q", rec.Header().Get("Location"))
	}
}

func TestCabinListParsing(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bestillingsvilkaar" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`<script>self.__next_f.push([1,"x:[{\"label\":\"Lille Tømtehytta\",\"value\":10780,\"type\":\"cabin\",\"areas\":[{\"id\":1,\"name\":\"Nordmarka\"}],\"x\":1},{\"label\":\"Hoemsbu\",\"value\":1088,\"type\":\"cabin\",\"areas\":[]}]"])</script>`))
	}))
	defer ts.Close()
	got, err := (&DNT{Base: ts.URL, Client: ts.Client()}).CabinList(context.Background())
	if err != nil || len(got) != 2 || got[0] != (Cabin{1088, "Hoemsbu", ""}) || got[1] != (Cabin{10780, "Lille Tømtehytta", "Nordmarka"}) {
		t.Fatalf("%+v, %v", got, err)
	}
}

func TestSecretLink(t *testing.T) {
	_, store, p, _, m := setup(t)
	const token = "AbCdEfGhIjKlMnOpQrStUvWxYz0123456789-_AbCdE"
	h := (&Server{Store: store, Poller: p, Address: m.Address, Token: token}).Routes()
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		return rec
	}

	// Uten lenken finnes ingenting, verken siden eller skjemaene.
	for _, path := range []string{"/", "/watches", "/feil-lenke/", "/" + token[:len(token)-1] + "/", "/" + token + "x/", "//" + token + "/"} {
		if rec := get(path); rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "Mine varsler") {
			t.Errorf("GET %s: %d, vil ha 404 uten innhold", path, rec.Code)
		}
	}
	if rec := post(h, "/watches", url.Values{"hytte": {"https://hyttebestilling.dnt.no/hytte/5"}, "dato": {future(30)}}); rec.Code != http.StatusNotFound || len(store.List()) != 0 {
		t.Fatalf("POST uten lenken: %d, %d overvåkinger", rec.Code, len(store.List()))
	}
	if rec := post(h, "/watches/1/delete", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("slett uten lenken: %d", rec.Code)
	}
	if rec := get("/healthz"); rec.Code != http.StatusOK {
		t.Fatalf("healthz skal være åpen for kubelet og selvsjekken: %d", rec.Code)
	}

	// Med lenken virker alt, og skjemaene peker tilbake under lenken.
	if rec := get("/" + token); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/"+token+"/" {
		t.Fatalf("uten skråstrek: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	rec := get("/" + token + "/")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `action="/`+token+`/watches"`) {
		t.Fatalf("siden under lenken: %d", rec.Code)
	}
	for k, want := range map[string]string{"Referrer-Policy": "no-referrer", "Cache-Control": "no-store", "X-Robots-Tag": "noindex, nofollow"} {
		if got := rec.Header().Get(k); got != want {
			t.Errorf("%s: %q", k, got)
		}
	}
	rec = post(h, "/"+token+"/watches", url.Values{"hytte": {"https://hyttebestilling.dnt.no/hytte/5"}, "dato": {future(30)}, "personer": {"2"}})
	if loc := rec.Header().Get("Location"); rec.Code != http.StatusSeeOther || !strings.HasPrefix(loc, "/"+token+"/?ok=") || len(store.List()) != 1 {
		t.Fatalf("legg til under lenken: %d %q", rec.Code, loc)
	}
	if body := get("/" + token + "/").Body.String(); !strings.Contains(body, `action="/`+token+`/watches/1/delete"`) {
		t.Fatalf("fjern-skjemaet peker ikke under lenken")
	}
	rec = post(h, "/"+token+"/watches/1/delete", nil)
	if !strings.HasPrefix(rec.Header().Get("Location"), "/"+token+"/?ok=") || len(store.List()) != 0 {
		t.Fatalf("fjern under lenken: %q", rec.Header().Get("Location"))
	}
}

func TestLinkMailWaitsUntilReachable(t *testing.T) {
	_, store, _, sent, m := setup(t)
	up := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !up {
			http.Error(w, "nei", http.StatusBadGateway)
		}
	}))
	defer ts.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // én runde, uten å vente et minutt
	RunLink(ctx, store, m, "https://eksempel.ts.net/hemmelig/", ts.URL+"/healthz", ts.Client())
	if len(sent.msgs) != 0 || store.LinkSent() {
		t.Fatal("lenken skal ikke sendes før siden svarer")
	}
	up = true
	RunLink(context.Background(), store, m, "https://eksempel.ts.net/hemmelig/", ts.URL+"/healthz", ts.Client())
	RunLink(context.Background(), store, m, "https://eksempel.ts.net/hemmelig/", ts.URL+"/healthz", ts.Client())
	if got := sent.subjects(); len(got) != 1 || !strings.Contains(sent.msgs[0].HTML, `href="https://eksempel.ts.net/hemmelig/"`) || !store.LinkSent() {
		t.Fatalf("vil ha lenken sendt én gang: %v", got)
	}

	dir := t.TempDir()
	a, err := loadOrCreateToken(dir)
	b, _ := loadOrCreateToken(dir)
	if err != nil || len(a) != 43 || a != b {
		t.Fatalf("lenken skal lages én gang og være stabil: %q %q %v", a, b, err)
	}
}
