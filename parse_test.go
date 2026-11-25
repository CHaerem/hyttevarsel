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
		{"rute med datoer i lenkene", "", "https://hyttebestilling.dnt.no/hytte/111?fromDate=2027-08-03&toDate=2027-08-04 og https://hyttebestilling.dnt.no/hytte/222?fromDate=2027-08-04&toDate=2027-08-06",
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
			w := r.Watch
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
			if tc.kind == ReqAdd && !slices.Equal(r.Watch.Starts, []string{"2027-07-14"}) {
				t.Fatalf("starts %v", r.Watch.Starts)
			}
		})
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
	w = Watch{Guests: 1, Legs: []Leg{{CabinID: 1, Nights: 1}, {CabinID: 2, Offset: 1, Nights: 1}}, Starts: starts}
	if err := w.Init(parseNow); err == nil {
		t.Error("62 kall per runde skal avvises")
	}
}
