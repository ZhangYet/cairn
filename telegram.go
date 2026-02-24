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
	"strconv"
	"strings"
	"time"
)

// TelegramResponse is the common response from Telegram Bot API.
type TelegramResponse struct {
	OK          bool   `json:"ok"`
	Description string `json:"description,omitempty"`
	Result      *struct {
		MessageID int64 `json:"message_id"`
	} `json:"result,omitempty"`
}

// telegramMediaGroupResponse is the response from sendMediaGroup (result is array of Message).
type telegramMediaGroupResponse struct {
	OK          bool   `json:"ok"`
	Description string `json:"description,omitempty"`
	Result      []struct {
		MessageID int64 `json:"message_id"`
	} `json:"result,omitempty"`
}

func ensureCairnTag(content string) string {
	contentLower := strings.ToLower(content)
	if strings.Contains(contentLower, "#cairn") {
		return content
	}
	content = strings.TrimRight(content, " \n\t")
	if content != "" {
		return content + " #cairn"
	}
	return "#cairn"
}

func httpPost(url string, jsonData []byte) (*http.Response, error) {
	client := &http.Client{Timeout: 10 * time.Second}
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
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("HTTP error: %d: %s", resp.StatusCode, string(body))
	}
	return resp, nil
}

func postToTelegram(botToken, channelID, content string) (messageID int64, err error) {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", botToken)
	content = ensureCairnTag(content)
	payload := map[string]interface{}{
		"chat_id":    channelID,
		"text":       content,
		"parse_mode": "HTML",
	}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("failed to marshal payload: %w", err)
	}
	resp, err := httpPost(url, jsonData)
	if err != nil {
		return 0, fmt.Errorf("failed to post to Telegram: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("failed to read response: %w", err)
	}
	var telegramResp TelegramResponse
	if err := json.Unmarshal(body, &telegramResp); err != nil {
		return 0, fmt.Errorf("failed to parse response: %w", err)
	}
	if !telegramResp.OK {
		return 0, fmt.Errorf("telegram API error: %s", telegramResp.Description)
	}
	if telegramResp.Result != nil {
		messageID = telegramResp.Result.MessageID
		fmt.Fprintf(os.Stderr, "Successfully posted to Telegram channel (message_id: %d)\n", messageID)
	} else {
		fmt.Fprintln(os.Stderr, "Successfully posted to Telegram channel")
	}
	return messageID, nil
}

func editMessageTelegram(botToken, channelID string, messageID int64, content string) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/editMessageText", botToken)
	content = ensureCairnTag(content)
	payload := map[string]interface{}{
		"chat_id":    channelID,
		"message_id": messageID,
		"text":       content,
		"parse_mode": "HTML",
	}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}
	resp, err := httpPost(url, jsonData)
	if err != nil {
		return fmt.Errorf("failed to edit message: %w", err)
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
	fmt.Fprintln(os.Stderr, "Successfully updated message")
	return nil
}

func editMessageCaptionTelegram(botToken, channelID string, messageID int64, caption string) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/editMessageCaption", botToken)
	caption = ensureCairnTag(caption)
	payload := map[string]interface{}{
		"chat_id":    channelID,
		"message_id": messageID,
		"caption":    caption,
		"parse_mode": "HTML",
	}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}
	resp, err := httpPost(url, jsonData)
	if err != nil {
		return fmt.Errorf("failed to edit caption: %w", err)
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
	fmt.Fprintln(os.Stderr, "Successfully updated caption")
	return nil
}

func editMessageMediaTelegram(botToken, channelID string, messageID int64, photoPath, caption string) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/editMessageMedia", botToken)
	photoFile, err := os.Open(photoPath)
	if err != nil {
		return fmt.Errorf("failed to open photo file: %w", err)
	}
	defer photoFile.Close()
	caption = ensureCairnTag(caption)
	if caption == "" {
		caption = "#cairn"
	}
	media := map[string]string{
		"type":       "photo",
		"media":      "attach://photo0",
		"caption":    caption,
		"parse_mode": "HTML",
	}
	mediaJSON, err := json.Marshal(media)
	if err != nil {
		return fmt.Errorf("failed to marshal media: %w", err)
	}
	var requestBody bytes.Buffer
	writer := multipart.NewWriter(&requestBody)
	for _, field := range []struct{ name, value string }{
		{"chat_id", channelID},
		{"message_id", strconv.FormatInt(messageID, 10)},
		{"media", string(mediaJSON)},
	} {
		f, _ := writer.CreateFormField(field.name)
		f.Write([]byte(field.value))
	}
	photoField, err := writer.CreateFormFile("photo0", filepath.Base(photoPath))
	if err != nil {
		return fmt.Errorf("failed to create form file: %w", err)
	}
	if _, err := io.Copy(photoField, photoFile); err != nil {
		return fmt.Errorf("failed to copy photo: %w", err)
	}
	writer.Close()
	req, err := http.NewRequest("POST", url, &requestBody)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to edit media: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP error: %d: %s", resp.StatusCode, string(body))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	var telegramResp TelegramResponse
	if err := json.Unmarshal(body, &telegramResp); err != nil {
		return err
	}
	if !telegramResp.OK {
		return fmt.Errorf("telegram API error: %s", telegramResp.Description)
	}
	fmt.Fprintln(os.Stderr, "Successfully replaced photo")
	return nil
}

func postPhotoToTelegram(botToken, channelID, photoPath, caption string) (messageID int64, err error) {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendPhoto", botToken)
	photoFile, err := os.Open(photoPath)
	if err != nil {
		return 0, fmt.Errorf("failed to open photo file: %w", err)
	}
	defer photoFile.Close()
	var requestBody bytes.Buffer
	writer := multipart.NewWriter(&requestBody)
	chatIDField, _ := writer.CreateFormField("chat_id")
	chatIDField.Write([]byte(channelID))
	if caption != "" {
		caption = ensureCairnTag(caption)
	} else {
		caption = "#cairn"
	}
	captionField, _ := writer.CreateFormField("caption")
	captionField.Write([]byte(caption))
	parseModeField, _ := writer.CreateFormField("parse_mode")
	parseModeField.Write([]byte("HTML"))
	photoField, err := writer.CreateFormFile("photo", filepath.Base(photoPath))
	if err != nil {
		return 0, fmt.Errorf("failed to create form file: %w", err)
	}
	io.Copy(photoField, photoFile)
	writer.Close()
	req, err := http.NewRequest("POST", url, &requestBody)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("failed to post to Telegram: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("HTTP error: %d: %s", resp.StatusCode, string(body))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}
	var telegramResp TelegramResponse
	if err := json.Unmarshal(body, &telegramResp); err != nil {
		return 0, err
	}
	if !telegramResp.OK {
		return 0, fmt.Errorf("telegram API error: %s", telegramResp.Description)
	}
	if telegramResp.Result != nil {
		messageID = telegramResp.Result.MessageID
		fmt.Fprintf(os.Stderr, "Successfully posted photo to Telegram channel (message_id: %d)\n", messageID)
	} else {
		fmt.Fprintln(os.Stderr, "Successfully posted photo to Telegram channel")
	}
	return messageID, nil
}

func postMultiplePhotosToTelegram(botToken, channelID string, photoPaths []string, caption string) (firstMessageID int64, err error) {
	if len(photoPaths) > 10 {
		return 0, fmt.Errorf("maximum 10 photos allowed, got %d", len(photoPaths))
	}
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMediaGroup", botToken)
	if caption != "" {
		caption = ensureCairnTag(caption)
	} else {
		caption = "#cairn"
	}
	var requestBody bytes.Buffer
	writer := multipart.NewWriter(&requestBody)
	chatIDField, _ := writer.CreateFormField("chat_id")
	chatIDField.Write([]byte(channelID))
	media := make([]map[string]interface{}, len(photoPaths))
	for i := range photoPaths {
		filename := fmt.Sprintf("photo%d", i)
		mediaItem := map[string]interface{}{
			"type":  "photo",
			"media": fmt.Sprintf("attach://%s", filename),
		}
		if i == 0 {
			mediaItem["caption"] = caption
		}
		media[i] = mediaItem
	}
	mediaJSON, _ := json.Marshal(media)
	mediaField, _ := writer.CreateFormField("media")
	mediaField.Write(mediaJSON)
	parseModeField, _ := writer.CreateFormField("parse_mode")
	parseModeField.Write([]byte("HTML"))
	for i, photoPath := range photoPaths {
		photoFile, err := os.Open(photoPath)
		if err != nil {
			return 0, fmt.Errorf("failed to open photo file %s: %w", photoPath, err)
		}
		photoField, err := writer.CreateFormFile(fmt.Sprintf("photo%d", i), filepath.Base(photoPath))
		if err != nil {
			photoFile.Close()
			return 0, err
		}
		io.Copy(photoField, photoFile)
		photoFile.Close()
	}
	writer.Close()
	req, err := http.NewRequest("POST", url, &requestBody)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("HTTP error: %d: %s", resp.StatusCode, string(body))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}
	var mediaResp telegramMediaGroupResponse
	if err := json.Unmarshal(body, &mediaResp); err != nil {
		return 0, err
	}
	if !mediaResp.OK {
		return 0, fmt.Errorf("telegram API error: %s", mediaResp.Description)
	}
	if len(mediaResp.Result) > 0 {
		firstMessageID = mediaResp.Result[0].MessageID
		ids := make([]string, len(mediaResp.Result))
		for i, m := range mediaResp.Result {
			ids[i] = strconv.FormatInt(m.MessageID, 10)
		}
		fmt.Fprintf(os.Stderr, "Successfully posted %d photo(s) to Telegram channel (message_ids: %s)\n", len(photoPaths), strings.Join(ids, ", "))
	} else {
		fmt.Fprintf(os.Stderr, "Successfully posted %d photo(s) to Telegram channel\n", len(photoPaths))
	}
	return firstMessageID, nil
}
