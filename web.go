package main

import (
	"embed"
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

var templates = template.Must(template.New("").Funcs(template.FuncMap{
	"describe": Describe,
	"kort":     func(s string) string { return kortDato(parseDate(s)) },
	"legDato": func(l Leg, start string) string {
		from, _ := l.Stay(start)
		return kortDato(from)
	},
	"klokke": func(t time.Time) string {
		if t.IsZero() {
			return "ikke ennå"
		}
		return t.In(oslo).Format("02.01. kl. 15:04")
	},
	"bookingURL": func(l Leg, start string, guests int) string {
		from, to := l.Stay(start)
		return BookingURL(l.CabinID, from, to, guests)
	},
}).ParseFS(templateFS, "templates/*.html"))

type Server struct {
	Store   *Store
	Poller  *Poller
	Mailer  *Mailer
	Address string // varseladressen, vises på siden
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.index)
	mux.HandleFunc("POST /watches", s.add)
	mux.HandleFunc("POST /watches/{id}/delete", s.delete)
	mux.HandleFunc("GET /healthz", s.healthz)
	return mux
}

type watchView struct {
	Watch
	Free        []string
	LastChecked time.Time
	Errors      int
}

type indexData struct {
	Active, Expired []watchView
	Error, Notice   string
	Today, MaxDate  string
	Address         string
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	now := time.Now().In(oslo)
	d := indexData{
		Error:   r.URL.Query().Get("feil"),
		Notice:  r.URL.Query().Get("ok"),
		Today:   now.Format(dateLayout),
		MaxDate: now.AddDate(0, bookAheadMo, 0).Format(dateLayout),
		Address: s.Address,
	}
	for _, wa := range s.Store.List() {
		v := watchView{Watch: wa, Free: wa.FreeStarts(now)}
		for _, c := range wa.Checks {
			if c.LastChecked.After(v.LastChecked) {
				v.LastChecked = c.LastChecked
			}
			if c.LastError != "" {
				v.Errors++
			}
		}
		if wa.Expired(now) {
			d.Expired = append(d.Expired, v)
		} else {
			d.Active = append(d.Active, v)
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.ExecuteTemplate(w, "index.html", d); err != nil {
		slog.Error("template", "err", err)
	}
}

func redirect(w http.ResponseWriter, r *http.Request, key, msg string) {
	http.Redirect(w, r, "/?"+url.Values{key: {msg}}.Encode(), http.StatusSeeOther)
}

func (s *Server) add(w http.ResponseWriter, r *http.Request) {
	links := parseLinks(r.FormValue("hytter"))
	if len(links) == 0 {
		// Bare tallet godtas også.
		if id, ok := ParseCabinID(r.FormValue("hytter")); ok {
			links = []cabinLink{{id: id}}
		}
	}
	if len(links) == 0 {
		redirect(w, r, "feil", "Fant ingen hytte. Lim inn lenken fra hyttebestilling.dnt.no (den med /hytte/ og et tall).")
		return
	}
	from, err := time.ParseInLocation(dateLayout, r.FormValue("dato"), oslo)
	if err != nil {
		redirect(w, r, "feil", "Velg en ankomstdato.")
		return
	}
	until := from
	if v := r.FormValue("til"); v != "" {
		if until, err = time.ParseInLocation(dateLayout, v, oslo); err != nil || until.Before(from) {
			redirect(w, r, "feil", "Siste ankomstdato må være etter den første.")
			return
		}
	}
	nights := atoiDefault(r.FormValue("netter"), 1)
	guests := atoiDefault(r.FormValue("personer"), 1)
	var starts []string
	for d := from; !d.After(until); d = d.AddDate(0, 0, 1) {
		starts = append(starts, d.Format(dateLayout))
	}
	// Flere lenker følges hver for seg, som på e-post. «Rute» gjør dem til
	// én tur der alle må være ledige etter tur.
	var watches []Watch
	if r.FormValue("rute") != "" && len(links) > 1 {
		wa := Watch{Guests: guests, Starts: starts, Flexible: len(starts) > 1}
		for i, l := range links {
			wa.Legs = append(wa.Legs, Leg{CabinID: l.id, Offset: i * nights, Nights: nights})
		}
		watches = []Watch{wa}
	} else {
		for _, l := range links {
			watches = append(watches, Watch{Guests: guests, Starts: slices.Clone(starts), Flexible: len(starts) > 1, Legs: []Leg{{CabinID: l.id, Nights: nights}}})
		}
	}
	added, failed := AddAll(r.Context(), s.Store, s.Poller, s.Mailer, watches, "", "")
	if len(added) == 0 {
		redirect(w, r, "feil", "Fikk ikke lagt inn: "+strings.Join(failed, "; ")+".")
		return
	}
	names := make([]string, len(added))
	for i, wa := range added {
		names[i] = wa.Title()
	}
	redirect(w, r, "ok", "Følger nå med på "+strings.Join(names, ", ")+".")
}

func (s *Server) delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := s.Store.Delete(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	redirect(w, r, "ok", "Overvåkingen er fjernet.")
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
