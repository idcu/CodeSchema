package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	httputil "gitee.com/idcu-go/httputil"
	log "gitee.com/idcu-go/log"
	retry "gitee.com/idcu-go/retry"
	"github.com/idcu/codeschema/internal/errors"
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
		client:  &http.Client{Timeout: cfg.Timeout},
		logger:  log.WithModule("ai.http"),
	}
}

// openAICompatClient 实现 LLMClient 接口（OpenAI Chat Completions 协议）。
type openAICompatClient struct {
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
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
//
// 对可重试错误（网络超时 / 429 限流等，由 retry.RetryableError 启发式判定）做
// 指数退避重试，提高 LLM 真实可用性（P21 生产化：并发增强下 429 限流常见，
// 退避重试可显著提升成功率）。不可重试错误（参数 / 权限 / 不存在等）立即返回。
func (c *openAICompatClient) chat(ctx context.Context, prompt string) (string, error) {
	var lastErr error
	var content string
	err := retry.Do(ctx, func() error {
		data, statusCode, err := httputil.DoRaw(ctx, http.MethodPost, c.baseURL+"/chat/completions", chatRequest{
			Model:     c.model,
			Messages:  []chatMessage{{Role: "user", Content: prompt}},
			MaxTokens: 512,
		}, httputil.WithClient(c.client), httputil.WithToken(c.apiKey))
		if err != nil {
			lastErr = fmt.Errorf("%w: %v", errors.ErrLLMUnavailable, err)
			return lastErr
		}
		if statusCode != http.StatusOK {
			var errResp chatResponse
			_ = json.Unmarshal(data, &errResp)
			msg := "non-200 status"
			if errResp.Error != nil && errResp.Error.Message != "" {
				msg = errResp.Error.Message
			}
			lastErr = fmt.Errorf("%w: status %d: %s", errors.ErrLLMUnavailable, statusCode, msg)
			if statusCode == http.StatusTooManyRequests {
				c.logger.Warn("llm rate limited, will retry", "status", statusCode, "error", msg)
			} else {
				c.logger.Warn("llm call failed", "status", statusCode, "error", msg)
			}
			// 429 等可重试错误由 retry.Do 退避重试；其余立即返回。
			return lastErr
		}

		var parsed chatResponse
		if err := json.Unmarshal(data, &parsed); err != nil {
			lastErr = fmt.Errorf("%w: parse response: %v", errors.ErrEnhanceFailed, err)
			return lastErr
		}
		if len(parsed.Choices) == 0 {
			lastErr = fmt.Errorf("%w: empty choices", errors.ErrEnhanceFailed)
			return lastErr
		}
		content = parsed.Choices[0].Message.Content
		return nil
	},
		retry.WithMaxAttempts(c.retryAttempts()),
		retry.WithExponentialBackoff(200*time.Millisecond, 3*time.Second),
		retry.WithRetryIf(retry.RetryableError),
	)
	if err != nil {
		return "", err
	}
	return content, nil
}

// retryAttempts 返回 LLM 调用的最大尝试次数（含首次）。
// 高并发增强下 429 限流常见，退避重试 3 次（默认）提升成功率；过多反而加剧限流。
func (c *openAICompatClient) retryAttempts() int {
	return 3
}
