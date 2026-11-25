package main

import (
	"context"
	"strings"
	"testing"
)

func rawMail(from, auth, subject, body string) []byte {
	return []byte(strings.Join([]string{
		"Authentication-Results: " + auth,
		"From: Kristine <" + from + ">",
		"To: varsel@example.com",
		"Subject: " + subject,
		"Message-ID: <abc@example.com>",
		"Content-Type: text/plain; charset=utf-8",
		"",
		body,
	}, "\r\n"))
}

const goodAuth = "mx.google.com; dkim=pass header.i=@example.com header.s=x; spf=pass smtp.mailfrom=kristine@example.com; dmarc=pass (p=NONE) header.from=example.com"

func TestParseIncomingVerification(t *testing.T) {
	in, err := ParseIncoming(rawMail("Kristine@Example.com", goodAuth, "12. juli", skåla), "mx.google.com")
	if err != nil {
		t.Fatal(err)
	}
	if in.From != "kristine@example.com" || !in.Verified || in.Subject != "12. juli" || !strings.Contains(in.Text, skåla) {
		t.Fatalf("%+v", in)
	}
	// Forfalsket avsender: Gmail skriver dmarc=fail.
	in, _ = ParseIncoming(rawMail("kristine@example.com", "mx.google.com; spf=softfail; dmarc=fail header.from=example.com", "x", "y"), "mx.google.com")
	if in.Verified {
		t.Fatal("dmarc=fail skal ikke godtas")
	}
	// En Authentication-Results fra en annen tjener teller ikke.
	in, _ = ParseIncoming(rawMail("kristine@example.com", "evil.example; dmarc=pass", "x", "y"), "mx.google.com")
	if in.Verified {
		t.Fatal("fremmed Authentication-Results skal ikke godtas")
	}
}

func TestMailHandlerFlow(t *testing.T) {
	_, store, p, sent, m := setup(t)
	h := &MailHandler{Store: store, Poller: p, Mailer: m, Allowed: []string{"kristine@example.com"}}
	ctx := context.Background()

	h.Handle(ctx, Incoming{From: "fremmed@example.com", Verified: true, Subject: future(20), Text: "https://hyttebestilling.dnt.no/hytte/7"})
	h.Handle(ctx, Incoming{From: "kristine@example.com", Verified: false, Subject: future(20), Text: "https://hyttebestilling.dnt.no/hytte/7"})
	if len(store.List()) != 0 || len(sent.msgs) != 0 {
		t.Fatal("ukjent eller ubekreftet avsender skal ignoreres")
	}

	h.Handle(ctx, Incoming{From: "kristine@example.com", Verified: true, Subject: "Fwd: Skåla " + future(20), Text: "https://hyttebestilling.dnt.no/hytte/7", MessageID: "<1@x>"})
	ws := store.List()
	if len(ws) != 1 || ws[0].Requester != "kristine@example.com" {
		t.Fatalf("overvåkinger %+v", ws)
	}
	if got := sent.subjects(); len(got) != 1 || !strings.HasPrefix(got[0], "Følger med: Hytte nr 7") || sent.msgs[0].InReplyTo != "<1@x>" || strings.Contains(got[0], "#") {
		t.Fatalf("vil ha bekreftelse som svar, fikk %v", got)
	}

	h.Handle(ctx, Incoming{From: "kristine@example.com", Verified: true, Subject: "Oversikt", Text: ""})
	if last := sent.msgs[len(sent.msgs)-1]; !strings.Contains(last.Text, "Hytte nr 7") {
		t.Fatalf("oversikten mangler hytta:\n%s", last.Text)
	}

	// «stopp» på oversikten: hun følger bare med på én, så den stoppes.
	h.Handle(ctx, Incoming{From: "kristine@example.com", Verified: true, Subject: "Re: Hyttevarsel: oversikt", Text: "stopp"})
	if len(store.List()) != 0 {
		t.Fatal("stopp skulle fjerne den eneste overvåkingen")
	}
	if last := sent.msgs[len(sent.msgs)-1]; !strings.HasPrefix(last.Subject, "Re: ") || !strings.Contains(last.Text, "følger ikke lenger med på Hytte nr 7") {
		t.Fatalf("vil ha bekreftelse på stopp: %q\n%s", last.Subject, last.Text)
	}

	h.Handle(ctx, Incoming{From: "kristine@example.com", Verified: true, Subject: "Hei", Text: "hva gjør jeg?"})
	if last := sent.msgs[len(sent.msgs)-1]; !strings.Contains(last.Text, "Send lenken til hytta") {
		t.Fatalf("vil ha hjelpetekst:\n%s", last.Text)
	}
}

func TestStopWithSeveralWatches(t *testing.T) {
	_, store, p, sent, m := setup(t)
	h := &MailHandler{Store: store, Poller: p, Mailer: m, Allowed: []string{"kristine@example.com"}}
	ctx := context.Background()
	var ws []Watch
	for _, cabin := range []int{21, 22, 23} {
		w, err := AddWatch(ctx, store, p, Watch{Guests: 1, Legs: []Leg{{CabinID: cabin, Nights: 1}}, Starts: []string{future(30)}})
		if err != nil {
			t.Fatal(err)
		}
		ws = append(ws, w)
	}
	say := func(subject, text string, id int) {
		h.Handle(ctx, Incoming{From: "kristine@example.com", Verified: true, Subject: subject, Text: text, WatchID: id})
	}

	say("Re: Hyttevarsel: oversikt", "stopp", 0)
	if len(store.List()) != 3 {
		t.Fatal("uklart stopp skal ikke fjerne noe")
	}
	if last := sent.msgs[len(sent.msgs)-1]; !strings.Contains(last.Text, "Du følger med på flere hytter") || strings.Count(last.HTML, ">Stopp</a>") != 3 {
		t.Fatalf("vil ha spørsmål om hvilken:\n%s", last.HTML)
	}

	say("Re: Hyttevarsel: oversikt", "Stopp hytte nr 22, takk", 0)
	if _, ok := store.Get(ws[1].ID); ok || len(store.List()) != 2 {
		t.Fatal("stopp med navn skulle fjerne Hytte nr 22")
	}

	say("Stopp", "Slutt å følge med.", ws[0].ID) // lenken i e-posten, til pluss-adressen
	if _, ok := store.Get(ws[0].ID); ok || len(store.List()) != 1 {
		t.Fatal("stopp til pluss-adressen skulle fjerne den overvåkingen")
	}
	say("Stopp", "Slutt å følge med.", ws[0].ID)
	if last := sent.msgs[len(sent.msgs)-1]; !strings.Contains(last.Text, "allerede stoppet") {
		t.Fatalf("vil ha «allerede stoppet»:\n%s", last.Text)
	}
}

// Et svar som siterer varselet vårt uten sitattegn skal ikke bli lest som
// stopp eller som en ny hyttelenke.
func TestUnquotedReplyIsHarmless(t *testing.T) {
	f, store, p, sent, m := setup(t)
	h := &MailHandler{Store: store, Poller: p, Mailer: m, Allowed: []string{"kristine@example.com"}}
	ctx := context.Background()
	w, _ := AddWatch(ctx, store, p, Watch{Guests: 1, Legs: []Leg{{CabinID: 41, Nights: 1}}, Starts: []string{future(25)}})
	f.set(41, future(25), 1)
	p.Round(ctx)
	if len(sent.msgs) != 1 {
		t.Fatalf("vil ha ett varsel: %v", sent.subjects())
	}
	for _, quoted := range []string{sent.msgs[0].Text, HTMLToText(sent.msgs[0].HTML)} {
		h.Handle(ctx, Incoming{From: "kristine@example.com", Verified: true, WatchID: w.ID, Subject: "Re: " + sent.msgs[0].Subject, Text: "Takk!\n\n" + quoted})
	}
	if len(store.List()) != 1 || len(sent.msgs) != 1 {
		t.Fatalf("sitatet ble tolket: %d overvåkinger, e-poster %v", len(store.List()), sent.subjects())
	}
}

func TestAlreadyFreeGivesOneMail(t *testing.T) {
	f, store, p, sent, m := setup(t)
	h := &MailHandler{Store: store, Poller: p, Mailer: m, Allowed: []string{"kristine@example.com"}}
	ctx := context.Background()
	f.set(31, future(20), 4)
	h.Handle(ctx, Incoming{From: "kristine@example.com", Verified: true, Subject: future(20) + " 2 personer", Text: "https://hyttebestilling.dnt.no/hytte/31", MessageID: "<2@x>"})
	if got := sent.subjects(); len(got) != 1 || !strings.HasPrefix(got[0], "Ledig: Hytte nr 31") {
		t.Fatalf("vil ha ett varsel og ingen bekreftelse: %v", got)
	}
	if !strings.Contains(sent.msgs[0].Text, "allerede plass til 2 personer") || sent.msgs[0].InReplyTo != "<2@x>" {
		t.Fatalf("varselet:\n%s", sent.msgs[0].Text)
	}
	p.Round(ctx)
	expire(store, store.List()[0].ID)
	p.Round(ctx)
	if len(sent.msgs) != 1 {
		t.Fatalf("skal ikke varsle på nytt for samme ledighet: %v", sent.subjects())
	}
}

func TestForwardedDNTMailIsIgnored(t *testing.T) {
	_, store, p, sent, m := setup(t)
	h := &MailHandler{Store: store, Poller: p, Mailer: m, Allowed: []string{"kristine@example.com"}, BookingDomains: []string{"dnt.no"}}
	h.Handle(context.Background(), Incoming{From: "kristine@example.com", Verified: true, Subject: "VS: Nyhetsbrev fra DNT",
		Text: "Fra: DNT <nyhetsbrev@dnt.no>\nSe hytta: https://hyttebestilling.dnt.no/hytte/7?fromDate=" + future(20) + "&toDate=" + future(21)})
	if len(store.List()) != 0 || len(sent.msgs) != 0 {
		t.Fatalf("videresendt nyhetsbrev skal ikke gi overvåking eller svar: %v", sent.subjects())
	}
}

func TestPlusAddressAndWelcome(t *testing.T) {
	raw := strings.Replace(string(rawMail("kristine@example.com", goodAuth, "Re: Ledig: Skåla", "Stopp")), "To: varsel@example.com", "To: Hyttevarsel <varsel+h42@example.com>", 1)
	in, err := ParseIncoming([]byte(raw), "mx.google.com")
	if err != nil || in.WatchID != 42 {
		t.Fatalf("WatchID %d, err %v", in.WatchID, err)
	}
	_, store, _, sent, m := setup(t)
	m.PageURL = "https://example.com/side/"
	RunWelcome(context.Background(), store, m)
	RunWelcome(context.Background(), store, m)
	if got := sent.subjects(); len(got) != 1 || got[0] != "Hyttevarsel er klart" || !strings.Contains(sent.msgs[0].HTML, "https://example.com/side/#til=varsel@example.com") {
		t.Fatalf("velkomst: %v", got)
	}
}
