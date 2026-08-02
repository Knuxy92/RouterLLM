package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"routerllm/internal/config"
)

var ErrAutoModelDisabled = errors.New("Model is disabled")

func resolveAutoModel(ctx context.Context, client *http.Client, cfg config.AutoModelConfig, body map[string]any) (string, error) {
	result, err := classifyAutoModel(ctx, client, cfg, body)
	if err != nil {
		return "", err
	}

	return result.model, nil
}

type autoModelResult struct {
	model      string
	category   string
	rawContent string
}

func classifyAutoModel(ctx context.Context, client *http.Client, cfg config.AutoModelConfig, body map[string]any) (autoModelResult, error) {
	if !cfg.Enabled {
		return autoModelResult{}, ErrAutoModelDisabled
	}

	promptInput := body["messages"]
	if promptInput == nil {
		promptInput = body["input"]
	}
	messages, _ := json.Marshal(promptInput)

	var invalidContent string
	for attempt := 0; attempt < 2; attempt++ {
		prompt := cfg.Prompt
		if attempt > 0 {
			prompt += fmt.Sprintf("\n\nYour previous output %q was invalid. Return exactly one valid category and nothing else.", invalidContent)
		}

		rawContent, err := requestAutoModel(ctx, client, cfg, prompt, string(messages))
		if err != nil {
			return autoModelResult{}, err
		}
		if result, ok := parseAutoModelResult(cfg, rawContent); ok {
			return result, nil
		}
		invalidContent = rawContent
	}

	return autoModelResult{}, fmt.Errorf("auto_model returned invalid category %q", invalidContent)
}

func requestAutoModel(ctx context.Context, client *http.Client, cfg config.AutoModelConfig, prompt, messages string) (string, error) {
	requestBody := map[string]any{
		"model": cfg.Model,
		"messages": []map[string]string{
			{"role": "system", "content": prompt},
			{"role": "user", "content": messages},
		},
		"temperature":      0,
		"reasoning_effort": "low",
		"enable_thinking":  false,
		"stream":           false,
	}
	raw, err := json.Marshal(requestBody)
	if err != nil {
		return "", fmt.Errorf("failed to encode auto_model request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.BaseURL+"/v1/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return "", fmt.Errorf("failed to build auto_model request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("auto_model request failed: %w", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read auto_model response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("auto_model returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}

	var response struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return "", fmt.Errorf("invalid auto_model response: %w", err)
	}
	if len(response.Choices) == 0 {
		return "", errors.New("auto_model returned no choices")
	}

	return response.Choices[0].Message.Content, nil
}

func parseAutoModelResult(cfg config.AutoModelConfig, rawContent string) (autoModelResult, bool) {
	switch strings.TrimSpace(rawContent) {
	case "Small Model":
		return autoModelResult{model: cfg.SmallModel, category: "Small Model", rawContent: rawContent}, true
	case "Analysis Model":
		return autoModelResult{model: cfg.AnalysisModel, category: "Analysis Model", rawContent: rawContent}, true
	case "Coding Model":
		return autoModelResult{model: cfg.CodingModel, category: "Coding Model", rawContent: rawContent}, true
	default:
		return autoModelResult{}, false
	}
}
