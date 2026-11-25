package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

const dateLayout = "2006-01-02"

// Status for siste sjekk av en overvåking.
const (
	StatusUnknown = ""      // ikke sjekket ennå
	StatusFull    = "full"  // ingen plass til hele følget
	StatusFree    = "ledig" // plass nok, varsel sendt ved overgangen
)

type Watch struct {
	ID        int       `json:"id"`
	CabinID   int       `json:"cabin_id"`
	CabinName string    `json:"cabin_name"`
	From      string    `json:"from"` // ankomst, YYYY-MM-DD
	Nights    int       `json:"nights"`
	Guests    int       `json:"guests"`
	CreatedAt time.Time `json:"created_at"`

	Status       string    `json:"status"`
	Free         int       `json:"free"`
	LastChecked  time.Time `json:"last_checked"`
	LastError    string    `json:"last_error,omitempty"` // siste sjekk feilet; Status er forrige kjente
	LastNotified time.Time `json:"last_notified"`
}

func (w Watch) FromDate() time.Time {
	t, _ := time.ParseInLocation(dateLayout, w.From, oslo)
	return t
}

func (w Watch) ToDate() time.Time { return w.FromDate().AddDate(0, 0, w.Nights) }

// Expired er sann når ankomstdagen er passert.
func (w Watch) Expired(now time.Time) bool {
	y, m, d := now.In(oslo).Date()
	return w.FromDate().Before(time.Date(y, m, d, 0, 0, 0, 0, oslo))
}

// Store holder overvåkingene i én JSON-fil. Det er en håndfull rader og én
// prosess, så en fil som skrives atomisk er nok (ingen database å drifte).
type Store struct {
	path string
	mu   sync.Mutex
	data storeData
}

type storeData struct {
	NextID  int     `json:"next_id"`
	Watches []Watch `json:"watches"`
}

func OpenStore(dir string) (*Store, error) {
	s := &Store{path: filepath.Join(dir, "watches.json"), data: storeData{NextID: 1}}
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &s.data); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) saveLocked() error {
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *Store) List() []Watch {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := slices.Clone(s.data.Watches)
	slices.SortFunc(out, func(a, b Watch) int {
		if a.From != b.From {
			if a.From < b.From {
				return -1
			}
			return 1
		}
		return a.ID - b.ID
	})
	return out
}

func (s *Store) Add(w Watch) (Watch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w.ID = s.data.NextID
	s.data.NextID++
	s.data.Watches = append(s.data.Watches, w)
	return w, s.saveLocked()
}

func (s *Store) Delete(id int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Watches = slices.DeleteFunc(s.data.Watches, func(w Watch) bool { return w.ID == id })
	return s.saveLocked()
}

// Update kjører fn på overvåkingen med id og lagrer. Er den slettet i
// mellomtiden, skjer ingenting.
func (s *Store) Update(id int, fn func(*Watch)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Watches {
		if s.data.Watches[i].ID == id {
			fn(&s.data.Watches[i])
			return s.saveLocked()
		}
	}
	return nil
}
