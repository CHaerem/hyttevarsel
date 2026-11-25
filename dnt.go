package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Endepunktet hyttebestilling.dnt.no selv bruker for ledighet (målt
// 25. nov. 2026). Det krever ikke innlogging. `availability` har én verdi
// per rom eller seng: ledige plasser gjennom hele oppholdet.
const dntBase = "https://hyttebestilling.dnt.no"

const userAgent = "hyttevarsel (privat varsling for ett DNT-medlem; github.com/CHaerem/hyttevarsel)"

type Availability struct {
	Free     int // sum av ledige plasser over alle enheter
	Capacity int
}

type DNT struct {
	Base   string
	Client *http.Client
}

func NewDNT() *DNT {
	return &DNT{Base: dntBase, Client: &http.Client{Timeout: 30 * time.Second}}
}

type cabinAvailabilityResponse struct {
	Data *struct {
		Availability                    []int `json:"availability"`
		MaxTotalUnitsThatCanBeAvailable int   `json:"maxTotalUnitsThatCanBeAvailable"`
	} `json:"data"`
	Error any `json:"error"`
}

// Check spør om ledighet for oppholdet [from, to).
func (d *DNT) Check(ctx context.Context, cabinID int, from, to time.Time) (Availability, error) {
	q := url.Values{}
	q.Set("cabinId", strconv.Itoa(cabinID))
	q.Set("fromDate", from.Format(dateLayout))
	q.Set("toDate", to.Format(dateLayout))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.Base+"/api/booking/cabin-availability?"+q.Encode(), nil)
	if err != nil {
		return Availability{}, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := d.Client.Do(req)
	if err != nil {
		return Availability{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Availability{}, fmt.Errorf("DNT svarte %s", resp.Status)
	}
	var r cabinAvailabilityResponse
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return Availability{}, fmt.Errorf("uventet svar fra DNT: %w", err)
	}
	if r.Data == nil {
		// Hytter uten nettbooking svarer «Couldn't fetch web products».
		return Availability{}, fmt.Errorf("DNT: %v", r.Error)
	}
	a := Availability{Capacity: r.Data.MaxTotalUnitsThatCanBeAvailable}
	for _, n := range r.Data.Availability {
		a.Free += n
	}
	return a, nil
}

// BookingURL lenker rett til hyttesiden med datoene og antall voksne fylt inn.
func BookingURL(cabinID int, from, to time.Time, guests int) string {
	q := url.Values{}
	q.Set("fromDate", from.Format(dateLayout))
	q.Set("toDate", to.Format(dateLayout))
	q.Set("adults", strconv.Itoa(guests))
	return fmt.Sprintf("%s/hytte/%d?%s", dntBase, cabinID, q.Encode())
}

var (
	cabinURLPattern = regexp.MustCompile(`/hytte/(\d+)`)
	cabinIDPattern  = regexp.MustCompile(`^\s*(\d+)\s*$`)
)

// ParseCabinID godtar både en lenke fra hyttebestilling.dnt.no og bare tallet.
func ParseCabinID(s string) (int, bool) {
	m := cabinURLPattern.FindStringSubmatch(s)
	if m == nil {
		m = cabinIDPattern.FindStringSubmatch(s)
	}
	if m == nil {
		return 0, false
	}
	id, err := strconv.Atoi(m[1])
	return id, err == nil && id > 0
}

var titlePattern = regexp.MustCompile(`<title>([^<|]+?)\s*(?:\|[^<]*)?</title>`)

// CabinName henter navnet fra hyttesidens <title> («Babord - Gressholmen |
// DNT Hyttebestilling»). Ledighetssvaret har bare romnavnene. Kalles én gang,
// når overvåkingen legges til.
func (d *DNT) CabinName(ctx context.Context, cabinID int) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/hytte/%d", d.Base, cabinID), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := d.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("DNT svarte %s", resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10)) // <title> står tidlig i <head>
	if err != nil {
		return "", err
	}
	m := titlePattern.FindSubmatch(b)
	if m == nil {
		return "", fmt.Errorf("fant ikke navnet på hytte %d", cabinID)
	}
	return html.UnescapeString(strings.TrimSpace(string(m[1]))), nil
}
