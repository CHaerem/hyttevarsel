package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// VarselAgent starter Homelab-plattformens varselagent (Claude-rutinen
// «Homelab-varsel», docs/varselagent.md i Homelab). URL og token leses
// fra filer hver gang, slik at en Secret som kommer eller fornyes etter
// oppstart tas i bruk uten omstart.
type VarselAgent struct {
	URLFile, TokenFile string
	Client             *http.Client
}

func (v *VarselAgent) Raise(ctx context.Context, title, text string) error {
	u, err1 := os.ReadFile(v.URLFile)
	t, err2 := os.ReadFile(v.TokenFile)
	if err1 != nil || err2 != nil {
		return fmt.Errorf("varselagenten er ikke satt opp (%s)", v.URLFile)
	}
	body, _ := json.Marshal(map[string]string{"text": fmt.Sprintf(
		"Vert: hyttevarsel (deployment i klusteret)\nTittel: %s\nMottaker: agent\nTid: %s\n\n%s\n",
		title, time.Now().UTC().Format(time.RFC3339), text)})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSpace(string(u)), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(t)))
	req.Header.Set("anthropic-version", "2026-10-21")
	req.Header.Set("Content-Type", "application/json")
	resp, err := v.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("varselagenten svarte %s", resp.Status)
	}
	return nil
}
