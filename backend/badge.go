package main

import (
	"encoding/json"
	"fmt"
	"html"
	"os"
	"regexp"
	"strings"
)

// Shields-style "uptime | 99.9%" badges for READMEs, one per service,
// served from the same JSON the status page uses. Service names become
// slugs ("Personal site" -> "personal-site").

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(name string) string {
	return strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(name), "-"), "-")
}

type badgeService struct {
	Name          string   `json:"name"`
	OK            bool     `json:"ok"`
	UptimePercent *float64 `json:"uptimePercent"`
}

// findBadgeService returns the service whose slug matches, or nil.
func findBadgeService(dataFile, slug string) (*badgeService, error) {
	b, err := os.ReadFile(dataFile)
	if err != nil {
		return nil, err
	}
	var data struct {
		Services []badgeService `json:"services"`
	}
	if err := json.Unmarshal(b, &data); err != nil {
		return nil, err
	}
	for i := range data.Services {
		if slugify(data.Services[i].Name) == slug {
			return &data.Services[i], nil
		}
	}
	return nil, nil
}

// badgeFor picks the right-hand text and color: down beats uptime, and the
// uptime color steps down like the shields.io defaults.
func badgeFor(s *badgeService) (value, color string) {
	switch {
	case s == nil:
		return "unknown", "#9f9f9f"
	case !s.OK:
		return "down", "#e05d44"
	case s.UptimePercent == nil:
		return "up", "#4c1"
	}
	u := *s.UptimePercent
	value = strings.TrimSuffix(strings.TrimSuffix(fmt.Sprintf("%.2f", u), "0"), ".0") + "%"
	switch {
	case u >= 99.9:
		color = "#4c1"
	case u >= 99:
		color = "#97ca00"
	case u >= 95:
		color = "#dfb317"
	default:
		color = "#e05d44"
	}
	return value, color
}

// Rough Verdana 11px advance width; close enough for short labels.
func textWidth(s string) int {
	return len(s)*7 + 10
}

func renderBadge(label, value, color string) string {
	lw, vw := textWidth(label), textWidth(value)
	w := lw + vw
	label, value = html.EscapeString(label), html.EscapeString(value)
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%[1]d" height="20" role="img" aria-label="%[4]s: %[5]s">
<title>%[4]s: %[5]s</title>
<linearGradient id="s" x2="0" y2="100%%"><stop offset="0" stop-color="#bbb" stop-opacity=".1"/><stop offset="1" stop-opacity=".1"/></linearGradient>
<clipPath id="r"><rect width="%[1]d" height="20" rx="3" fill="#fff"/></clipPath>
<g clip-path="url(#r)"><rect width="%[2]d" height="20" fill="#555"/><rect x="%[2]d" width="%[3]d" height="20" fill="%[6]s"/><rect width="%[1]d" height="20" fill="url(#s)"/></g>
<g fill="#fff" text-anchor="middle" font-family="Verdana,Geneva,DejaVu Sans,sans-serif" font-size="11">
<text x="%[7]d" y="15" fill="#010101" fill-opacity=".3">%[4]s</text><text x="%[7]d" y="14">%[4]s</text>
<text x="%[8]d" y="15" fill="#010101" fill-opacity=".3">%[5]s</text><text x="%[8]d" y="14">%[5]s</text>
</g></svg>`, w, lw, vw, label, value, color, lw/2, lw+vw/2)
}
