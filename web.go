package main

import (
	"context"
	"crypto/subtle"
	"embed"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

//go:embed templates/*.html
var templateFS embed.FS

var templates = template.Must(template.New("").ParseFS(templateFS, "templates/*.html"))

// Server er «Mine varsler»: siden der hun legger til hytter, ser status og
// fjerner dem. E-post brukes bare til selve varselet. Server-rendret HTML
// med vanlige skjemaer, uten JavaScript.
type Server struct {
	Store   *Store
	Poller  *Poller
	Cabins  *Directory // hyttelista det søkes i; kan mangle
	Address string     // varseladressen, for dem som heller sender e-post

	// Token gjør siden til en hemmelig personlig lenke: alt ligger under
	// /<token>/, og alt annet enn /healthz svarer 404. Brukes når siden er
	// åpnet mot internett (Tailscale Funnel), der tailnettet ikke lenger
	// er porten. Lenken ligger i stien og ikke i en informasjonskapsel, så
	// den virker likt som bokmerke, på hjemskjermen og i en annen nettleser.
	Token string
}

func (s *Server) base() string {
	if s.Token == "" {
		return ""
	}
	return "/" + s.Token
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.index)
	mux.HandleFunc("POST /watches", s.add)
	mux.HandleFunc("POST /watches/{id}/delete", s.delete)
	mux.HandleFunc("GET /healthz", s.healthz)
	app := secure(mux)
	if s.Token == "" {
		return app
	}
	inner := http.StripPrefix("/"+s.Token, app)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			s.healthz(w, r)
			return
		}
		first, _, slash := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
		if subtle.ConstantTimeCompare([]byte(first), []byte(s.Token)) != 1 {
			http.NotFound(w, r)
			return
		}
		if !slash { // /<token> uten skråstrek: skjemaene trenger den
			http.Redirect(w, r, "/"+s.Token+"/", http.StatusSeeOther)
			return
		}
		inner.ServeHTTP(w, r)
	})
}

// secure setter hodene en side på åpent nett bør ha: ingen henviser ut
// (lenken er hemmelig), ingen mellomlagring, ingen innramming, og bare
// sidens egne stiler og skjemaer.
func secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Robots-Tag", "noindex, nofollow")
		h.Set("Cache-Control", "no-store")
		h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src data:; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		next.ServeHTTP(w, r)
	})
}

type freeLink struct{ Label, URL string }

type watchView struct {
	ID          int
	Title, When string
	Status      string // ledig, fullt, sjekkes eller feil
	Free        []freeLink
	MoreFree    int
	LastChecked string
}

// formValues er det som står i skjemaet: det hun nettopp skrev når noe gikk
// galt, ellers datoene fra forrige hytte, så neste hytte er ett felt unna.
type formValues struct {
	Cabin, Date, Until string
	Weekends           bool
	Nights, Guests     int
	More               bool // «Flere datoer eller netter» står åpen
}

type indexData struct {
	Active, Passed []watchView
	Error, Notice  string
	Today, MaxDate string
	Address        string
	Cabins         []Cabin
	Form           formValues
	Base           string // stien skjemaene sender til, tom eller /<token>
}

const maxFreeLinks = 6

func view(w Watch, now time.Time) watchView {
	v := watchView{ID: w.ID, Title: w.Title(), When: Describe(w), Status: "fullt"}
	var last time.Time
	known, failed := 0, 0
	for _, c := range w.Checks {
		if c.LastChecked.After(last) {
			last = c.LastChecked
		}
		if c.LastError != "" {
			failed++
		} else if c.Known {
			known++
		}
	}
	free := w.FreeStarts(now)
	switch {
	case len(free) > 0:
		v.Status = "ledig"
	case known == 0 && failed > 0:
		v.Status = "feil"
	case known == 0:
		v.Status = "sjekkes"
	}
	for i, s := range free {
		if i == maxFreeLinks {
			v.MoreFree = len(free) - i
			break
		}
		for _, l := range w.Legs {
			from, to := l.Stay(s)
			label := kortDato(from)
			if w.IsRoute() {
				label = l.Name() + " " + label
			}
			v.Free = append(v.Free, freeLink{label, BookingURL(l.CabinID, from, to, w.Guests)})
		}
	}
	if !last.IsZero() {
		v.LastChecked = last.In(oslo).Format("15:04")
		if today(last) != today(now) {
			v.LastChecked = last.In(oslo).Format("2.1. 15:04")
		}
	}
	return v
}

func isWeekendSet(starts []string) bool {
	for _, s := range starts {
		if d := parseDate(s).Weekday(); d != time.Friday && d != time.Saturday {
			return false
		}
	}
	return len(starts) > 1
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	now := time.Now().In(oslo)
	q := r.URL.Query()
	d := indexData{
		Error:   q.Get("feil"),
		Notice:  q.Get("ok"),
		Today:   now.Format(dateLayout),
		MaxDate: now.AddDate(0, bookAheadMo, 0).Format(dateLayout),
		Address: s.Address,
		Base:    s.base(),
		Cabins:  s.Cabins.List(),
		Form:    formValues{Nights: 1, Guests: 2},
	}
	var latest *Watch
	for _, wa := range s.Store.List() {
		if wa.Expired(now) {
			d.Passed = append(d.Passed, view(wa, now))
			continue
		}
		d.Active = append(d.Active, view(wa, now))
		if !wa.IsRoute() && (latest == nil || wa.ID > latest.ID) {
			latest = &wa
		}
	}
	if latest != nil {
		starts := latest.ActiveStarts(now)
		d.Form = formValues{Date: starts[0], Nights: latest.Legs[0].Nights, Guests: latest.Guests}
		if len(starts) > 1 {
			d.Form.Until, d.Form.Weekends = starts[len(starts)-1], isWeekendSet(starts)
		}
	}
	if q.Has("hytte") { // skjemaet kom tilbake med en feil: vis det hun skrev
		d.Form = formValues{
			Cabin: q.Get("hytte"), Date: q.Get("dato"), Until: q.Get("til"), Weekends: q.Get("dager") == "helg",
			Nights: max(atoiDefault(q.Get("netter"), 1), 1), Guests: max(atoiDefault(q.Get("personer"), 2), 1),
		}
	}
	d.Form.More = d.Form.Until != "" || d.Form.Nights > 1
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.ExecuteTemplate(w, "index.html", d); err != nil {
		slog.Error("template", "err", err)
	}
}

func (s *Server) redirect(w http.ResponseWriter, r *http.Request, key, msg string) {
	http.Redirect(w, r, s.base()+"/?"+url.Values{key: {msg}}.Encode(), http.StatusSeeOther)
}

// fail sender henne tilbake til skjemaet med feilen og det hun skrev.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, msg string) {
	q := url.Values{"feil": {msg}}
	for _, k := range []string{"hytte", "dato", "til", "dager", "netter", "personer"} {
		q.Set(k, r.FormValue(k))
	}
	http.Redirect(w, r, s.base()+"/?"+q.Encode()+"#ny", http.StatusSeeOther)
}

// cabins finner hyttene i søkefeltet: lenker fra DNT, eller et navn.
func (s *Server) cabins(field string) ([]cabinLink, string) {
	field = strings.TrimSpace(field)
	if links := parseLinks(field); len(links) > 0 {
		return links, ""
	}
	if id, ok := ParseCabinID(field); ok {
		return []cabinLink{{id: id}}, ""
	}
	if field == "" {
		return nil, "Skriv navnet på hytta, eller lim inn lenken fra hyttebestilling.dnt.no."
	}
	found := s.Cabins.Find(field)
	switch {
	case len(found) == 1:
		return []cabinLink{{id: found[0].ID}}, ""
	case len(found) == 0:
		return nil, fmt.Sprintf("Fant ingen hytte som heter «%s». Prøv et annet navn, eller lim inn lenken fra hyttebestilling.dnt.no.", field)
	}
	names := make([]string, 0, 5)
	for _, c := range found[:min(len(found), 5)] {
		names = append(names, c.Label())
	}
	more := ""
	if len(found) > 5 {
		more = fmt.Sprintf(" og %d til", len(found)-5)
	}
	return nil, fmt.Sprintf("«%s» passer på flere hytter: %s%s. Velg en av dem fra lista.", field, strings.Join(names, "; "), more)
}

func (s *Server) add(w http.ResponseWriter, r *http.Request) {
	links, msg := s.cabins(r.FormValue("hytte"))
	if msg != "" {
		s.fail(w, r, msg)
		return
	}
	nights := atoiDefault(r.FormValue("netter"), 1)
	guests := atoiDefault(r.FormValue("personer"), 1)

	// Datoen fra skjemaet. Limte hun inn en lenke med datoer og lot feltet
	// stå tomt, gjelder datoene i lenken.
	from, err := time.ParseInLocation(dateLayout, r.FormValue("dato"), oslo)
	if err != nil {
		if links[0].from.IsZero() {
			s.fail(w, r, "Velg en ankomstdato.")
			return
		}
		from = links[0].from
		if !links[0].to.IsZero() && r.FormValue("netter") == "" {
			nights = int(links[0].to.Sub(from).Hours()/24 + 0.5)
		}
	}
	until := from
	if v := r.FormValue("til"); v != "" {
		if until, err = time.ParseInLocation(dateLayout, v, oslo); err != nil || until.Before(from) {
			s.fail(w, r, "«Til og med» må være samme dag som ankomst eller senere.")
			return
		}
	}
	weekends := r.FormValue("dager") == "helg"
	var starts []string
	for d := from; !d.After(until); d = d.AddDate(0, 0, 1) {
		if weekends && until.After(from) && d.Weekday() != time.Friday && d.Weekday() != time.Saturday {
			continue
		}
		starts = append(starts, d.Format(dateLayout))
	}
	if len(starts) == 0 {
		s.fail(w, r, "Det er ingen fredager eller lørdager mellom datoene du valgte.")
		return
	}

	var names, failed []string
	for i, l := range links {
		if i > 0 && !s.Poller.sleep(r.Context()) {
			break
		}
		wa, err := AddQuiet(r.Context(), s.Store, s.Poller, Watch{
			Guests: guests, Starts: slices.Clone(starts), Flexible: len(starts) > 1,
			Legs: []Leg{{CabinID: l.id, Nights: nights}},
		})
		if err != nil {
			failed = append(failed, err.Error())
			continue
		}
		names = append(names, wa.Title())
	}
	if len(names) == 0 {
		s.fail(w, r, capitalize(strings.Join(failed, "; "))+".")
		return
	}
	s.redirect(w, r, "ok", "Følger med på "+strings.Join(names, ", ")+". Du får e-post når det blir ledig.")
}

// AddQuiet legger inn en overvåking fra siden. Hun ser resultatet der, så
// det sendes ingen bekreftelse på e-post, og det som alt er ledig vises på
// siden i stedet for å bli varslet.
func AddQuiet(ctx context.Context, store *Store, p *Poller, w Watch) (Watch, error) {
	probe := w
	if err := probe.Init(time.Now()); err == nil {
		existing := store.List()
		if j := slices.IndexFunc(existing, func(e Watch) bool { return sameWatch(e, probe) }); j >= 0 {
			return existing[j], nil
		}
	}
	w, err := AddWatch(ctx, store, p, w)
	if err != nil {
		return Watch{}, err
	}
	if len(w.Starts)*len(w.Legs) > 6 {
		return w, nil // mange datoer: polleren sjekker dem, med kalenderen
	}
	p.CheckWatch(ctx, w.ID)
	w, _ = store.Get(w.ID)
	var free []string
	for _, s := range w.ActiveStarts(time.Now()) {
		if w.OptionFree(s) {
			free = append(free, s)
		}
	}
	p.MarkNotified(w.ID, free)
	w, _ = store.Get(w.ID)
	return w, nil
}

func (s *Server) delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	wa, ok := s.Store.Get(id)
	if _, err := s.Store.Delete(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		s.redirect(w, r, "ok", "Den var allerede fjernet.")
		return
	}
	s.redirect(w, r, "ok", "Fjernet "+wa.Title()+".")
}

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	if !s.Poller.Healthy(time.Now()) {
		http.Error(w, "overvåkingen har stått stille", http.StatusServiceUnavailable)
		return
	}
	w.Write([]byte("ok\n"))
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return -1
	}
	return n
}
