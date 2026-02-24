package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// Free Dictionary API (https://dictionaryapi.dev/) - no API key required.
const dictAPIBase = "https://api.dictionaryapi.dev/api/v2/entries/en"

// Word list for "Did you mean?" (GitHub, no auth).
const wordsAlphaURL = "https://raw.githubusercontent.com/dwyl/english-words/master/words_alpha.txt"

var (
	wordList     []string
	wordListOnce sync.Once
)

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
	Title  string `json:"title"`
	Message string `json:"message"`
	Resolution string `json:"resolution"`
}

// loadWordList fetches the English word list from GitHub and caches it.
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

// levenshtein returns the edit distance between a and b.
func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	na, nb := len(ra), len(rb)
	if na == 0 {
		return nb
	}
	if nb == 0 {
		return na
	}
	// dp[i][j] = distance between a[:i] and b[:j]
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

// suggestClosest returns the closest word from the list within maxEditDistance, and true if found.
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
		// Narrow: same first letter and similar length
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
		// Prefer smaller distance; on tie, prefer same length (e.g. "absorb" over "abord" for "absord")
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

// Dict looks up the word via the Free Dictionary API and prints its meaning to stdout.
// On typo (404), suggests a close match and shows its definition.
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
	for _, e := range entries {
		// Headword
		fmt.Fprintf(out, "\n%s", strings.ToLower(e.Word))

		// Phonetics (first with text)
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

		var allExamples []string
		for _, m := range e.Meanings {
			fmt.Fprintf(out, "\n  [%s]\n", m.PartOfSpeech)
			for i, d := range m.Definitions {
				fmt.Fprintf(out, "    %d. %s\n", i+1, d.Definition)
				if d.Example != "" {
					ex := strings.TrimSpace(d.Example)
					if !strings.HasPrefix(ex, "\"") && !strings.HasPrefix(ex, "'") {
						ex = "\"" + ex + "\""
					}
					fmt.Fprintf(out, "       Example: %s\n", ex)
					allExamples = append(allExamples, strings.TrimSpace(d.Example))
				}
			}
			if len(m.Synonyms) > 0 {
				fmt.Fprintf(out, "    Synonyms: %s\n", strings.Join(m.Synonyms, ", "))
			}
			if len(m.Antonyms) > 0 {
				fmt.Fprintf(out, "    Antonyms: %s\n", strings.Join(m.Antonyms, ", "))
			}
		}

		// Examples section (deduplicated)
		if len(allExamples) > 0 {
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
					fmt.Fprintf(out, "    • %s\n", ex)
				}
			}
		}
	}
	fmt.Fprintln(out)
}
