package main

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

//go:embed templates/*.html
var templateFS embed.FS

var templates = template.Must(template.New("").Funcs(template.FuncMap{
	"dato":       norskDato,
	"netter":     netter,
	"personer":   personer,
	"bookingURL": func(w Watch) string { return BookingURL(w.CabinID, w.FromDate(), w.ToDate(), w.Guests) },
	"klokke": func(t time.Time) string {
		if t.IsZero() {
			return "ikke ennå"
		}
		return t.In(oslo).Format("02.01. kl. 15:04")
	},
}).ParseFS(templateFS, "templates/*.html"))

type Server struct {
	Store  *Store
	Poller *Poller
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.index)
	mux.HandleFunc("POST /watches", s.add)
	mux.HandleFunc("POST /watches/{id}/delete", s.delete)
	mux.HandleFunc("GET /healthz", s.healthz)
	return mux
}

type indexData struct {
	Active, Expired []Watch
	Error, Notice   string
	Today, MaxDate  string
	Interval        time.Duration
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	now := time.Now().In(oslo)
	d := indexData{
		Error:    r.URL.Query().Get("feil"),
		Notice:   r.URL.Query().Get("ok"),
		Today:    now.Format(dateLayout),
		MaxDate:  now.AddDate(1, 1, 0).Format(dateLayout),
		Interval: s.Poller.Interval,
	}
	for _, wa := range s.Store.List() {
		if wa.Expired(now) {
			d.Expired = append(d.Expired, wa)
		} else {
			d.Active = append(d.Active, wa)
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
	cabinID, ok := ParseCabinID(r.FormValue("hytte"))
	if !ok {
		redirect(w, r, "feil", "Fant ikke hytta i lenken. Lim inn lenken fra hyttebestilling.dnt.no (den med /hytte/ og et tall).")
		return
	}
	from, err := time.ParseInLocation(dateLayout, r.FormValue("dato"), oslo)
	if err != nil {
		redirect(w, r, "feil", "Velg en ankomstdato.")
		return
	}
	nights := atoiDefault(r.FormValue("netter"), 1)
	guests := atoiDefault(r.FormValue("personer"), 1)
	if nights < 1 || nights > 14 || guests < 1 || guests > 20 {
		redirect(w, r, "feil", "Antall netter må være 1–14 og antall personer 1–20.")
		return
	}
	wa := Watch{CabinID: cabinID, From: from.Format(dateLayout), Nights: nights, Guests: guests, CreatedAt: time.Now()}
	if wa.Expired(time.Now()) {
		redirect(w, r, "feil", "Datoen har vært.")
		return
	}

	// Sjekk med en gang: da får vi navnet på hytta, og en hytte uten
	// nettbooking avvises her i stedet for å feile stille hver runde.
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if _, err := s.Poller.DNT.Check(ctx, wa.CabinID, wa.FromDate(), wa.ToDate()); err != nil {
		msg := "DNT svarte ikke som ventet for denne hytta. Har den nettbooking?"
		if strings.Contains(err.Error(), "web products") {
			msg = "Denne hytta kan ikke bestilles på nett, så den kan ikke overvåkes."
		}
		redirect(w, r, "feil", msg)
		return
	}
	name, err := s.Poller.DNT.CabinName(ctx, wa.CabinID)
	if err != nil {
		slog.Warn("fant ikke hyttenavnet", "cabin", wa.CabinID, "err", err)
		name = fmt.Sprintf("Hytte %d", wa.CabinID)
	}
	wa.CabinName = name
	wa, err = s.Store.Add(wa)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.Poller.CheckOne(ctx, wa)
	redirect(w, r, "ok", "Følger nå med på "+wa.CabinName+", "+norskDato(wa.FromDate())+".")
}

func (s *Server) delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := s.Store.Delete(id); err != nil {
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
