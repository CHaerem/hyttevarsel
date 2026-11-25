package main

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Ordrebekreftelser fra DNT, videresendt til varseladressen av et filter
// hos Kristine, stopper overvåkingen for hytta og datoen hun bestilte. Da
// slipper hun å trykke «Jeg har booket».
//
// Vi har ikke sett en ekte bekreftelse ennå (25. nov. 2026), så gjenkjenningen
// er bevisst romslig og trygg: e-posten må ligne en bestilling, nevne
// hyttenavnet og ankomstdatoen til en aktiv overvåking. Treffer den ikke,
// skjer ingenting.

var bookingWords = regexp.MustCompile(`(?i)bestilling|ordre|booking|bekreft|kvittering|reservasjon|order|confirmation`)

var spaces = regexp.MustCompile(`\s+`)

func normalize(s string) string {
	return spaces.ReplaceAllString(strings.ToLower(s), " ")
}

// dateForms er måtene en ankomstdato kan stå skrevet i en bekreftelse.
func dateForms(t time.Time) []string {
	d, m, y := t.Day(), int(t.Month()), t.Year()
	mon := måneder[m-1]
	return []string{
		t.Format(dateLayout),
		fmt.Sprintf("%02d.%02d.%d", d, m, y),
		fmt.Sprintf("%d.%d.%d", d, m, y),
		fmt.Sprintf("%02d/%02d/%d", d, m, y),
		fmt.Sprintf("%d. %s %d", d, mon, y),
		fmt.Sprintf("%d %s %d", d, mon, y),
		fmt.Sprintf("%d. %s %d", d, mon[:3], y),
		fmt.Sprintf("%d %s %d", d, mon[:3], y),
		fmt.Sprintf("%02d.%02d.%02d", d, m, y%100),
	}
}

// nameForms er hyttenavnet og delene av det («Babord - Gressholmen» kan
// stå som «Babord»).
func nameForms(name string) []string {
	out := []string{normalize(name)}
	for _, part := range strings.Split(name, " - ") {
		if p := normalize(strings.TrimSpace(part)); len([]rune(p)) >= 4 && p != out[0] {
			out = append(out, p)
		}
	}
	return out
}

func containsAny(text string, forms []string) bool {
	for _, f := range forms {
		if strings.Contains(text, f) {
			return true
		}
	}
	return false
}

// containsDate er som containsAny, men «1. juli» treffer ikke i «11. juli».
func containsDate(text string, forms []string) bool {
	for _, f := range forms {
		for i := 0; ; {
			j := strings.Index(text[i:], f)
			if j < 0 {
				break
			}
			j += i
			if j == 0 || text[j-1] < '0' || text[j-1] > '9' {
				return true
			}
			i = j + 1
		}
	}
	return false
}

// MatchBooking finner overvåkingene en bestillingsbekreftelse gjelder.
func MatchBooking(watches []Watch, subject, text string, now time.Time) []Watch {
	all := normalize(subject + "\n" + text)
	if !bookingWords.MatchString(all) {
		return nil
	}
	var out []Watch
	for _, w := range watches {
		if w.Expired(now) {
			continue
		}
		matched := false
		for _, l := range w.Legs {
			if l.CabinName == "" || !containsAny(all, nameForms(l.CabinName)) {
				continue
			}
			for _, s := range w.ActiveStarts(now) {
				from, _ := l.Stay(s)
				if containsDate(all, dateForms(from)) {
					matched = true
				}
			}
		}
		if matched {
			out = append(out, w)
		}
	}
	return out
}
