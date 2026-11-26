package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// SecretLink er den hemmelige delen av lenken til «Mine varsler»: 32
// tilfeldige byte som tjenesten lager selv og legger i volumet, så de aldri
// går gjennom git, chat eller loggen. Den kan byttes mens tjenesten kjører
// («ny lenke» på e-post); da slutter den gamle å virke med en gang.
type SecretLink struct {
	path  string
	mu    sync.RWMutex
	token string
}

func newToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// OpenSecretLink leser lenken fra volumet, eller lager den første gang.
func OpenSecretLink(dir string) (*SecretLink, error) {
	l := &SecretLink{path: filepath.Join(dir, "lenke")}
	if b, err := os.ReadFile(l.path); err == nil {
		if t := strings.TrimSpace(string(b)); len(t) >= 40 {
			l.token = t
			return l, nil
		}
	}
	_, err := l.Rotate()
	return l, err
}

func (l *SecretLink) Token() string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.token
}

// Match sammenligner uten å røpe hvor langt en gjetning var riktig.
func (l *SecretLink) Match(candidate string) bool {
	return subtle.ConstantTimeCompare([]byte(candidate), []byte(l.Token())) == 1
}

// Rotate lager en ny lenke og lagrer den. Den gamle virker ikke lenger.
func (l *SecretLink) Rotate() (string, error) {
	t, err := newToken()
	if err != nil {
		return "", err
	}
	if l.path != "" {
		tmp := l.path + ".tmp"
		if err := os.WriteFile(tmp, []byte(t+"\n"), 0o600); err != nil {
			return "", err
		}
		if err := os.Rename(tmp, l.path); err != nil {
			return "", err
		}
	}
	l.mu.Lock()
	l.token = t
	l.mu.Unlock()
	return t, nil
}
