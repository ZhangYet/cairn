package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/pflag"
)

const version = "0.1.2"

type Config struct {
	Telegram TelegramConfig `toml:"telegram"`
}

type TelegramConfig struct {
	BotToken  string `toml:"bot_token"`
	ChannelID string `toml:"channel_id"`
}

type TelegramResponse struct {
	OK          bool   `json:"ok"`
	Description string `json:"description,omitempty"`
}

func loadConfig(configPath string) (*TelegramConfig, error) {
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
			return nil, fmt.Errorf("config file not found at %s\nPlease create a config file with the following structure:\n[telegram]\nbot_token = \"your_bot_token\"\nchannel_id = \"@your_channel\"", expandedPath)
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

	return &config.Telegram, nil
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
			"type":    "photo",
			"media":   fmt.Sprintf("attach://%s", filename),
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

Examples:
  cairn -p "Hello world #tag1 #tag2"
  cairn -f message.txt
  cairn -P image.jpg -p "Photo caption #tag1"
  cairn -P image1.jpg,image2.jpg -p "Multiple photos"
  cairn --photo image.jpg -f caption.txt
  cairn -c ~/.custom_cairn.toml -p "Custom config"
`, version)
}

func main() {
	configPath := pflag.StringP("config", "c", "~/.cairn.toml", "Path to config file")
	postContent := pflag.StringP("post", "p", "", "Content to post")
	filePath := pflag.StringP("file", "f", "", "Read content from a file")
	photoPathStr := pflag.StringP("photo", "P", "", "Path to photo file(s) to post (comma-separated)")
	help := pflag.BoolP("help", "h", false, "Show help message")

	pflag.Parse()

	// Handle help flag
	if *help {
		printHelp()
		os.Exit(0)
	}

	cfgPath := *configPath
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
			fmt.Fprintln(os.Stderr, "Error: Either --post or --file must be provided (or use -P/--photo to post a photo)")
			printHelp()
			os.Exit(1)
		}

		if content != "" && file != "" {
			fmt.Fprintln(os.Stderr, "Error: Cannot use both --post and --file at the same time")
			os.Exit(1)
		}
	}

	// Load configuration
	telegramConfig, err := loadConfig(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
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
			if err := postPhotoToTelegram(telegramConfig.BotToken, telegramConfig.ChannelID, photos[0], finalContent); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
		} else {
			// Post multiple photos with caption
			if err := postMultiplePhotosToTelegram(telegramConfig.BotToken, telegramConfig.ChannelID, photos, finalContent); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
		}
	} else {
		// Post text message
		if err := postToTelegram(telegramConfig.BotToken, telegramConfig.ChannelID, finalContent); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	}
}
