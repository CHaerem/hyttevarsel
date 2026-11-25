package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

const dateLayout = "2006-01-02"

// Grenser per overvåking, så én e-post ikke kan gi hundrevis av kall.
const (
	maxLegs     = 6
	maxStarts   = 62 // helgene i et halvt år
	maxChecks   = 62
	maxNights   = 14
	maxGuests   = 20
	bookAheadMo = 13 // DNT åpner et år fram; litt slakk
)

// Leg er én hytte i en overvåking. En vanlig overvåking har én; en
// hytte-til-hytte-rute har flere, med ankomst Offset dager etter start.
type Leg struct {
	CabinID   int    `json:"cabin_id"`
	CabinName string `json:"cabin_name"`
	Offset    int    `json:"offset"`
	Nights    int    `json:"nights"`
}

// Check er siste kjente ledighet for én hytte og ett opphold.
type Check struct {
	Free        int       `json:"free"`
	Known       bool      `json:"known"`
	LastChecked time.Time `json:"last_checked"`
	NextCheck   time.Time `json:"next_check"`
	LastError   string    `json:"last_error,omitempty"`
}

// Option er én mulig startdato. Den er ledig når alle hyttene har plass.
type Option struct {
	Free         bool      `json:"free"`
	LastNotified time.Time `json:"last_notified"`
}

type Watch struct {
	ID        int       `json:"id"`
	Legs      []Leg     `json:"legs"`
	Starts    []string  `json:"starts"` // mulige startdatoer, YYYY-MM-DD, sortert
	Guests    int       `json:"guests"`
	Flexible  bool      `json:"flexible"` // ett av flere startdatoer holder
	CreatedAt time.Time `json:"created_at"`
	Requester string    `json:"requester,omitempty"` // e-postadressen som la den inn

	Checks  map[string]*Check  `json:"checks"`
	Options map[string]*Option `json:"options"`
}

func parseDate(s string) time.Time {
	t, _ := time.ParseInLocation(dateLayout, s, oslo)
	return t
}

func today(now time.Time) time.Time {
	y, m, d := now.In(oslo).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, oslo)
}

// LegStay er ankomst og avreise for et ben gitt en startdato.
func (l Leg) Stay(start string) (from, to time.Time) {
	from = parseDate(start).AddDate(0, 0, l.Offset)
	return from, from.AddDate(0, 0, l.Nights)
}

func checkKey(l Leg, start string) string {
	from, _ := l.Stay(start)
	return fmt.Sprintf("%d/%s/%d", l.CabinID, from.Format(dateLayout), l.Nights)
}

// ActiveStarts er startdatoene som ikke har vært.
func (w Watch) ActiveStarts(now time.Time) []string {
	t := today(now).Format(dateLayout)
	var out []string
	for _, s := range w.Starts {
		if s >= t {
			out = append(out, s)
		}
	}
	return out
}

func (w Watch) Expired(now time.Time) bool { return len(w.ActiveStarts(now)) == 0 }

func (w Watch) LastStart() time.Time { return parseDate(w.Starts[len(w.Starts)-1]) }

// OptionFree er sann når alle benene for startdatoen har plass til følget.
func (w Watch) OptionFree(start string) bool {
	for _, l := range w.Legs {
		c := w.Checks[checkKey(l, start)]
		if c == nil || !c.Known || c.LastError != "" || c.Free < w.Guests {
			return false
		}
	}
	return true
}

// FreeStarts er startdatoene som er ledige nå.
func (w Watch) FreeStarts(now time.Time) []string {
	var out []string
	for _, s := range w.ActiveStarts(now) {
		if o := w.Options[s]; o != nil && o.Free {
			out = append(out, s)
		}
	}
	return out
}

func (w Watch) IsRoute() bool { return len(w.Legs) > 1 }

func (w Watch) Title() string {
	names := make([]string, len(w.Legs))
	for i, l := range w.Legs {
		names[i] = l.Name()
	}
	return strings.Join(names, " → ")
}

func (l Leg) Name() string {
	if l.CabinName != "" {
		return l.CabinName
	}
	return fmt.Sprintf("Hytte %d", l.CabinID)
}

// Init lager tilstanden og sjekker grensene. Kalles før Add.
func (w *Watch) Init(now time.Time) error {
	if len(w.Legs) == 0 {
		return errors.New("fant ingen hytte")
	}
	if len(w.Legs) > maxLegs {
		return fmt.Errorf("en rute kan ha høyst %d hytter", maxLegs)
	}
	slices.Sort(w.Starts)
	w.Starts = slices.Compact(w.Starts)
	w.Starts = w.ActiveStarts(now)
	if len(w.Starts) == 0 {
		return errors.New("datoen har vært")
	}
	if len(w.Starts) > maxStarts {
		return fmt.Errorf("høyst %d mulige datoer per overvåking", maxStarts)
	}
	if len(w.Starts)*len(w.Legs) > maxChecks {
		return fmt.Errorf("for mange kombinasjoner av hytter og datoer (høyst %d)", maxChecks)
	}
	if last := today(now).AddDate(0, bookAheadMo, 0); w.LastStart().After(last) {
		return errors.New("DNT åpner bare for bestilling omtrent et år fram")
	}
	if w.Guests < 1 || w.Guests > maxGuests {
		return fmt.Errorf("antall personer må være 1–%d", maxGuests)
	}
	for _, l := range w.Legs {
		if l.Nights < 1 || l.Nights > maxNights {
			return fmt.Errorf("antall netter må være 1–%d", maxNights)
		}
	}
	w.Checks = map[string]*Check{}
	w.Options = map[string]*Option{}
	for _, s := range w.Starts {
		w.Options[s] = &Option{}
		for _, l := range w.Legs {
			w.Checks[checkKey(l, s)] = &Check{}
		}
	}
	if w.CreatedAt.IsZero() {
		w.CreatedAt = now
	}
	return nil
}

// Store holder overvåkingene i én JSON-fil. Det er en håndfull rader og én
// prosess, så en fil som skrives atomisk er nok (ingen database å drifte).
type Store struct {
	path string
	mu   sync.Mutex
	data storeData
}

type storeData struct {
	NextID      int       `json:"next_id"`
	Watches     []*Watch  `json:"watches"`
	WelcomeSent bool      `json:"welcome_sent"`
	LinkSent    bool      `json:"link_sent"`
	LastSummary time.Time `json:"last_summary"`
	LastAlarm   time.Time `json:"last_alarm"`
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

func clone(w *Watch) Watch {
	b, _ := json.Marshal(w)
	var c Watch
	json.Unmarshal(b, &c)
	return c
}

// List gir kopier, sortert på første startdato.
func (s *Store) List() []Watch {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Watch, len(s.data.Watches))
	for i, w := range s.data.Watches {
		out[i] = clone(w)
	}
	slices.SortFunc(out, func(a, b Watch) int {
		if c := strings.Compare(a.Starts[0], b.Starts[0]); c != 0 {
			return c
		}
		return a.ID - b.ID
	})
	return out
}

func (s *Store) Get(id int) (Watch, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, w := range s.data.Watches {
		if w.ID == id {
			return clone(w), true
		}
	}
	return Watch{}, false
}

func (s *Store) Add(w Watch) (Watch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w.ID = s.data.NextID
	s.data.NextID++
	c := clone(&w)
	s.data.Watches = append(s.data.Watches, &c)
	return w, s.saveLocked()
}

func (s *Store) Delete(id int) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.data.Watches)
	s.data.Watches = slices.DeleteFunc(s.data.Watches, func(w *Watch) bool { return w.ID == id })
	if len(s.data.Watches) == n {
		return false, nil
	}
	return true, s.saveLocked()
}

// Update kjører fn på overvåkingen med id og lagrer. Er den slettet i
// mellomtiden, skjer ingenting.
func (s *Store) Update(id int, fn func(*Watch)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, w := range s.data.Watches {
		if w.ID == id {
			fn(w)
			return s.saveLocked()
		}
	}
	return nil
}

func (s *Store) WelcomeSent() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.WelcomeSent
}

func (s *Store) SetWelcomeSent() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.WelcomeSent = true
	return s.saveLocked()
}

// LinkSent er sann når hun har fått lenken til «Mine varsler» på e-post.
func (s *Store) LinkSent() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.LinkSent
}

func (s *Store) SetLinkSent() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.LinkSent = true
	return s.saveLocked()
}

// Meta leser og endrer tidsstemplene for oppsummering og alarm.
func (s *Store) Meta(fn func(lastSummary, lastAlarm *time.Time)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.data.LastSummary, &s.data.LastAlarm)
	return s.saveLocked()
}
