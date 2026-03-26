package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"
	"time"
)

const googleGeocodeURL = "https://maps.googleapis.com/maps/api/geocode/json"

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

// GeocodePlaces resolves each query with the Google Geocoding API and prints a table.
// On ZERO_RESULTS, address and coordinates are left blank for that row.
func GeocodePlaces(cfg *Config, queries []string) error {
	if cfg.Google.APIKey == "" {
		return fmt.Errorf("set [google] api_key in your config (Maps Platform Geocoding API)")
	}
	type row struct {
		place, address, lat, lng string
	}
	var rows []row
	for _, q := range queries {
		q = strings.TrimSpace(q)
		if q == "" {
			continue
		}
		addr, lat, lng, err := geocodeOne(cfg.Google.APIKey, q)
		if err != nil {
			return err
		}
		rows = append(rows, row{place: q, address: addr, lat: lat, lng: lng})
	}
	if len(rows) == 0 {
		return fmt.Errorf("no place names to look up")
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "Place\tAddress\tLatitude\tLongitude")
	fmt.Fprintln(w, "-----\t-------\t--------\t---------")
	for _, r := range rows {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", r.place, r.address, r.lat, r.lng)
	}
	return w.Flush()
}
