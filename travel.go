package main

import (
	"fmt"
	"math"
	"os"
)

const earthRadiusKm = 6371.0

// tspExactMax is the maximum number of places for which we solve TSP exactly (Held–Karp).
const tspExactMax = 18

func haversineKm(lat1, lon1, lat2, lon2 float64) float64 {
	r1 := lat1 * math.Pi / 180
	r2 := lat2 * math.Pi / 180
	dphi := (lat2 - lat1) * math.Pi / 180
	dlambda := (lon2 - lon1) * math.Pi / 180
	a := math.Sin(dphi/2)*math.Sin(dphi/2) + math.Cos(r1)*math.Cos(r2)*math.Sin(dlambda/2)*math.Sin(dlambda/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return earthRadiusKm * c
}

func buildDistMatrix(lat, lng []float64) [][]float64 {
	n := len(lat)
	d := make([][]float64, n)
	for i := range d {
		d[i] = make([]float64, n)
	}
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			km := haversineKm(lat[i], lng[i], lat[j], lng[j])
			d[i][j] = km
			d[j][i] = km
		}
	}
	return d
}

// tspHeldKarp finds a minimum-cost route starting at index 0 and visiting all vertices.
// If returnToStart is true, the tour closes with an edge back to 0 (round trip).
// If false, the route ends at whichever last stop minimizes total length (open path).
func tspHeldKarp(dist [][]float64, returnToStart bool) (order []int, totalKm float64) {
	n := len(dist)
	if n == 0 {
		return nil, 0
	}
	if n == 1 {
		return []int{0}, 0
	}
	full := (1 << n) - 1
	size := (1 << n) * n
	dp := make([]float64, size)
	parent := make([]int, size)
	for i := range dp {
		dp[i] = math.Inf(1)
		parent[i] = -1
	}
	idx := func(mask, i int) int {
		return mask*n + i
	}

	dp[idx(1, 0)] = 0

	for mask := 1; mask <= full; mask++ {
		if mask&1 == 0 {
			continue
		}
		for i := 0; i < n; i++ {
			if mask&(1<<i) == 0 {
				continue
			}
			ii := idx(mask, i)
			if i == 0 {
				if mask == 1 {
					dp[ii] = 0
				}
				continue
			}
			prev := mask ^ (1 << i)
			if prev&1 == 0 {
				continue
			}
			for j := 0; j < n; j++ {
				if prev&(1<<j) == 0 {
					continue
				}
				cand := dp[idx(prev, j)] + dist[j][i]
				if cand < dp[ii] {
					dp[ii] = cand
					parent[ii] = j
				}
			}
		}
	}

	bestEnd := 0
	best := math.Inf(1)
	for i := 0; i < n; i++ {
		cand := dp[idx(full, i)]
		if returnToStart {
			cand += dist[i][0]
		}
		if cand < best {
			best = cand
			bestEnd = i
		}
	}

	rev := make([]int, 0, n)
	mask := full
	cur := bestEnd
	for {
		rev = append(rev, cur)
		if mask == 1 && cur == 0 {
			break
		}
		p := parent[idx(mask, cur)]
		if p < 0 {
			break
		}
		mask ^= (1 << cur)
		cur = p
	}
	order = make([]int, len(rev))
	for i := range rev {
		order[i] = rev[len(rev)-1-i]
	}
	return order, best
}

// tspNearestNeighbor is a greedy chain from 0 through all vertices; if returnToStart,
// adds the closing leg back to 0.
func tspNearestNeighbor(dist [][]float64, returnToStart bool) (order []int, totalKm float64) {
	n := len(dist)
	if n == 0 {
		return nil, 0
	}
	if n == 1 {
		return []int{0}, 0
	}
	visited := make([]bool, n)
	order = make([]int, 0, n)
	cur := 0
	visited[0] = true
	order = append(order, 0)
	totalKm = 0
	for len(order) < n {
		bestJ := -1
		bestD := math.Inf(1)
		for j := 0; j < n; j++ {
			if visited[j] {
				continue
			}
			if dist[cur][j] < bestD {
				bestD = dist[cur][j]
				bestJ = j
			}
		}
		if bestJ < 0 {
			break
		}
		totalKm += dist[cur][bestJ]
		visited[bestJ] = true
		order = append(order, bestJ)
		cur = bestJ
	}
	if returnToStart {
		totalKm += dist[cur][0]
	}
	return order, totalKm
}

// TravelPlacesRoute geocodes names, optimizes visit order from the first place (index 0).
// If returnToStart is true (mode 1), the route returns to the first place; if false (mode 2), it ends at the last stop.
func TravelPlacesRoute(cfg *Config, names []string, returnToStart bool) error {
	if cfg.Google.APIKey == "" {
		return fmt.Errorf("set [google] api_key in your config (Maps Platform Geocoding API)")
	}
	if len(names) == 0 {
		return fmt.Errorf("no places to visit")
	}
	if len(names) == 1 {
		addr, _, _, err := geocodeOneLatLng(cfg.Google.APIKey, names[0])
		if err != nil {
			return err
		}
		if returnToStart {
			fmt.Fprintf(os.Stdout, "Single place (start and end): %s\n", names[0])
		} else {
			fmt.Fprintf(os.Stdout, "Single place (start only): %s\n", names[0])
		}
		if addr != "" {
			fmt.Fprintf(os.Stdout, "  %s\n", addr)
		}
		fmt.Fprintln(os.Stdout, "Total great-circle distance: 0 km (no other stops)")
		return nil
	}

	lat := make([]float64, len(names))
	lng := make([]float64, len(names))
	addrs := make([]string, len(names))
	for i, q := range names {
		addr, la, ln, err := geocodeOneLatLng(cfg.Google.APIKey, q)
		if err != nil {
			return err
		}
		addrs[i], lat[i], lng[i] = addr, la, ln
	}

	dist := buildDistMatrix(lat, lng)
	var order []int
	var total float64
	exact := len(names) <= tspExactMax
	if exact {
		order, total = tspHeldKarp(dist, returnToStart)
	} else {
		fmt.Fprintf(os.Stderr, "Note: more than %d places — using nearest-neighbor heuristic (not guaranteed optimal).\n", tspExactMax)
		order, total = tspNearestNeighbor(dist, returnToStart)
	}

	if returnToStart {
		fmt.Fprintln(os.Stdout, "Mode 1 — round trip (great-circle distance, km)")
		fmt.Fprintf(os.Stdout, "Start and return to: %s (first line in file)\n\n", names[0])
	} else {
		fmt.Fprintln(os.Stdout, "Mode 2 — open route, no return (great-circle distance, km)")
		fmt.Fprintf(os.Stdout, "Start at: %s (first line in file); end at last stop below\n\n", names[0])
	}
	if !exact {
		fmt.Fprintln(os.Stdout, "(Heuristic order)")
	}

	for step, idx := range order {
		title := names[idx]
		if step == 0 {
			fmt.Fprintf(os.Stdout, "%2d. %s\n", step+1, title)
			if addrs[idx] != "" {
				fmt.Fprintf(os.Stdout, "    %s\n", addrs[idx])
			}
			continue
		}
		prev := order[step-1]
		leg := dist[prev][idx]
		fmt.Fprintf(os.Stdout, "%2d. %s  (+%.2f km from previous)\n", step+1, title, leg)
		if addrs[idx] != "" {
			fmt.Fprintf(os.Stdout, "    %s\n", addrs[idx])
		}
	}
	if returnToStart {
		back := dist[order[len(order)-1]][0]
		fmt.Fprintf(os.Stdout, "\n    Return to start (%s): +%.2f km\n", names[0], back)
	}
	fmt.Fprintf(os.Stdout, "\nTotal: %.2f km", total)
	if exact {
		fmt.Fprintln(os.Stdout, " (optimal for this distance model)")
	} else {
		fmt.Fprintln(os.Stdout)
	}
	return nil
}
