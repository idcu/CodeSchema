package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/idcu/codeschema/internal/errors"
	"github.com/idcu/codeschema/internal/log"
)

// HTTPClientConfig OpenAI 兼容 Chat Completions 客户端的配置。
type HTTPClientConfig struct {
	// BaseURL API 根地址（如 https://api.openai.com/v1）。空则禁用。
	BaseURL string
	// APIKey 鉴权密钥。空则禁用（返回 nil client）。
	APIKey string
	// Model 模型名（如 gpt-4o-mini）。
	Model string
	// Timeout 单次调用超时，默认 30s。
	Timeout time.Duration
}

// NewOpenAICompatClient 创建 OpenAI 兼容的 Chat Completions LLMClient。
//
// 配置不完整（缺 BaseURL/APIKey/Model 任一）时返回 nil（不视为错误），
// 调用方据此跳过 AI 增强——保证「未配置 LLM 时主流程零影响」。
func NewOpenAICompatClient(cfg HTTPClientConfig) LLMClient {
	if cfg.BaseURL == "" || cfg.APIKey == "" || cfg.Model == "" {
		return nil
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	return &openAICompatClient{
		baseURL: strings.TrimSuffix(cfg.BaseURL, "/"),
		apiKey:  cfg.APIKey,
		model:   cfg.Model,
		timeout: cfg.Timeout,
		http:    &http.Client{Timeout: cfg.Timeout},
		logger:  log.WithModule("ai.http"),
	}
}

// openAICompatClient 实现 LLMClient 接口（OpenAI Chat Completions 协议）。
type openAICompatClient struct {
	baseURL string
	apiKey  string
	model   string
	timeout time.Duration
	http    *http.Client
	logger  *log.Logger
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Temperature float64       `json:"temperature,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Complete 发送补全提示词，按行切分返回补全结果。
func (c *openAICompatClient) Complete(ctx context.Context, prompt string) ([]string, error) {
	content, err := c.chat(ctx, prompt)
	if err != nil {
		return nil, err
	}
	lines := make([]string, 0, 4)
	for _, ln := range strings.Split(content, "\n") {
		ln = strings.TrimSpace(ln)
		ln = strings.TrimPrefix(ln, "- ")
		ln = strings.TrimPrefix(ln, "* ")
		if ln != "" && !strings.HasPrefix(ln, "```") {
			lines = append(lines, ln)
		}
	}
	return lines, nil
}

// Choose 发送选择型提示词，解析返回的索引（从 0 开始）。
func (c *openAICompatClient) Choose(ctx context.Context, prompt string) (int, error) {
	content, err := c.chat(ctx, prompt)
	if err != nil {
		return 0, err
	}
	// 容忍模型返回 "[3]" / "索引: 3" / "3。" / "（1）" 等格式：先剥离常见装饰，再取首个数字
	content = strings.TrimSpace(content)
	content = strings.Trim(content, "[]()（）。.")
	if idx := strings.LastIndex(content, ":"); idx >= 0 {
		content = strings.TrimSpace(content[idx+1:])
	}
	// 提取字符串中第一个连续数字（如 "3" / "第3个"）
	digitStart, digitEnd := -1, -1
	for i, r := range content {
		if r >= '0' && r <= '9' {
			if digitStart < 0 {
				digitStart = i
			}
			digitEnd = i + 1
		} else if digitStart >= 0 {
			break
		}
	}
	if digitStart < 0 {
		return 0, fmt.Errorf("%w: parse choice index from %q", errors.ErrEnhanceFailed, content)
	}
	n, err := strconv.Atoi(content[digitStart:digitEnd])
	if err != nil {
		return 0, fmt.Errorf("%w: parse choice index from %q", errors.ErrEnhanceFailed, content)
	}
	return n, nil
}

// chat 执行一次 Chat Completions 调用。
func (c *openAICompatClient) chat(ctx context.Context, prompt string) (string, error) {
	body, err := json.Marshal(chatRequest{
		Model:     c.model,
		Messages:  []chatMessage{{Role: "user", Content: prompt}},
		MaxTokens: 512,
	})
	if err != nil {
		return "", fmt.Errorf("%w: marshal request: %v", errors.ErrEnhanceFailed, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("%w: new request: %v", errors.ErrLLMUnavailable, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: %v", errors.ErrLLMUnavailable, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("%w: read response: %v", errors.ErrEnhanceFailed, err)
	}
	if resp.StatusCode != http.StatusOK {
		var errResp chatResponse
		_ = json.Unmarshal(data, &errResp)
		msg := "non-200 status"
		if errResp.Error != nil && errResp.Error.Message != "" {
			msg = errResp.Error.Message
		}
		c.logger.Warn("llm call failed", "status", resp.StatusCode, "error", msg)
		return "", fmt.Errorf("%w: status %d: %s", errors.ErrLLMUnavailable, resp.StatusCode, msg)
	}

	var parsed chatResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		return "", fmt.Errorf("%w: parse response: %v", errors.ErrEnhanceFailed, err)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("%w: empty choices", errors.ErrEnhanceFailed)
	}
	return parsed.Choices[0].Message.Content, nil
}
