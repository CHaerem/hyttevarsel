package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Cabin er en hytte som kan bestilles på nett.
type Cabin struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Area string `json:"area,omitempty"`
}

// Label er navnet slik det står i søkefeltet: «Lille Tømtehytta, Nordmarka».
// Området skiller hytter med samme navn.
func (c Cabin) Label() string {
	if c.Area == "" {
		return c.Name
	}
	return c.Name + ", " + c.Area
}

// Søkelista til hyttebestilling.dnt.no ligger innbakt i hver side
// (målt 25. nov. 2026: 587 hytter). Vilkårssiden er den minste av dem.
var cabinItem = regexp.MustCompile(`\{"label":"([^"]+)","value":(\d+),"type":"cabin","areas":\[(?:\{"id":\d+,"name":"([^"]*)"\})?`)

// CabinList henter alle hyttene med nettbooking, med navn og område.
func (d *DNT) CabinList(ctx context.Context) ([]Cabin, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.Base+"/bestillingsvilkaar", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := d.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("DNT svarte %s", resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	// Lista står som JSON inne i en JavaScript-streng, med \" for ".
	text := strings.ReplaceAll(string(b), `\"`, `"`)
	seen := map[int]bool{}
	var out []Cabin
	for _, m := range cabinItem.FindAllStringSubmatch(text, -1) {
		id, _ := strconv.Atoi(m[2])
		if id == 0 || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, Cabin{ID: id, Name: m[1], Area: m[3]})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("fant ingen hytter i søkelista hos DNT")
	}
	slices.SortFunc(out, func(a, b Cabin) int { return strings.Compare(strings.ToLower(a.Label()), strings.ToLower(b.Label())) })
	return out, nil
}

type cabinLister interface {
	CabinList(ctx context.Context) ([]Cabin, error)
}

// Directory er hyttelista vi søker i. Den hentes fra DNT én gang i døgnet
// og ligger i en fil, så en omstart eller en feil hos DNT ikke tømmer den.
type Directory struct {
	Source cabinLister
	path   string

	mu      sync.RWMutex
	cabins  []Cabin
	fetched time.Time
}

type directoryFile struct {
	Fetched time.Time `json:"fetched"`
	Cabins  []Cabin   `json:"cabins"`
}

func OpenDirectory(dir string, source cabinLister) *Directory {
	d := &Directory{Source: source, path: filepath.Join(dir, "hytter.json")}
	if b, err := os.ReadFile(d.path); err == nil {
		var f directoryFile
		if json.Unmarshal(b, &f) == nil {
			d.cabins, d.fetched = f.Cabins, f.Fetched
		}
	}
	return d
}

func (d *Directory) Refresh(ctx context.Context) error {
	cabins, err := d.Source.CabinList(ctx)
	if err != nil {
		return err
	}
	d.mu.Lock()
	d.cabins, d.fetched = cabins, time.Now()
	f := directoryFile{Fetched: d.fetched, Cabins: cabins}
	d.mu.Unlock()
	b, _ := json.Marshal(f)
	tmp := d.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, d.path)
}

// Run holder lista fersk.
func (d *Directory) Run(ctx context.Context) {
	for {
		d.mu.RLock()
		age := time.Since(d.fetched)
		d.mu.RUnlock()
		if age > 24*time.Hour {
			if err := d.Refresh(ctx); err != nil {
				slog.Warn("hyttelista ble ikke oppdatert", "err", err)
			} else {
				slog.Info("hyttelista oppdatert", "hytter", len(d.List()))
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Hour):
		}
	}
}

func (d *Directory) List() []Cabin {
	if d == nil {
		return nil
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	return slices.Clone(d.cabins)
}

// Name gir navnet på hytta, eller tom streng når den ikke står i lista.
func (d *Directory) Name(id int) string {
	if d == nil {
		return ""
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	for _, c := range d.cabins {
		if c.ID == id {
			return c.Name
		}
	}
	return ""
}

// Find slår opp det hun skrev i søkefeltet: hele «Navn, Område», bare
// navnet, eller en del av det. Flere treff betyr at hun må velge.
func (d *Directory) Find(query string) []Cabin {
	q := normalize(strings.TrimSpace(query))
	if q == "" || d == nil {
		return nil
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	var exact, prefix, contains []Cabin
	for _, c := range d.cabins {
		label, name := normalize(c.Label()), normalize(c.Name)
		switch {
		case label == q:
			return []Cabin{c}
		case name == q:
			exact = append(exact, c)
		case strings.HasPrefix(name, q):
			prefix = append(prefix, c)
		case strings.Contains(label, q):
			contains = append(contains, c)
		}
	}
	for _, group := range [][]Cabin{exact, prefix, contains} {
		if len(group) > 0 {
			return group
		}
	}
	return nil
}
