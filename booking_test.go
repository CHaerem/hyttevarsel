package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestMatchBooking(t *testing.T) {
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, oslo)
	ws := []Watch{
		{ID: 1, Guests: 1, Legs: []Leg{{CabinID: 1, CabinName: "Babord - Gressholmen", Nights: 1}}, Starts: []string{"2027-07-01"}},
		{ID: 2, Guests: 1, Legs: []Leg{{CabinID: 2, CabinName: "Skåla", Nights: 1}}, Starts: []string{"2027-07-10", "2027-07-11"}},
	}
	for _, tc := range []struct {
		name, subject, text string
		want                []int
	}{
		{"full dato og navn", "Ordrebekreftelse", "Takk for bestillingen!\nBabord - Gressholmen\nAnkomst: 01.07.2027", []int{1}},
		{"del av navnet og tekstdato", "Din bestilling", "Hytte: Babord\nFra 1. juli 2027 til 2. juli 2027", []int{1}},
		{"fleksibel: én av datoene", "Booking confirmation", "SKÅLA, arrival 2027-07-11", []int{2}},
		{"11. juli er ikke 1. juli", "Ordre", "Babord - Gressholmen 11. juli 2027", nil},
		{"uten bestillingsord", "Hei", "Babord - Gressholmen 01.07.2027", nil},
		{"feil dato", "Ordrebekreftelse", "Skåla 12.07.2027", nil},
	} {
		var got []int
		for _, w := range MatchBooking(ws, tc.subject, tc.text, now) {
			got = append(got, w.ID)
		}
		if len(got) != len(tc.want) || (len(got) > 0 && got[0] != tc.want[0]) {
			t.Errorf("%s: %v, vil ha %v", tc.name, got, tc.want)
		}
	}
}

func TestBookingConfirmationStopsWatch(t *testing.T) {
	_, store, p, sent, m := setup(t)
	ctx := context.Background()
	h := &MailHandler{Store: store, Poller: p, Mailer: m, Allowed: []string{"kristine@example.com"}, BookingDomains: []string{"dnt.no"}}
	start := future(40)
	w, err := AddWatch(ctx, store, p, Watch{Guests: 1, Requester: "kristine@example.com", Legs: []Leg{{CabinID: 8, Nights: 1}}, Starts: []string{start}})
	if err != nil {
		t.Fatal(err)
	}
	// Fra DNT, men om en annen hytte: ingenting skjer, og ingen svar til DNT.
	h.Handle(ctx, Incoming{From: "noreply@dnt.no", Verified: true, Subject: "Ordrebekreftelse", Text: "Skåla " + start})
	if len(store.List()) != 1 || len(sent.msgs) != 0 {
		t.Fatal("bekreftelse for en annen hytte skal ikke gjøre noe")
	}
	// DNT kan ikke legge inn hytter.
	h.Handle(ctx, Incoming{From: "noreply@dnt.no", Verified: true, Subject: start, Text: "https://hyttebestilling.dnt.no/hytte/9"})
	if len(store.List()) != 1 {
		t.Fatal("e-post fra DNT skal ikke kunne legge inn hytter")
	}
	h.Handle(ctx, Incoming{From: "noreply@dnt.no", Verified: true, Subject: "Ordrebekreftelse", Text: "Takk for bestillingen. Hytte nr 8, ankomst " + start})
	if len(store.List()) != 0 {
		t.Fatal("bekreftelsen skulle stoppe overvåkingen")
	}
	if got := sent.subjects(); len(got) != 1 || !strings.HasPrefix(got[0], "Booket: Hytte nr 8") || sent.msgs[0].To[0] != "kristine@example.com" {
		t.Fatalf("vil ha melding til Kristine, fikk %v %v", got, sent.msgs)
	}
	_ = w
}

func TestAskForDateRoundTrip(t *testing.T) {
	_, store, p, sent, m := setup(t)
	ctx := context.Background()
	h := &MailHandler{Store: store, Poller: p, Mailer: m, Allowed: []string{"kristine@example.com"}}
	h.Handle(ctx, Incoming{From: "kristine@example.com", Verified: true, Subject: "Skåla", Text: "https://hyttebestilling.dnt.no/hytte/11"})
	if len(sent.msgs) != 1 || !strings.HasPrefix(sent.msgs[0].Subject, "Hvilken dato for Hytte nr 11? [hytte 11]") {
		t.Fatalf("vil ha spørsmål om dato, fikk %v", sent.subjects())
	}
	d := parseDate(future(45))
	h.Handle(ctx, Incoming{From: "kristine@example.com", Verified: true, Subject: "Re: " + sent.msgs[0].Subject,
		Text: d.Format("02.01.2006") + ", 2 personer\n\nDen 25. nov. 2026 kl. 14:00 skrev Hyttevarsel:\n> Hvilken dato"})
	ws := store.List()
	if len(ws) != 1 || ws[0].Legs[0].CabinID != 11 || ws[0].Starts[0] != future(45) || ws[0].Guests != 2 {
		t.Fatalf("overvåkinger %+v", ws)
	}
}
