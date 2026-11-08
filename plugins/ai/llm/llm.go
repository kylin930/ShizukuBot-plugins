package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const maxLLMResponseBytes = 8 << 20

var llmClient = &http.Client{Timeout: 120 * time.Second}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature,omitempty"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Stream      bool          `json:"stream"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
		} `json:"message"`
	} `json:"choices"`
	// Ollama /api/chat 风格
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// chatCompletion 调用 OpenAI 兼容的 chat/completions 接口。
func chatCompletion(ctx context.Context, cfg config, messages []chatMessage) (string, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return "", fmt.Errorf("未配置 API 密钥")
	}
	if strings.TrimSpace(cfg.APIURL) == "" {
		return "", fmt.Errorf("未配置接口地址")
	}
	body := chatRequest{
		Model:       cfg.Model,
		Messages:    messages,
		Temperature: cfg.Temperature,
		MaxTokens:   cfg.MaxTokens,
		Stream:      false,
	}
	data, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.APIURL, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(cfg.APIKey))
	resp, err := llmClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxLLMResponseBytes))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}
	var result chatResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", err
	}
	if result.Error != nil && result.Error.Message != "" {
		return "", fmt.Errorf("%s", result.Error.Message)
	}
	if len(result.Choices) > 0 {
		content := strings.TrimSpace(result.Choices[0].Message.Content)
		if content != "" {
			return content, nil
		}
		if reasoning := strings.TrimSpace(result.Choices[0].Message.ReasoningContent); reasoning != "" {
			return reasoning, nil
		}
	}
	if content := strings.TrimSpace(result.Message.Content); content != "" {
		return content, nil
	}
	return "", fmt.Errorf("模型未返回内容")
}

func truncate(text string, limit int) string {
	runes := []rune(text)
	if limit > 0 && len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return text
}
