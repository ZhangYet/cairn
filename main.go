package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/pflag"
)

const version = "0.1.3"

type Config struct {
	Telegram   TelegramConfig   `toml:"telegram"`
	Fitbit     FitbitConfig     `toml:"fitbit"`
	OpenRouter OpenRouterConfig `toml:"openrouter"`
}

type TelegramConfig struct {
	BotToken  string `toml:"bot_token"`
	ChannelID string `toml:"channel_id"`
}

type FitbitConfig struct {
	ClientID     string `toml:"client_id"`
	ClientSecret string `toml:"client_secret"`
}

type OpenRouterConfig struct {
	APIKey string `toml:"api_key"`
	Model  string `toml:"model"`
}

type TelegramResponse struct {
	OK          bool   `json:"ok"`
	Description string `json:"description,omitempty"`
}

type FitbitTokens struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
}

type FitbitSleepResponse struct {
	Sleep []FitbitSleepLog `json:"sleep"`
}

type FitbitSleepLog struct {
	DateOfSleep         string            `json:"dateOfSleep"`
	Duration            int               `json:"duration"` // in milliseconds
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
	ValueOfSleepScore   *int              `json:"valueOfSleepScore,omitempty"` // Sleep Score if available
}

type FitbitSleepLevels struct {
	Data    []FitbitSleepLevelData `json:"data"`
	Short   []FitbitSleepLevelData `json:"short"`
	Summary FitbitSleepSummary     `json:"summary"`
}

type FitbitSleepLevelData struct {
	DateTime string `json:"dateTime"`
	Level    string `json:"level"`
	Seconds  int    `json:"seconds"`
}

type FitbitSleepSummary struct {
	Deep  FitbitSleepStageSummary `json:"deep"`
	Light FitbitSleepStageSummary `json:"light"`
	Rem   FitbitSleepStageSummary `json:"rem"`
	Wake  FitbitSleepStageSummary `json:"wake"`
}

type FitbitSleepStageSummary struct {
	Count   int `json:"count"`
	Minutes int `json:"minutes"`
}

// OpenRouter chat completion types
type openRouterReq struct {
	Model    string          `json:"model"`
	Messages []openRouterMsg `json:"messages"`
	Stream   bool            `json:"stream,omitempty"`
}

type openRouterMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openRouterResp struct {
	Choices []openRouterChoice `json:"choices"`
}

type openRouterChoice struct {
	Message openRouterMsg `json:"message"`
	Delta   *struct {
		Content string `json:"content"`
	} `json:"delta,omitempty"`
}

func loadConfig(configPath string) (*Config, error) {
	// Expand user home directory
	expandedPath := configPath
	if len(configPath) > 0 && configPath[0] == '~' {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get home directory: %w", err)
		}
		if len(configPath) > 1 {
			expandedPath = filepath.Join(home, configPath[2:])
		} else {
			expandedPath = home
		}
	}

	data, err := os.ReadFile(expandedPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("config file not found at %s\nPlease create a config file with the following structure:\n[telegram]\nbot_token = \"your_bot_token\"\nchannel_id = \"@your_channel\"\n\n[fitbit]\nclient_id = \"your_client_id\"\nclient_secret = \"your_client_secret\"\n\n[openrouter]\napi_key = \"your_openrouter_api_key\"\nmodel = \"openrouter/model-id\"", expandedPath)
		}
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config Config
	if err := toml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse TOML config: %w", err)
	}

	if config.Telegram.BotToken == "" {
		return nil, fmt.Errorf("'bot_token' not found in config file")
	}

	if config.Telegram.ChannelID == "" {
		return nil, fmt.Errorf("'channel_id' not found in config file")
	}

	return &config, nil
}

func httpPost(url string, jsonData []byte) (*http.Response, error) {
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("HTTP error: %d", resp.StatusCode)
	}

	return resp, nil
}

func postToTelegram(botToken, channelID, content string) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", botToken)
	content = ensureCairnTag(content)
	payload := map[string]interface{}{
		"chat_id":    channelID,
		"text":       content,
		"parse_mode": "HTML",
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	resp, err := httpPost(url, jsonData)
	if err != nil {
		return fmt.Errorf("failed to post to Telegram: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	var telegramResp TelegramResponse
	if err := json.Unmarshal(body, &telegramResp); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	if !telegramResp.OK {
		return fmt.Errorf("telegram API error: %s", telegramResp.Description)
	}

	fmt.Fprintln(os.Stderr, "Successfully posted to Telegram channel")
	return nil
}

func postPhotoToTelegram(botToken, channelID, photoPath, caption string) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendPhoto", botToken)

	// Read photo file
	photoFile, err := os.Open(photoPath)
	if err != nil {
		return fmt.Errorf("failed to open photo file: %w", err)
	}
	defer photoFile.Close()

	// Create multipart form
	var requestBody bytes.Buffer
	writer := multipart.NewWriter(&requestBody)

	// Add chat_id
	chatIDField, err := writer.CreateFormField("chat_id")
	if err != nil {
		return fmt.Errorf("failed to create form field: %w", err)
	}
	chatIDField.Write([]byte(channelID))

	// Add caption if provided (ensure #cairn tag is present)
	if caption != "" {
		caption = ensureCairnTag(caption)
		captionField, err := writer.CreateFormField("caption")
		if err != nil {
			return fmt.Errorf("failed to create caption field: %w", err)
		}
		captionField.Write([]byte(caption))
	} else {
		// Even if no caption provided, add #cairn tag as caption
		captionField, err := writer.CreateFormField("caption")
		if err != nil {
			return fmt.Errorf("failed to create caption field: %w", err)
		}
		captionField.Write([]byte("#cairn"))
	}

	// Add parse_mode
	parseModeField, err := writer.CreateFormField("parse_mode")
	if err != nil {
		return fmt.Errorf("failed to create parse_mode field: %w", err)
	}
	parseModeField.Write([]byte("HTML"))

	// Add photo file
	photoField, err := writer.CreateFormFile("photo", filepath.Base(photoPath))
	if err != nil {
		return fmt.Errorf("failed to create form file: %w", err)
	}
	_, err = io.Copy(photoField, photoFile)
	if err != nil {
		return fmt.Errorf("failed to copy photo data: %w", err)
	}

	writer.Close()

	// Create HTTP request
	client := &http.Client{
		Timeout: 30 * time.Second, // Longer timeout for file uploads
	}

	req, err := http.NewRequest("POST", url, &requestBody)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to post to Telegram: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP error: %d, response: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	var telegramResp TelegramResponse
	if err := json.Unmarshal(body, &telegramResp); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	if !telegramResp.OK {
		return fmt.Errorf("telegram API error: %s", telegramResp.Description)
	}

	fmt.Fprintln(os.Stderr, "Successfully posted photo to Telegram channel")
	return nil
}

func postMultiplePhotosToTelegram(botToken, channelID string, photoPaths []string, caption string) error {
	// Telegram allows up to 10 media files in a media group
	if len(photoPaths) > 10 {
		return fmt.Errorf("maximum 10 photos allowed, got %d", len(photoPaths))
	}

	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMediaGroup", botToken)

	// Prepare caption (only for the first photo in media group)
	if caption != "" {
		caption = ensureCairnTag(caption)
	} else {
		caption = "#cairn"
	}

	// Create multipart form
	var requestBody bytes.Buffer
	writer := multipart.NewWriter(&requestBody)

	// Add chat_id
	chatIDField, err := writer.CreateFormField("chat_id")
	if err != nil {
		return fmt.Errorf("failed to create form field: %w", err)
	}
	chatIDField.Write([]byte(channelID))

	// Build media array JSON
	media := make([]map[string]interface{}, len(photoPaths))
	for i := range photoPaths {
		filename := fmt.Sprintf("photo%d", i)
		mediaItem := map[string]interface{}{
			"type":  "photo",
			"media": fmt.Sprintf("attach://%s", filename),
		}
		// Only first photo gets the caption
		if i == 0 {
			mediaItem["caption"] = caption
		}
		media[i] = mediaItem
	}

	mediaJSON, err := json.Marshal(media)
	if err != nil {
		return fmt.Errorf("failed to marshal media: %w", err)
	}

	// Add media field
	mediaField, err := writer.CreateFormField("media")
	if err != nil {
		return fmt.Errorf("failed to create media field: %w", err)
	}
	mediaField.Write(mediaJSON)

	// Add parse_mode
	parseModeField, err := writer.CreateFormField("parse_mode")
	if err != nil {
		return fmt.Errorf("failed to create parse_mode field: %w", err)
	}
	parseModeField.Write([]byte("HTML"))

	// Add all photo files
	for i, photoPath := range photoPaths {
		photoFile, err := os.Open(photoPath)
		if err != nil {
			return fmt.Errorf("failed to open photo file %s: %w", photoPath, err)
		}

		photoField, err := writer.CreateFormFile(fmt.Sprintf("photo%d", i), filepath.Base(photoPath))
		if err != nil {
			photoFile.Close()
			return fmt.Errorf("failed to create form file: %w", err)
		}

		_, err = io.Copy(photoField, photoFile)
		photoFile.Close()
		if err != nil {
			return fmt.Errorf("failed to copy photo data: %w", err)
		}
	}

	writer.Close()

	// Create HTTP request
	client := &http.Client{
		Timeout: 60 * time.Second, // Longer timeout for multiple file uploads
	}

	req, err := http.NewRequest("POST", url, &requestBody)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to post to Telegram: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP error: %d, response: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	var telegramResp TelegramResponse
	if err := json.Unmarshal(body, &telegramResp); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	if !telegramResp.OK {
		return fmt.Errorf("telegram API error: %s", telegramResp.Description)
	}

	fmt.Fprintf(os.Stderr, "Successfully posted %d photo(s) to Telegram channel\n", len(photoPaths))
	return nil
}

func readFileContent(filePath string) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to read file: %w", err)
	}
	return string(data), nil
}

func ensureCairnTag(content string) string {
	// Check if content already contains #cairn tag (case-insensitive)
	contentLower := strings.ToLower(content)
	if strings.Contains(contentLower, "#cairn") {
		return content
	}

	// Add #cairn tag to the content
	// Trim trailing whitespace and add the tag
	content = strings.TrimRight(content, " \n\t")
	if content != "" {
		return content + " #cairn"
	}
	return "#cairn"
}

// PKCE helper functions
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
			return nil, nil // No tokens yet
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
		return fmt.Errorf("failed to marshal tokens: %w", err)
	}

	if err := os.WriteFile(tokenPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write token file: %w", err)
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
		return nil, fmt.Errorf("failed to parse token response: %w", err)
	}

	tokens := &FitbitTokens{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second),
	}

	return tokens, nil
}

func getValidFitbitToken(clientID, clientSecret string) (string, error) {
	tokens, err := loadFitbitTokens()
	if err != nil {
		return "", err
	}

	if tokens == nil {
		return "", fmt.Errorf("no Fitbit tokens found. Please run authorization first")
	}

	// Check if token is expired (with 5 minute buffer)
	if time.Now().Add(5 * time.Minute).After(tokens.ExpiresAt) {
		// Refresh token
		newTokens, err := refreshFitbitToken(clientID, clientSecret, tokens.RefreshToken)
		if err != nil {
			return "", fmt.Errorf("failed to refresh token: %w", err)
		}
		if err := saveFitbitTokens(newTokens); err != nil {
			return "", fmt.Errorf("failed to save refreshed tokens: %w", err)
		}
		return newTokens.AccessToken, nil
	}

	return tokens.AccessToken, nil
}

func authorizeFitbit(clientID, clientSecret, callbackURL string) error {
	codeVerifier, err := generateCodeVerifier()
	if err != nil {
		return fmt.Errorf("failed to generate code verifier: %w", err)
	}

	codeChallenge := generateCodeChallenge(codeVerifier)
	state := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%d", time.Now().Unix())))

	authURL := fmt.Sprintf(
		"https://www.fitbit.com/oauth2/authorize?client_id=%s&response_type=code&scope=sleep&redirect_uri=%s&code_challenge=%s&code_challenge_method=S256&state=%s",
		url.QueryEscape(clientID),
		url.QueryEscape(callbackURL),
		url.QueryEscape(codeChallenge),
		url.QueryEscape(state),
	)

	fmt.Fprintf(os.Stderr, "Opening browser for authorization...\n")

	// Open browser automatically
	if err := openBrowser(authURL); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Could not open browser automatically: %v\n", err)
		fmt.Fprintf(os.Stderr, "Please visit this URL to authorize:\n%s\n\n", authURL)
	}

	fmt.Fprintln(os.Stderr, "Waiting for authorization callback...")

	// Start local server to handle callback
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

	server := &http.Server{
		Addr:    ":8765",
		Handler: mux,
	}

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errorChan <- err
		}
	}()

	// Shutdown server after receiving code
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		server.Shutdown(ctx)
	}()

	// Wait for authorization code or error
	select {
	case code := <-authCodeChan:
		// Exchange code for tokens
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
			return fmt.Errorf("failed to parse token response: %w", err)
		}

		tokens := &FitbitTokens{
			AccessToken:  tokenResp.AccessToken,
			RefreshToken: tokenResp.RefreshToken,
			ExpiresAt:    time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second),
		}

		if err := saveFitbitTokens(tokens); err != nil {
			return fmt.Errorf("failed to save tokens: %w", err)
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

func httpGet(url string, accessToken string) (*http.Response, error) {
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	req, err := http.NewRequest("GET", url, nil)
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
		return nil, fmt.Errorf("HTTP error: %d, response: %s", resp.StatusCode, string(body))
	}

	return resp, nil
}

func getSleepData(accessToken string, date string) (*FitbitSleepResponse, error) {
	// date format: yyyy-MM-dd
	urlStr := fmt.Sprintf("https://api.fitbit.com/1/user/-/sleep/date/%s.json", date)

	resp, err := httpGet(urlStr, accessToken)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var sleepResp FitbitSleepResponse
	if err := json.Unmarshal(body, &sleepResp); err != nil {
		return nil, fmt.Errorf("failed to parse sleep data: %w", err)
	}

	// Try to get sleep score from a separate endpoint if available
	// Note: This endpoint may not be available for all users/devices
	scoreURL := fmt.Sprintf("https://api.fitbit.com/1/user/-/sleep/score/date/%s.json", date)
	scoreResp, err := httpGet(scoreURL, accessToken)
	if err == nil {
		defer scoreResp.Body.Close()
		scoreBody, err := io.ReadAll(scoreResp.Body)
		if err == nil {
			var scoreData struct {
				ValueOfSleepScore int `json:"valueOfSleepScore"`
			}
			if json.Unmarshal(scoreBody, &scoreData) == nil && scoreData.ValueOfSleepScore > 0 {
				// Add score to the first sleep log if available
				if len(sleepResp.Sleep) > 0 {
					sleepResp.Sleep[0].ValueOfSleepScore = &scoreData.ValueOfSleepScore
				}
			}
		}
	}
	// If score endpoint fails, that's okay - we'll just use what we have

	return &sleepResp, nil
}

func formatSleepData(sleepResp *FitbitSleepResponse) string {
	if len(sleepResp.Sleep) == 0 {
		return "No sleep data found for today."
	}

	var builder strings.Builder
	builder.WriteString("<b>🌙 Sleep Summary</b>\n\n")

	for _, sleep := range sleepResp.Sleep {
		// Format duration
		hours := sleep.Duration / (1000 * 60 * 60)
		minutes := (sleep.Duration / (1000 * 60)) % 60

		// Format time in bed
		timeInBedHours := sleep.TimeInBed / 60
		timeInBedMinutes := sleep.TimeInBed % 60

		builder.WriteString(fmt.Sprintf("<b>Date:</b> %s\n", sleep.DateOfSleep))

		// Add Sleep Score if available
		if sleep.ValueOfSleepScore != nil {
			builder.WriteString(fmt.Sprintf("<b>Sleep Score:</b> %d\n", *sleep.ValueOfSleepScore))
		}

		builder.WriteString(fmt.Sprintf("<b>Duration:</b> %dh %dm\n", hours, minutes))
		builder.WriteString(fmt.Sprintf("<b>Time in Bed:</b> %dh %dm\n", timeInBedHours, timeInBedMinutes))
		builder.WriteString(fmt.Sprintf("<b>Minutes Asleep:</b> %d\n", sleep.MinutesAsleep))
		builder.WriteString(fmt.Sprintf("<b>Minutes Awake:</b> %d\n", sleep.MinutesAwake))
		builder.WriteString(fmt.Sprintf("<b>Efficiency:</b> %d%%\n", sleep.Efficiency))

		if sleep.MinutesToFallAsleep > 0 {
			builder.WriteString(fmt.Sprintf("<b>Time to Fall Asleep:</b> %d min\n", sleep.MinutesToFallAsleep))
		}

		// Sleep stages
		if sleep.Levels.Summary.Deep.Minutes > 0 || sleep.Levels.Summary.Light.Minutes > 0 || sleep.Levels.Summary.Rem.Minutes > 0 {
			builder.WriteString("\n<b>Sleep Stages:</b>\n")
			builder.WriteString(fmt.Sprintf("  Deep: %d min\n", sleep.Levels.Summary.Deep.Minutes))
			builder.WriteString(fmt.Sprintf("  Light: %d min\n", sleep.Levels.Summary.Light.Minutes))
			builder.WriteString(fmt.Sprintf("  REM: %d min\n", sleep.Levels.Summary.Rem.Minutes))
			builder.WriteString(fmt.Sprintf("  Awake: %d min\n", sleep.Levels.Summary.Wake.Minutes))
		}

		builder.WriteString(fmt.Sprintf("\n<b>Start:</b> %s\n", sleep.StartTime))
		builder.WriteString(fmt.Sprintf("<b>End:</b> %s\n", sleep.EndTime))
	}

	builder.WriteString("\n#sleep")

	return builder.String()
}

func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default: // linux and others
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Run()
}

func morningFunction(config *Config, additionalText string) error {
	// Check Fitbit config
	if config.Fitbit.ClientID == "" {
		return fmt.Errorf("'fitbit.client_id' not found in config file")
	}
	if config.Fitbit.ClientSecret == "" {
		return fmt.Errorf("'fitbit.client_secret' not found in config file")
	}

	// Get valid access token
	accessToken, err := getValidFitbitToken(config.Fitbit.ClientID, config.Fitbit.ClientSecret)
	if err != nil {
		if strings.Contains(err.Error(), "no Fitbit tokens found") {
			// Try to authorize
			callbackURL := "http://127.0.0.1:8765/callback"
			fmt.Fprintln(os.Stderr, "No Fitbit tokens found. Starting authorization...")
			if err := authorizeFitbit(config.Fitbit.ClientID, config.Fitbit.ClientSecret, callbackURL); err != nil {
				return fmt.Errorf("authorization failed: %w", err)
			}
			// Retry getting token
			accessToken, err = getValidFitbitToken(config.Fitbit.ClientID, config.Fitbit.ClientSecret)
			if err != nil {
				return fmt.Errorf("failed to get token after authorization: %w", err)
			}
		} else {
			return err
		}
	}

	// Get today's date
	today := time.Now().Format("2006-01-02")

	// Fetch sleep data
	sleepResp, err := getSleepData(accessToken, today)
	if err != nil {
		return fmt.Errorf("failed to get sleep data: %w", err)
	}

	// Format sleep data
	sleepMessage := formatSleepData(sleepResp)

	// Add additional text if provided
	if additionalText != "" {
		sleepMessage = sleepMessage + "\n\n" + strings.TrimSpace(additionalText)
	}

	// Post to Telegram
	if err := postToTelegram(config.Telegram.BotToken, config.Telegram.ChannelID, sleepMessage); err != nil {
		return fmt.Errorf("failed to post to Telegram: %w", err)
	}

	return nil
}

const openRouterURL = "https://openrouter.ai/api/v1/chat/completions"

// streamChunk is one SSE chunk from OpenRouter (streaming).
type streamChunk struct {
	Choices []openRouterChoice `json:"choices"`
}

func writerFunction(config *Config, settingPath, outputPath string) error {
	if config.OpenRouter.APIKey == "" {
		return fmt.Errorf("'openrouter.api_key' not found in config file")
	}
	if config.OpenRouter.Model == "" {
		return fmt.Errorf("'openrouter.model' not found in config file")
	}

	prompt, err := readFileContent(settingPath)
	if err != nil {
		return fmt.Errorf("failed to read setting file: %w", err)
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return fmt.Errorf("setting file is empty")
	}

	reqBody := openRouterReq{
		Model: config.OpenRouter.Model,
		Messages: []openRouterMsg{
			{Role: "user", Content: prompt},
		},
		Stream: true,
	}
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", openRouterURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+config.OpenRouter.APIKey)

	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to call OpenRouter: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("OpenRouter API error: %d %s", resp.StatusCode, string(body))
	}

	// Parse SSE stream: "data: {...}\n" or "data: [DONE]\n", ignore ": OPENROUTER PROCESSING"
	var contentBuilder strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, ": ") {
			// Comment line (e.g. ": OPENROUTER PROCESSING")
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		payload = strings.TrimSpace(payload)
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var chunk streamChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue // skip malformed chunks
		}
		if len(chunk.Choices) > 0 && chunk.Choices[0].Delta != nil {
			contentBuilder.WriteString(chunk.Choices[0].Delta.Content)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("failed to read stream: %w", err)
	}

	content := strings.TrimSpace(contentBuilder.String())
	if content == "" {
		return fmt.Errorf("OpenRouter returned empty content")
	}

	if outputPath != "" {
		if err := os.WriteFile(outputPath, []byte(content), 0644); err != nil {
			return fmt.Errorf("failed to write output file: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Wrote result to %s\n", outputPath)
	}

	if err := postToTelegram(config.Telegram.BotToken, config.Telegram.ChannelID, content); err != nil {
		return fmt.Errorf("failed to post to Telegram: %w", err)
	}

	fmt.Fprintln(os.Stderr, "Successfully generated content and posted to Telegram channel")
	return nil
}

func printHelp() {
	fmt.Printf(`cairn - A command-line tool to post content to Telegram channels
Version: %s

Usage:
  cairn [flags]

Flags:
  -h, --help          Show this help message
  -c, --config PATH   Path to config file (default: ~/.cairn.toml)
  -p, --post TEXT     Content to post (can include tags with #)
  -f, --file PATH     Read content from a file
  -P, --photo PATH    Path to photo file(s) to post (comma-separated, caption from -p or -f)
  -m, --morning       Get Fitbit sleep data and post to Telegram channel
  -W, --writer PATH   Read setting from file, send to OpenRouter (streaming), post generated content
  -o, --output PATH   Write generated content to file (use with -W)

Examples:
  cairn -p "Hello world #tag1 #tag2"
  cairn -f message.txt
  cairn -P image.jpg -p "Photo caption #tag1"
  cairn -P image1.jpg,image2.jpg -p "Multiple photos"
  cairn --photo image.jpg -f caption.txt
  cairn -c ~/.custom_cairn.toml -p "Custom config"
  cairn --morning
  cairn -W prompt.txt
  cairn -W prompt.txt -o result.txt
`, version)
}

func main() {
	configPath := pflag.StringP("config", "c", "~/.cairn.toml", "Path to config file")
	postContent := pflag.StringP("post", "p", "", "Content to post")
	filePath := pflag.StringP("file", "f", "", "Read content from a file")
	photoPathStr := pflag.StringP("photo", "P", "", "Path to photo file(s) to post (comma-separated)")
	morning := pflag.BoolP("morning", "m", false, "Get Fitbit sleep data and post to Telegram channel")
	writerPath := pflag.StringP("writer", "W", "", "Read setting from file, send to OpenRouter (streaming), post generated content")
	outputPath := pflag.StringP("output", "o", "", "Write generated content to file (use with -W)")
	help := pflag.BoolP("help", "h", false, "Show help message")

	pflag.Parse()

	// Handle help flag
	if *help {
		printHelp()
		os.Exit(0)
	}

	cfgPath := *configPath

	// Load configuration
	config, err := loadConfig(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Handle morning command
	if *morning {
		// Get additional text content if provided
		var additionalText string
		content := *postContent
		file := *filePath

		if file != "" {
			var err error
			additionalText, err = readFileContent(file)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
		} else if content != "" {
			additionalText = content
		}

		if err := morningFunction(config, additionalText); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Handle writer command
	if *writerPath != "" {
		if err := writerFunction(config, *writerPath, *outputPath); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	content := *postContent
	file := *filePath

	// Parse comma-separated photo paths
	var photos []string
	if *photoPathStr != "" {
		photoList := strings.Split(*photoPathStr, ",")
		for _, p := range photoList {
			p = strings.TrimSpace(p)
			if p != "" {
				photos = append(photos, p)
			}
		}
	}

	// If photo is provided, caption is optional (can be empty)
	// If photo is not provided, either --post or --file must be provided
	if len(photos) == 0 {
		if content == "" && file == "" {
			fmt.Fprintln(os.Stderr, "Error: Either --post or --file must be provided (or use -P/--photo to post a photo, -m/--morning for sleep data, or -W/--writer for OpenRouter)")
			printHelp()
			os.Exit(1)
		}

		if content != "" && file != "" {
			fmt.Fprintln(os.Stderr, "Error: Cannot use both --post and --file at the same time")
			os.Exit(1)
		}
	}

	// Get content (caption for photo or main content for text post)
	var finalContent string
	if file != "" {
		var err error
		finalContent, err = readFileContent(file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	} else {
		finalContent = content
	}

	// Post to Telegram
	if len(photos) > 0 {
		if len(photos) == 1 {
			// Post single photo with caption
			if err := postPhotoToTelegram(config.Telegram.BotToken, config.Telegram.ChannelID, photos[0], finalContent); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
		} else {
			// Post multiple photos with caption
			if err := postMultiplePhotosToTelegram(config.Telegram.BotToken, config.Telegram.ChannelID, photos, finalContent); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
		}
	} else {
		// Post text message
		if err := postToTelegram(config.Telegram.BotToken, config.Telegram.ChannelID, finalContent); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	}
}
