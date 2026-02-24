package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// FitbitTokens stores OAuth tokens for Fitbit API.
type FitbitTokens struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// FitbitSleepResponse is the response from Get Sleep Logs by Date.
type FitbitSleepResponse struct {
	Sleep []FitbitSleepLog `json:"sleep"`
}

// FitbitSleepLog is one sleep log entry.
type FitbitSleepLog struct {
	DateOfSleep         string            `json:"dateOfSleep"`
	Duration            int               `json:"duration"`
	Efficiency          int               `json:"efficiency"`
	EndTime             string            `json:"endTime"`
	InfoCode            int               `json:"infoCode"`
	IsMainSleep         bool              `json:"isMainSleep"`
	Levels              FitbitSleepLevels `json:"levels"`
	LogID               int64             `json:"logId"`
	MinutesAsleep       int               `json:"minutesAsleep"`
	MinutesAwake        int               `json:"minutesAwake"`
	MinutesToFallAsleep int               `json:"minutesToFallAsleep"`
	StartTime           string            `json:"startTime"`
	TimeInBed           int               `json:"timeInBed"`
	Type                string            `json:"type"`
	ValueOfSleepScore   *int              `json:"valueOfSleepScore,omitempty"`
}

// FitbitSleepLevels contains sleep stage data.
type FitbitSleepLevels struct {
	Data    []FitbitSleepLevelData `json:"data"`
	Short   []FitbitSleepLevelData `json:"short"`
	Summary FitbitSleepSummary     `json:"summary"`
}

// FitbitSleepLevelData is one level segment.
type FitbitSleepLevelData struct {
	DateTime string `json:"dateTime"`
	Level    string `json:"level"`
	Seconds  int    `json:"seconds"`
}

// FitbitSleepSummary summarizes stages.
type FitbitSleepSummary struct {
	Deep  FitbitSleepStageSummary `json:"deep"`
	Light FitbitSleepStageSummary `json:"light"`
	Rem   FitbitSleepStageSummary `json:"rem"`
	Wake  FitbitSleepStageSummary `json:"wake"`
}

// FitbitSleepStageSummary is stage count and minutes.
type FitbitSleepStageSummary struct {
	Count   int `json:"count"`
	Minutes int `json:"minutes"`
}

func generateCodeVerifier() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func generateCodeChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

func getTokenFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cairn_fitbit_tokens.json"), nil
}

func loadFitbitTokens() (*FitbitTokens, error) {
	tokenPath, err := getTokenFilePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(tokenPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read token file: %w", err)
	}
	var tokens FitbitTokens
	if err := json.Unmarshal(data, &tokens); err != nil {
		return nil, fmt.Errorf("failed to parse token file: %w", err)
	}
	return &tokens, nil
}

func saveFitbitTokens(tokens *FitbitTokens) error {
	tokenPath, err := getTokenFilePath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(tokens, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(tokenPath, data, 0600)
}

func clearFitbitTokens() error {
	tokenPath, err := getTokenFilePath()
	if err != nil {
		return err
	}
	if err := os.Remove(tokenPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func refreshFitbitToken(clientID, clientSecret, refreshToken string) (*FitbitTokens, error) {
	urlStr := "https://api.fitbit.com/oauth2/token"
	data := url.Values{}
	data.Set("grant_type", "refresh_token")
	data.Set("refresh_token", refreshToken)
	req, err := http.NewRequest("POST", urlStr, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(clientID, clientSecret)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to refresh token: %d %s", resp.StatusCode, string(body))
	}
	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, err
	}
	return &FitbitTokens{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second),
	}, nil
}

func getValidFitbitToken(clientID, clientSecret string) (string, error) {
	tokens, err := loadFitbitTokens()
	if err != nil {
		return "", err
	}
	if tokens == nil {
		return "", fmt.Errorf("no Fitbit tokens found. Please run authorization first")
	}
	if time.Now().Add(5 * time.Minute).After(tokens.ExpiresAt) {
		newTokens, err := refreshFitbitToken(clientID, clientSecret, tokens.RefreshToken)
		if err != nil {
			if strings.Contains(err.Error(), "invalid_grant") || strings.Contains(err.Error(), "Refresh token invalid") {
				_ = clearFitbitTokens()
				return "", fmt.Errorf("no Fitbit tokens found. Please run authorization first")
			}
			return "", fmt.Errorf("failed to refresh token: %w", err)
		}
		saveFitbitTokens(newTokens)
		return newTokens.AccessToken, nil
	}
	return tokens.AccessToken, nil
}

func authorizeFitbit(clientID, clientSecret, callbackURL string) error {
	codeVerifier, err := generateCodeVerifier()
	if err != nil {
		return err
	}
	codeChallenge := generateCodeChallenge(codeVerifier)
	state := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%d", time.Now().Unix())))
	authURL := fmt.Sprintf(
		"https://www.fitbit.com/oauth2/authorize?client_id=%s&response_type=code&scope=sleep&redirect_uri=%s&code_challenge=%s&code_challenge_method=S256&state=%s",
		url.QueryEscape(clientID), url.QueryEscape(callbackURL), url.QueryEscape(codeChallenge), url.QueryEscape(state),
	)
	fmt.Fprintf(os.Stderr, "Opening browser for authorization...\n")
	if err := openBrowser(authURL); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Could not open browser: %v\nPlease visit: %s\n\n", err, authURL)
	}
	fmt.Fprintln(os.Stderr, "Waiting for authorization callback...")
	mux := http.NewServeMux()
	authCodeChan := make(chan string, 1)
	errorChan := make(chan error, 1)
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		if code == "" {
			errorChan <- fmt.Errorf("no authorization code received")
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("Authorization failed: no code received"))
			return
		}
		authCodeChan <- code
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Authorization successful! You can close this window."))
	})
	server := &http.Server{Addr: ":8765", Handler: mux}
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errorChan <- err
		}
	}()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		server.Shutdown(ctx)
	}()
	select {
	case code := <-authCodeChan:
		urlStr := "https://api.fitbit.com/oauth2/token"
		data := url.Values{}
		data.Set("client_id", clientID)
		data.Set("grant_type", "authorization_code")
		data.Set("code", code)
		data.Set("redirect_uri", callbackURL)
		data.Set("code_verifier", codeVerifier)
		req, err := http.NewRequest("POST", urlStr, strings.NewReader(data.Encode()))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetBasicAuth(clientID, clientSecret)
		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("failed to exchange code for token: %d %s", resp.StatusCode, string(body))
		}
		var tokenResp struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			ExpiresIn    int    `json:"expires_in"`
		}
		if err := json.Unmarshal(body, &tokenResp); err != nil {
			return err
		}
		tokens := &FitbitTokens{
			AccessToken:  tokenResp.AccessToken,
			RefreshToken: tokenResp.RefreshToken,
			ExpiresAt:    time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second),
		}
		if err := saveFitbitTokens(tokens); err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "Successfully authorized and saved tokens!")
		return nil
	case err := <-errorChan:
		return err
	case <-time.After(5 * time.Minute):
		server.Shutdown(context.Background())
		return fmt.Errorf("authorization timeout")
	}
}

func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Run()
}

func fitbitHTTPGet(urlStr, accessToken string) (*http.Response, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", urlStr, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", accessToken))
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("HTTP error: %d: %s", resp.StatusCode, string(body))
	}
	return resp, nil
}

func getSleepData(accessToken string, date string) (*FitbitSleepResponse, error) {
	urlStr := fmt.Sprintf("https://api.fitbit.com/1/user/-/sleep/date/%s.json", date)
	resp, err := fitbitHTTPGet(urlStr, accessToken)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var sleepResp FitbitSleepResponse
	if err := json.Unmarshal(body, &sleepResp); err != nil {
		return nil, err
	}
	scoreURL := fmt.Sprintf("https://api.fitbit.com/1/user/-/sleep/score/date/%s.json", date)
	scoreResp, err := fitbitHTTPGet(scoreURL, accessToken)
	if err == nil {
		defer scoreResp.Body.Close()
		scoreBody, _ := io.ReadAll(scoreResp.Body)
		var scoreData struct {
			ValueOfSleepScore int `json:"valueOfSleepScore"`
		}
		if json.Unmarshal(scoreBody, &scoreData) == nil && scoreData.ValueOfSleepScore > 0 && len(sleepResp.Sleep) > 0 {
			sleepResp.Sleep[0].ValueOfSleepScore = &scoreData.ValueOfSleepScore
		}
	}
	return &sleepResp, nil
}

func formatSleepData(sleepResp *FitbitSleepResponse) string {
	if len(sleepResp.Sleep) == 0 {
		return "No sleep data found for today."
	}
	var b strings.Builder
	b.WriteString("<b>🌙 Sleep Summary</b>\n\n")
	for _, sleep := range sleepResp.Sleep {
		hours := sleep.Duration / (1000 * 60 * 60)
		minutes := (sleep.Duration / (1000 * 60)) % 60
		timeInBedHours := sleep.TimeInBed / 60
		timeInBedMinutes := sleep.TimeInBed % 60
		b.WriteString(fmt.Sprintf("<b>Date:</b> %s\n", sleep.DateOfSleep))
		if sleep.ValueOfSleepScore != nil {
			b.WriteString(fmt.Sprintf("<b>Sleep Score:</b> %d\n", *sleep.ValueOfSleepScore))
		}
		b.WriteString(fmt.Sprintf("<b>Duration:</b> %dh %dm\n", hours, minutes))
		b.WriteString(fmt.Sprintf("<b>Time in Bed:</b> %dh %dm\n", timeInBedHours, timeInBedMinutes))
		b.WriteString(fmt.Sprintf("<b>Minutes Asleep:</b> %d\n", sleep.MinutesAsleep))
		b.WriteString(fmt.Sprintf("<b>Minutes Awake:</b> %d\n", sleep.MinutesAwake))
		b.WriteString(fmt.Sprintf("<b>Efficiency:</b> %d%%\n", sleep.Efficiency))
		if sleep.MinutesToFallAsleep > 0 {
			b.WriteString(fmt.Sprintf("<b>Time to Fall Asleep:</b> %d min\n", sleep.MinutesToFallAsleep))
		}
		if sleep.Levels.Summary.Deep.Minutes > 0 || sleep.Levels.Summary.Light.Minutes > 0 || sleep.Levels.Summary.Rem.Minutes > 0 {
			b.WriteString("\n<b>Sleep Stages:</b>\n")
			b.WriteString(fmt.Sprintf("  Deep: %d min\n", sleep.Levels.Summary.Deep.Minutes))
			b.WriteString(fmt.Sprintf("  Light: %d min\n", sleep.Levels.Summary.Light.Minutes))
			b.WriteString(fmt.Sprintf("  REM: %d min\n", sleep.Levels.Summary.Rem.Minutes))
			b.WriteString(fmt.Sprintf("  Awake: %d min\n", sleep.Levels.Summary.Wake.Minutes))
		}
		b.WriteString(fmt.Sprintf("\n<b>Start:</b> %s\n", sleep.StartTime))
		b.WriteString(fmt.Sprintf("<b>End:</b> %s\n", sleep.EndTime))
	}
	b.WriteString("\n#sleep")
	return b.String()
}

// Morning runs the morning flow: get Fitbit sleep data and post to Telegram.
func Morning(config *Config, additionalText string) error {
	if config.Fitbit.ClientID == "" {
		return fmt.Errorf("'fitbit.client_id' not found in config file")
	}
	if config.Fitbit.ClientSecret == "" {
		return fmt.Errorf("'fitbit.client_secret' not found in config file")
	}
	accessToken, err := getValidFitbitToken(config.Fitbit.ClientID, config.Fitbit.ClientSecret)
	if err != nil {
		if strings.Contains(err.Error(), "no Fitbit tokens found") {
			callbackURL := "http://127.0.0.1:8765/callback"
			fmt.Fprintln(os.Stderr, "No Fitbit tokens found. Starting authorization...")
			if err := authorizeFitbit(config.Fitbit.ClientID, config.Fitbit.ClientSecret, callbackURL); err != nil {
				return fmt.Errorf("authorization failed: %w", err)
			}
			accessToken, err = getValidFitbitToken(config.Fitbit.ClientID, config.Fitbit.ClientSecret)
			if err != nil {
				return fmt.Errorf("failed to get token after authorization: %w", err)
			}
		} else {
			return err
		}
	}
	today := time.Now().Format("2006-01-02")
	sleepResp, err := getSleepData(accessToken, today)
	if err != nil {
		return fmt.Errorf("failed to get sleep data: %w", err)
	}
	sleepMessage := formatSleepData(sleepResp)
	if additionalText != "" {
		sleepMessage = sleepMessage + "\n\n" + strings.TrimSpace(additionalText)
	}
	if _, err := postToTelegram(config.Telegram.BotToken, config.Telegram.ChannelID, sleepMessage); err != nil {
		return fmt.Errorf("failed to post to Telegram: %w", err)
	}
	return nil
}
