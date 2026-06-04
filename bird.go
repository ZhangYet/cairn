package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"math/rand"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const ebirdTaxonomyURL = "https://api.ebird.org/v2/ref/taxonomy/ebird?fmt=json&cat=species"
const xenoCantoAPI = "https://xeno-canto.org/api/3/recordings"
const wikidataSearchAPI = "https://www.wikidata.org/w/api.php"
const wikidataEntityAPI = "https://www.wikidata.org/wiki/Special:EntityData"

func birdDBPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cairn_birds.db"), nil
}

func birdAudioBaseDir(audioDir string) (string, error) {
	if audioDir != "" {
		return audioDir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cairn_bird_audio"), nil
}

func initBirdDB() (*sql.DB, error) {
	p, err := birdDBPath()
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", p)
	if err != nil {
		return nil, err
	}
	schemas := []string{
		`CREATE TABLE IF NOT EXISTS ebird_taxonomy (
			species_code TEXT PRIMARY KEY,
			sci_name     TEXT NOT NULL,
			com_name     TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS birds (
			species_code TEXT PRIMARY KEY,
			sci_name     TEXT NOT NULL,
			com_name     TEXT NOT NULL,
			chn_name     TEXT DEFAULT '',
			ebird_url    TEXT NOT NULL,
			created_at   TEXT DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS bird_audio (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			species_code TEXT NOT NULL,
			xc_id        TEXT NOT NULL,
			audio_url    TEXT NOT NULL,
			local_path   TEXT NOT NULL,
			quality      TEXT,
			recorder     TEXT,
			length       TEXT DEFAULT '',
			FOREIGN KEY (species_code) REFERENCES birds(species_code)
		)`,
	}
	for _, s := range schemas {
		if _, err := db.Exec(s); err != nil {
			db.Close()
			return nil, err
		}
	}
	db.Exec(`ALTER TABLE bird_audio ADD COLUMN length TEXT DEFAULT ''`)
	return db, nil
}

var sciNameRE = regexp.MustCompile(`^[A-Z][a-z]+ [a-z]+$`)

func isScientificName(s string) bool {
	return sciNameRE.MatchString(strings.TrimSpace(s))
}

// ----- taxonomy -----

func ensureTaxonomy() error {
	db, err := initBirdDB()
	if err != nil {
		return err
	}
	defer db.Close()

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM ebird_taxonomy").Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	fmt.Fprintln(os.Stderr, "Downloading eBird taxonomy (this may take a moment)...")
	resp, err := http.Get(ebirdTaxonomyURL)
	if err != nil {
		return fmt.Errorf("failed to download eBird taxonomy: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("eBird taxonomy HTTP %d: %s", resp.StatusCode, string(body))
	}

	decoder := json.NewDecoder(resp.Body)
	_, err = decoder.Token()
	if err != nil {
		return fmt.Errorf("failed to parse taxonomy JSON: %w", err)
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare("INSERT OR IGNORE INTO ebird_taxonomy (species_code, sci_name, com_name) VALUES (?, ?, ?)")
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()

	i := 0
	for decoder.More() {
		var entry struct {
			SciName     string `json:"sciName"`
			ComName     string `json:"comName"`
			SpeciesCode string `json:"speciesCode"`
			Category    string `json:"category"`
		}
		if err := decoder.Decode(&entry); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: skipping malformed taxonomy entry: %v\n", err)
			continue
		}
		if entry.SpeciesCode == "" || entry.SciName == "" {
			continue
		}
		if _, err := stmt.Exec(entry.SpeciesCode, entry.SciName, entry.ComName); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: skipping duplicate species_code %q: %v\n", entry.SpeciesCode, err)
			continue
		}
		i++
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	_, err = decoder.Token()
	if err != nil && err.Error() != "EOF" {
		fmt.Fprintf(os.Stderr, "Warning: taxonomy JSON trailing token: %v\n", err)
	}

	fmt.Fprintf(os.Stderr, "Loaded %d species into taxonomy cache.\n", i)
	return nil
}

func searchTaxonomyByName(name string) (speciesCode, sciName, comName string, err error) {
	db, err := initBirdDB()
	if err != nil {
		return "", "", "", err
	}
	defer db.Close()

	normalized := strings.ToLower(strings.TrimSpace(name))
	row := db.QueryRow("SELECT species_code, sci_name, com_name FROM ebird_taxonomy WHERE LOWER(sci_name) = ? OR LOWER(com_name) = ?",
		normalized, normalized)
	err = row.Scan(&speciesCode, &sciName, &comName)
	if err == sql.ErrNoRows {
		rows, qerr := db.Query(
			"SELECT species_code, sci_name, com_name FROM ebird_taxonomy WHERE LOWER(sci_name) LIKE ? OR LOWER(com_name) LIKE ? LIMIT 10",
			"%"+normalized+"%", "%"+normalized+"%")
		if qerr != nil {
			return "", "", "", qerr
		}
		defer rows.Close()
		type match struct {
			code, sci, com string
		}
		var matches []match
		for rows.Next() {
			var m match
			if err := rows.Scan(&m.code, &m.sci, &m.com); err != nil {
				continue
			}
			matches = append(matches, m)
		}
		if len(matches) == 0 {
			return "", "", "", fmt.Errorf("no species found for %q", name)
		}
		if len(matches) == 1 {
			return matches[0].code, matches[0].sci, matches[0].com, nil
		}
		fmt.Fprintf(os.Stderr, "Multiple matches for %q:\n", name)
		for i, m := range matches {
			fmt.Fprintf(os.Stderr, "  %d. %s (%s) [%s]\n", i+1, m.com, m.sci, m.code)
		}
		fmt.Fprintf(os.Stderr, "Please be more specific.\n")
		return "", "", "", fmt.Errorf("ambiguous name %q: %d matches found", name, len(matches))
	}
	if err != nil {
		return "", "", "", err
	}
	return speciesCode, sciName, comName, nil
}

func searchLocalBirdByChineseName(name string) (speciesCode string, err error) {
	db, err := initBirdDB()
	if err != nil {
		return "", err
	}
	defer db.Close()

	normalized := strings.TrimSpace(name)
	row := db.QueryRow("SELECT species_code FROM birds WHERE chn_name = ?", normalized)
	var code string
	err = row.Scan(&code)
	if err == sql.ErrNoRows {
		rows, qerr := db.Query("SELECT species_code, chn_name FROM birds WHERE chn_name LIKE ? LIMIT 10",
			"%"+normalized+"%")
		if qerr != nil {
			return "", qerr
		}
		defer rows.Close()
		type m struct{ code, chn string }
		var matches []m
		for rows.Next() {
			var x m
			if rows.Scan(&x.code, &x.chn); err != nil {
				continue
			}
			matches = append(matches, x)
		}
		if len(matches) == 0 {
			return "", fmt.Errorf("no bird found with Chinese name %q", name)
		}
		if len(matches) == 1 {
			return matches[0].code, nil
		}
		fmt.Fprintf(os.Stderr, "Multiple matches for Chinese name %q:\n", name)
		for i, ma := range matches {
			fmt.Fprintf(os.Stderr, "  %d. %s (%s)\n", i+1, ma.chn, ma.code)
		}
		return "", fmt.Errorf("ambiguous Chinese name %q: %d matches found", name, len(matches))
	}
	if err != nil {
		return "", err
	}
	return code, nil
}

func resolveBirdName(name string) (speciesCode, sciName, comName, chnName string, err error) {
	name = strings.TrimSpace(name)

	if err := ensureTaxonomy(); err != nil {
		return "", "", "", "", err
	}

	if isScientificName(name) {
		code, sci, com, err := searchTaxonomyByName(name)
		if err != nil {
			return "", "", "", "", err
		}
		chn, _ := fetchChineseName(sci)
		return code, sci, com, chn, nil
	}

	code, sci, com, err := searchTaxonomyByName(name)
	if err == nil {
		chn, _ := fetchChineseName(sci)
		return code, sci, com, chn, nil
	}
	if strings.Contains(err.Error(), "ambiguous") || strings.Contains(err.Error(), "Multiple matches") {
		return "", "", "", "", err
	}

	code, err = searchLocalBirdByChineseName(name)
	if err == nil {
		db, dbErr := initBirdDB()
		if dbErr != nil {
			return "", "", "", "", dbErr
		}
		defer db.Close()
		row := db.QueryRow("SELECT sci_name, com_name, chn_name FROM birds WHERE species_code = ?", code)
		var sciN, comN, chnN string
		if err := row.Scan(&sciN, &comN, &chnN); err != nil {
			return "", "", "", "", err
		}
		return code, sciN, comN, chnN, nil
	}

	if containsCJK(name) {
		sciName, err := fetchSciNameByChinese(name)
		if err == nil && sciName != "" {
			code, sci, com, err := searchTaxonomyByName(sciName)
			if err == nil {
				chn, _ := fetchChineseName(sci)
				return code, sci, com, chn, nil
			}
		}
		return "", "", "", "", fmt.Errorf("cannot resolve Chinese name %q: not found", name)
	}

	db, dbErr := initBirdDB()
	if dbErr != nil {
		return "", "", "", "", dbErr
	}
	defer db.Close()
	row := db.QueryRow("SELECT sci_name, com_name, chn_name FROM birds WHERE species_code = ?", code)
	var sciN, comN, chnN string
	if err := row.Scan(&sciN, &comN, &chnN); err != nil {
		return "", "", "", "", err
	}
	return code, sciN, comN, chnN, nil
}

func containsCJK(s string) bool {
	for _, r := range s {
		if (r >= 0x4E00 && r <= 0x9FFF) ||
			(r >= 0x3400 && r <= 0x4DBF) ||
			(r >= 0x20000 && r <= 0x2A6DF) {
			return true
		}
	}
	return false
}

// ----- Wikidata Chinese name -----

func fetchChineseName(sciName string) (string, error) {
	searchURL := fmt.Sprintf("%s?action=wbsearchentities&search=%s&language=en&format=json&limit=1",
		wikidataSearchAPI, url.QueryEscape(sciName))
	resp, err := httpGet(searchURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("wikidata search HTTP %d", resp.StatusCode)
	}
	var sr struct {
		Search []struct {
			ID string `json:"id"`
		} `json:"search"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return "", err
	}
	if len(sr.Search) == 0 {
		return "", nil
	}
	qid := sr.Search[0].ID

	entityURL := fmt.Sprintf("%s/%s.json", wikidataEntityAPI, qid)
	resp2, err := httpGet(entityURL)
	if err != nil {
		return "", err
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		return "", fmt.Errorf("wikidata entity HTTP %d", resp2.StatusCode)
	}
	var ed struct {
		Entities map[string]struct {
			Labels map[string]struct {
				Value string `json:"value"`
			} `json:"labels"`
		} `json:"entities"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&ed); err != nil {
		return "", err
	}
	entity, ok := ed.Entities[qid]
	if !ok {
		return "", nil
	}
	for _, lang := range []string{"zh-cn", "zh-hans", "zh", "zh-tw", "zh-hant"} {
		if label, ok := entity.Labels[lang]; ok && label.Value != "" {
			return label.Value, nil
		}
	}
	return "", nil
}

func fetchSciNameByChinese(chnName string) (string, error) {
	searchURL := fmt.Sprintf("%s?action=wbsearchentities&search=%s&language=zh&format=json&limit=3",
		wikidataSearchAPI, url.QueryEscape(chnName))
	resp, err := httpGet(searchURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("wikidata search HTTP %d", resp.StatusCode)
	}
	var sr struct {
		Search []struct {
			ID          string `json:"id"`
			Description string `json:"description"`
		} `json:"search"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return "", err
	}
	for _, result := range sr.Search {
		desc := strings.ToLower(result.Description)
		if !strings.Contains(desc, "bird") && !strings.Contains(desc, "species") && !strings.Contains(desc, "鸟") {
			continue
		}
		entityURL := fmt.Sprintf("%s/%s.json", wikidataEntityAPI, result.ID)
		resp2, err := httpGet(entityURL)
		if err != nil {
			continue
		}
		defer resp2.Body.Close()
		if resp2.StatusCode != http.StatusOK {
			resp2.Body.Close()
			continue
		}
		var ed struct {
			Entities map[string]struct {
				Labels map[string]struct {
					Value string `json:"value"`
				} `json:"labels"`
			} `json:"entities"`
		}
		if err := json.NewDecoder(resp2.Body).Decode(&ed); err != nil {
			resp2.Body.Close()
			continue
		}
		resp2.Body.Close()
		entity, ok := ed.Entities[result.ID]
		if !ok {
			continue
		}
		for _, lang := range []string{"en"} {
			if label, ok := entity.Labels[lang]; ok && label.Value != "" {
				sciname := label.Value
				if isScientificName(sciname) {
					return sciname, nil
				}
				return sciname, nil
			}
		}
	}
	return "", fmt.Errorf("no Wikidata entity found for Chinese name %q", chnName)
}

// ----- Xeno-Canto -----

type xcRecording struct {
	ID       string `json:"id"`
	File     string `json:"file"`
	FileName string `json:"file-name"`
	Quality  string `json:"q"`
	Recorder string `json:"rec"`
	Length   string `json:"length"`
}

type xcResponse struct {
	NumRecordings string        `json:"numRecordings"`
	NumSpecies    string        `json:"numSpecies"`
	Recordings    []xcRecording `json:"recordings"`
}

func buildXCQuery(sciName string) string {
	parts := strings.Fields(sciName)
	if len(parts) >= 2 {
		return "gen:" + parts[0] + " sp:" + parts[1]
	}
	return sciName
}

func fetchRecordings(sciName string, maxResults int, xcKey string) ([]xcRecording, error) {
	query := buildXCQuery(sciName)
	apiURL := fmt.Sprintf("%s?query=%s", xenoCantoAPI, url.QueryEscape(query))
	if xcKey != "" {
		apiURL += "&key=" + url.QueryEscape(xcKey)
	}
	resp, err := httpGet(apiURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("xeno-canto HTTP %d: %s", resp.StatusCode, string(body))
	}
	var data xcResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}
	recordings := data.Recordings
	sortByQuality(recordings)
	if len(recordings) > maxResults {
		recordings = recordings[:maxResults]
	}
	return recordings, nil
}

var qualityOrder = map[string]int{"A": 0, "B": 1, "C": 2, "D": 3, "E": 4}

func sortByQuality(recs []xcRecording) {
	for i := 0; i < len(recs); i++ {
		for j := i + 1; j < len(recs); j++ {
			qi := qualityOrder[recs[i].Quality]
			qj := qualityOrder[recs[j].Quality]
			if qi < 0 {
				qi = 99
			}
			if qj < 0 {
				qj = 99
			}
			if qj < qi {
				recs[i], recs[j] = recs[j], recs[i]
			}
		}
	}
}

func downloadAudioFile(urlStr, destPath, xcKey string) error {
	if xcKey != "" {
		if strings.Contains(urlStr, "?") {
			urlStr += "&key=" + url.QueryEscape(xcKey)
		} else {
			urlStr += "?key=" + url.QueryEscape(xcKey)
		}
	}
	client := &http.Client{Timeout: 60 * time.Second}
	req, err := http.NewRequest("GET", urlStr, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "cairn/"+version)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("audio download HTTP %d: %s", resp.StatusCode, string(body))
	}
	f, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return err
	}
	return nil
}

// ----- Download -----

func BirdDownload(name, audioDir, xcKey string) error {
	speciesCode, sciName, comName, chnName, err := resolveBirdName(name)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "Resolved: %s (%s) [%s]", comName, sciName, speciesCode)
	if chnName != "" {
		fmt.Fprintf(os.Stderr, " — %s", chnName)
	}
	fmt.Fprintln(os.Stderr)

	ebirdURL := fmt.Sprintf("https://ebird.org/species/%s", speciesCode)
	baseDir, err := birdAudioBaseDir(audioDir)
	if err != nil {
		return err
	}

	db, err := initBirdDB()
	if err != nil {
		return err
	}
	defer db.Close()

	var existing int
	db.QueryRow("SELECT COUNT(*) FROM birds WHERE species_code = ?", speciesCode).Scan(&existing)
	if existing > 0 {
		return fmt.Errorf("%s (%s) is already downloaded", comName, speciesCode)
	}

	fmt.Fprintln(os.Stderr, "Fetching recordings from Xeno-Canto...")
	recordings, err := fetchRecordings(sciName, 5, xcKey)
	if err != nil {
		serr := err.Error()
		if strings.Contains(serr, "401") || strings.Contains(serr, "key") || strings.Contains(serr, "Unauthorized") {
			return fmt.Errorf("Xeno-Canto API requires an API key.\n  Get one at: https://xeno-canto.org/account (open in browser)\n  Then add to ~/.cairn.toml:\n    [ebird]\n    xc_api_key = \"YOUR_KEY\"")
		}
		return err
	}
	if len(recordings) == 0 {
		return fmt.Errorf("no recordings found for %s on Xeno-Canto", sciName)
	}
	fmt.Fprintf(os.Stderr, "Found %d recordings, downloading top %d...\n", len(recordings), len(recordings))

	speciesDir := filepath.Join(baseDir, speciesCode)
	if err := os.MkdirAll(speciesDir, 0755); err != nil {
		return err
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}

	_, err = tx.Exec(
		"INSERT OR REPLACE INTO birds (species_code, sci_name, com_name, chn_name, ebird_url) VALUES (?, ?, ?, ?, ?)",
		speciesCode, sciName, comName, chnName, ebirdURL)
	if err != nil {
		tx.Rollback()
		return err
	}

	downloaded := 0
	for _, rec := range recordings {
		if rec.File == "" {
			fmt.Fprintf(os.Stderr, "  Skipping XC%s: audio not downloadable (restricted species)\n", rec.ID)
			continue
		}
		ext := filepath.Ext(rec.FileName)
		if ext == "" {
			ext = ".mp3"
		}
		fileName := fmt.Sprintf("%s_%s%s", speciesCode, rec.ID, ext)
		localPath := filepath.Join(speciesDir, fileName)

		audioURL := rec.File
		if strings.HasPrefix(audioURL, "//") {
			audioURL = "https:" + audioURL
		}

		fmt.Fprintf(os.Stderr, "  Downloading %s (quality: %s)...", rec.ID, rec.Quality)
		if err := downloadAudioFile(audioURL, localPath, xcKey); err != nil {
			fmt.Fprintf(os.Stderr, " FAILED: %v\n", err)
			continue
		}
		fmt.Fprintln(os.Stderr, " done.")

		_, err := tx.Exec(
			"INSERT INTO bird_audio (species_code, xc_id, audio_url, local_path, quality, recorder, length) VALUES (?, ?, ?, ?, ?, ?, ?)",
			speciesCode, rec.ID, rec.File, localPath, rec.Quality, rec.Recorder, rec.Length)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  Warning: failed to save audio record: %v\n", err)
		}
		downloaded++
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	if downloaded == 0 {
		fmt.Fprintf(os.Stderr, "Warning: no audio downloaded for %s (%s). Bird metadata saved for listing, but quiz requires audio.\n", comName, sciName)
	} else {
		fmt.Fprintf(os.Stderr, "Downloaded %s (%s) with %d recording(s).\n", comName, sciName, downloaded)
	}
	return nil
}

// ----- List -----

func BirdList() error {
	db, err := initBirdDB()
	if err != nil {
		return err
	}
	defer db.Close()

	rows, err := db.Query("SELECT sci_name, com_name, chn_name, ebird_url, species_code FROM birds ORDER BY created_at DESC")
	if err != nil {
		return err
	}
	defer rows.Close()

	fmt.Println()
	hasRows := false
	for rows.Next() {
		hasRows = true
		var sci, com, chn, ebirdURL, code string
		if err := rows.Scan(&sci, &com, &chn, &ebirdURL, &code); err != nil {
			continue
		}
		display := fmt.Sprintf("%s / %s", sci, com)
		if chn != "" {
			display = fmt.Sprintf("%s / %s / %s", sci, com, chn)
		}
		fmt.Printf("  %s\n", display)
		fmt.Printf("    %s\n\n", ebirdURL)
	}
	if !hasRows {
		fmt.Println("  (no birds downloaded yet)")
	}
	return nil
}

// ----- Quiz -----

func BirdQuiz(numChoices int, audioDir string) error {
	if numChoices <= 0 {
		numChoices = 4
	}
	if numChoices < 2 {
		return fmt.Errorf("quiz requires at least 2 choices")
	}

	db, err := initBirdDB()
	if err != nil {
		return err
	}
	defer db.Close()

	var total int
	if err := db.QueryRow("SELECT COUNT(*) FROM birds").Scan(&total); err != nil {
		return err
	}
	if total < numChoices {
		return fmt.Errorf("need at least %d birds in the library, but only %d downloaded", numChoices, total)
	}
	if total < 2 {
		return fmt.Errorf("need at least 2 birds in the library for a quiz")
	}

	rows, err := db.Query("SELECT species_code, sci_name, com_name, chn_name FROM birds")
	if err != nil {
		return err
	}
	defer rows.Close()

	type birdInfo struct {
		code, sci, com, chn string
	}
	var allBirds []birdInfo
	for rows.Next() {
		var b birdInfo
		if err := rows.Scan(&b.code, &b.sci, &b.com, &b.chn); err != nil {
			continue
		}
		allBirds = append(allBirds, b)
	}

	var correct, incorrect int
	const maxRounds = 5

	for round := 1; round <= maxRounds; round++ {
		fmt.Printf("\n  --- Round %d/%d ---\n", round, maxRounds)
		target := allBirds[rand.Intn(len(allBirds))]

		var audioPath string
		if err := func() error {
			arows, err := db.Query(
				"SELECT local_path, xc_id, quality, recorder FROM bird_audio WHERE species_code = ? ORDER BY RANDOM()",
				target.code)
			if err != nil {
				return err
			}
			defer arows.Close()
			var audios []struct {
				path, xcID, quality, recorder string
			}
			for arows.Next() {
				var a struct {
					path, xcID, quality, recorder string
				}
				if err := arows.Scan(&a.path, &a.xcID, &a.quality, &a.recorder); err != nil {
					continue
				}
				audios = append(audios, a)
			}
			if len(audios) == 0 {
				return fmt.Errorf("no audio recordings for %s", target.com)
			}
			picked := audios[rand.Intn(len(audios))]
			audioPath = picked.path
			return nil
		}(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			continue
		}

		options := []birdInfo{target}
		available := make([]birdInfo, 0, len(allBirds)-1)
		for _, b := range allBirds {
			if b.code != target.code {
				available = append(available, b)
			}
		}
		rand.Shuffle(len(available), func(i, j int) {
			available[i], available[j] = available[j], available[i]
		})
		for i := 0; i < numChoices-1 && i < len(available); i++ {
			options = append(options, available[i])
		}

		shuffled := make([]birdInfo, len(options))
		copy(shuffled, options)
		rand.Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})

		fmt.Printf("  Now playing...\n")
		audioCmd := playAudio(audioPath)

		fmt.Println("  Options:")
		for i, b := range shuffled {
			label := fmt.Sprintf("%s (%s)", b.com, b.sci)
			if b.chn != "" {
				label = fmt.Sprintf("%s / %s (%s)", b.chn, b.com, b.sci)
			}
			fmt.Printf("    %d. %s\n", i+1, label)
		}

		fmt.Printf("\n  Your choice (1-%d): ", len(shuffled))
		var input string
		_, scanErr := fmt.Scanln(&input)
		if audioCmd != nil {
			audioCmd.Process.Kill()
		}
		if scanErr != nil {
			input = ""
		}
		input = strings.TrimSpace(input)
		n, convErr := strconv.Atoi(input)
		if convErr != nil || n < 1 || n > len(shuffled) {
			fmt.Fprintf(os.Stderr, "  Invalid choice: %s\n", input)
			continue
		}
		chosen := shuffled[n-1]

		if chosen.code == target.code {
			fmt.Printf("  Correct! It is %s (%s).\n", target.com, target.sci)
			correct++
		} else {
			fmt.Printf("  Wrong. It was %s (%s). You picked %s (%s).\n",
				target.com, target.sci, chosen.com, chosen.sci)
			incorrect++
		}
	}

	fmt.Printf("\n  Score: %d correct, %d wrong\n", correct, incorrect)
	return nil
}

// ----- Play -----

func BirdPlay(name string) error {
	db, err := initBirdDB()
	if err != nil {
		return err
	}
	defer db.Close()

	name = strings.TrimSpace(name)
	var speciesCode, sciName, comName, chnName string
	row := db.QueryRow("SELECT species_code, sci_name, com_name, chn_name FROM birds WHERE chn_name = ? OR species_code = ?",
		name, name)
	err = row.Scan(&speciesCode, &sciName, &comName, &chnName)
	if err == sql.ErrNoRows {
		row = db.QueryRow(
			"SELECT species_code, sci_name, com_name, chn_name FROM birds WHERE LOWER(com_name) = ?",
			strings.ToLower(name))
		err = row.Scan(&speciesCode, &sciName, &comName, &chnName)
	}
	if err == sql.ErrNoRows {
		row = db.QueryRow(
			"SELECT species_code, sci_name, com_name, chn_name FROM birds WHERE LOWER(sci_name) = ?",
			strings.ToLower(name))
		err = row.Scan(&speciesCode, &sciName, &comName, &chnName)
	}
	if err == sql.ErrNoRows {
		rows, qerr := db.Query(
			"SELECT species_code, sci_name, com_name, chn_name FROM birds WHERE chn_name LIKE ? OR LOWER(com_name) LIKE ? ORDER BY chn_name",
			"%"+name+"%", "%"+strings.ToLower(name)+"%")
		if qerr != nil {
			return qerr
		}
		defer rows.Close()
		type m struct{ code, sci, com, chn string }
		var matches []m
		for rows.Next() {
			var x m
			if rows.Scan(&x.code, &x.sci, &x.com, &x.chn); err != nil {
				continue
			}
			matches = append(matches, x)
		}
		if len(matches) == 0 {
			return fmt.Errorf("no downloaded bird matches %q", name)
		}
		speciesCode = matches[0].code
		sciName = matches[0].sci
		comName = matches[0].com
		chnName = matches[0].chn
	} else if err != nil {
		return err
	}

	arows, err := db.Query("SELECT local_path, xc_id, quality, recorder, length FROM bird_audio WHERE species_code = ? ORDER BY quality",
		speciesCode)
	if err != nil {
		return err
	}
	defer arows.Close()

	type audio struct {
		path, xcID, quality, recorder, length string
	}
	var audios []audio
	for arows.Next() {
		var a audio
		if err := arows.Scan(&a.path, &a.xcID, &a.quality, &a.recorder, &a.length); err != nil {
			continue
		}
		audios = append(audios, a)
	}
	if len(audios) == 0 {
		return fmt.Errorf("no audio recordings for %s", comName)
	}

	fmt.Printf("\n  %s (%s)", comName, sciName)
	if chnName != "" {
		fmt.Printf(" / %s", chnName)
	}
	fmt.Printf(" — %d recording(s)\n\n", len(audios))

	for i, a := range audios {
		lenStr := a.length
		if lenStr == "" {
			lenStr = "?:??"
		}
		fmt.Printf("  [%d/%d] XC%s Q:%s (%s)\n", i+1, len(audios), a.xcID, a.quality, lenStr)
		playAudioWithProgress(a.path, lenStr)
		if i < len(audios)-1 {
			time.Sleep(1 * time.Second)
		}
	}
	return nil
}

func playAudioWithProgress(path, lengthStr string) {
	totalSecs := parseDuration(lengthStr)
	done := make(chan struct{})
	start := time.Now()

	go func() {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				elapsed := time.Since(start)
				if totalSecs > 0 {
					fmt.Printf("\r  ▶ %s / %s", formatDuration(int(elapsed.Seconds())), lengthStr)
				} else {
					fmt.Printf("\r  ▶ %s", formatDuration(int(elapsed.Seconds())))
				}
			}
		}
	}()

	err := playBlocking(path)
	close(done)

	if err != nil {
		fmt.Fprintf(os.Stderr, "  FAILED: %v — removing broken file\n", err)
		os.Remove(path)
	} else if totalSecs > 0 {
		fmt.Printf("\r  ▶ %s / %s\n", lengthStr, lengthStr)
	} else {
		fmt.Println()
	}
}

func formatDuration(secs int) string {
	m := secs / 60
	s := secs % 60
	return fmt.Sprintf("%d:%02d", m, s)
}

func parseDuration(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	parts := strings.Split(s, ":")
	if len(parts) == 2 {
		m, _ := strconv.Atoi(parts[0])
		sec, _ := strconv.Atoi(parts[1])
		return m*60 + sec
	}
	return 0
}

func playBlocking(path string) error {
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("file not found: %s", path)
	}
	var cmd *exec.Cmd
	if runtime.GOOS == "darwin" {
		cmd = exec.Command("afplay", path)
	} else if runtime.GOOS == "linux" {
		cmd = exec.Command("mpv", "--no-video", "--really-quiet", path)
	} else {
		return nil
	}
	return cmd.Run()
}

func playAudio(path string) *exec.Cmd {
	if _, err := os.Stat(path); err != nil {
		fmt.Fprintf(os.Stderr, "  Audio file not found: %s\n", path)
		return nil
	}
	var cmd *exec.Cmd
	if runtime.GOOS == "darwin" {
		cmd = exec.Command("afplay", path)
	} else if runtime.GOOS == "linux" {
		cmd = exec.Command("mpv", "--no-video", "--really-quiet", path)
	} else {
		fmt.Fprintf(os.Stderr, "  Audio file: %s (manual playback required)\n", path)
		return nil
	}
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "  Failed to play audio: %v\n  File: %s\n", err, path)
		return nil
	}
	return cmd
}

func httpGet(urlStr string) (*http.Response, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequest("GET", urlStr, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "cairn/"+version)
	return client.Do(req)
}
