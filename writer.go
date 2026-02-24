package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	openRouterURL = "https://openrouter.ai/api/v1/chat/completions"
	openAIURL     = "https://api.openai.com/v1/chat/completions"
)

type openRouterReq struct {
	Model    string          `json:"model"`
	Messages []openRouterMsg `json:"messages"`
	Stream   bool            `json:"stream,omitempty"`
}

type openRouterMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openRouterChoice struct {
	Message openRouterMsg `json:"message"`
	Delta   *struct {
		Content string `json:"content"`
	} `json:"delta,omitempty"`
}

type streamChunk struct {
	Choices []openRouterChoice `json:"choices"`
}

// Writer reads prompt from settingPath, calls OpenAI or OpenRouter (streaming), writes result to outputPath if set.
func Writer(config *Config, settingPath, outputPath string) error {
	useOpenAI := config.OpenAI.APIKey != "" && config.OpenAI.Model != ""
	useOpenRouter := config.OpenRouter.APIKey != "" && config.OpenRouter.Model != ""
	if !useOpenAI && !useOpenRouter {
		return fmt.Errorf("for -W/--writer, set either [openai] api_key and model, or [openrouter] api_key and model in config")
	}
	prompt, err := readFileContent(settingPath)
	if err != nil {
		return fmt.Errorf("failed to read setting file: %w", err)
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return fmt.Errorf("setting file is empty")
	}
	var apiURL, apiKey, apiName string
	if useOpenAI {
		apiURL = openAIURL
		apiKey = config.OpenAI.APIKey
		apiName = "OpenAI"
	} else {
		apiURL = openRouterURL
		apiKey = config.OpenRouter.APIKey
		apiName = "OpenRouter"
	}
	model := config.OpenAI.Model
	if !useOpenAI {
		model = config.OpenRouter.Model
	}
	reqBody := openRouterReq{
		Model:    model,
		Messages: []openRouterMsg{{Role: "user", Content: prompt}},
		Stream:   true,
	}
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}
	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to call %s: %w", apiName, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s API error: %d %s", apiName, resp.StatusCode, string(body))
	}
	var contentBuilder strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, ": ") {
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data: "))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var chunk streamChunk
		if json.Unmarshal([]byte(payload), &chunk) != nil {
			continue
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
		return fmt.Errorf("API returned empty content")
	}
	if outputPath != "" {
		if err := os.WriteFile(outputPath, []byte(content), 0644); err != nil {
			return fmt.Errorf("failed to write output file: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Wrote result to %s\n", outputPath)
	}
	fmt.Fprintln(os.Stderr, "Successfully generated content")
	return nil
}
