package main

import (
	"context"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// mailParts leser e-posten slik den går ut på SMTP, og gir tekst- og
// HTML-delen tilbake slik e-postprogrammet hennes ser dem.
func mailParts(t *testing.T, m Message) (text, html string) {
	t.Helper()
	raw, err := buildMIME("Hyttevarsel <varsel@example.com>", m)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	_, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil {
		t.Fatal(err)
	}
	mr := multipart.NewReader(msg.Body, params["boundary"])
	for {
		p, err := mr.NextPart()
		if err != nil {
			break
		}
		b, _ := io.ReadAll(p)
		if strings.HasPrefix(p.Header.Get("Content-Type"), "text/html") {
			html = string(b)
		} else {
			text = string(b)
		}
	}
	for _, line := range strings.Split(string(raw), "\r\n") {
		if len(line) > 998 {
			t.Errorf("linje på %d tegn i e-posten; SMTP tåler 998", len(line))
		}
	}
	return text, html
}

var (
	buttonHref = regexp.MustCompile(`<a href="([^"]+)"[^>]*>Åpne Mine varsler</a>`)
	formAction = regexp.MustCompile(`<form[^>]* action="([^"]+)"`)
)

// reply er et svar fra e-postprogrammet hennes, med Gmails stempel på at
// avsenderen er ekte.
func reply(t *testing.T, subject, body string) Incoming {
	t.Helper()
	in, err := ParseIncoming(rawMail("kristine@example.com", goodAuth, subject, body), "mx.google.com")
	if err != nil {
		t.Fatal(err)
	}
	return in
}

// Hele løpet slik Kristine møter det: e-posten med lenken, siden, et varsel
// når noen avbestiller, bytte av lenke og en omstart av tjenesten.
func TestHeleLopet(t *testing.T) {
	f, store, p, sent, m := setup(t)
	p.Spacing = 0
	ctx := context.Background()
	dataDir := t.TempDir()
	cabins := OpenDirectory(t.TempDir(), fakeList{
		{ID: 71, Name: "Lille Tømtehytta", Area: "Nordmarka"},
		{ID: 72, Name: "Tømtehytta", Area: "Nordmarka"},
	})
	if err := cabins.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	p.Names = cabins
	link, err := OpenSecretLink(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{Store: store, Poller: p, Cabins: cabins, Address: m.Address, Link: link}
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()
	appURL := func() string { return ts.URL + "/" + link.Token() + "/" }
	m.AppURL = func() string {
		if store.LinkSent() {
			return appURL()
		}
		return ""
	}
	inbox := &MailHandler{Store: store, Poller: p, Mailer: m, Allowed: []string{"kristine@example.com"}, Link: link, AppURL: appURL}

	// Nettleseren hennes: følger videresendinger, ingen informasjonskapsler.
	get := func(u string) (int, string) {
		t.Helper()
		res, err := http.Get(u)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}
	submit := func(pageURL, action string, form url.Values) string {
		t.Helper()
		base, _ := url.Parse(pageURL)
		target, err := base.Parse(action)
		if err != nil {
			t.Fatal(err)
		}
		res, err := http.PostForm(target.String(), form)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("skjemaet til %s svarte %d", action, res.StatusCode)
		}
		return string(b)
	}
	action := func(body, suffix string) string {
		t.Helper()
		for _, a := range formAction.FindAllStringSubmatch(body, -1) {
			if strings.HasSuffix(a[1], suffix) {
				return a[1]
			}
		}
		t.Fatalf("fant ikke skjemaet %s på siden", suffix)
		return ""
	}

	// 1. Før hun har fått lenken, finnes ingenting utenfra.
	for _, path := range []string{"/", "/watches", "/mine-varsler/"} {
		if code, body := get(ts.URL + path); code != http.StatusNotFound || strings.Contains(body, "Mine varsler") {
			t.Fatalf("GET %s uten lenken: %d", path, code)
		}
	}

	// 2. E-posten med lenken, én gang, og lenken er hel i begge delene.
	RunLink(ctx, store, m, appURL)
	RunLink(ctx, store, m, appURL)
	if got := sent.subjects(); len(got) != 1 || got[0] != "Mine varsler: din side hos Hyttevarsel" || sent.msgs[0].To[0] != "kristine@example.com" {
		t.Fatalf("vil ha én e-post med lenken til henne: %v", got)
	}
	text, html := mailParts(t, sent.msgs[0])
	btn := buttonHref.FindStringSubmatch(html)
	if btn == nil || btn[1] != appURL() || !strings.Contains(text, "\r\n"+appURL()+"\r\n") {
		t.Fatalf("lenken i e-posten er ikke den som virker:\n%s", text)
	}
	mine := btn[1]
	sent.msgs = nil

	// 3. Hun trykker på knappen og legger inn en fullbooket hytte ved navn.
	code, body := get(mine)
	if code != http.StatusOK || !strings.Contains(body, "<h1>Mine varsler</h1>") || !strings.Contains(body, "Du følger ikke med på noen hytter ennå") {
		t.Fatalf("siden bak lenken: %d", code)
	}
	if strings.Contains(body, "<script") {
		t.Fatal("siden skal være uten JavaScript")
	}
	body = submit(mine, action(body, "/watches"), url.Values{"hytte": {"Lille Tømtehytta, Nordmarka"}, "dato": {future(20)}, "netter": {"1"}, "personer": {"2"}})
	if !strings.Contains(body, "Følger med på Lille Tømtehytta") || !strings.Contains(body, `<span class="badge">Fullt</span>`) {
		t.Fatalf("hytta står ikke som fullt på siden:\n%s", body)
	}
	if len(sent.msgs) != 0 {
		t.Fatalf("det hun legger inn på siden, bekreftes der og ikke på e-post: %v", sent.subjects())
	}

	// 4. Noen avbestiller. Hun får ett varsel, med bestillingslenken og
	// lenken tilbake til siden, og siden viser det samme.
	w := store.List()[0]
	f.set(71, future(20), 4)
	expire(store, w.ID)
	p.Round(ctx)
	expire(store, w.ID)
	p.Round(ctx)
	if got := sent.subjects(); len(got) != 1 || !strings.HasPrefix(got[0], "Ledig: Lille Tømtehytta") {
		t.Fatalf("vil ha ett varsel: %v", got)
	}
	book := "https://hyttebestilling.dnt.no/hytte/71?adults=2&fromDate=" + future(20)
	text, html = mailParts(t, sent.msgs[0])
	if !strings.Contains(text, book) || !strings.Contains(html, strings.ReplaceAll(book, "&", "&amp;")) {
		t.Fatalf("varselet mangler bestillingslenken:\n%s", text)
	}
	if !strings.Contains(html, `<a href="`+mine+`"`) || !strings.Contains(text, mine) {
		t.Fatalf("varselet mangler lenken til Mine varsler:\n%s", text)
	}
	_, body = get(mine)
	if !strings.Contains(body, "Ledig nå") || !strings.Contains(body, strings.ReplaceAll(book, "&", "&amp;")) {
		t.Fatalf("siden viser ikke at det er ledig:\n%s", body)
	}
	sent.msgs = nil

	// 5. Hun svarer «takk» på varselet. Det skal ikke gjøre noe.
	inbox.Handle(ctx, reply(t, "SV: "+w.Title(), "Takk!\r\n\r\n________________________________\r\nFra: Hyttevarsel <varsel@example.com>\r\nSendt: torsdag 26. november 2026 10:00\r\n\r\n"+text))
	if len(store.List()) != 1 || len(sent.msgs) != 0 || link.Token() != strings.Trim(strings.TrimPrefix(mine, ts.URL), "/") {
		t.Fatalf("et takk skal ikke endre noe: %v", sent.subjects())
	}

	// 6. Hun skriver et hyttenavn feil. Forklaringen står ved skjemaet, der
	// siden lander, og det hun skrev står igjen.
	body = submit(mine, action(body, "/watches"), url.Values{"hytte": {"Tømtehyta"}, "dato": {future(30)}, "netter": {"1"}, "personer": {"2"}})
	if i, j := strings.Index(body, `id="ny"`), strings.Index(body, "Fant ingen hytte som heter «Tømtehyta»"); i < 0 || j < i || !strings.Contains(body, `value="Tømtehyta"`) || len(store.List()) != 1 {
		t.Fatalf("feilen står ikke ved skjemaet:\n%s", body)
	}

	// Helger i en periode for en hytte til, og så fjerner hun den første.
	body = submit(mine, action(body, "/watches"), url.Values{"hytte": {"tømtehytta"}, "dato": {future(30)}, "til": {future(60)}, "dager": {"helg"}, "netter": {"2"}, "personer": {"3"}})
	if !strings.Contains(body, "ankomst fredager og lørdager") || len(store.List()) != 2 {
		t.Fatalf("helgene kom ikke inn:\n%s", body)
	}
	body = submit(mine, action(body, "/watches/1/delete"), nil)
	if !strings.Contains(body, "Fjernet Lille Tømtehytta") || len(store.List()) != 1 {
		t.Fatalf("den første ble ikke fjernet")
	}

	// 7. Lenken har kommet på avveie. Hun svarer «ny lenke» på den første
	// e-posten, med hele e-posten vår sitert under.
	inbox.Handle(ctx, reply(t, "SV: Mine varsler: din side hos Hyttevarsel", "Ny lenke\r\n\r\nFå Outlook for iOS\r\n________________________________\r\nFra: Hyttevarsel <varsel@example.com>\r\nEmne: Mine varsler: din side hos Hyttevarsel\r\n\r\nÅpne Mine varsler:\r\n"+mine+"\r\n"))
	if got := sent.subjects(); len(got) != 1 || got[0] != "Ny lenke til Mine varsler" {
		t.Fatalf("vil ha én e-post med ny lenke: %v", got)
	}
	newText, newHTML := mailParts(t, sent.msgs[0])
	btn = buttonHref.FindStringSubmatch(newHTML)
	if btn == nil || btn[1] == mine || btn[1] != appURL() {
		t.Fatalf("den nye lenken er ikke i e-posten")
	}
	if code, body := get(mine); code != http.StatusNotFound || strings.Contains(body, "Tømtehytta") {
		t.Fatalf("den gamle lenken virker fortsatt: %d", code)
	}
	if res, err := http.PostForm(mine+"watches", url.Values{"hytte": {"Tømtehytta, Nordmarka"}, "dato": {future(40)}}); err != nil || res.StatusCode != http.StatusNotFound || len(store.List()) != 1 {
		t.Fatalf("den gamle lenken tar fortsatt imot skjemaer")
	}
	mine = btn[1]
	if code, body := get(mine); code != http.StatusOK || !strings.Contains(body, "Tømtehytta") {
		t.Fatalf("den nye lenken viser ikke varslene hennes: %d", code)
	}
	sent.msgs = nil

	// 8. Hun svarer «takk» på e-posten med den nye lenken, i et program som
	// siterer uten noe vi kjenner igjen. Lenken skal ikke byttes en gang til.
	inbox.Handle(ctx, reply(t, "SV: Ny lenke til Mine varsler", "Tusen takk!\r\n\r\nNy lenke til Mine varsler\r\n"+newText))
	if len(sent.msgs) != 0 || appURL() != mine {
		t.Fatalf("et takk på den nye lenken byttet den igjen: %v", sent.subjects())
	}

	// 9. Tjenesten starter på nytt: samme lenke, samme varsler, ingen ny e-post.
	store2, err := OpenStore(filepath.Dir(store.path))
	if err != nil {
		t.Fatal(err)
	}
	link2, err := OpenSecretLink(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if !link2.Match(link.Token()) || len(store2.List()) != 1 || !store2.LinkSent() {
		t.Fatalf("omstarten mistet noe: %d varsler, lenke sendt %v", len(store2.List()), store2.LinkSent())
	}
	RunLink(ctx, store2, m, appURL)
	if len(sent.msgs) != 0 {
		t.Fatalf("lenken skal ikke sendes en gang til etter omstart: %v", sent.subjects())
	}
}
