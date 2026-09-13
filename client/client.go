// Package client executes operation contracts against local services or remote
// Gantry HTTP endpoints without duplicating product business logic.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	coreauth "github.com/gantry-tools/gantry-core/auth"
	"github.com/gantry-tools/gantry-core/operation"
	"github.com/gantry-tools/gantry-core/protocol"
)

type TokenSource interface {
	Token(context.Context) (string, error)
}

type TokenSourceFunc func(context.Context) (string, error)

func (f TokenSourceFunc) Token(ctx context.Context) (string, error) { return f(ctx) }

type FileTokenSource string

func (path FileTokenSource) Token(context.Context) (string, error) {
	info, err := os.Stat(string(path))
	if err != nil {
		return "", err
	}
	if info.Mode().Perm()&0077 != 0 {
		return "", errors.New("token file must not be accessible by group or other users")
	}
	body, err := os.ReadFile(string(path))
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(body))
	if token == "" {
		return "", errors.New("token file is empty")
	}
	return token, nil
}

type Options struct {
	BaseURL      string
	HTTPClient   *http.Client
	TokenSource  TokenSource
	MaxBodyBytes int64
	MaxAttempts  int
}

type Client struct {
	base        *url.URL
	http        *http.Client
	tokens      TokenSource
	maximum     int64
	maxAttempts int
}

func New(options Options) (*Client, error) {
	base, err := url.Parse(strings.TrimRight(options.BaseURL, "/"))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return nil, errors.New("base URL must be an http(s) origin")
	}
	if base.Path != "" {
		return nil, errors.New("base URL must not contain a path")
	}
	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	maximum := options.MaxBodyBytes
	if maximum <= 0 {
		maximum = protocol.DefaultMaxBody
	}
	attempts := options.MaxAttempts
	if attempts < 1 {
		attempts = 1
	}
	return &Client{base: base, http: httpClient, tokens: options.TokenSource, maximum: maximum, maxAttempts: attempts}, nil
}

type Request struct {
	PathValues     map[string]string
	Query          url.Values
	Input          any
	RequestID      string
	IdempotencyKey string
}

func (c *Client) Call(ctx context.Context, contract operation.Contract, request Request, output any) error {
	if err := contract.Validate(); err != nil {
		return err
	}
	endpoint, err := c.endpoint(contract.Route.Path, request.PathValues, request.Query)
	if err != nil {
		return err
	}
	var body []byte
	if request.Input != nil {
		body, err = json.Marshal(request.Input)
		if err != nil {
			return err
		}
	}
	attempts := 1
	if retrySafe(contract, request.IdempotencyKey) {
		attempts = c.maxAttempts
	}
	for attempt := 1; attempt <= attempts; attempt++ {
		err = c.callOnce(ctx, contract.Route.Method, endpoint, body, request, output)
		if err == nil || attempt == attempts || !retryable(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(attempt) * 25 * time.Millisecond):
		}
	}
	return err
}

func (c *Client) callOnce(ctx context.Context, method, endpoint string, body []byte, call Request, output any) error {
	request, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	if len(body) > 0 {
		request.Header.Set("Content-Type", "application/json")
	}
	if call.RequestID != "" {
		request.Header.Set("X-Request-ID", call.RequestID)
	}
	if call.IdempotencyKey != "" {
		request.Header.Set("Idempotency-Key", call.IdempotencyKey)
	}
	if c.tokens != nil {
		token, tokenErr := c.tokens.Token(ctx)
		if tokenErr != nil {
			return tokenErr
		}
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return &transportError{cause: err}
	}
	defer response.Body.Close()
	limited := io.LimitReader(response.Body, c.maximum+1)
	responseBody, err := io.ReadAll(limited)
	if err != nil {
		return &transportError{cause: err}
	}
	if int64(len(responseBody)) > c.maximum {
		return &protocol.Error{Code: protocol.Unavailable, Message: "response exceeds the size limit"}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		apiErr := &protocol.Error{Code: codeForStatus(response.StatusCode), Message: strings.TrimSpace(string(responseBody)), RequestID: response.Header.Get("X-Request-ID")}
		_ = json.Unmarshal(responseBody, apiErr)
		if apiErr.Message == "" {
			apiErr.Message = http.StatusText(response.StatusCode)
		}
		return apiErr
	}
	if output == nil || response.StatusCode == http.StatusNoContent || len(responseBody) == 0 {
		return nil
	}
	if err := json.Unmarshal(responseBody, output); err != nil {
		return &protocol.Error{Code: protocol.Unavailable, Message: "invalid JSON response"}
	}
	return nil
}

func (c *Client) endpoint(pattern string, values map[string]string, query url.Values) (string, error) {
	pathValue := pattern
	for name, value := range values {
		marker := "{" + name + "}"
		if !strings.Contains(pathValue, marker) {
			return "", fmt.Errorf("unknown path value %q", name)
		}
		pathValue = strings.ReplaceAll(pathValue, marker, url.PathEscape(value))
	}
	if strings.Contains(pathValue, "{") {
		return "", errors.New("not all route path values were supplied")
	}
	result := *c.base
	result.Path = pathValue
	result.RawQuery = query.Encode()
	return result.String(), nil
}

func retrySafe(contract operation.Contract, key string) bool {
	if contract.Kind == operation.Read {
		return true
	}
	return contract.Idempotency.Supported && key != "" && protocol.ValidKey(key)
}

type transportError struct{ cause error }

func (e *transportError) Error() string { return e.cause.Error() }

func retryable(err error) bool {
	var transport *transportError
	if errors.As(err, &transport) {
		return true
	}
	var apiErr *protocol.Error
	return errors.As(err, &apiErr) && apiErr.Code == protocol.Unavailable
}

func codeForStatus(status int) protocol.ErrorCode {
	switch status {
	case 400, 405, 413, 415, 422:
		return protocol.Invalid
	case 401:
		return protocol.Unauthorized
	case 403:
		return protocol.Forbidden
	case 404:
		return protocol.NotFound
	case 409:
		return protocol.Conflict
	case 502, 503, 504:
		return protocol.Unavailable
	default:
		return protocol.Internal
	}
}

type Handler func(context.Context, coreauth.Execution, json.RawMessage) (any, error)

type LocalExecutor struct {
	mu       sync.RWMutex
	handlers map[string]Handler
}

func NewLocalExecutor() *LocalExecutor {
	return &LocalExecutor{handlers: map[string]Handler{}}
}

func (e *LocalExecutor) Register(contract operation.Contract, handler Handler) error {
	if err := contract.Validate(); err != nil {
		return err
	}
	if handler == nil {
		return errors.New("operation handler is required")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, exists := e.handlers[contract.ID]; exists {
		return fmt.Errorf("operation %q is already registered", contract.ID)
	}
	e.handlers[contract.ID] = handler
	return nil
}

func (e *LocalExecutor) Call(ctx context.Context, contract operation.Contract, execution coreauth.Execution, input json.RawMessage) (any, error) {
	if err := execution.Validate(); err != nil {
		return nil, err
	}
	if err := coreauth.Authorize(contract, &execution.Actor); err != nil {
		return nil, err
	}
	e.mu.RLock()
	handler := e.handlers[contract.ID]
	e.mu.RUnlock()
	if handler == nil {
		return nil, &protocol.Error{Code: protocol.NotFound, Message: "operation is not registered"}
	}
	return handler(ctx, execution, input)
}
