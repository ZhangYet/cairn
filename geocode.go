package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"
)

const googleGeocodeURL = "https://maps.googleapis.com/maps/api/geocode/json"

// maxAddrDisplayCols caps the Address column (display width) so the table stays readable.
const maxAddrDisplayCols = 58

type geocodeRow struct {
	place, address, lat, lng string
}

type geocodeResponse struct {
	Status       string `json:"status"`
	ErrorMessage string `json:"error_message"`
	Results      []struct {
		FormattedAddress string `json:"formatted_address"`
		Geometry         struct {
			Location struct {
				Lat float64 `json:"lat"`
				Lng float64 `json:"lng"`
			} `json:"location"`
		} `json:"geometry"`
	} `json:"results"`
}

func geocodeOne(apiKey, query string) (address, lat, lng string, err error) {
	u, err := url.Parse(googleGeocodeURL)
	if err != nil {
		return "", "", "", err
	}
	q := u.Query()
	q.Set("address", query)
	q.Set("key", apiKey)
	u.RawQuery = q.Encode()

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Get(u.String())
	if err != nil {
		return "", "", "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return "", "", "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", "", "", fmt.Errorf("google geocoding HTTP %s", resp.Status)
	}

	var out geocodeResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return "", "", "", fmt.Errorf("decode response: %w", err)
	}

	switch out.Status {
	case "OK":
		if len(out.Results) == 0 {
			return "", "", "", nil
		}
		r := out.Results[0]
		addr := strings.TrimSpace(strings.ReplaceAll(r.FormattedAddress, "\n", " "))
		return addr,
			fmt.Sprintf("%.7f", r.Geometry.Location.Lat),
			fmt.Sprintf("%.7f", r.Geometry.Location.Lng),
			nil
	case "ZERO_RESULTS":
		return "", "", "", nil
	case "OVER_QUERY_LIMIT", "REQUEST_DENIED", "INVALID_REQUEST", "UNKNOWN_ERROR":
		msg := strings.TrimSpace(out.ErrorMessage)
		if msg == "" {
			msg = out.Status
		}
		return "", "", "", fmt.Errorf("google geocoding: %s", msg)
	default:
		return "", "", "", fmt.Errorf("google geocoding: %s", out.Status)
	}
}

// geocodeOneLatLng geocodes query and returns coordinates; fails if there is no result.
func geocodeOneLatLng(apiKey, query string) (address string, lat, lng float64, err error) {
	addr, latStr, lngStr, err := geocodeOne(apiKey, query)
	if err != nil {
		return "", 0, 0, err
	}
	if latStr == "" || lngStr == "" {
		return addr, 0, 0, fmt.Errorf("no geocoding result for %q", query)
	}
	lat, err = strconv.ParseFloat(latStr, 64)
	if err != nil {
		return "", 0, 0, fmt.Errorf("parse latitude for %q: %w", query, err)
	}
	lng, err = strconv.ParseFloat(lngStr, 64)
	if err != nil {
		return "", 0, 0, fmt.Errorf("parse longitude for %q: %w", query, err)
	}
	return addr, lat, lng, nil
}

// readPlacesFromFile returns non-empty lines as place names. Blank lines and lines whose
// first non-space character is # are skipped. A UTF-8 BOM on a line is stripped.
// path "-" reads from stdin.
func readPlacesFromFile(path string) ([]string, error) {
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, fmt.Errorf("read places file: %w", err)
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "\ufeff")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out, nil
}

// GeocodePlaces resolves each query with the Google Geocoding API and prints a table.
// On ZERO_RESULTS, address and coordinates are left blank for that row.
func GeocodePlaces(cfg *Config, queries []string) error {
	if cfg.Google.APIKey == "" {
		return fmt.Errorf("set [google] api_key in your config (Maps Platform Geocoding API)")
	}
	var rows []geocodeRow
	for _, q := range queries {
		q = strings.TrimSpace(q)
		if q == "" {
			continue
		}
		addr, lat, lng, err := geocodeOne(cfg.Google.APIKey, q)
		if err != nil {
			return err
		}
		rows = append(rows, geocodeRow{place: q, address: addr, lat: lat, lng: lng})
	}
	if len(rows) == 0 {
		return fmt.Errorf("no place names to look up")
	}

	printGeocodeTable(os.Stdout, rows)
	return nil
}

func geocodeCell(s string, width int) string {
	if runewidth.StringWidth(s) > width {
		s = runewidth.Truncate(s, width, "…")
	}
	return runewidth.FillRight(s, width)
}

func printGeocodeTable(out *os.File, rows []geocodeRow) {
	wPlace := runewidth.StringWidth("Place")
	wAddr := runewidth.StringWidth("Address")
	for _, r := range rows {
		if n := runewidth.StringWidth(r.place); n > wPlace {
			wPlace = n
		}
		if n := runewidth.StringWidth(r.address); n > wAddr {
			wAddr = n
		}
	}
	if wAddr > maxAddrDisplayCols {
		wAddr = maxAddrDisplayCols
	}
	wLat := runewidth.StringWidth("Latitude")
	wLng := runewidth.StringWidth("Longitude")
	for _, r := range rows {
		if n := runewidth.StringWidth(r.lat); n > wLat {
			wLat = n
		}
		if n := runewidth.StringWidth(r.lng); n > wLng {
			wLng = n
		}
	}

	sep := strings.Repeat("-", wPlace) + " | " + strings.Repeat("-", wAddr) + " | " + strings.Repeat("-", wLat) + " | " + strings.Repeat("-", wLng)

	line := func(a, b, c, d string) {
		fmt.Fprintf(out, "%s | %s | %s | %s\n",
			geocodeCell(a, wPlace), geocodeCell(b, wAddr), geocodeCell(c, wLat), geocodeCell(d, wLng))
	}

	line("Place", "Address", "Latitude", "Longitude")
	fmt.Fprintln(out, sep)
	for _, r := range rows {
		line(r.place, r.address, r.lat, r.lng)
	}
}
