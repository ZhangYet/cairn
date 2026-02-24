package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

// Config holds all configuration from .cairn.toml.
type Config struct {
	Telegram   TelegramConfig   `toml:"telegram"`
	Fitbit     FitbitConfig     `toml:"fitbit"`
	OpenRouter OpenRouterConfig `toml:"openrouter"`
	OpenAI     OpenAIConfig     `toml:"openai"`
}

// TelegramConfig is the [telegram] section.
type TelegramConfig struct {
	BotToken  string `toml:"bot_token"`
	ChannelID string `toml:"channel_id"`
}

// FitbitConfig is the [fitbit] section.
type FitbitConfig struct {
	ClientID     string `toml:"client_id"`
	ClientSecret string `toml:"client_secret"`
}

// OpenRouterConfig is the [openrouter] section.
type OpenRouterConfig struct {
	APIKey string `toml:"api_key"`
	Model  string `toml:"model"`
}

// OpenAIConfig is the [openai] section.
type OpenAIConfig struct {
	APIKey string `toml:"api_key"`
	Model  string `toml:"model"`
}

func loadConfig(configPath string) (*Config, error) {
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
			return nil, fmt.Errorf("config file not found at %s\nPlease create a config file with the following structure:\n[telegram]\nbot_token = \"your_bot_token\"\nchannel_id = \"@your_channel\"\n\n[fitbit]\nclient_id = \"your_client_id\"\nclient_secret = \"your_client_secret\"\n\n[openrouter]\napi_key = \"your_openrouter_api_key\"\nmodel = \"openrouter/model-id\"\n\n[openai]\napi_key = \"your_openai_api_key\"\nmodel = \"gpt-4o-mini\"", expandedPath)
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

func readFileContent(filePath string) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to read file: %w", err)
	}
	return string(data), nil
}
