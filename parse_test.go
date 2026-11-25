package main

import (
	"errors"
	"slices"
	"testing"
	"time"
)

var parseNow = time.Date(2026, 11, 25, 13, 0, 0, 0, oslo)

const skåla = "https://hyttebestilling.dnt.no/hytte/101022071"

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

func TestParseAdd(t *testing.T) {
	type leg struct{ id, offset, nights int }
	for _, tc := range []struct {
		name, subject, body string
		starts              []string
		legs                []leg
		guests              int
		flexible            bool
	}{
		{"lenke med datoer fra DNT-siden", "", "Se her: " + skåla + "?fromDate=2027-07-12&toDate=2027-07-14&adults=2",
			[]string{"2027-07-12"}, []leg{{101022071, 0, 2}}, 2, false},
		{"dato i emnet", "12. juli", skåla,
			[]string{"2027-07-12"}, []leg{{101022071, 0, 1}}, 1, false},
		{"tall-dato, netter og personer", "12.07.2027 2 netter, vi er 3", skåla,
			[]string{"2027-07-12"}, []leg{{101022071, 0, 2}}, 3, false},
		{"dato uten år i desember er i år", "", skåla + " 4.12 to personer",
			[]string{"2026-12-04"}, []leg{{101022071, 0, 1}}, 2, false},
		{"periode er ankomst og avreise", "12.–14. juli", skåla,
			[]string{"2027-07-12"}, []leg{{101022071, 0, 2}}, 1, false},
		{"en natt mellom", "en natt mellom 10. og 14. juli", skåla,
			[]string{"2027-07-10", "2027-07-11", "2027-07-12", "2027-07-13"}, []leg{{101022071, 0, 1}}, 1, true},
		{"periode med netter i tillegg", "10.07–14.07 2 netter", skåla,
			[]string{"2027-07-10", "2027-07-11", "2027-07-12"}, []leg{{101022071, 0, 2}}, 1, true},
		{"lørdager", "lørdager i august", skåla,
			[]string{"2027-08-07", "2027-08-14", "2027-08-21", "2027-08-28"}, []leg{{101022071, 0, 1}}, 1, true},
		{"helger", "helger i januar 2027", skåla,
			[]string{"2027-01-01", "2027-01-02", "2027-01-08", "2027-01-09", "2027-01-15", "2027-01-16", "2027-01-22", "2027-01-23", "2027-01-29", "2027-01-30"}, []leg{{101022071, 0, 1}}, 1, true},
		{"rute", "Rute 3. august, 2 personer", "https://hyttebestilling.dnt.no/hytte/111\nhttps://hyttebestilling.dnt.no/hytte/222\nhttps://hyttebestilling.dnt.no/hytte/333",
			[]string{"2027-08-03"}, []leg{{111, 0, 1}, {222, 1, 1}, {333, 2, 1}}, 2, false},
		{"rute med datoer i lenkene", "Rute", "https://hyttebestilling.dnt.no/hytte/111?fromDate=2027-08-03&toDate=2027-08-04 og https://hyttebestilling.dnt.no/hytte/222?fromDate=2027-08-04&toDate=2027-08-06",
			[]string{"2027-08-03"}, []leg{{111, 0, 1}, {222, 1, 2}}, 1, false},
		{"barn og ungdom telles med", "", skåla + "?fromDate=2027-07-12&toDate=2027-07-13&adults=2&youth=1&children=1&infants=1",
			[]string{"2027-07-12"}, []leg{{101022071, 0, 1}}, 4, false},
		{"klokkeslett er ikke en dato", "", skåla + " 12. juli. Sendt kl. 13.40",
			[]string{"2027-07-12"}, []leg{{101022071, 0, 1}}, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := ParseMessage(tc.subject, tc.body, 0, parseNow)
			if err != nil {
				t.Fatal(err)
			}
			if r.Kind != ReqAdd {
				t.Fatalf("kind %v", r.Kind)
			}
			if len(r.Watches) != 1 {
				t.Fatalf("vil ha én overvåking, fikk %d", len(r.Watches))
			}
			w := r.Watches[0]
			if !slices.Equal(w.Starts, tc.starts) {
				t.Errorf("starts %v, vil ha %v", w.Starts, tc.starts)
			}
			var legs []leg
			for _, l := range w.Legs {
				legs = append(legs, leg{l.CabinID, l.Offset, l.Nights})
			}
			if !slices.Equal(legs, tc.legs) {
				t.Errorf("legs %v, vil ha %v", legs, tc.legs)
			}
			if w.Guests != tc.guests || w.Flexible != tc.flexible {
				t.Errorf("guests %d flexible %v, vil ha %d %v", w.Guests, w.Flexible, tc.guests, tc.flexible)
			}
		})
	}
}

func TestParseCommands(t *testing.T) {
	for _, tc := range []struct {
		name, subject, body string
		kind                RequestKind
		id                  int
		err                 error
		hint                int
	}{
		{"svar til pluss-adressen", "Re: Ledig: Skåla lør 12. juli", "Stopp", ReqStop, 9, nil, 9},
		{"mailto-lenken", "Stopp", "Jeg har booket.", ReqStop, 9, nil, 9},
		{"stopp på oversikten", "Re: Hyttevarsel: oversikt", "stopp", ReqStop, 0, nil, 0},
		{"stopp med nummer i teksten", "Re: Hyttevarsel: oversikt", "stopp [#3]", ReqStop, 3, nil, 0},
		{"andre har booket er ikke stopp", "Re: Ledig: Skåla lør 12. juli", "Rakk det ikke, noen andre hadde booket den", 0, 0, ErrIgnore, 9},
		{"stopp alt i svar på et varsel", "Re: Ledig: Skåla lør 12. juli", "stopp alt", ReqStopAll, 0, nil, 9},
		{"oversikt i svar", "SV: Hyttevarsel: oversikt", "oversikt", ReqList, 0, nil, 0},
		{"eldre mailto-lenke", "Stopp [#12]", "Jeg har booket, du kan slutte å følge med.", ReqStop, 12, nil, 0},
		{"svar stopp", "Re: Ledig: Skåla lør 12. juli [#7]", "Stopp\n\nSendt fra min iPhone\n\nDen 25. nov. 2026 kl. 13:40 skrev Hyttevarsel <x@gmail.com>:\n> Bestill " + skåla, ReqStop, 7, nil, 0},
		{"booket", "Re: Ledig: Skåla [#7]", "Vi har booket, takk", ReqStop, 7, nil, 0},
		{"takk er ingen beskjed", "Re: Ledig: Skåla [#7]", "Takk!\n\n> stopp-lenke " + skåla, 0, 0, ErrIgnore, 0},
		{"stopp alt", "stopp alt", "", ReqStopAll, 0, nil, 0},
		{"oversikt", "Oversikt", "", ReqList, 0, nil, 0},
		{"ingen lenke", "Hei", "Kan du følge med på Skåla?", 0, 0, ErrHelp, 0},
		{"ny lenke i svar bruker ikke datoen i emnet", "Re: Følger med: Skåla [#3]", "Denne også: https://hyttebestilling.dnt.no/hytte/444 14. juli", ReqAdd, 0, nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := ParseMessage(tc.subject, tc.body, tc.hint, parseNow)
			if tc.err != nil {
				if !errors.Is(err, tc.err) {
					t.Fatalf("err %v, vil ha %v", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if r.Kind != tc.kind || r.WatchID != tc.id {
				t.Fatalf("kind %v id %d, vil ha %v %d", r.Kind, r.WatchID, tc.kind, tc.id)
			}
			if tc.kind == ReqAdd && !slices.Equal(r.Watches[0].Starts, []string{"2027-07-14"}) {
				t.Fatalf("starts %v", r.Watches[0].Starts)
			}
		})
	}
}

func TestParseManyCabinsAndMonths(t *testing.T) {
	three := "https://hyttebestilling.dnt.no/hytte/111\nhttps://hyttebestilling.dnt.no/hytte/222\nhttps://hyttebestilling.dnt.no/hytte/333"
	r, err := ParseMessage("helger i juni–august, 2 personer", three, 0, parseNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Watches) != 3 {
		t.Fatalf("vil ha tre hytter hver for seg, fikk %d", len(r.Watches))
	}
	for i, id := range []int{111, 222, 333} {
		w := r.Watches[i]
		if len(w.Legs) != 1 || w.Legs[0].CabinID != id || w.Guests != 2 || !w.Flexible {
			t.Errorf("hytte %d: %+v", id, w)
		}
		// Fredager og lørdager i juni, juli og august 2027: 4+4, 5+5, 4+4.
		if len(w.Starts) != 26 || w.Starts[0] != "2027-06-04" || w.Starts[25] != "2027-08-28" {
			t.Errorf("hytte %d: %d datoer, %s til %s", id, len(w.Starts), w.Starts[0], w.Starts[len(w.Starts)-1])
		}
		if err := w.Init(parseNow); err != nil {
			t.Errorf("hytte %d ble avvist: %v", id, err)
		}
	}

	// Datoer i lenkene, uten «rute»: hver hytte med sin egen dato.
	r, err = ParseMessage("", "https://hyttebestilling.dnt.no/hytte/111?fromDate=2027-08-03&toDate=2027-08-04&adults=2 https://hyttebestilling.dnt.no/hytte/222?fromDate=2027-08-10&toDate=2027-08-12", 0, parseNow)
	if err != nil || len(r.Watches) != 2 || r.Watches[0].Starts[0] != "2027-08-03" || r.Watches[1].Starts[0] != "2027-08-10" || r.Watches[1].Legs[0].Nights != 2 || r.Watches[0].Guests != 2 {
		t.Fatalf("lenker med egne datoer: %+v, %v", r.Watches, err)
	}

	for text, want := range map[string][2]string{
		"lørdager i juni og juli":      {"2027-06-05", "2027-07-31"},
		"helger i sommer":              {"2027-06-04", "2027-08-28"},
		"lørdager i desember–februar":  {"2026-12-05", "2027-02-27"},
		"hele juli":                    {"2027-07-01", "2027-07-31"},
		"en natt i november":           {"2026-11-25", "2026-11-30"},
		"fredager i august 2028":       {"2028-08-04", "2028-08-25"},
		"søndager i juni, juli og sep": {"2027-06-06", "2027-09-26"},
	} {
		r, err := ParseMessage(text, skåla, 0, parseNow)
		if err != nil || len(r.Watches) != 1 {
			t.Errorf("%q: %v", text, err)
			continue
		}
		s := r.Watches[0].Starts
		if s[0] != want[0] || s[len(s)-1] != want[1] {
			t.Errorf("%q: %s til %s (%d), vil ha %s til %s", text, s[0], s[len(s)-1], len(s), want[0], want[1])
		}
	}
	if r, _ := ParseMessage("søndager i juni, juli og sep", skåla, 0, parseNow); len(r.Watches[0].Starts) != 12 {
		t.Errorf("juni, juli og sep skal ikke ta med august: %v", r.Watches[0].Starts)
	}
}

func TestParseNoDate(t *testing.T) {
	if _, err := ParseMessage("", skåla, 0, parseNow); err == nil || errors.Is(err, ErrHelp) {
		t.Fatalf("vil ha feil om manglende dato, fikk %v", err)
	}
}

func TestHTMLToTextKeepsLinks(t *testing.T) {
	got := HTMLToText(`<div>Se <a href="https://hyttebestilling.dnt.no/hytte/101022071?fromDate=2027-07-12&amp;toDate=2027-07-13">Skåla</a></div>`)
	links := parseLinks(got)
	if len(links) != 1 || links[0].id != 101022071 || links[0].from.Format(dateLayout) != "2027-07-12" {
		t.Fatalf("lenker %+v fra %q", links, got)
	}
}

func TestInitLimits(t *testing.T) {
	w := Watch{Guests: 1, Legs: []Leg{{CabinID: 1, Nights: 1}}, Starts: []string{"2026-11-20"}}
	if err := w.Init(parseNow); err == nil {
		t.Error("passert dato skal avvises")
	}
	w = Watch{Guests: 1, Legs: []Leg{{CabinID: 1, Nights: 1}}, Starts: []string{"2028-06-01"}}
	if err := w.Init(parseNow); err == nil {
		t.Error("mer enn et år fram skal avvises")
	}
	var starts []string
	for d := parseDate("2027-07-01"); d.Month() == time.July; d = d.AddDate(0, 0, 1) {
		starts = append(starts, d.Format(dateLayout))
	}
	w = Watch{Guests: 1, Legs: []Leg{{CabinID: 1, Nights: 1}, {CabinID: 2, Offset: 1, Nights: 1}, {CabinID: 3, Offset: 2, Nights: 1}}, Starts: starts}
	if err := w.Init(parseNow); err == nil {
		t.Error("93 kall per runde skal avvises")
	}
}
