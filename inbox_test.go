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
	if got := sent.subjects(); len(got) != 1 || !strings.HasPrefix(got[0], "Følger med: Hytte nr 7") || sent.msgs[0].InReplyTo != "<1@x>" {
		t.Fatalf("vil ha bekreftelse som svar, fikk %v", got)
	}

	h.Handle(ctx, Incoming{From: "kristine@example.com", Verified: true, Subject: "Oversikt", Text: ""})
	if last := sent.msgs[len(sent.msgs)-1]; !strings.Contains(last.Text, "Hytte nr 7") {
		t.Fatalf("oversikten mangler hytta:\n%s", last.Text)
	}

	h.Handle(ctx, Incoming{From: "kristine@example.com", Verified: true, Subject: "Stopp " + tag(ws[0]), Text: "Jeg har booket"})
	if len(store.List()) != 0 {
		t.Fatal("stopp skulle fjerne overvåkingen")
	}

	h.Handle(ctx, Incoming{From: "kristine@example.com", Verified: true, Subject: "Hei", Text: "hva gjør jeg?"})
	if last := sent.msgs[len(sent.msgs)-1]; !strings.Contains(last.Text, "Slik legger du til en hytte") {
		t.Fatalf("vil ha hjelpetekst:\n%s", last.Text)
	}
}
