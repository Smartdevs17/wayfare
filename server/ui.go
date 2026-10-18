package server

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"

	)

// uiFS holds the single-page UI. It is embedded so the binary is the whole
// deployment: no build step, no asset pipeline, no separately-served static
// directory that can drift out of sync with the API it talks to.
//
//go:embed index.html
var uiFS embed.FS

// defaultCorridorKey is the corridor the page opens on (the API's default
// from/to pair). The <noscript> summary is rendered for it, so text browsers
// and crawlers see the same history-first record the API would serve.
const defaultCorridorKey = "USDC-NGNC"

// uiSummary is the data the <noscript> block renders (issue #636). Every
// value is already in the record's stored form — nothing here derives,
// rounds, or fills an absent figure; an empty struct renders the explicit
// no-history message instead.
type uiSummary struct {
	Corridor     string
	Integrity    string
	FloorLossPct string
	WorstLossPct string
	RecordedAt   string
	Age          string
}

// uiPageData is the template data for the served page.
type uiPageData struct {
	Summary *uiSummary
}

var uiTemplate = template.Must(template.ParseFS(uiFS, "index.html"))

// summaryFor renders the noscript summary from the latest stored record for
// the default corridor, or nil when there is no store or no history yet.
func (s *Server) summaryFor(ctx context.Context) *uiSummary {
	if s.Store == nil {
		return nil
	}
	rec, err := s.Store.Latest(ctx, defaultCorridorKey)
	if err != nil || rec == nil {
		return nil
	}
	age := time.Since(rec.RecordedAt)
	if age < 0 {
		age = 0
	}
	return &uiSummary{
		Corridor:     defaultCorridorKey,
		Integrity:    rec.Integrity,
		FloorLossPct: rec.FloorLossPct,
		WorstLossPct: rec.WorstLossPct,
		RecordedAt:   rec.RecordedAt.UTC().Format(time.RFC3339),
		Age:          uiHumanAge(age),
	}
}

// uiHumanAge renders a duration the way the stored reading is described in
// prose: one unit, coarse enough to read at a glance.
func uiHumanAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		m := int(d.Minutes())
		return uiPlural(m, "minute")
	case d < 48*time.Hour:
		h := int(d.Hours())
		return uiPlural(h, "hour")
	default:
		days := int(d.Hours() / 24)
		return uiPlural(days, "day")
	}
}

func uiPlural(n int, unit string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s ago", unit)
	}
	return fmt.Sprintf("%d %ss ago", n, unit)
}

// uiHandler serves the single-page UI, rendering the <noscript> summary from
// the history-first record when one exists. A template failure falls back to
// the raw embedded page: the UI must never 500 because a summary field
// could not be rendered.
func (s *Server) uiHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		data := uiPageData{Summary: s.summaryFor(r.Context())}
		var buf bytes.Buffer
		if err := uiTemplate.Execute(&buf, data); err != nil {
			body, err := uiFS.ReadFile("index.html")
			if err != nil {
				http.Error(w, "ui unavailable", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(body)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(buf.Bytes())
	})
}

// uiSourceRaw returns the raw embedded page source, for tests that assert on
// the file's text rather than a rendered response.
func uiSourceRaw() (string, error) {
	b, err := uiFS.ReadFile("index.html")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// noscriptSummary extracts the rendered <noscript> block from a page body.
// Used by tests to assert on what a reader without JavaScript sees.
func noscriptSummary(page string) string {
	start := strings.Index(page, "<noscript>")
	end := strings.Index(page, "</noscript>")
	if start < 0 || end < 0 || end < start {
		return ""
	}
	return page[start : end+len("</noscript>")]
}
