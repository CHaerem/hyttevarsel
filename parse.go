package main

import (
	"cmp"
	"errors"
	"html"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Det Kristine kan skrive i en e-post (emne eller tekst):
//
//	<lenke>                                   datoene fra lenken (velg dem på DNT-siden før du deler)
//	<lenke> 12. juli                          én natt
//	<lenke> 12.07.2027 2 netter 3 personer
//	<lenke> 12.–14. juli                      ankomst 12., avreise 14.
//	<lenke> en natt mellom 10. og 20. juli    første ledige natt i perioden
//	<lenke> lørdager i august                 alle lørdager
//	<lenke> <lenke> <lenke> 12. juli          rute: én natt på hver, i rekkefølge
//	stopp [#12]                               (svar på et varsel) stopper overvåking 12
//	stopp alt, oversikt, hjelp
type RequestKind int

const (
	ReqAdd RequestKind = iota
	ReqStop
	ReqStopAll
	ReqList
)

type Request struct {
	Kind    RequestKind
	WatchID int
	Watch   Watch // ReqAdd: hyttene og datoene, uten navn og tilstand
}

var (
	ErrHelp   = errors.New("forstod ikke e-posten")
	ErrIgnore = errors.New("svar uten beskjed") // «takk!» på et varsel
	ErrNoDate = errors.New("fant ingen dato")
)

// Når hun deler en lenke uten datoer, spør vi om datoen med hytta i emnet
// («[hytte 101022071]»), så svaret hennes bare trenger datoen.
var hytteTag = regexp.MustCompile(`\[hytte ([\d,]+)\]`)

func HytteTag(links []cabinLink) string {
	ids := make([]string, len(links))
	for i, l := range links {
		ids[i] = strconv.Itoa(l.id)
	}
	return "[hytte " + strings.Join(ids, ",") + "]"
}

var (
	idPattern       = regexp.MustCompile(`\[#(\d+)\]`)
	linkPattern     = regexp.MustCompile(`(?i)(?:https?://)?hyttebestilling\.dnt\.no/hytte/\d+[^\s<>"')\]]*`)
	stopPattern     = regexp.MustCompile(`(?i)(^|[^\pL])(stopp|stop|booket|bestilt|slett|avslutt)([^\pL]|$)`)
	allPattern      = regexp.MustCompile(`(?i)(^|[^\pL])(alt|alle)([^\pL]|$)`)
	listPattern     = regexp.MustCompile(`(?i)(^|[^\pL])(oversikt|liste|status)([^\pL]|$)`)
	flexPattern     = regexp.MustCompile(`(?i)mellom|fleksib|hvilken som helst|ledig natt|når som helst`)
	quoteHeader     = regexp.MustCompile(`(?i)^(on|den|man|tir|ons|tor|fre|lør|søn|\d).*(wrote|skrev|skreiv)( .*)?:\s*$|^-{2,}\s*(original|opprinnelig|videresendt|forwarded)|^(fra|from):\s.*@`)
	numberWord      = `(\d{1,2}|en|én|ei|ett|to|tre|fire|fem|seks|sju|syv|åtte|ni|ti)`
	nightsPattern   = regexp.MustCompile(`(?i)(^|[^\pL\d])` + numberWord + `\s+(natt|netter)([^\pL]|$)`)
	guestsPattern   = regexp.MustCompile(`(?i)(^|[^\pL\d])` + numberWord + `\s+(personer|person|pers|voksne|voksen|stk|plasser|senger|seng)([^\pL]|$)|vi er\s+` + numberWord + `([^\pL]|$)`)
	monthNames      = `(januar|februar|mars|april|mai|juni|juli|august|september|oktober|november|desember|jan|feb|mar|apr|jun|jul|aug|sept|sep|okt|nov|des)`
	isoDate         = regexp.MustCompile(`(\d{4})-(\d{2})-(\d{2})`)
	numDate         = regexp.MustCompile(`(\d{1,2})\.(\d{1,2})(?:\.(\d{4}|\d{2}))?`)
	textDate        = regexp.MustCompile(`(?i)(^|[^\d])(\d{1,2})\.?\s*` + monthNames + `\.?(?:\s+(\d{4}))?`)
	textRange       = regexp.MustCompile(`(?i)(^|[^\d])(\d{1,2})\.?\s*(?:-|–|—|til|og)\s*(\d{1,2})\.?\s*` + monthNames + `\.?(?:\s+(\d{4}))?`)
	rangeSeparator  = regexp.MustCompile(`(?i)^\s*(?:-|–|—|til|og|to)\s*$`)
	weekdayInMonth  = regexp.MustCompile(`(?i)((?:(?:mandag|tirsdag|onsdag|torsdag|fredag|lørdag|søndag|helg)(?:er|ene|en)?\s*(?:,|og)?\s*)+)\s+i\s+` + monthNames + `(?:\s+(\d{4}))?`)
	weekdayWord     = regexp.MustCompile(`(?i)mandag|tirsdag|onsdag|torsdag|fredag|lørdag|søndag|helg`)
	tagPattern      = regexp.MustCompile(`(?s)<[^>]*>`)
	hrefPattern     = regexp.MustCompile(`(?i)<a\s[^>]*href="([^"]+)"[^>]*>`)
	blockTagPattern = regexp.MustCompile(`(?i)<(br|/p|/div|/li|/tr)[^>]*>`)
)

var numberWords = map[string]int{"en": 1, "én": 1, "ei": 1, "ett": 1, "to": 2, "tre": 3, "fire": 4, "fem": 5, "seks": 6, "sju": 7, "syv": 7, "åtte": 8, "ni": 9, "ti": 10}

func parseNumber(s string) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return numberWords[strings.ToLower(s)]
}

var months = map[string]time.Month{
	"jan": 1, "feb": 2, "mar": 3, "apr": 4, "mai": 5, "jun": 6, "jul": 7, "aug": 8, "sep": 9, "sept": 9, "okt": 10, "nov": 11, "des": 12,
}

func parseMonth(s string) time.Month {
	s = strings.ToLower(s)
	if len(s) > 3 && s != "sept" {
		s = s[:3]
	}
	return months[s]
}

var weekdays = map[string][]time.Weekday{
	"man": {time.Monday}, "tir": {time.Tuesday}, "ons": {time.Wednesday}, "tor": {time.Thursday},
	"fre": {time.Friday}, "lør": {time.Saturday}, "søn": {time.Sunday}, "hel": {time.Friday, time.Saturday},
}

// HTMLToText gjør en HTML-e-post om til tekst og beholder lenkene, siden
// en delt lenke ofte bare står i href-en.
func HTMLToText(s string) string {
	s = hrefPattern.ReplaceAllString(s, " $1 ")
	s = blockTagPattern.ReplaceAllString(s, "\n")
	s = tagPattern.ReplaceAllString(s, " ")
	return html.UnescapeString(s)
}

// StripQuoted kutter sitert tekst fra tidligere e-poster i tråden, så et
// «stopp» eller en lenke i sitatet ikke blir lest som en ny beskjed.
func StripQuoted(body string) string {
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, ">") || quoteHeader.MatchString(t) {
			break
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

type cabinLink struct {
	id       int
	from, to time.Time
	guests   int
}

func parseLinks(text string) []cabinLink {
	var out []cabinLink
	for _, raw := range linkPattern.FindAllString(text, -1) {
		raw = strings.TrimRight(raw, ".,;:!?")
		id, ok := ParseCabinID(raw)
		if !ok {
			continue
		}
		l := cabinLink{id: id}
		if u, err := url.Parse(ensureScheme(raw)); err == nil {
			q := u.Query()
			l.from, _ = time.ParseInLocation(dateLayout, q.Get("fromDate"), oslo)
			l.to, _ = time.ParseInLocation(dateLayout, q.Get("toDate"), oslo)
			l.guests = parseNumber(q.Get("numberOfGuests"))
			if a := parseNumber(q.Get("adults")); a > l.guests {
				l.guests = a
			}
		}
		if len(out) > 0 && out[len(out)-1].id == id {
			continue // samme lenke to ganger (tekst og href)
		}
		out = append(out, l)
	}
	return out
}

func ensureScheme(s string) string {
	if strings.HasPrefix(s, "http") {
		return s
	}
	return "https://" + s
}

// inferYear gir neste forekomst av dag og måned fra i dag.
func inferYear(day int, m time.Month, year string, now time.Time) (time.Time, bool) {
	if day < 1 || day > 31 || m < 1 || m > 12 {
		return time.Time{}, false
	}
	t0 := today(now)
	y := t0.Year()
	if year != "" {
		y, _ = strconv.Atoi(year)
		if y < 100 {
			y += 2000
		}
	}
	t := time.Date(y, m, day, 0, 0, 0, 0, oslo)
	if t.Day() != day {
		return time.Time{}, false // 31. juni o.l.
	}
	if year == "" && t.Before(t0) {
		t = t.AddDate(1, 0, 0)
	}
	return t, true
}

type dateToken struct {
	t          time.Time
	start, end int
}

func findDates(text string, now time.Time) []dateToken {
	var out []dateToken
	for _, m := range isoDate.FindAllStringSubmatchIndex(text, -1) {
		y, _ := strconv.Atoi(text[m[2]:m[3]])
		mo, _ := strconv.Atoi(text[m[4]:m[5]])
		d, _ := strconv.Atoi(text[m[6]:m[7]])
		if t, ok := inferYear(d, time.Month(mo), strconv.Itoa(y), now); ok {
			out = append(out, dateToken{t, m[0], m[1]})
		}
	}
	for _, m := range numDate.FindAllStringSubmatchIndex(text, -1) {
		// RE2 har ikke lookaround: sjekk grensene for hånd, så «112.07» og
		// «12.075» ikke blir datoer.
		if m[0] > 0 && strings.ContainsRune("0123456789.", rune(text[m[0]-1])) ||
			m[1] < len(text) && text[m[1]] >= '0' && text[m[1]] <= '9' {
			continue
		}
		d, _ := strconv.Atoi(text[m[2]:m[3]])
		mo, _ := strconv.Atoi(text[m[4]:m[5]])
		year := ""
		if m[6] >= 0 {
			year = text[m[6]:m[7]]
		}
		if t, ok := inferYear(d, time.Month(mo), year, now); ok {
			out = append(out, dateToken{t, m[0], m[1]})
		}
	}
	for _, m := range textDate.FindAllStringSubmatchIndex(text, -1) {
		d, _ := strconv.Atoi(text[m[4]:m[5]])
		year := ""
		if m[8] >= 0 {
			year = text[m[8]:m[9]]
		}
		if t, ok := inferYear(d, parseMonth(text[m[6]:m[7]]), year, now); ok {
			out = append(out, dateToken{t, m[4], m[1]})
		}
	}
	slices.SortFunc(out, func(a, b dateToken) int { return a.start - b.start })
	return out
}

type parsedDates struct {
	starts   []time.Time
	nights   int  // 0 = ikke gitt av datoene
	flexible bool // flere mulige startdatoer
}

func parseDates(text string, explicitNights int, now time.Time) (parsedDates, bool) {
	flex := flexPattern.MatchString(text)

	// «lørdager i august», «fredager og lørdager i juli», «helger i juni»
	if m := weekdayInMonth.FindStringSubmatch(text); m != nil {
		var days []time.Weekday
		for _, w := range weekdayWord.FindAllString(m[1], -1) {
			days = append(days, weekdays[string([]rune(strings.ToLower(w))[:3])]...)
		}
		first, ok := inferYear(1, parseMonth(m[2]), m[3], now)
		if !ok {
			return parsedDates{}, false
		}
		var starts []time.Time
		for d := first; d.Month() == first.Month(); d = d.AddDate(0, 0, 1) {
			if slices.Contains(days, d.Weekday()) && !d.Before(today(now)) {
				starts = append(starts, d)
			}
		}
		return parsedDates{starts: starts, flexible: true}, len(starts) > 0
	}

	var a, b time.Time
	// «10.–20. juli», «10 til 20 juli»
	if m := textRange.FindStringSubmatch(text); m != nil {
		d1, _ := strconv.Atoi(m[2])
		d2, _ := strconv.Atoi(m[3])
		mo := parseMonth(m[4])
		var ok1, ok2 bool
		a, ok1 = inferYear(d1, mo, m[5], now)
		b, ok2 = inferYear(d2, mo, m[5], now)
		if !ok1 || !ok2 {
			return parsedDates{}, false
		}
		if b.Before(a) {
			b = b.AddDate(0, 1, 0) // «28.–2. august» er sjelden; anta neste måned
		}
	} else {
		toks := findDates(text, now)
		if len(toks) == 0 {
			return parsedDates{}, false
		}
		// To datoer med bare «–», «til» eller «og» mellom seg er en periode.
		if len(toks) == 2 && rangeSeparator.MatchString(text[toks[0].end:toks[1].start]) && toks[1].t.After(toks[0].t) {
			a, b = toks[0].t, toks[1].t
		} else {
			var starts []time.Time
			for _, t := range toks {
				starts = append(starts, t.t)
			}
			return parsedDates{starts: starts, flexible: len(starts) > 1}, true
		}
	}

	// En periode: et opphold fra a til b, eller en mulig ankomst hver dag
	// når hun ber om én natt «mellom» eller gir antall netter i tillegg.
	if !flex && explicitNights == 0 {
		return parsedDates{starts: []time.Time{a}, nights: int(b.Sub(a).Hours()/24 + 0.5)}, true
	}
	n := max(explicitNights, 1)
	var starts []time.Time
	for d := a; !d.After(b.AddDate(0, 0, -n)) || d.Equal(a); d = d.AddDate(0, 0, 1) {
		starts = append(starts, d)
	}
	return parsedDates{starts: starts, flexible: len(starts) > 1}, true
}

// ParseMessage tolker emne og tekst fra en e-post.
func ParseMessage(subject, body string, now time.Time) (Request, error) {
	body = StripQuoted(body)
	id := 0
	if m := idPattern.FindStringSubmatch(subject); m != nil {
		id, _ = strconv.Atoi(m[1])
	}
	clean := func(s string) string {
		return idPattern.ReplaceAllString(linkPattern.ReplaceAllString(s, " "), " ")
	}
	all := clean(subject + "\n" + body)
	// Et svar på våre e-poster har hyttenavn og dato i emnet. Da teller bare
	// det hun skrev selv.
	own := all
	if id > 0 {
		own = clean(body)
	}
	links := parseLinks(subject + "\n" + body)
	if m := hytteTag.FindStringSubmatch(subject); m != nil && len(links) == 0 {
		for _, id := range strings.Split(m[1], ",") {
			if n, err := strconv.Atoi(id); err == nil {
				links = append(links, cabinLink{id: n})
			}
		}
	}
	all = hytteTag.ReplaceAllString(all, " ")
	own = hytteTag.ReplaceAllString(own, " ")

	if stopPattern.MatchString(all) {
		switch {
		case id > 0:
			return Request{Kind: ReqStop, WatchID: id}, nil
		case allPattern.MatchString(own) && len(links) == 0:
			return Request{Kind: ReqStopAll}, nil
		}
	}
	if len(links) == 0 {
		if listPattern.MatchString(own) {
			return Request{Kind: ReqList}, nil
		}
		if id > 0 {
			return Request{}, ErrIgnore
		}
		return Request{}, ErrHelp
	}
	return parseAdd(own, links, now)
}

func legsOf(links []cabinLink) []Leg {
	legs := make([]Leg, len(links))
	for i, l := range links {
		legs[i] = Leg{CabinID: l.id}
	}
	return legs
}

func parseAdd(plain string, links []cabinLink, now time.Time) (Request, error) {
	nights := 0
	if m := nightsPattern.FindStringSubmatch(plain); m != nil {
		nights = parseNumber(m[2])
	}
	guests := 0
	if m := guestsPattern.FindStringSubmatch(plain); m != nil {
		guests = parseNumber(m[2])
		if guests == 0 {
			guests = parseNumber(m[5])
		}
	}
	if guests == 0 {
		for _, l := range links {
			guests = max(guests, l.guests)
		}
	}
	w := Watch{Guests: max(guests, 1)}

	if pd, ok := parseDates(plain, nights, now); ok {
		n := max(nights, pd.nights, 1)
		offset := 0
		for _, l := range links {
			w.Legs = append(w.Legs, Leg{CabinID: l.id, Offset: offset, Nights: n})
			offset += n
		}
		for _, s := range pd.starts {
			w.Starts = append(w.Starts, s.Format(dateLayout))
		}
		w.Flexible = pd.flexible
		return Request{Kind: ReqAdd, Watch: w}, nil
	}

	// Ingen dato i teksten: bruk datoene i lenkene (DNT-siden legger dem i
	// adressen når hun har valgt datoer før hun deler).
	var start time.Time
	for _, l := range links {
		if l.from.IsZero() || !l.to.After(l.from) {
			return Request{Watch: Watch{Legs: legsOf(links)}}, ErrNoDate
		}
		if start.IsZero() || l.from.Before(start) {
			start = l.from
		}
	}
	for _, l := range links {
		w.Legs = append(w.Legs, Leg{
			CabinID: l.id,
			Offset:  int(l.from.Sub(start).Hours()/24 + 0.5),
			Nights:  cmp.Or(nights, int(l.to.Sub(l.from).Hours()/24+0.5)),
		})
	}
	w.Starts = []string{start.Format(dateLayout)}
	return Request{Kind: ReqAdd, Watch: w}, nil
}
