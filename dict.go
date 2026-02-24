package main

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const dictAPIBase = "https://api.dictionaryapi.dev/api/v2/entries/en"
const wordsAlphaURL = "https://raw.githubusercontent.com/dwyl/english-words/master/words_alpha.txt"
const wiktionaryAPI = "https://en.wiktionary.org/w/api.php"
const etymonlineBase = "https://www.etymonline.com/word/"

var (
	wordList     []string
	wordListOnce sync.Once
)

// dictDBPath returns the path to the local SQLite DB for saved dictionary words.
func dictDBPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cairn_dict.db"), nil
}

func initDictDB() (*sql.DB, error) {
	p, err := dictDBPath()
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", p)
	if err != nil {
		return nil, err
	}
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS dict_words (word TEXT PRIMARY KEY, created_at TEXT)`)
	if err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func saveDictWord(word string) {
	word = strings.TrimSpace(strings.ToLower(word))
	if word == "" {
		return
	}
	db, err := initDictDB()
	if err != nil {
		return
	}
	defer db.Close()
	// REPLACE so re-looking-up a word updates created_at and it counts as "recent" again
	_, _ = db.Exec(`INSERT OR REPLACE INTO dict_words (word, created_at) VALUES (?, datetime('now'))`, word)
}

func loadDictWords() map[string]bool {
	db, err := initDictDB()
	if err != nil {
		return nil
	}
	defer db.Close()
	rows, err := db.Query(`SELECT word FROM dict_words`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	m := make(map[string]bool)
	for rows.Next() {
		var w string
		if err := rows.Scan(&w); err != nil {
			continue
		}
		m[strings.ToLower(w)] = true
	}
	return m
}

// loadRecentDictWords returns the n most recently looked-up words (for highlighting only).
func loadRecentDictWords(n int) map[string]bool {
	if n <= 0 {
		return nil
	}
	db, err := initDictDB()
	if err != nil {
		return nil
	}
	defer db.Close()
	rows, err := db.Query(`SELECT word FROM dict_words ORDER BY created_at DESC LIMIT ?`, n)
	if err != nil {
		return nil
	}
	defer rows.Close()
	m := make(map[string]bool)
	for rows.Next() {
		var w string
		if err := rows.Scan(&w); err != nil {
			continue
		}
		m[strings.ToLower(w)] = true
	}
	return m
}

// wordBaseForms returns the word and possible base forms for variation matching (e.g. running -> run, words -> word).
func wordBaseForms(w string) []string {
	w = strings.ToLower(w)
	if w == "" {
		return nil
	}
	candidates := []string{w}
	// -ies -> -y (berries -> berry)
	if len(w) > 3 && strings.HasSuffix(w, "ies") {
		candidates = append(candidates, w[:len(w)-3]+"y")
	}
	// -ied -> -y (tried -> try)
	if len(w) > 3 && strings.HasSuffix(w, "ied") {
		candidates = append(candidates, w[:len(w)-3]+"y")
	}
	// -ing (running -> run, being -> be)
	if len(w) > 4 && strings.HasSuffix(w, "ing") {
		base := w[:len(w)-3]
		candidates = append(candidates, base)
		if len(base) > 1 && base[len(base)-1] == base[len(base)-2] {
			candidates = append(candidates, base[:len(base)-1])
		}
	}
	// -ed (played -> play, stopped -> stop)
	if len(w) > 3 && strings.HasSuffix(w, "ed") {
		base := w[:len(w)-2]
		candidates = append(candidates, base)
		if len(base) > 1 && base[len(base)-1] == base[len(base)-2] {
			candidates = append(candidates, base[:len(base)-1])
		}
	}
	// -es (watches -> watch, goes -> go)
	if len(w) > 2 && strings.HasSuffix(w, "es") {
		candidates = append(candidates, w[:len(w)-2])
	}
	// -s (words -> word)
	if len(w) > 1 && strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") {
		candidates = append(candidates, w[:len(w)-1])
	}
	// -er (runner -> run, bigger -> big)
	if len(w) > 3 && strings.HasSuffix(w, "er") {
		candidates = append(candidates, w[:len(w)-2])
		if len(w) > 4 && w[len(w)-3] == w[len(w)-4] {
			candidates = append(candidates, w[:len(w)-3])
		}
	}
	// -est (fastest -> fast)
	if len(w) > 4 && strings.HasSuffix(w, "est") {
		candidates = append(candidates, w[:len(w)-3])
		if len(w) > 5 && w[len(w)-4] == w[len(w)-5] {
			candidates = append(candidates, w[:len(w)-4])
		}
	}
	// -ly (quickly -> quick)
	if len(w) > 3 && strings.HasSuffix(w, "ly") {
		candidates = append(candidates, w[:len(w)-2])
	}
	return candidates
}

func shouldHighlightWord(token string, saved map[string]bool) bool {
	if saved == nil || len(token) == 0 {
		return false
	}
	for _, base := range wordBaseForms(token) {
		if saved[base] {
			return true
		}
	}
	return false
}

var wordTokenRe = regexp.MustCompile(`[A-Za-z]+(?:'[A-Za-z]+)?`)

// ANSI codes for highlighting when stdout is a TTY
const (
	ansiBoldGreen = "\033[1;32m" // word we're currently searching
	ansiBoldCyan  = "\033[1;36m" // words we searched before (recent 3)
	ansiReset     = "\033[0m"
)

func isTerminal(f *os.File) bool {
	if f == nil {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

// highlightText highlights the current-search word in one color and previously searched words in another.
// currentWords = word we're looking up (and its variations); previousWords = recent 3 from DB.
func highlightText(text string, currentWords, previousWords map[string]bool, useColor bool) string {
	hasAny := (len(currentWords) > 0 || len(previousWords) > 0)
	if !hasAny {
		return text
	}
	return wordTokenRe.ReplaceAllStringFunc(text, func(match string) string {
		if shouldHighlightWord(match, currentWords) {
			if useColor {
				return ansiBoldGreen + match + ansiReset
			}
			return "**" + match + "**"
		}
		if shouldHighlightWord(match, previousWords) {
			if useColor {
				return ansiBoldCyan + match + ansiReset
			}
			return "**" + match + "**"
		}
		return match
	})
}

type dictPhonetic struct {
	Text  string `json:"text"`
	Audio string `json:"audio"`
}
type dictDefinition struct {
	Definition string   `json:"definition"`
	Example    string   `json:"example"`
	Synonyms   []string `json:"synonyms"`
	Antonyms   []string `json:"antonyms"`
}
type dictMeaning struct {
	PartOfSpeech string           `json:"partOfSpeech"`
	Definitions  []dictDefinition `json:"definitions"`
	Synonyms     []string         `json:"synonyms"`
	Antonyms     []string         `json:"antonyms"`
}
type dictEntry struct {
	Word      string         `json:"word"`
	Phonetics []dictPhonetic `json:"phonetics"`
	Meanings  []dictMeaning  `json:"meanings"`
}
type dictError struct {
	Title      string `json:"title"`
	Message    string `json:"message"`
	Resolution string `json:"resolution"`
}

func loadWordList() ([]string, error) {
	var loadErr error
	wordListOnce.Do(func() {
		client := &http.Client{Timeout: 30 * time.Second}
		resp, err := client.Get(wordsAlphaURL)
		if err != nil {
			loadErr = err
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			loadErr = fmt.Errorf("word list returned %s", resp.Status)
			return
		}
		var words []string
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			w := strings.TrimSpace(strings.ToLower(sc.Text()))
			if w != "" {
				words = append(words, w)
			}
		}
		loadErr = sc.Err()
		if loadErr == nil {
			wordList = words
		}
	})
	if loadErr != nil {
		return nil, loadErr
	}
	return wordList, nil
}

func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	na, nb := len(ra), len(rb)
	if na == 0 {
		return nb
	}
	if nb == 0 {
		return na
	}
	prev := make([]int, nb+1)
	curr := make([]int, nb+1)
	for j := 0; j <= nb; j++ {
		prev[j] = j
	}
	for i := 1; i <= na; i++ {
		curr[0] = i
		for j := 1; j <= nb; j++ {
			cost := 0
			if ra[i-1] != rb[j-1] {
				cost = 1
			}
			curr[j] = min(curr[j-1]+1, min(prev[j]+1, prev[j-1]+cost))
		}
		prev, curr = curr, prev
	}
	return prev[nb]
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func suggestClosest(word string, maxEditDistance int) (string, bool) {
	list, err := loadWordList()
	if err != nil || len(list) == 0 {
		return "", false
	}
	word = strings.ToLower(word)
	if word == "" {
		return "", false
	}
	runes := []rune(word)
	first := ""
	if len(runes) > 0 {
		first = string(runes[0])
	}
	bestWord := ""
	bestDist := maxEditDistance + 1
	wordLen := len(runes)
	for _, w := range list {
		w = strings.ToLower(w)
		if w == word {
			return w, true
		}
		rw := []rune(w)
		if first != "" && (len(rw) == 0 || string(rw[0]) != first) {
			continue
		}
		diff := len(rw) - wordLen
		if diff < -2 || diff > 2 {
			continue
		}
		d := levenshtein(word, w)
		if d > maxEditDistance {
			continue
		}
		sameLen := len(rw) == wordLen
		bestSameLen := bestWord != "" && len([]rune(bestWord)) == wordLen
		update := d < bestDist || (d == bestDist && sameLen && !bestSameLen)
		if update {
			bestDist = d
			bestWord = w
		}
	}
	if bestWord != "" {
		return bestWord, true
	}
	return "", false
}

// fetchEtymonlineEtymology fetches etymology from Etymonline (authoritative source).
// Parses the meta description which contains a short etymology; no API key needed.
func fetchEtymonlineEtymology(word string) (string, error) {
	word = strings.TrimSpace(strings.ToLower(word))
	if word == "" {
		return "", fmt.Errorf("no word")
	}
	u := etymonlineBase + url.PathEscape(word)
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "cairn/1.0 (CLI dictionary tool; etymology)")
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("etymonline returned %s", resp.Status)
	}
	// If we were redirected to a different word (e.g. /word/advertise → /word/advert), don't use this content
	if resp.Request != nil && resp.Request.URL != nil {
		path := strings.TrimSuffix(resp.Request.URL.Path, "/")
		parts := strings.Split(path, "/")
		if len(parts) >= 1 {
			finalWord := strings.ToLower(parts[len(parts)-1])
			if finalWord != word {
				return "", nil
			}
		}
	}
	// Read body (reasonably sized) to parse meta description
	const maxBody = 64 * 1024
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return "", err
	}
	html := string(body)
	// Meta description contains short etymology: content="...from Latin... See origin..."
	re := regexp.MustCompile(`<meta name="description" content="([^"]+)"`)
	m := re.FindStringSubmatch(html)
	if len(m) < 2 {
		re = regexp.MustCompile(`<meta property="og:description" content="([^"]+)"`)
		m = re.FindStringSubmatch(html)
	}
	if len(m) < 2 {
		return "", nil
	}
	etym := m[1]
	// Remove trailing " See origin and meaning of X."
	if idx := strings.Index(etym, " See origin"); idx > 0 {
		etym = etym[:idx]
	}
	etym = strings.TrimSpace(etym)
	if etym == "" {
		return "", nil
	}
	// Decode common HTML entities
	etym = strings.ReplaceAll(etym, "&quot;", "\"")
	etym = strings.ReplaceAll(etym, "&amp;", "&")
	etym = strings.ReplaceAll(etym, "&#39;", "'")
	etym = strings.ReplaceAll(etym, "&hellip;", "...")
	return etym, nil
}

func fetchWiktionaryEtymology(word string) (etym string, example string, err error) {
	word = strings.TrimSpace(strings.ToLower(word))
	if word == "" {
		return "", "", fmt.Errorf("no word")
	}
	u := wiktionaryAPI + "?action=query&prop=revisions&rvprop=content&rvslots=main&format=json&titles=" + url.QueryEscape(word)
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", "cairn/1.0 (CLI dictionary tool; etymology lookup)")
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("wiktionary returned %s", resp.Status)
	}
	var out struct {
		Query struct {
			Pages map[string]struct {
				Revisions []struct {
					Slots struct {
						Main struct {
							Star string `json:"*"`
						} `json:"main"`
					} `json:"slots"`
				} `json:"revisions"`
			} `json:"pages"`
		} `json:"query"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", "", err
	}
	for _, p := range out.Query.Pages {
		if len(p.Revisions) == 0 {
			return "", "", nil
		}
		raw := strings.TrimSpace(p.Revisions[0].Slots.Main.Star)
		if raw == "" {
			return "", "", nil
		}
		etym = extractAndCleanEtymology(raw)
		example = extractWiktionaryExample(raw)
		return etym, example, nil
	}
	return "", "", nil
}

func extractAndCleanEtymology(wikitext string) string {
	wikitext = strings.ReplaceAll(wikitext, "\r\n", "\n")
	wikitext = strings.ReplaceAll(wikitext, "\r", "\n")
	etymRegex := regexp.MustCompile(`(?m)^===Etymology(?:\s+\d+)?===\s*\n([\s\S]*?)(?:\n===|$)`)

	englishBlock := regexp.MustCompile(`(?m)^==English==\s*\n([\s\S]*?)(?:\n==[^=\n][^\n]*|$)`).FindStringSubmatch(wikitext)
	if len(englishBlock) >= 2 && englishBlock[1] != "" {
		section := englishBlock[1]
		all := etymRegex.FindAllStringSubmatch(section, -1)
		if len(all) > 0 {
			return joinEtymologyParts(all)
		}
	}
	// Fallback: use only the first ===Etymology=== on the page (almost always English).
	all := etymRegex.FindAllStringSubmatch(wikitext, 1)
	if len(all) > 0 {
		return joinEtymologyParts(all)
	}
	return ""
}

func joinEtymologyParts(all [][]string) string {
	var parts []string
	for _, m := range all {
		if len(m) < 2 {
			continue
		}
		raw := strings.TrimSpace(m[1])
		if raw != "" {
			parts = append(parts, cleanEtymologyWikitext(raw))
		}
	}
	return strings.Join(parts, "\n\n")
}

func extractWiktionaryExample(wikitext string) string {
	wikitext = strings.ReplaceAll(wikitext, "\r\n", "\n")
	wikitext = strings.ReplaceAll(wikitext, "\r", "\n")
	englishBlock := regexp.MustCompile(`(?m)^==English==\s*\n([\s\S]*?)(?:\n==[^=\n][^\n]*|$)`).FindStringSubmatch(wikitext)
	if len(englishBlock) < 2 {
		return ""
	}
	section := englishBlock[1]
	if m := regexp.MustCompile(`\{\{ux\|en\|([^}|]+)(?:\|[^}]*)?\}\}`).FindStringSubmatch(section); len(m) >= 2 {
		return cleanExampleText(m[1])
	}
	if m := regexp.MustCompile(`\|passage=([^}|]+)(?:\|[^}]*)?\}\}`).FindStringSubmatch(section); len(m) >= 2 {
		ex := m[1]
		if len(ex) > 300 {
			ex = ex[:297] + "..."
		}
		return cleanExampleText(ex)
	}
	if m := regexp.MustCompile(`(?m)^#:\s*([^\n]+)`).FindStringSubmatch(section); len(m) >= 2 {
		ex := strings.TrimSpace(m[1])
		if idx := strings.Index(ex, "{{"); idx > 0 {
			ex = strings.TrimSpace(ex[:idx])
		}
		if ex != "" && len(ex) > 20 {
			if len(ex) > 280 {
				ex = ex[:277] + "..."
			}
			return cleanExampleText(ex)
		}
	}
	return ""
}

func cleanExampleText(s string) string {
	s = strings.TrimSpace(s)
	s = regexp.MustCompile(`'''([^']*)'''`).ReplaceAllString(s, "$1")
	s = regexp.MustCompile(`\[\[([^\]|]+)(?:\|[^\]]+)?\]\]`).ReplaceAllString(s, "$1")
	return strings.TrimSpace(s)
}

func cleanEtymologyWikitext(s string) string {
	s = regexp.MustCompile(`\{\{inh\|[^|]*\|enm\|([^}|]+)\}\}`).ReplaceAllString(s, "Middle English $1")
	s = regexp.MustCompile(`\{\{inh\|[^|]*\|ang\|([^}|]+)\}\}`).ReplaceAllString(s, "Old English $1")
	s = regexp.MustCompile(`\{\{inh\|[^|]*\|fro\|([^}|]+)\}\}`).ReplaceAllString(s, "Old French $1")
	s = regexp.MustCompile(`\{\{inh\|[^|]*\|la\|([^}|]+)\}\}`).ReplaceAllString(s, "Latin $1")
	s = regexp.MustCompile(`\{\{inh\|[^|]*\|grc\|([^}|]+)\}\}`).ReplaceAllString(s, "Ancient Greek $1")
	s = regexp.MustCompile(`\{\{inh\|[^|]*\|[^|]*\|([^}|]+)\}\}`).ReplaceAllString(s, "$1")
	s = regexp.MustCompile(`\{\{der\|[^|]*\|fro\|([^}|]+)\}\}`).ReplaceAllString(s, "Old French $1")
	s = regexp.MustCompile(`\{\{der\|[^|]*\|la\|([^}|]+)\}\}`).ReplaceAllString(s, "Latin $1")
	s = regexp.MustCompile(`\{\{der\|[^|]*\|enm\|([^}|]+)\}\}`).ReplaceAllString(s, "Middle English $1")
	s = regexp.MustCompile(`\{\{der\|[^|]*\|grc\|([^}|]+)\}\}`).ReplaceAllString(s, "Ancient Greek $1")
	s = regexp.MustCompile(`\{\{der\|[^|]*\|[^|]*\|([^}|]+)\}\}`).ReplaceAllString(s, "$1")
	s = regexp.MustCompile(`\{\{m\|[^|]*\|([^}|]+)(?:\|[^}]*)?\}\}`).ReplaceAllString(s, "$1")
	s = regexp.MustCompile(`\{\{l\|[^|]*\|([^}|]+)\}\}`).ReplaceAllString(s, "$1")
	s = regexp.MustCompile(`\{\{cog\|[^|]*\|([^}|]+)\}\}`).ReplaceAllString(s, "$1")
	s = regexp.MustCompile(`\{\{ncog\|[^|]*\|([^}|]+)\}\}`).ReplaceAllString(s, "$1")
	s = regexp.MustCompile(`\[\[w:[^\]|]*(?:\|([^\]]+))?\]\]`).ReplaceAllString(s, "$1")
	s = regexp.MustCompile(`\[\[([^\]|]+)(?:\|[^\]]+)?\]\]`).ReplaceAllString(s, "$1")
	s = regexp.MustCompile(`''([^']*)''`).ReplaceAllString(s, "$1")
	s = regexp.MustCompile(`<ref[^>]*>[\s\S]*?</ref>`).ReplaceAllString(s, "")
	s = regexp.MustCompile(`<ref[^>]*/>`).ReplaceAllString(s, "")
	s = regexp.MustCompile(`\{\{[^}]*\}\}`).ReplaceAllString(s, "")
	s = regexp.MustCompile(`\n{3,}`).ReplaceAllString(s, "\n\n")
	s = regexp.MustCompile(`[ \t]+`).ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

func Dict(word string) error {
	return dictLookup(word, true)
}

func dictLookup(word string, allowSuggest bool) error {
	word = strings.TrimSpace(strings.ToLower(word))
	if word == "" {
		return fmt.Errorf("no word provided")
	}
	reqURL := dictAPIBase + "/" + url.PathEscape(word)
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(reqURL)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		if allowSuggest {
			if suggestion, ok := suggestClosest(word, 3); ok {
				fmt.Fprintf(os.Stdout, "Word not found. Did you mean: %s?\n\n", suggestion)
				return dictLookup(suggestion, false)
			}
		}
		var errBody []dictError
		_ = json.NewDecoder(resp.Body).Decode(&errBody)
		if len(errBody) > 0 && errBody[0].Message != "" {
			return fmt.Errorf("%s", errBody[0].Message)
		}
		return fmt.Errorf("word not found: %q", word)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("dictionary API returned %s", resp.Status)
	}
	var entries []dictEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if len(entries) == 0 {
		return fmt.Errorf("no definition for %q", word)
	}
	printDictEntries(os.Stdout, entries)
	return nil
}

func printDictEntries(out *os.File, entries []dictEntry) {
	previous := loadRecentDictWords(3) // words we searched before (highlight in cyan)
	useColor := isTerminal(out)
	for _, e := range entries {
		current := map[string]bool{strings.ToLower(e.Word): true} // word we're searching (highlight in green)
		fmt.Fprintf(out, "\n%s", strings.ToLower(e.Word))
		var phonetics []string
		for _, p := range e.Phonetics {
			if p.Text != "" {
				phonetics = append(phonetics, p.Text)
			}
		}
		if len(phonetics) > 0 {
			fmt.Fprintf(out, "  %s\n", strings.Join(phonetics, " "))
		} else {
			fmt.Fprintln(out)
		}
			// Etymology: fetch both; prefer Wiktionary when it has longer (full) text
		etym, _ := fetchEtymonlineEtymology(e.Word)
		wiktionaryEtym, wiktionaryExample, wikErr := fetchWiktionaryEtymology(e.Word)
		if wikErr != nil && etym == "" {
			fmt.Fprintf(os.Stderr, "  (Etymology unavailable: %v)\n", wikErr)
		}
		if wiktionaryEtym != "" && (etym == "" || len(wiktionaryEtym) > len(etym)) {
			etym = wiktionaryEtym
		}
		if etym != "" {
			fmt.Fprintf(out, "\n  Etymology:\n")
			for _, line := range strings.Split(etym, "\n") {
				line = strings.TrimSpace(line)
				if line != "" {
					fmt.Fprintf(out, "    %s\n", highlightText(line, current, previous, useColor))
				}
			}
			// If still truncated (from Etymonline meta), point to full entry
			if strings.HasSuffix(etym, "…") || strings.HasSuffix(etym, "...") {
				fmt.Fprintf(out, "    (Full entry: %s%s)\n", etymonlineBase, url.PathEscape(strings.ToLower(e.Word)))
			}
		}
		if wiktionaryExample != "" {
			fmt.Fprintf(out, "\n  Example: %s\n", highlightText(wiktionaryExample, current, previous, useColor))
		}
		var allExamples []string
		for _, m := range e.Meanings {
			fmt.Fprintf(out, "\n  [%s]\n", m.PartOfSpeech)
			for i, d := range m.Definitions {
				fmt.Fprintf(out, "    %d. %s\n", i+1, highlightText(d.Definition, current, previous, useColor))
				if d.Example != "" {
					ex := strings.TrimSpace(d.Example)
					if !strings.HasPrefix(ex, "\"") && !strings.HasPrefix(ex, "'") {
						ex = "\"" + ex + "\""
					}
					fmt.Fprintf(out, "       Example: %s\n", highlightText(ex, current, previous, useColor))
					allExamples = append(allExamples, strings.TrimSpace(d.Example))
				}
			}
			if len(m.Synonyms) > 0 {
				fmt.Fprintf(out, "    Synonyms: %s\n", highlightText(strings.Join(m.Synonyms, ", "), current, previous, useColor))
			}
			if len(m.Antonyms) > 0 {
				fmt.Fprintf(out, "    Antonyms: %s\n", highlightText(strings.Join(m.Antonyms, ", "), current, previous, useColor))
			}
		}
		seen := make(map[string]bool)
		var uniq []string
		for _, ex := range allExamples {
			ex = strings.TrimSpace(ex)
			if ex != "" && !seen[ex] {
				seen[ex] = true
				uniq = append(uniq, ex)
			}
		}
		if len(uniq) > 0 {
			fmt.Fprintf(out, "\n  Examples:\n")
			for _, ex := range uniq {
				fmt.Fprintf(out, "    • %s\n", highlightText(ex, current, previous, useColor))
			}
		}
		saveDictWord(e.Word)
	}
	fmt.Fprintln(out)
}
