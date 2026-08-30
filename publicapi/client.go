package publicapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	maxDecodedBodyBytes          = 4 << 20
	maxDiagnosticBodyBytes       = 64 << 10
	maxSSEEventBytes             = 4 << 20
	defaultResponseTimeout       = 30 * time.Second
	defaultSSEIdleTimeout        = 60 * time.Second
	defaultSSEMaxRetries         = 5
	defaultSSEReconnectBaseDelay = 200 * time.Millisecond
	maxSSERetries                = 100
)

type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

type HTTPClientFunc func(*http.Request) (*http.Response, error)

func (f HTTPClientFunc) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

// RequestEditorFn mutates a request immediately before transport execution.
// The context is the operation context, including its response timeout.
type RequestEditorFn func(context.Context, *http.Request) error

type Option func(*ClientOptions)

type ClientOptions struct {
	BaseURL                 string
	httpClient              HTTPClient
	requestEditors          []RequestEditorFn
	responseTimeout         time.Duration
	sseIdleTimeout          time.Duration
	sseMaxRetries           int
	sseReconnectBaseDelay   time.Duration
	sseReconnectOnStreamEnd bool
}

// WithHTTPClient replaces the default http.DefaultClient without mutating a
// caller-owned http.Client.
func WithHTTPClient(client HTTPClient) Option {
	return func(options *ClientOptions) {
		options.httpClient = client
	}
}

// WithRequestEditorFn appends a client-level request editor. Editors run in
// registration order and must be safe for concurrent use when Client is shared.
func WithRequestEditorFn(editor RequestEditorFn) Option {
	return func(options *ClientOptions) {
		options.requestEditors = append(options.requestEditors, editor)
	}
}

// WithResponseTimeout bounds response headers and, for ordinary responses,
// reading and decoding the response body. For SSE it only bounds connection
// establishment. Zero disables the timeout.
func WithResponseTimeout(timeout time.Duration) Option {
	return func(options *ClientOptions) {
		options.responseTimeout = timeout
	}
}

// WithSSEIdleTimeout bounds silence between SSE chunks. Every received chunk,
// including a heartbeat comment, resets it. Zero disables the timeout.
func WithSSEIdleTimeout(timeout time.Duration) Option {
	return func(options *ClientOptions) {
		options.sseIdleTimeout = timeout
	}
}

// WithSSEMaxRetries bounds consecutive reconnect failures for generated GET
// event streams. Zero disables reconnect attempts; the hard cap is 100.
func WithSSEMaxRetries(retries int) Option {
	return func(options *ClientOptions) {
		options.sseMaxRetries = retries
	}
}

// WithSSEReconnectBaseDelay configures the first reconnect backoff.
func WithSSEReconnectBaseDelay(delay time.Duration) Option {
	return func(options *ClientOptions) {
		options.sseReconnectBaseDelay = delay
	}
}

// WithSSEReconnectOnStreamEnd controls whether a clean EOF reconnects a
// generated event stream. It defaults to true for compatibility with streams
// that use EOF as a transient disconnect; terminal-aware proxies can disable it.
func WithSSEReconnectOnStreamEnd(reconnect bool) Option {
	return func(options *ClientOptions) {
		options.sseReconnectOnStreamEnd = reconnect
	}
}

type Client struct {
	baseURL                 string
	httpClient              HTTPClient
	requestEditors          []RequestEditorFn
	responseTimeout         time.Duration
	sseIdleTimeout          time.Duration
	sseMaxRetries           int
	sseReconnectBaseDelay   time.Duration
	sseReconnectOnStreamEnd bool
}

func NewClient(config ClientOptions, options ...Option) (*Client, error) {
	configuredBaseURL := strings.TrimSpace(config.BaseURL)
	if configuredBaseURL == "" {
		return nil, fmt.Errorf("base URL must not be empty: %q", config.BaseURL)
	}
	parsed, err := url.Parse(configuredBaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse base URL %q: %w", config.BaseURL, err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("base URL %q must include scheme and host", config.BaseURL)
	}
	config.httpClient = http.DefaultClient
	config.responseTimeout = defaultResponseTimeout
	config.sseIdleTimeout = defaultSSEIdleTimeout
	config.sseMaxRetries = defaultSSEMaxRetries
	config.sseReconnectBaseDelay = defaultSSEReconnectBaseDelay
	config.sseReconnectOnStreamEnd = true
	for _, option := range options {
		if option == nil {
			return nil, fmt.Errorf("client option must not be nil")
		}
		option(&config)
	}
	if config.httpClient == nil {
		return nil, fmt.Errorf("HTTP client must not be nil")
	}
	if config.responseTimeout < 0 {
		return nil, fmt.Errorf("response timeout must not be negative")
	}
	if config.sseIdleTimeout < 0 {
		return nil, fmt.Errorf("SSE idle timeout must not be negative")
	}
	if config.sseMaxRetries < 0 || config.sseMaxRetries > maxSSERetries {
		return nil, fmt.Errorf("SSE max retries must be between 0 and %d", maxSSERetries)
	}
	if config.sseReconnectBaseDelay < 0 {
		return nil, fmt.Errorf("SSE reconnect base delay must not be negative")
	}
	for index, editor := range config.requestEditors {
		if editor == nil {
			return nil, fmt.Errorf("request editor at index %d must not be nil", index)
		}
	}
	return &Client{
		baseURL:                 strings.TrimRight(parsed.String(), "/"),
		httpClient:              config.httpClient,
		requestEditors:          append([]RequestEditorFn(nil), config.requestEditors...),
		responseTimeout:         config.responseTimeout,
		sseIdleTimeout:          config.sseIdleTimeout,
		sseMaxRetries:           config.sseMaxRetries,
		sseReconnectBaseDelay:   config.sseReconnectBaseDelay,
		sseReconnectOnStreamEnd: config.sseReconnectOnStreamEnd,
	}, nil
}

func (c *Client) do(ctx context.Context, request *http.Request) (*http.Response, error) {
	for index, editor := range c.requestEditors {
		if err := editor(ctx, request); err != nil {
			return nil, fmt.Errorf("request editor at index %d: %w", index, err)
		}
	}
	// Keep caller cancellation and response timeout authoritative even if an
	// editor replaces the request value or its context.
	request = request.Clone(ctx)
	return c.httpClient.Do(request)
}

type ResponseTimeoutError struct {
	Duration time.Duration
}

func (err *ResponseTimeoutError) Error() string {
	return fmt.Sprintf("response timed out after %s", err.Duration)
}

func (err *ResponseTimeoutError) Timeout() bool { return true }

type responseLifecycle struct {
	cancel    context.CancelCauseFunc
	timer     *time.Timer
	closeOnce sync.Once
}

func (c *Client) responseContext(parent context.Context) (context.Context, *responseLifecycle) {
	ctx, cancel := context.WithCancelCause(parent)
	lifecycle := &responseLifecycle{cancel: cancel}
	if c.responseTimeout > 0 {
		lifecycle.timer = time.AfterFunc(c.responseTimeout, func() {
			cancel(&ResponseTimeoutError{Duration: c.responseTimeout})
		})
	}
	return ctx, lifecycle
}

func (lifecycle *responseLifecycle) stopTimeout() {
	if lifecycle.timer != nil {
		lifecycle.timer.Stop()
	}
}

func (lifecycle *responseLifecycle) close() {
	lifecycle.closeOnce.Do(func() {
		lifecycle.stopTimeout()
		lifecycle.cancel(context.Canceled)
	})
}

type UnexpectedStatusError struct {
	Method     string
	URL        string
	StatusCode int
	Body       string
}

func (err *UnexpectedStatusError) Error() string {
	if err.Body == "" {
		return fmt.Sprintf("%s %s returned unexpected HTTP status %d", err.Method, err.URL, err.StatusCode)
	}
	return fmt.Sprintf("%s %s returned unexpected HTTP status %d: %s", err.Method, err.URL, err.StatusCode, err.Body)
}

type sseAttempt struct {
	response  *http.Response
	lifecycle *responseLifecycle
}

func retryableSSEStatus(status int) bool {
	return status == http.StatusBadGateway || status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout
}

func sseRetryDelay(base time.Duration, failure int, serverDelay *time.Duration) time.Duration {
	if serverDelay != nil {
		return *serverDelay
	}
	scale := 1.0
	for index := 1; index < failure && scale < 1<<20; index++ {
		scale *= 2
	}
	return time.Duration(float64(base) * scale * (0.9 + rand.Float64()*0.2))
}

func waitForSSERetry(ctx context.Context, delay time.Duration) error {
	if err := sseContextError(ctx); err != nil {
		return err
	}
	timer := time.NewTimer(maxDuration(delay, 0))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return sseContextError(ctx)
	case <-timer.C:
		return nil
	}
}

func sseContextError(ctx context.Context) error {
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	return ctx.Err()
}

func maxDuration(value time.Duration, minimum time.Duration) time.Duration {
	if value < minimum {
		return minimum
	}
	return value
}

func (c *Client) openReconnectingSSE(
	ctx context.Context,
	open func(context.Context) (*http.Response, *responseLifecycle, error),
) (*sseAttempt, error) {
	failures := 0
	for {
		if err := sseContextError(ctx); err != nil {
			return nil, err
		}
		response, lifecycle, err := open(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil, sseContextError(ctx)
			}
			if failures >= c.sseMaxRetries {
				return nil, err
			}
			failures++
			if err := waitForSSERetry(ctx, sseRetryDelay(c.sseReconnectBaseDelay, failures, nil)); err != nil {
				return nil, err
			}
			continue
		}
		attempt := &sseAttempt{response: response, lifecycle: lifecycle}
		if !retryableSSEStatus(response.StatusCode) {
			return attempt, nil
		}
		if failures >= c.sseMaxRetries {
			return attempt, nil
		}
		_ = response.Body.Close()
		lifecycle.close()
		failures++
		if err := waitForSSERetry(ctx, sseRetryDelay(c.sseReconnectBaseDelay, failures, nil)); err != nil {
			return nil, err
		}
	}
}

type reconnectingSSEBody struct {
	ctx                  context.Context
	open                 func(context.Context) (*http.Response, *responseLifecycle, error)
	maxRetries           int
	baseDelay            time.Duration
	idleTimeout          time.Duration
	reconnectOnStreamEnd bool

	mu               sync.Mutex
	current          io.ReadCloser
	currentLifecycle *responseLifecycle
	closed           bool
	terminalErr      error
	failures         int
	serverDelay      *time.Duration
	pending          []byte
	ready            [][]byte
}

func newReconnectingSSEBody(
	attempt *sseAttempt,
	ctx context.Context,
	open func(context.Context) (*http.Response, *responseLifecycle, error),
	maxRetries int,
	baseDelay time.Duration,
	idleTimeout time.Duration,
	reconnectOnStreamEnd bool,
) io.ReadCloser {
	return &reconnectingSSEBody{
		ctx:                  ctx,
		open:                 open,
		maxRetries:           maxRetries,
		baseDelay:            baseDelay,
		idleTimeout:          idleTimeout,
		reconnectOnStreamEnd: reconnectOnStreamEnd,
		current:              newSSEIdleBody(attempt.response.Body, idleTimeout),
		currentLifecycle:     attempt.lifecycle,
	}
}

func newSSEIdleBody(body io.ReadCloser, timeout time.Duration) io.ReadCloser {
	if body == nil {
		body = http.NoBody
	}
	return newIdleTimeoutBody(body, timeout, nil)
}

func (body *reconnectingSSEBody) Read(buffer []byte) (int, error) {
	if len(buffer) == 0 {
		return 0, nil
	}
	for {
		if read := body.popReady(buffer); read > 0 {
			return read, nil
		}
		body.mu.Lock()
		if body.closed {
			err := body.terminalErr
			body.mu.Unlock()
			if err == nil {
				return 0, io.EOF
			}
			return 0, err
		}
		current := body.current
		body.mu.Unlock()
		if err := sseContextError(body.ctx); err != nil {
			body.fail(err)
			return 0, err
		}
		if current == nil {
			err := fmt.Errorf("SSE response has no readable body")
			body.fail(err)
			return 0, err
		}
		chunk := make([]byte, 32<<10)
		read, err := current.Read(chunk)
		if read > 0 {
			body.push(chunk[:read])
			if ready := body.popReady(buffer); ready > 0 {
				return ready, nil
			}
		}
		if err == nil {
			continue
		}
		if err == io.EOF && !body.reconnectOnStreamEnd {
			_ = body.Close()
			return 0, io.EOF
		}
		if reconnectErr := body.reconnect(err); reconnectErr != nil {
			body.fail(reconnectErr)
			return 0, reconnectErr
		}
	}
}

func (body *reconnectingSSEBody) push(chunk []byte) {
	body.mu.Lock()
	defer body.mu.Unlock()
	body.pending = append(body.pending, chunk...)
	for {
		end := sseFrameEnd(body.pending)
		if end < 0 {
			if len(body.pending) > maxSSEEventBytes {
				body.terminalErr = fmt.Errorf("SSE event exceeds %d bytes", maxSSEEventBytes)
			}
			return
		}
		frame := append([]byte(nil), body.pending[:end]...)
		body.pending = append([]byte(nil), body.pending[end:]...)
		if retry, ok := sseRetryValue(frame); ok {
			body.serverDelay = &retry
		}
		body.failures = 0
		body.ready = append(body.ready, frame)
	}
}

func (body *reconnectingSSEBody) popReady(buffer []byte) int {
	body.mu.Lock()
	defer body.mu.Unlock()
	if len(body.ready) == 0 {
		return 0
	}
	frame := body.ready[0]
	read := copy(buffer, frame)
	if read == len(frame) {
		body.ready = body.ready[1:]
	} else {
		body.ready[0] = frame[read:]
	}
	return read
}

func (body *reconnectingSSEBody) reconnect(lastErr error) error {
	for {
		body.mu.Lock()
		if body.closed {
			err := body.terminalErr
			body.mu.Unlock()
			if err == nil {
				return io.ErrClosedPipe
			}
			return err
		}
		if body.failures >= body.maxRetries {
			body.mu.Unlock()
			return lastErr
		}
		body.failures++
		failure := body.failures
		serverDelay := body.serverDelay
		body.serverDelay = nil
		current := body.current
		lifecycle := body.currentLifecycle
		body.current = nil
		body.currentLifecycle = nil
		body.pending = nil
		body.mu.Unlock()
		if current != nil {
			_ = current.Close()
		}
		if lifecycle != nil {
			lifecycle.close()
		}
		if err := waitForSSERetry(body.ctx, sseRetryDelay(body.baseDelay, failure, serverDelay)); err != nil {
			return err
		}
		response, nextLifecycle, err := body.open(body.ctx)
		if err != nil {
			lastErr = err
			continue
		}
		if retryableSSEStatus(response.StatusCode) {
			_ = response.Body.Close()
			nextLifecycle.close()
			lastErr = fmt.Errorf("SSE reconnect returned HTTP %d", response.StatusCode)
			body.mu.Lock()
			if body.failures >= body.maxRetries {
				body.mu.Unlock()
				return lastErr
			}
			body.failures++
			failure = body.failures
			body.mu.Unlock()
			if err := waitForSSERetry(body.ctx, sseRetryDelay(body.baseDelay, failure, nil)); err != nil {
				return err
			}
			continue
		}
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			_ = response.Body.Close()
			nextLifecycle.close()
			return &UnexpectedStatusError{
				Method:     response.Request.Method,
				URL:        response.Request.URL.String(),
				StatusCode: response.StatusCode,
			}
		}
		nextBody := newSSEIdleBody(response.Body, body.idleTimeout)
		body.mu.Lock()
		if body.closed {
			body.mu.Unlock()
			_ = nextBody.Close()
			nextLifecycle.close()
			return io.ErrClosedPipe
		}
		body.current = nextBody
		body.currentLifecycle = nextLifecycle
		body.mu.Unlock()
		return nil
	}
}

func (body *reconnectingSSEBody) fail(err error) {
	body.mu.Lock()
	if !body.closed {
		body.terminalErr = err
		body.closed = true
	}
	body.mu.Unlock()
}

func (body *reconnectingSSEBody) Close() error {
	body.mu.Lock()
	if body.closed {
		err := body.terminalErr
		body.mu.Unlock()
		return err
	}
	body.closed = true
	current := body.current
	lifecycle := body.currentLifecycle
	body.current = nil
	body.currentLifecycle = nil
	body.mu.Unlock()
	if current != nil {
		_ = current.Close()
	}
	if lifecycle != nil {
		lifecycle.close()
	}
	return nil
}

func sseFrameEnd(bytes []byte) int {
	for index := 1; index < len(bytes); index++ {
		if bytes[index] == '\n' && (bytes[index-1] == '\n' || (bytes[index-1] == '\r' && index >= 2 && bytes[index-2] == '\n')) {
			return index + 1
		}
	}
	return -1
}

func sseRetryValue(frame []byte) (time.Duration, bool) {
	for _, line := range strings.Split(strings.ReplaceAll(string(frame), "\r\n", "\n"), "\n") {
		if !strings.HasPrefix(line, "retry:") {
			continue
		}
		var millis int64
		if _, err := fmt.Sscanf(strings.TrimSpace(strings.TrimPrefix(line, "retry:")), "%d", &millis); err == nil && millis >= 0 {
			return time.Duration(millis) * time.Millisecond, true
		}
	}
	return 0, false
}

type OpenapiResponse struct {
	StatusCode int
	Raw        *http.Response
	Status200  *OpenAPIJSONDocument
	Status304  bool
	Status400  *ValidationErr
}

func (c *Client) NewOpenapiRequest(ctx context.Context) (*http.Request, error) {
	if ctx == nil {
		return nil, fmt.Errorf("build Openapi request: context must not be nil")
	}
	path := "/"

	endpoint, err := url.Parse(c.baseURL + path)
	if err != nil {
		return nil, fmt.Errorf("build Openapi URL: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build Openapi request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	return req, nil
}

func (c *Client) Openapi(ctx context.Context) (*OpenapiResponse, error) {

	req, err := c.NewOpenapiRequest(ctx)
	if err != nil {
		return nil, err
	}
	responseCtx, lifecycle := c.responseContext(ctx)
	req = req.Clone(responseCtx)
	keepLifecycle := false
	defer func() {
		if !keepLifecycle {
			lifecycle.close()
		}
	}()
	res, err := c.do(responseCtx, req)
	if err != nil {
		return nil, fmt.Errorf("execute Openapi request: %w", err)
	}
	if res == nil {
		return nil, fmt.Errorf("execute Openapi request: HTTP client returned nil response")
	}

	result := &OpenapiResponse{StatusCode: res.StatusCode, Raw: res}
	switch res.StatusCode {
	case 200:
		var decoded OpenAPIJSONDocument
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode Openapi status 200 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status200 = &decoded
		return result, nil
	case 304:
		_ = res.Body.Close()
		result.Status304 = true
		return result, nil
	case 400:
		var decoded ValidationErr
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode Openapi status 400 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status400 = &decoded
		return result, nil
	default:
		rawBody, readErr := io.ReadAll(io.LimitReader(res.Body, maxDiagnosticBodyBytes))
		_ = res.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read unexpected Openapi response status %d: %w", res.StatusCode, readErr)
		}
		return nil, &UnexpectedStatusError{
			Method:     req.Method,
			URL:        req.URL.String(),
			StatusCode: res.StatusCode,
			Body:       strings.TrimSpace(string(rawBody)),
		}
	}
}

type HeadOpenapiResponse struct {
	StatusCode int
	Raw        *http.Response
	Status200  bool
	Status304  bool
	Status400  bool
}

func (c *Client) NewHeadOpenapiRequest(ctx context.Context) (*http.Request, error) {
	if ctx == nil {
		return nil, fmt.Errorf("build HeadOpenapi request: context must not be nil")
	}
	path := "/"

	endpoint, err := url.Parse(c.baseURL + path)
	if err != nil {
		return nil, fmt.Errorf("build HeadOpenapi URL: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "HEAD", endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build HeadOpenapi request: %w", err)
	}
	req.Header.Set("Accept", "*/*")
	return req, nil
}

func (c *Client) HeadOpenapi(ctx context.Context) (*HeadOpenapiResponse, error) {

	req, err := c.NewHeadOpenapiRequest(ctx)
	if err != nil {
		return nil, err
	}
	responseCtx, lifecycle := c.responseContext(ctx)
	req = req.Clone(responseCtx)
	keepLifecycle := false
	defer func() {
		if !keepLifecycle {
			lifecycle.close()
		}
	}()
	res, err := c.do(responseCtx, req)
	if err != nil {
		return nil, fmt.Errorf("execute HeadOpenapi request: %w", err)
	}
	if res == nil {
		return nil, fmt.Errorf("execute HeadOpenapi request: HTTP client returned nil response")
	}

	result := &HeadOpenapiResponse{StatusCode: res.StatusCode, Raw: res}
	switch res.StatusCode {
	case 200:
		_ = res.Body.Close()
		result.Status200 = true
		return result, nil
	case 304:
		_ = res.Body.Close()
		result.Status304 = true
		return result, nil
	case 400:
		_ = res.Body.Close()
		result.Status400 = true
		return result, nil
	default:
		rawBody, readErr := io.ReadAll(io.LimitReader(res.Body, maxDiagnosticBodyBytes))
		_ = res.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read unexpected HeadOpenapi response status %d: %w", res.StatusCode, readErr)
		}
		return nil, &UnexpectedStatusError{
			Method:     req.Method,
			URL:        req.URL.String(),
			StatusCode: res.StatusCode,
			Body:       strings.TrimSpace(string(rawBody)),
		}
	}
}

type CreateCLIAuthorizationParams struct {
	Body CreateCLIAuthorization
}

type CreateCLIAuthorizationResponse struct {
	StatusCode int
	Raw        *http.Response
	Status201  *CreatedCLIAuthorization
	Status400  *ValidationErr
	Status401  bool
}

func (c *Client) NewCreateCLIAuthorizationRequest(ctx context.Context, params CreateCLIAuthorizationParams) (*http.Request, error) {
	if ctx == nil {
		return nil, fmt.Errorf("build CreateCLIAuthorization request: context must not be nil")
	}
	path := "/cli/authorizations"

	endpoint, err := url.Parse(c.baseURL + path)
	if err != nil {
		return nil, fmt.Errorf("build CreateCLIAuthorization URL: %w", err)
	}
	var requestBody io.Reader
	encodedBody, err := json.Marshal(params.Body)
	if err != nil {
		return nil, fmt.Errorf("encode CreateCLIAuthorization JSON body: %w", err)
	}
	requestBody = bytes.NewReader(encodedBody)
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint.String(), requestBody)
	if err != nil {
		return nil, fmt.Errorf("build CreateCLIAuthorization request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func (c *Client) CreateCLIAuthorization(ctx context.Context, params CreateCLIAuthorizationParams) (*CreateCLIAuthorizationResponse, error) {

	req, err := c.NewCreateCLIAuthorizationRequest(ctx, params)
	if err != nil {
		return nil, err
	}
	responseCtx, lifecycle := c.responseContext(ctx)
	req = req.Clone(responseCtx)
	keepLifecycle := false
	defer func() {
		if !keepLifecycle {
			lifecycle.close()
		}
	}()
	res, err := c.do(responseCtx, req)
	if err != nil {
		return nil, fmt.Errorf("execute CreateCLIAuthorization request: %w", err)
	}
	if res == nil {
		return nil, fmt.Errorf("execute CreateCLIAuthorization request: HTTP client returned nil response")
	}

	result := &CreateCLIAuthorizationResponse{StatusCode: res.StatusCode, Raw: res}
	switch res.StatusCode {
	case 201:
		var decoded CreatedCLIAuthorization
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode CreateCLIAuthorization status 201 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status201 = &decoded
		return result, nil
	case 400:
		var decoded ValidationErr
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode CreateCLIAuthorization status 400 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status400 = &decoded
		return result, nil
	case 401:
		_ = res.Body.Close()
		result.Status401 = true
		return result, nil
	default:
		rawBody, readErr := io.ReadAll(io.LimitReader(res.Body, maxDiagnosticBodyBytes))
		_ = res.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read unexpected CreateCLIAuthorization response status %d: %w", res.StatusCode, readErr)
		}
		return nil, &UnexpectedStatusError{
			Method:     req.Method,
			URL:        req.URL.String(),
			StatusCode: res.StatusCode,
			Body:       strings.TrimSpace(string(rawBody)),
		}
	}
}

type CliAuthorizationEventsParams struct {
	Code CLIAuthorizationCode
}

type CliAuthorizationEventsResponse struct {
	StatusCode int
	Raw        *http.Response
	Status200  *SSEStream[CLIAuthorizationEvent]
	Status400  *ValidationErr
	Status404  bool
}

func (c *Client) NewCliAuthorizationEventsRequest(ctx context.Context, params CliAuthorizationEventsParams) (*http.Request, error) {
	if ctx == nil {
		return nil, fmt.Errorf("build CliAuthorizationEvents request: context must not be nil")
	}
	if params.Code == "" {
		return nil, fmt.Errorf("build CliAuthorizationEvents request: required parameter code is empty")
	}
	path := "/cli/authorizations/{code}/events"

	path = strings.ReplaceAll(path, "{code}", url.PathEscape(fmt.Sprint(params.Code)))
	endpoint, err := url.Parse(c.baseURL + path)
	if err != nil {
		return nil, fmt.Errorf("build CliAuthorizationEvents URL: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build CliAuthorizationEvents request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	return req, nil
}

func (c *Client) CliAuthorizationEvents(ctx context.Context, params CliAuthorizationEventsParams) (*CliAuthorizationEventsResponse, error) {

	var requestForError *http.Request
	open := func(attemptCtx context.Context) (*http.Response, *responseLifecycle, error) {
		req, err := c.NewCliAuthorizationEventsRequest(attemptCtx, params)
		if err != nil {
			return nil, nil, err
		}
		requestForError = req
		responseCtx, lifecycle := c.responseContext(attemptCtx)
		req = req.Clone(responseCtx)
		res, err := c.do(responseCtx, req)
		if err != nil {
			lifecycle.close()
			return nil, nil, fmt.Errorf("execute CliAuthorizationEvents request: %w", err)
		}
		if res == nil {
			lifecycle.close()
			return nil, nil, fmt.Errorf("execute CliAuthorizationEvents request: HTTP client returned nil response")
		}
		if res.Request == nil {
			res.Request = req
		}
		return res, lifecycle, nil
	}
	attempt, err := c.openReconnectingSSE(ctx, open)
	if err != nil {
		return nil, err
	}
	res := attempt.response
	lifecycle := attempt.lifecycle
	keepLifecycle := false
	defer func() {
		if !keepLifecycle {
			lifecycle.close()
		}
	}()

	result := &CliAuthorizationEventsResponse{StatusCode: res.StatusCode, Raw: res}
	switch res.StatusCode {
	case 200:
		lifecycle.stopTimeout()
		keepLifecycle = true
		body := newReconnectingSSEBody(
			attempt,
			ctx,
			open,
			c.sseMaxRetries,
			c.sseReconnectBaseDelay,
			c.sseIdleTimeout,
			c.sseReconnectOnStreamEnd,
		)
		res.Body = body
		result.Status200 = newReconnectingSSEStream[CLIAuthorizationEvent](body, lifecycle.close)

		return result, nil
	case 400:
		var decoded ValidationErr
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode CliAuthorizationEvents status 400 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status400 = &decoded
		return result, nil
	case 404:
		_ = res.Body.Close()
		result.Status404 = true
		return result, nil
	default:
		rawBody, readErr := io.ReadAll(io.LimitReader(res.Body, maxDiagnosticBodyBytes))
		_ = res.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read unexpected CliAuthorizationEvents response status %d: %w", res.StatusCode, readErr)
		}
		return nil, &UnexpectedStatusError{
			Method:     requestForError.Method,
			URL:        requestForError.URL.String(),
			StatusCode: res.StatusCode,
			Body:       strings.TrimSpace(string(rawBody)),
		}
	}
}

type EpisodeDeletionEventsParams struct {
	EpisodeDeletionRunId EpisodeDeletionRunID
}

type EpisodeDeletionEventsResponse struct {
	StatusCode int
	Raw        *http.Response
	Status200  *SSEStream[EpisodeDeletionEvent]
	Status400  *ValidationErr
	Status401  bool
	Status403  bool
	Status404  bool
}

func (c *Client) NewEpisodeDeletionEventsRequest(ctx context.Context, params EpisodeDeletionEventsParams) (*http.Request, error) {
	if ctx == nil {
		return nil, fmt.Errorf("build EpisodeDeletionEvents request: context must not be nil")
	}
	if params.EpisodeDeletionRunId == "" {
		return nil, fmt.Errorf("build EpisodeDeletionEvents request: required parameter episode_deletion_run_id is empty")
	}
	path := "/s/episode-deletions/{episode_deletion_run_id}/events"

	path = strings.ReplaceAll(path, "{episode_deletion_run_id}", url.PathEscape(fmt.Sprint(params.EpisodeDeletionRunId)))
	endpoint, err := url.Parse(c.baseURL + path)
	if err != nil {
		return nil, fmt.Errorf("build EpisodeDeletionEvents URL: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build EpisodeDeletionEvents request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	return req, nil
}

func (c *Client) EpisodeDeletionEvents(ctx context.Context, params EpisodeDeletionEventsParams) (*EpisodeDeletionEventsResponse, error) {

	var requestForError *http.Request
	open := func(attemptCtx context.Context) (*http.Response, *responseLifecycle, error) {
		req, err := c.NewEpisodeDeletionEventsRequest(attemptCtx, params)
		if err != nil {
			return nil, nil, err
		}
		requestForError = req
		responseCtx, lifecycle := c.responseContext(attemptCtx)
		req = req.Clone(responseCtx)
		res, err := c.do(responseCtx, req)
		if err != nil {
			lifecycle.close()
			return nil, nil, fmt.Errorf("execute EpisodeDeletionEvents request: %w", err)
		}
		if res == nil {
			lifecycle.close()
			return nil, nil, fmt.Errorf("execute EpisodeDeletionEvents request: HTTP client returned nil response")
		}
		if res.Request == nil {
			res.Request = req
		}
		return res, lifecycle, nil
	}
	attempt, err := c.openReconnectingSSE(ctx, open)
	if err != nil {
		return nil, err
	}
	res := attempt.response
	lifecycle := attempt.lifecycle
	keepLifecycle := false
	defer func() {
		if !keepLifecycle {
			lifecycle.close()
		}
	}()

	result := &EpisodeDeletionEventsResponse{StatusCode: res.StatusCode, Raw: res}
	switch res.StatusCode {
	case 200:
		lifecycle.stopTimeout()
		keepLifecycle = true
		body := newReconnectingSSEBody(
			attempt,
			ctx,
			open,
			c.sseMaxRetries,
			c.sseReconnectBaseDelay,
			c.sseIdleTimeout,
			c.sseReconnectOnStreamEnd,
		)
		res.Body = body
		result.Status200 = newReconnectingSSEStream[EpisodeDeletionEvent](body, lifecycle.close)

		return result, nil
	case 400:
		var decoded ValidationErr
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode EpisodeDeletionEvents status 400 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status400 = &decoded
		return result, nil
	case 401:
		_ = res.Body.Close()
		result.Status401 = true
		return result, nil
	case 403:
		_ = res.Body.Close()
		result.Status403 = true
		return result, nil
	case 404:
		_ = res.Body.Close()
		result.Status404 = true
		return result, nil
	default:
		rawBody, readErr := io.ReadAll(io.LimitReader(res.Body, maxDiagnosticBodyBytes))
		_ = res.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read unexpected EpisodeDeletionEvents response status %d: %w", res.StatusCode, readErr)
		}
		return nil, &UnexpectedStatusError{
			Method:     requestForError.Method,
			URL:        requestForError.URL.String(),
			StatusCode: res.StatusCode,
			Body:       strings.TrimSpace(string(rawBody)),
		}
	}
}

type CreateEpisodeUploadSessionParams struct {
	Body CreateEpisodeUploadSession
}

type CreateEpisodeUploadSessionResponse struct {
	StatusCode int
	Raw        *http.Response
	Status200  *EpisodeUploadSession
	Status201  *EpisodeUploadSession
	Status400  *ValidationErr
	Status401  bool
	Status403  bool
	Status404  bool
	Status409  bool
	Status410  bool
}

func (c *Client) NewCreateEpisodeUploadSessionRequest(ctx context.Context, params CreateEpisodeUploadSessionParams) (*http.Request, error) {
	if ctx == nil {
		return nil, fmt.Errorf("build CreateEpisodeUploadSession request: context must not be nil")
	}
	path := "/s/episode-upload-sessions"

	endpoint, err := url.Parse(c.baseURL + path)
	if err != nil {
		return nil, fmt.Errorf("build CreateEpisodeUploadSession URL: %w", err)
	}
	var requestBody io.Reader
	encodedBody, err := json.Marshal(params.Body)
	if err != nil {
		return nil, fmt.Errorf("encode CreateEpisodeUploadSession JSON body: %w", err)
	}
	requestBody = bytes.NewReader(encodedBody)
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint.String(), requestBody)
	if err != nil {
		return nil, fmt.Errorf("build CreateEpisodeUploadSession request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func (c *Client) CreateEpisodeUploadSession(ctx context.Context, params CreateEpisodeUploadSessionParams) (*CreateEpisodeUploadSessionResponse, error) {

	req, err := c.NewCreateEpisodeUploadSessionRequest(ctx, params)
	if err != nil {
		return nil, err
	}
	responseCtx, lifecycle := c.responseContext(ctx)
	req = req.Clone(responseCtx)
	keepLifecycle := false
	defer func() {
		if !keepLifecycle {
			lifecycle.close()
		}
	}()
	res, err := c.do(responseCtx, req)
	if err != nil {
		return nil, fmt.Errorf("execute CreateEpisodeUploadSession request: %w", err)
	}
	if res == nil {
		return nil, fmt.Errorf("execute CreateEpisodeUploadSession request: HTTP client returned nil response")
	}

	result := &CreateEpisodeUploadSessionResponse{StatusCode: res.StatusCode, Raw: res}
	switch res.StatusCode {
	case 200:
		var decoded EpisodeUploadSession
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode CreateEpisodeUploadSession status 200 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status200 = &decoded
		return result, nil
	case 201:
		var decoded EpisodeUploadSession
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode CreateEpisodeUploadSession status 201 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status201 = &decoded
		return result, nil
	case 400:
		var decoded ValidationErr
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode CreateEpisodeUploadSession status 400 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status400 = &decoded
		return result, nil
	case 401:
		_ = res.Body.Close()
		result.Status401 = true
		return result, nil
	case 403:
		_ = res.Body.Close()
		result.Status403 = true
		return result, nil
	case 404:
		_ = res.Body.Close()
		result.Status404 = true
		return result, nil
	case 409:
		_ = res.Body.Close()
		result.Status409 = true
		return result, nil
	case 410:
		_ = res.Body.Close()
		result.Status410 = true
		return result, nil
	default:
		rawBody, readErr := io.ReadAll(io.LimitReader(res.Body, maxDiagnosticBodyBytes))
		_ = res.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read unexpected CreateEpisodeUploadSession response status %d: %w", res.StatusCode, readErr)
		}
		return nil, &UnexpectedStatusError{
			Method:     req.Method,
			URL:        req.URL.String(),
			StatusCode: res.StatusCode,
			Body:       strings.TrimSpace(string(rawBody)),
		}
	}
}

type GetEpisodeUploadSessionParams struct {
	UploadSessionId UploadSessionID
}

type GetEpisodeUploadSessionResponse struct {
	StatusCode int
	Raw        *http.Response
	Status200  *EpisodeUploadSession
	Status400  *ValidationErr
	Status401  bool
	Status404  bool
	Status410  bool
}

func (c *Client) NewGetEpisodeUploadSessionRequest(ctx context.Context, params GetEpisodeUploadSessionParams) (*http.Request, error) {
	if ctx == nil {
		return nil, fmt.Errorf("build GetEpisodeUploadSession request: context must not be nil")
	}
	if params.UploadSessionId == "" {
		return nil, fmt.Errorf("build GetEpisodeUploadSession request: required parameter upload_session_id is empty")
	}
	path := "/s/episode-upload-sessions/{upload_session_id}"

	path = strings.ReplaceAll(path, "{upload_session_id}", url.PathEscape(fmt.Sprint(params.UploadSessionId)))
	endpoint, err := url.Parse(c.baseURL + path)
	if err != nil {
		return nil, fmt.Errorf("build GetEpisodeUploadSession URL: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build GetEpisodeUploadSession request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	return req, nil
}

func (c *Client) GetEpisodeUploadSession(ctx context.Context, params GetEpisodeUploadSessionParams) (*GetEpisodeUploadSessionResponse, error) {

	req, err := c.NewGetEpisodeUploadSessionRequest(ctx, params)
	if err != nil {
		return nil, err
	}
	responseCtx, lifecycle := c.responseContext(ctx)
	req = req.Clone(responseCtx)
	keepLifecycle := false
	defer func() {
		if !keepLifecycle {
			lifecycle.close()
		}
	}()
	res, err := c.do(responseCtx, req)
	if err != nil {
		return nil, fmt.Errorf("execute GetEpisodeUploadSession request: %w", err)
	}
	if res == nil {
		return nil, fmt.Errorf("execute GetEpisodeUploadSession request: HTTP client returned nil response")
	}

	result := &GetEpisodeUploadSessionResponse{StatusCode: res.StatusCode, Raw: res}
	switch res.StatusCode {
	case 200:
		var decoded EpisodeUploadSession
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode GetEpisodeUploadSession status 200 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status200 = &decoded
		return result, nil
	case 400:
		var decoded ValidationErr
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode GetEpisodeUploadSession status 400 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status400 = &decoded
		return result, nil
	case 401:
		_ = res.Body.Close()
		result.Status401 = true
		return result, nil
	case 404:
		_ = res.Body.Close()
		result.Status404 = true
		return result, nil
	case 410:
		_ = res.Body.Close()
		result.Status410 = true
		return result, nil
	default:
		rawBody, readErr := io.ReadAll(io.LimitReader(res.Body, maxDiagnosticBodyBytes))
		_ = res.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read unexpected GetEpisodeUploadSession response status %d: %w", res.StatusCode, readErr)
		}
		return nil, &UnexpectedStatusError{
			Method:     req.Method,
			URL:        req.URL.String(),
			StatusCode: res.StatusCode,
			Body:       strings.TrimSpace(string(rawBody)),
		}
	}
}

type UpdateEpisodeUploadSessionParams struct {
	UploadSessionId UploadSessionID
	Body            PatchEpisodeUploadSession
}

type UpdateEpisodeUploadSessionResponse struct {
	StatusCode int
	Raw        *http.Response
	Status200  *EpisodeUploadSession
	Status400  *ValidationErr
	Status401  bool
	Status404  bool
	Status409  bool
	Status410  bool
}

func (c *Client) NewUpdateEpisodeUploadSessionRequest(ctx context.Context, params UpdateEpisodeUploadSessionParams) (*http.Request, error) {
	if ctx == nil {
		return nil, fmt.Errorf("build UpdateEpisodeUploadSession request: context must not be nil")
	}
	if params.UploadSessionId == "" {
		return nil, fmt.Errorf("build UpdateEpisodeUploadSession request: required parameter upload_session_id is empty")
	}
	path := "/s/episode-upload-sessions/{upload_session_id}"

	path = strings.ReplaceAll(path, "{upload_session_id}", url.PathEscape(fmt.Sprint(params.UploadSessionId)))
	endpoint, err := url.Parse(c.baseURL + path)
	if err != nil {
		return nil, fmt.Errorf("build UpdateEpisodeUploadSession URL: %w", err)
	}
	var requestBody io.Reader
	encodedBody, err := json.Marshal(params.Body)
	if err != nil {
		return nil, fmt.Errorf("encode UpdateEpisodeUploadSession JSON body: %w", err)
	}
	requestBody = bytes.NewReader(encodedBody)
	req, err := http.NewRequestWithContext(ctx, "PUT", endpoint.String(), requestBody)
	if err != nil {
		return nil, fmt.Errorf("build UpdateEpisodeUploadSession request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func (c *Client) UpdateEpisodeUploadSession(ctx context.Context, params UpdateEpisodeUploadSessionParams) (*UpdateEpisodeUploadSessionResponse, error) {

	req, err := c.NewUpdateEpisodeUploadSessionRequest(ctx, params)
	if err != nil {
		return nil, err
	}
	responseCtx, lifecycle := c.responseContext(ctx)
	req = req.Clone(responseCtx)
	keepLifecycle := false
	defer func() {
		if !keepLifecycle {
			lifecycle.close()
		}
	}()
	res, err := c.do(responseCtx, req)
	if err != nil {
		return nil, fmt.Errorf("execute UpdateEpisodeUploadSession request: %w", err)
	}
	if res == nil {
		return nil, fmt.Errorf("execute UpdateEpisodeUploadSession request: HTTP client returned nil response")
	}

	result := &UpdateEpisodeUploadSessionResponse{StatusCode: res.StatusCode, Raw: res}
	switch res.StatusCode {
	case 200:
		var decoded EpisodeUploadSession
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode UpdateEpisodeUploadSession status 200 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status200 = &decoded
		return result, nil
	case 400:
		var decoded ValidationErr
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode UpdateEpisodeUploadSession status 400 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status400 = &decoded
		return result, nil
	case 401:
		_ = res.Body.Close()
		result.Status401 = true
		return result, nil
	case 404:
		_ = res.Body.Close()
		result.Status404 = true
		return result, nil
	case 409:
		_ = res.Body.Close()
		result.Status409 = true
		return result, nil
	case 410:
		_ = res.Body.Close()
		result.Status410 = true
		return result, nil
	default:
		rawBody, readErr := io.ReadAll(io.LimitReader(res.Body, maxDiagnosticBodyBytes))
		_ = res.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read unexpected UpdateEpisodeUploadSession response status %d: %w", res.StatusCode, readErr)
		}
		return nil, &UnexpectedStatusError{
			Method:     req.Method,
			URL:        req.URL.String(),
			StatusCode: res.StatusCode,
			Body:       strings.TrimSpace(string(rawBody)),
		}
	}
}

type CancelEpisodeUploadSessionParams struct {
	UploadSessionId UploadSessionID
}

type CancelEpisodeUploadSessionResponse struct {
	StatusCode int
	Raw        *http.Response
	Status204  bool
	Status400  *ValidationErr
	Status401  bool
	Status404  bool
	Status409  bool
}

func (c *Client) NewCancelEpisodeUploadSessionRequest(ctx context.Context, params CancelEpisodeUploadSessionParams) (*http.Request, error) {
	if ctx == nil {
		return nil, fmt.Errorf("build CancelEpisodeUploadSession request: context must not be nil")
	}
	if params.UploadSessionId == "" {
		return nil, fmt.Errorf("build CancelEpisodeUploadSession request: required parameter upload_session_id is empty")
	}
	path := "/s/episode-upload-sessions/{upload_session_id}"

	path = strings.ReplaceAll(path, "{upload_session_id}", url.PathEscape(fmt.Sprint(params.UploadSessionId)))
	endpoint, err := url.Parse(c.baseURL + path)
	if err != nil {
		return nil, fmt.Errorf("build CancelEpisodeUploadSession URL: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "DELETE", endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build CancelEpisodeUploadSession request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	return req, nil
}

func (c *Client) CancelEpisodeUploadSession(ctx context.Context, params CancelEpisodeUploadSessionParams) (*CancelEpisodeUploadSessionResponse, error) {

	req, err := c.NewCancelEpisodeUploadSessionRequest(ctx, params)
	if err != nil {
		return nil, err
	}
	responseCtx, lifecycle := c.responseContext(ctx)
	req = req.Clone(responseCtx)
	keepLifecycle := false
	defer func() {
		if !keepLifecycle {
			lifecycle.close()
		}
	}()
	res, err := c.do(responseCtx, req)
	if err != nil {
		return nil, fmt.Errorf("execute CancelEpisodeUploadSession request: %w", err)
	}
	if res == nil {
		return nil, fmt.Errorf("execute CancelEpisodeUploadSession request: HTTP client returned nil response")
	}

	result := &CancelEpisodeUploadSessionResponse{StatusCode: res.StatusCode, Raw: res}
	switch res.StatusCode {
	case 204:
		_ = res.Body.Close()
		result.Status204 = true
		return result, nil
	case 400:
		var decoded ValidationErr
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode CancelEpisodeUploadSession status 400 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status400 = &decoded
		return result, nil
	case 401:
		_ = res.Body.Close()
		result.Status401 = true
		return result, nil
	case 404:
		_ = res.Body.Close()
		result.Status404 = true
		return result, nil
	case 409:
		_ = res.Body.Close()
		result.Status409 = true
		return result, nil
	default:
		rawBody, readErr := io.ReadAll(io.LimitReader(res.Body, maxDiagnosticBodyBytes))
		_ = res.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read unexpected CancelEpisodeUploadSession response status %d: %w", res.StatusCode, readErr)
		}
		return nil, &UnexpectedStatusError{
			Method:     req.Method,
			URL:        req.URL.String(),
			StatusCode: res.StatusCode,
			Body:       strings.TrimSpace(string(rawBody)),
		}
	}
}

type CompleteEpisodeUploadSessionParams struct {
	UploadSessionId UploadSessionID
}

type CompleteEpisodeUploadSessionResponse struct {
	StatusCode int
	Raw        *http.Response
	Status200  *Episode
	Status201  *Episode
	Status400  *ValidationErr
	Status401  bool
	Status404  bool
	Status409  bool
	Status410  bool
}

func (c *Client) NewCompleteEpisodeUploadSessionRequest(ctx context.Context, params CompleteEpisodeUploadSessionParams) (*http.Request, error) {
	if ctx == nil {
		return nil, fmt.Errorf("build CompleteEpisodeUploadSession request: context must not be nil")
	}
	if params.UploadSessionId == "" {
		return nil, fmt.Errorf("build CompleteEpisodeUploadSession request: required parameter upload_session_id is empty")
	}
	path := "/s/episode-upload-sessions/{upload_session_id}/complete"

	path = strings.ReplaceAll(path, "{upload_session_id}", url.PathEscape(fmt.Sprint(params.UploadSessionId)))
	endpoint, err := url.Parse(c.baseURL + path)
	if err != nil {
		return nil, fmt.Errorf("build CompleteEpisodeUploadSession URL: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build CompleteEpisodeUploadSession request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	return req, nil
}

func (c *Client) CompleteEpisodeUploadSession(ctx context.Context, params CompleteEpisodeUploadSessionParams) (*CompleteEpisodeUploadSessionResponse, error) {

	req, err := c.NewCompleteEpisodeUploadSessionRequest(ctx, params)
	if err != nil {
		return nil, err
	}
	responseCtx, lifecycle := c.responseContext(ctx)
	req = req.Clone(responseCtx)
	keepLifecycle := false
	defer func() {
		if !keepLifecycle {
			lifecycle.close()
		}
	}()
	res, err := c.do(responseCtx, req)
	if err != nil {
		return nil, fmt.Errorf("execute CompleteEpisodeUploadSession request: %w", err)
	}
	if res == nil {
		return nil, fmt.Errorf("execute CompleteEpisodeUploadSession request: HTTP client returned nil response")
	}

	result := &CompleteEpisodeUploadSessionResponse{StatusCode: res.StatusCode, Raw: res}
	switch res.StatusCode {
	case 200:
		var decoded Episode
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode CompleteEpisodeUploadSession status 200 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status200 = &decoded
		return result, nil
	case 201:
		var decoded Episode
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode CompleteEpisodeUploadSession status 201 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status201 = &decoded
		return result, nil
	case 400:
		var decoded ValidationErr
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode CompleteEpisodeUploadSession status 400 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status400 = &decoded
		return result, nil
	case 401:
		_ = res.Body.Close()
		result.Status401 = true
		return result, nil
	case 404:
		_ = res.Body.Close()
		result.Status404 = true
		return result, nil
	case 409:
		_ = res.Body.Close()
		result.Status409 = true
		return result, nil
	case 410:
		_ = res.Body.Close()
		result.Status410 = true
		return result, nil
	default:
		rawBody, readErr := io.ReadAll(io.LimitReader(res.Body, maxDiagnosticBodyBytes))
		_ = res.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read unexpected CompleteEpisodeUploadSession response status %d: %w", res.StatusCode, readErr)
		}
		return nil, &UnexpectedStatusError{
			Method:     req.Method,
			URL:        req.URL.String(),
			StatusCode: res.StatusCode,
			Body:       strings.TrimSpace(string(rawBody)),
		}
	}
}

type EpisodeUploadSessionEventsParams struct {
	UploadSessionId UploadSessionID
}

type EpisodeUploadSessionEventsResponse struct {
	StatusCode int
	Raw        *http.Response
	Status200  *SSEStream[EpisodeProcessingEvent]
	Status400  *ValidationErr
	Status401  bool
	Status404  bool
}

func (c *Client) NewEpisodeUploadSessionEventsRequest(ctx context.Context, params EpisodeUploadSessionEventsParams) (*http.Request, error) {
	if ctx == nil {
		return nil, fmt.Errorf("build EpisodeUploadSessionEvents request: context must not be nil")
	}
	if params.UploadSessionId == "" {
		return nil, fmt.Errorf("build EpisodeUploadSessionEvents request: required parameter upload_session_id is empty")
	}
	path := "/s/episode-upload-sessions/{upload_session_id}/events"

	path = strings.ReplaceAll(path, "{upload_session_id}", url.PathEscape(fmt.Sprint(params.UploadSessionId)))
	endpoint, err := url.Parse(c.baseURL + path)
	if err != nil {
		return nil, fmt.Errorf("build EpisodeUploadSessionEvents URL: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build EpisodeUploadSessionEvents request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	return req, nil
}

func (c *Client) EpisodeUploadSessionEvents(ctx context.Context, params EpisodeUploadSessionEventsParams) (*EpisodeUploadSessionEventsResponse, error) {

	var requestForError *http.Request
	open := func(attemptCtx context.Context) (*http.Response, *responseLifecycle, error) {
		req, err := c.NewEpisodeUploadSessionEventsRequest(attemptCtx, params)
		if err != nil {
			return nil, nil, err
		}
		requestForError = req
		responseCtx, lifecycle := c.responseContext(attemptCtx)
		req = req.Clone(responseCtx)
		res, err := c.do(responseCtx, req)
		if err != nil {
			lifecycle.close()
			return nil, nil, fmt.Errorf("execute EpisodeUploadSessionEvents request: %w", err)
		}
		if res == nil {
			lifecycle.close()
			return nil, nil, fmt.Errorf("execute EpisodeUploadSessionEvents request: HTTP client returned nil response")
		}
		if res.Request == nil {
			res.Request = req
		}
		return res, lifecycle, nil
	}
	attempt, err := c.openReconnectingSSE(ctx, open)
	if err != nil {
		return nil, err
	}
	res := attempt.response
	lifecycle := attempt.lifecycle
	keepLifecycle := false
	defer func() {
		if !keepLifecycle {
			lifecycle.close()
		}
	}()

	result := &EpisodeUploadSessionEventsResponse{StatusCode: res.StatusCode, Raw: res}
	switch res.StatusCode {
	case 200:
		lifecycle.stopTimeout()
		keepLifecycle = true
		body := newReconnectingSSEBody(
			attempt,
			ctx,
			open,
			c.sseMaxRetries,
			c.sseReconnectBaseDelay,
			c.sseIdleTimeout,
			c.sseReconnectOnStreamEnd,
		)
		res.Body = body
		result.Status200 = newReconnectingSSEStream[EpisodeProcessingEvent](body, lifecycle.close)

		return result, nil
	case 400:
		var decoded ValidationErr
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode EpisodeUploadSessionEvents status 400 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status400 = &decoded
		return result, nil
	case 401:
		_ = res.Body.Close()
		result.Status401 = true
		return result, nil
	case 404:
		_ = res.Body.Close()
		result.Status404 = true
		return result, nil
	default:
		rawBody, readErr := io.ReadAll(io.LimitReader(res.Body, maxDiagnosticBodyBytes))
		_ = res.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read unexpected EpisodeUploadSessionEvents response status %d: %w", res.StatusCode, readErr)
		}
		return nil, &UnexpectedStatusError{
			Method:     requestForError.Method,
			URL:        requestForError.URL.String(),
			StatusCode: res.StatusCode,
			Body:       strings.TrimSpace(string(rawBody)),
		}
	}
}

type PresignEpisodeUploadSessionPartsParams struct {
	UploadSessionId UploadSessionID
	Body            PresignEpisodeUploadSessionParts
}

type PresignEpisodeUploadSessionPartsResponse struct {
	StatusCode int
	Raw        *http.Response
	Status201  *PresignedEpisodeUploadParts
	Status400  *ValidationErr
	Status401  bool
	Status402  bool
	Status404  bool
	Status409  bool
	Status410  bool
}

func (c *Client) NewPresignEpisodeUploadSessionPartsRequest(ctx context.Context, params PresignEpisodeUploadSessionPartsParams) (*http.Request, error) {
	if ctx == nil {
		return nil, fmt.Errorf("build PresignEpisodeUploadSessionParts request: context must not be nil")
	}
	if params.UploadSessionId == "" {
		return nil, fmt.Errorf("build PresignEpisodeUploadSessionParts request: required parameter upload_session_id is empty")
	}
	path := "/s/episode-upload-sessions/{upload_session_id}/parts/presign"

	path = strings.ReplaceAll(path, "{upload_session_id}", url.PathEscape(fmt.Sprint(params.UploadSessionId)))
	endpoint, err := url.Parse(c.baseURL + path)
	if err != nil {
		return nil, fmt.Errorf("build PresignEpisodeUploadSessionParts URL: %w", err)
	}
	var requestBody io.Reader
	encodedBody, err := json.Marshal(params.Body)
	if err != nil {
		return nil, fmt.Errorf("encode PresignEpisodeUploadSessionParts JSON body: %w", err)
	}
	requestBody = bytes.NewReader(encodedBody)
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint.String(), requestBody)
	if err != nil {
		return nil, fmt.Errorf("build PresignEpisodeUploadSessionParts request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func (c *Client) PresignEpisodeUploadSessionParts(ctx context.Context, params PresignEpisodeUploadSessionPartsParams) (*PresignEpisodeUploadSessionPartsResponse, error) {

	req, err := c.NewPresignEpisodeUploadSessionPartsRequest(ctx, params)
	if err != nil {
		return nil, err
	}
	responseCtx, lifecycle := c.responseContext(ctx)
	req = req.Clone(responseCtx)
	keepLifecycle := false
	defer func() {
		if !keepLifecycle {
			lifecycle.close()
		}
	}()
	res, err := c.do(responseCtx, req)
	if err != nil {
		return nil, fmt.Errorf("execute PresignEpisodeUploadSessionParts request: %w", err)
	}
	if res == nil {
		return nil, fmt.Errorf("execute PresignEpisodeUploadSessionParts request: HTTP client returned nil response")
	}

	result := &PresignEpisodeUploadSessionPartsResponse{StatusCode: res.StatusCode, Raw: res}
	switch res.StatusCode {
	case 201:
		var decoded PresignedEpisodeUploadParts
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode PresignEpisodeUploadSessionParts status 201 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status201 = &decoded
		return result, nil
	case 400:
		var decoded ValidationErr
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode PresignEpisodeUploadSessionParts status 400 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status400 = &decoded
		return result, nil
	case 401:
		_ = res.Body.Close()
		result.Status401 = true
		return result, nil
	case 402:
		_ = res.Body.Close()
		result.Status402 = true
		return result, nil
	case 404:
		_ = res.Body.Close()
		result.Status404 = true
		return result, nil
	case 409:
		_ = res.Body.Close()
		result.Status409 = true
		return result, nil
	case 410:
		_ = res.Body.Close()
		result.Status410 = true
		return result, nil
	default:
		rawBody, readErr := io.ReadAll(io.LimitReader(res.Body, maxDiagnosticBodyBytes))
		_ = res.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read unexpected PresignEpisodeUploadSessionParts response status %d: %w", res.StatusCode, readErr)
		}
		return nil, &UnexpectedStatusError{
			Method:     req.Method,
			URL:        req.URL.String(),
			StatusCode: res.StatusCode,
			Body:       strings.TrimSpace(string(rawBody)),
		}
	}
}

type CreateEpisodeDeletionParams struct {
	EpisodeId EpisodeID
}

type CreateEpisodeDeletionResponse struct {
	StatusCode int
	Raw        *http.Response
	Status202  *CreatedPublicEpisodeDeletion
	Status400  *ValidationErr
	Status401  bool
	Status403  bool
	Status404  bool
}

func (c *Client) NewCreateEpisodeDeletionRequest(ctx context.Context, params CreateEpisodeDeletionParams) (*http.Request, error) {
	if ctx == nil {
		return nil, fmt.Errorf("build CreateEpisodeDeletion request: context must not be nil")
	}
	if params.EpisodeId == "" {
		return nil, fmt.Errorf("build CreateEpisodeDeletion request: required parameter episode_id is empty")
	}
	path := "/s/episodes/{episode_id}/deletions"

	path = strings.ReplaceAll(path, "{episode_id}", url.PathEscape(fmt.Sprint(params.EpisodeId)))
	endpoint, err := url.Parse(c.baseURL + path)
	if err != nil {
		return nil, fmt.Errorf("build CreateEpisodeDeletion URL: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build CreateEpisodeDeletion request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	return req, nil
}

func (c *Client) CreateEpisodeDeletion(ctx context.Context, params CreateEpisodeDeletionParams) (*CreateEpisodeDeletionResponse, error) {

	req, err := c.NewCreateEpisodeDeletionRequest(ctx, params)
	if err != nil {
		return nil, err
	}
	responseCtx, lifecycle := c.responseContext(ctx)
	req = req.Clone(responseCtx)
	keepLifecycle := false
	defer func() {
		if !keepLifecycle {
			lifecycle.close()
		}
	}()
	res, err := c.do(responseCtx, req)
	if err != nil {
		return nil, fmt.Errorf("execute CreateEpisodeDeletion request: %w", err)
	}
	if res == nil {
		return nil, fmt.Errorf("execute CreateEpisodeDeletion request: HTTP client returned nil response")
	}

	result := &CreateEpisodeDeletionResponse{StatusCode: res.StatusCode, Raw: res}
	switch res.StatusCode {
	case 202:
		var decoded CreatedPublicEpisodeDeletion
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode CreateEpisodeDeletion status 202 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status202 = &decoded
		return result, nil
	case 400:
		var decoded ValidationErr
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode CreateEpisodeDeletion status 400 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status400 = &decoded
		return result, nil
	case 401:
		_ = res.Body.Close()
		result.Status401 = true
		return result, nil
	case 403:
		_ = res.Body.Close()
		result.Status403 = true
		return result, nil
	case 404:
		_ = res.Body.Close()
		result.Status404 = true
		return result, nil
	default:
		rawBody, readErr := io.ReadAll(io.LimitReader(res.Body, maxDiagnosticBodyBytes))
		_ = res.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read unexpected CreateEpisodeDeletion response status %d: %w", res.StatusCode, readErr)
		}
		return nil, &UnexpectedStatusError{
			Method:     req.Method,
			URL:        req.URL.String(),
			StatusCode: res.StatusCode,
			Body:       strings.TrimSpace(string(rawBody)),
		}
	}
}

type CreateImageUploadPresignParams struct {
	Body CreateImageUploadPresign
}

type CreateImageUploadPresignResponse struct {
	StatusCode int
	Raw        *http.Response
	Status201  *CreatedImageUploadPresign
	Status400  bool
	Status401  bool
	Status402  bool
	Status403  bool
	Status409  bool
}

func (c *Client) NewCreateImageUploadPresignRequest(ctx context.Context, params CreateImageUploadPresignParams) (*http.Request, error) {
	if ctx == nil {
		return nil, fmt.Errorf("build CreateImageUploadPresign request: context must not be nil")
	}
	path := "/s/image-uploads/presign"

	endpoint, err := url.Parse(c.baseURL + path)
	if err != nil {
		return nil, fmt.Errorf("build CreateImageUploadPresign URL: %w", err)
	}
	var requestBody io.Reader
	encodedBody, err := json.Marshal(params.Body)
	if err != nil {
		return nil, fmt.Errorf("encode CreateImageUploadPresign JSON body: %w", err)
	}
	requestBody = bytes.NewReader(encodedBody)
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint.String(), requestBody)
	if err != nil {
		return nil, fmt.Errorf("build CreateImageUploadPresign request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func (c *Client) CreateImageUploadPresign(ctx context.Context, params CreateImageUploadPresignParams) (*CreateImageUploadPresignResponse, error) {

	req, err := c.NewCreateImageUploadPresignRequest(ctx, params)
	if err != nil {
		return nil, err
	}
	responseCtx, lifecycle := c.responseContext(ctx)
	req = req.Clone(responseCtx)
	keepLifecycle := false
	defer func() {
		if !keepLifecycle {
			lifecycle.close()
		}
	}()
	res, err := c.do(responseCtx, req)
	if err != nil {
		return nil, fmt.Errorf("execute CreateImageUploadPresign request: %w", err)
	}
	if res == nil {
		return nil, fmt.Errorf("execute CreateImageUploadPresign request: HTTP client returned nil response")
	}

	result := &CreateImageUploadPresignResponse{StatusCode: res.StatusCode, Raw: res}
	switch res.StatusCode {
	case 201:
		var decoded CreatedImageUploadPresign
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode CreateImageUploadPresign status 201 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status201 = &decoded
		return result, nil
	case 400:
		_ = res.Body.Close()
		result.Status400 = true
		return result, nil
	case 401:
		_ = res.Body.Close()
		result.Status401 = true
		return result, nil
	case 402:
		_ = res.Body.Close()
		result.Status402 = true
		return result, nil
	case 403:
		_ = res.Body.Close()
		result.Status403 = true
		return result, nil
	case 409:
		_ = res.Body.Close()
		result.Status409 = true
		return result, nil
	default:
		rawBody, readErr := io.ReadAll(io.LimitReader(res.Body, maxDiagnosticBodyBytes))
		_ = res.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read unexpected CreateImageUploadPresign response status %d: %w", res.StatusCode, readErr)
		}
		return nil, &UnexpectedStatusError{
			Method:     req.Method,
			URL:        req.URL.String(),
			StatusCode: res.StatusCode,
			Body:       strings.TrimSpace(string(rawBody)),
		}
	}
}

type CompleteImageUploadParams struct {
	ImageAssetId ImageAssetID
	Body         CompleteImageUpload
}

type CompleteImageUploadResponse struct {
	StatusCode int
	Raw        *http.Response
	Status201  *ImageAsset
	Status400  bool
	Status401  bool
	Status403  bool
	Status404  bool
	Status409  bool
}

func (c *Client) NewCompleteImageUploadRequest(ctx context.Context, params CompleteImageUploadParams) (*http.Request, error) {
	if ctx == nil {
		return nil, fmt.Errorf("build CompleteImageUpload request: context must not be nil")
	}
	if params.ImageAssetId == "" {
		return nil, fmt.Errorf("build CompleteImageUpload request: required parameter image_asset_id is empty")
	}
	path := "/s/image-uploads/{image_asset_id}/complete"

	path = strings.ReplaceAll(path, "{image_asset_id}", url.PathEscape(fmt.Sprint(params.ImageAssetId)))
	endpoint, err := url.Parse(c.baseURL + path)
	if err != nil {
		return nil, fmt.Errorf("build CompleteImageUpload URL: %w", err)
	}
	var requestBody io.Reader
	encodedBody, err := json.Marshal(params.Body)
	if err != nil {
		return nil, fmt.Errorf("encode CompleteImageUpload JSON body: %w", err)
	}
	requestBody = bytes.NewReader(encodedBody)
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint.String(), requestBody)
	if err != nil {
		return nil, fmt.Errorf("build CompleteImageUpload request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func (c *Client) CompleteImageUpload(ctx context.Context, params CompleteImageUploadParams) (*CompleteImageUploadResponse, error) {

	req, err := c.NewCompleteImageUploadRequest(ctx, params)
	if err != nil {
		return nil, err
	}
	responseCtx, lifecycle := c.responseContext(ctx)
	req = req.Clone(responseCtx)
	keepLifecycle := false
	defer func() {
		if !keepLifecycle {
			lifecycle.close()
		}
	}()
	res, err := c.do(responseCtx, req)
	if err != nil {
		return nil, fmt.Errorf("execute CompleteImageUpload request: %w", err)
	}
	if res == nil {
		return nil, fmt.Errorf("execute CompleteImageUpload request: HTTP client returned nil response")
	}

	result := &CompleteImageUploadResponse{StatusCode: res.StatusCode, Raw: res}
	switch res.StatusCode {
	case 201:
		var decoded ImageAsset
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode CompleteImageUpload status 201 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status201 = &decoded
		return result, nil
	case 400:
		_ = res.Body.Close()
		result.Status400 = true
		return result, nil
	case 401:
		_ = res.Body.Close()
		result.Status401 = true
		return result, nil
	case 403:
		_ = res.Body.Close()
		result.Status403 = true
		return result, nil
	case 404:
		_ = res.Body.Close()
		result.Status404 = true
		return result, nil
	case 409:
		_ = res.Body.Close()
		result.Status409 = true
		return result, nil
	default:
		rawBody, readErr := io.ReadAll(io.LimitReader(res.Body, maxDiagnosticBodyBytes))
		_ = res.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read unexpected CompleteImageUpload response status %d: %w", res.StatusCode, readErr)
		}
		return nil, &UnexpectedStatusError{
			Method:     req.Method,
			URL:        req.URL.String(),
			StatusCode: res.StatusCode,
			Body:       strings.TrimSpace(string(rawBody)),
		}
	}
}

type ImportRSSParams struct {
	Body ImportRSSRequest
}

type ImportRSSResponse struct {
	StatusCode int
	Raw        *http.Response
	Status202  *CreatedPublicRSSImport
	Status400  *ValidationErr
	Status401  bool
	Status402  *RSSImportEntitlementError
	Status403  bool
	Status409  bool
}

func (c *Client) NewImportRSSRequest(ctx context.Context, params ImportRSSParams) (*http.Request, error) {
	if ctx == nil {
		return nil, fmt.Errorf("build ImportRSS request: context must not be nil")
	}
	path := "/s/rss-imports"

	endpoint, err := url.Parse(c.baseURL + path)
	if err != nil {
		return nil, fmt.Errorf("build ImportRSS URL: %w", err)
	}
	var requestBody io.Reader
	encodedBody, err := json.Marshal(params.Body)
	if err != nil {
		return nil, fmt.Errorf("encode ImportRSS JSON body: %w", err)
	}
	requestBody = bytes.NewReader(encodedBody)
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint.String(), requestBody)
	if err != nil {
		return nil, fmt.Errorf("build ImportRSS request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func (c *Client) ImportRSS(ctx context.Context, params ImportRSSParams) (*ImportRSSResponse, error) {

	req, err := c.NewImportRSSRequest(ctx, params)
	if err != nil {
		return nil, err
	}
	responseCtx, lifecycle := c.responseContext(ctx)
	req = req.Clone(responseCtx)
	keepLifecycle := false
	defer func() {
		if !keepLifecycle {
			lifecycle.close()
		}
	}()
	res, err := c.do(responseCtx, req)
	if err != nil {
		return nil, fmt.Errorf("execute ImportRSS request: %w", err)
	}
	if res == nil {
		return nil, fmt.Errorf("execute ImportRSS request: HTTP client returned nil response")
	}

	result := &ImportRSSResponse{StatusCode: res.StatusCode, Raw: res}
	switch res.StatusCode {
	case 202:
		var decoded CreatedPublicRSSImport
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode ImportRSS status 202 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status202 = &decoded
		return result, nil
	case 400:
		var decoded ValidationErr
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode ImportRSS status 400 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status400 = &decoded
		return result, nil
	case 401:
		_ = res.Body.Close()
		result.Status401 = true
		return result, nil
	case 402:
		var decoded RSSImportEntitlementError
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode ImportRSS status 402 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status402 = &decoded
		return result, nil
	case 403:
		_ = res.Body.Close()
		result.Status403 = true
		return result, nil
	case 409:
		_ = res.Body.Close()
		result.Status409 = true
		return result, nil
	default:
		rawBody, readErr := io.ReadAll(io.LimitReader(res.Body, maxDiagnosticBodyBytes))
		_ = res.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read unexpected ImportRSS response status %d: %w", res.StatusCode, readErr)
		}
		return nil, &UnexpectedStatusError{
			Method:     req.Method,
			URL:        req.URL.String(),
			StatusCode: res.StatusCode,
			Body:       strings.TrimSpace(string(rawBody)),
		}
	}
}

type ImportRSSRunEventsParams struct {
	ImportRunId ImportRunID
}

type ImportRSSRunEventsResponse struct {
	StatusCode int
	Raw        *http.Response
	Status200  *SSEStream[PublicRSSImportEvent]
	Status400  *ValidationErr
	Status401  bool
	Status403  bool
	Status404  bool
}

func (c *Client) NewImportRSSRunEventsRequest(ctx context.Context, params ImportRSSRunEventsParams) (*http.Request, error) {
	if ctx == nil {
		return nil, fmt.Errorf("build ImportRSSRunEvents request: context must not be nil")
	}
	if params.ImportRunId == "" {
		return nil, fmt.Errorf("build ImportRSSRunEvents request: required parameter import_run_id is empty")
	}
	path := "/s/rss-imports/{import_run_id}/events"

	path = strings.ReplaceAll(path, "{import_run_id}", url.PathEscape(fmt.Sprint(params.ImportRunId)))
	endpoint, err := url.Parse(c.baseURL + path)
	if err != nil {
		return nil, fmt.Errorf("build ImportRSSRunEvents URL: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build ImportRSSRunEvents request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	return req, nil
}

func (c *Client) ImportRSSRunEvents(ctx context.Context, params ImportRSSRunEventsParams) (*ImportRSSRunEventsResponse, error) {

	var requestForError *http.Request
	open := func(attemptCtx context.Context) (*http.Response, *responseLifecycle, error) {
		req, err := c.NewImportRSSRunEventsRequest(attemptCtx, params)
		if err != nil {
			return nil, nil, err
		}
		requestForError = req
		responseCtx, lifecycle := c.responseContext(attemptCtx)
		req = req.Clone(responseCtx)
		res, err := c.do(responseCtx, req)
		if err != nil {
			lifecycle.close()
			return nil, nil, fmt.Errorf("execute ImportRSSRunEvents request: %w", err)
		}
		if res == nil {
			lifecycle.close()
			return nil, nil, fmt.Errorf("execute ImportRSSRunEvents request: HTTP client returned nil response")
		}
		if res.Request == nil {
			res.Request = req
		}
		return res, lifecycle, nil
	}
	attempt, err := c.openReconnectingSSE(ctx, open)
	if err != nil {
		return nil, err
	}
	res := attempt.response
	lifecycle := attempt.lifecycle
	keepLifecycle := false
	defer func() {
		if !keepLifecycle {
			lifecycle.close()
		}
	}()

	result := &ImportRSSRunEventsResponse{StatusCode: res.StatusCode, Raw: res}
	switch res.StatusCode {
	case 200:
		lifecycle.stopTimeout()
		keepLifecycle = true
		body := newReconnectingSSEBody(
			attempt,
			ctx,
			open,
			c.sseMaxRetries,
			c.sseReconnectBaseDelay,
			c.sseIdleTimeout,
			c.sseReconnectOnStreamEnd,
		)
		res.Body = body
		result.Status200 = newReconnectingSSEStream[PublicRSSImportEvent](body, lifecycle.close)

		return result, nil
	case 400:
		var decoded ValidationErr
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode ImportRSSRunEvents status 400 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status400 = &decoded
		return result, nil
	case 401:
		_ = res.Body.Close()
		result.Status401 = true
		return result, nil
	case 403:
		_ = res.Body.Close()
		result.Status403 = true
		return result, nil
	case 404:
		_ = res.Body.Close()
		result.Status404 = true
		return result, nil
	default:
		rawBody, readErr := io.ReadAll(io.LimitReader(res.Body, maxDiagnosticBodyBytes))
		_ = res.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read unexpected ImportRSSRunEvents response status %d: %w", res.StatusCode, readErr)
		}
		return nil, &UnexpectedStatusError{
			Method:     requestForError.Method,
			URL:        requestForError.URL.String(),
			StatusCode: res.StatusCode,
			Body:       strings.TrimSpace(string(rawBody)),
		}
	}
}

type ShowDeletionEventsParams struct {
	ShowDeletionRunId ShowDeletionRunID
}

type ShowDeletionEventsResponse struct {
	StatusCode int
	Raw        *http.Response
	Status200  *SSEStream[ShowDeletionEvent]
	Status400  *ValidationErr
	Status401  bool
	Status403  bool
	Status404  bool
}

func (c *Client) NewShowDeletionEventsRequest(ctx context.Context, params ShowDeletionEventsParams) (*http.Request, error) {
	if ctx == nil {
		return nil, fmt.Errorf("build ShowDeletionEvents request: context must not be nil")
	}
	if params.ShowDeletionRunId == "" {
		return nil, fmt.Errorf("build ShowDeletionEvents request: required parameter show_deletion_run_id is empty")
	}
	path := "/s/show-deletions/{show_deletion_run_id}/events"

	path = strings.ReplaceAll(path, "{show_deletion_run_id}", url.PathEscape(fmt.Sprint(params.ShowDeletionRunId)))
	endpoint, err := url.Parse(c.baseURL + path)
	if err != nil {
		return nil, fmt.Errorf("build ShowDeletionEvents URL: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build ShowDeletionEvents request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	return req, nil
}

func (c *Client) ShowDeletionEvents(ctx context.Context, params ShowDeletionEventsParams) (*ShowDeletionEventsResponse, error) {

	var requestForError *http.Request
	open := func(attemptCtx context.Context) (*http.Response, *responseLifecycle, error) {
		req, err := c.NewShowDeletionEventsRequest(attemptCtx, params)
		if err != nil {
			return nil, nil, err
		}
		requestForError = req
		responseCtx, lifecycle := c.responseContext(attemptCtx)
		req = req.Clone(responseCtx)
		res, err := c.do(responseCtx, req)
		if err != nil {
			lifecycle.close()
			return nil, nil, fmt.Errorf("execute ShowDeletionEvents request: %w", err)
		}
		if res == nil {
			lifecycle.close()
			return nil, nil, fmt.Errorf("execute ShowDeletionEvents request: HTTP client returned nil response")
		}
		if res.Request == nil {
			res.Request = req
		}
		return res, lifecycle, nil
	}
	attempt, err := c.openReconnectingSSE(ctx, open)
	if err != nil {
		return nil, err
	}
	res := attempt.response
	lifecycle := attempt.lifecycle
	keepLifecycle := false
	defer func() {
		if !keepLifecycle {
			lifecycle.close()
		}
	}()

	result := &ShowDeletionEventsResponse{StatusCode: res.StatusCode, Raw: res}
	switch res.StatusCode {
	case 200:
		lifecycle.stopTimeout()
		keepLifecycle = true
		body := newReconnectingSSEBody(
			attempt,
			ctx,
			open,
			c.sseMaxRetries,
			c.sseReconnectBaseDelay,
			c.sseIdleTimeout,
			c.sseReconnectOnStreamEnd,
		)
		res.Body = body
		result.Status200 = newReconnectingSSEStream[ShowDeletionEvent](body, lifecycle.close)

		return result, nil
	case 400:
		var decoded ValidationErr
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode ShowDeletionEvents status 400 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status400 = &decoded
		return result, nil
	case 401:
		_ = res.Body.Close()
		result.Status401 = true
		return result, nil
	case 403:
		_ = res.Body.Close()
		result.Status403 = true
		return result, nil
	case 404:
		_ = res.Body.Close()
		result.Status404 = true
		return result, nil
	default:
		rawBody, readErr := io.ReadAll(io.LimitReader(res.Body, maxDiagnosticBodyBytes))
		_ = res.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read unexpected ShowDeletionEvents response status %d: %w", res.StatusCode, readErr)
		}
		return nil, &UnexpectedStatusError{
			Method:     requestForError.Method,
			URL:        requestForError.URL.String(),
			StatusCode: res.StatusCode,
			Body:       strings.TrimSpace(string(rawBody)),
		}
	}
}

type ListShowsResponse struct {
	StatusCode int
	Raw        *http.Response
	Status200  *[]Show
	Status400  *ValidationErr
	Status401  bool
}

func (c *Client) NewListShowsRequest(ctx context.Context) (*http.Request, error) {
	if ctx == nil {
		return nil, fmt.Errorf("build ListShows request: context must not be nil")
	}
	path := "/s/shows"

	endpoint, err := url.Parse(c.baseURL + path)
	if err != nil {
		return nil, fmt.Errorf("build ListShows URL: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build ListShows request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	return req, nil
}

func (c *Client) ListShows(ctx context.Context) (*ListShowsResponse, error) {

	req, err := c.NewListShowsRequest(ctx)
	if err != nil {
		return nil, err
	}
	responseCtx, lifecycle := c.responseContext(ctx)
	req = req.Clone(responseCtx)
	keepLifecycle := false
	defer func() {
		if !keepLifecycle {
			lifecycle.close()
		}
	}()
	res, err := c.do(responseCtx, req)
	if err != nil {
		return nil, fmt.Errorf("execute ListShows request: %w", err)
	}
	if res == nil {
		return nil, fmt.Errorf("execute ListShows request: HTTP client returned nil response")
	}

	result := &ListShowsResponse{StatusCode: res.StatusCode, Raw: res}
	switch res.StatusCode {
	case 200:
		var decoded []Show
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode ListShows status 200 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status200 = &decoded
		return result, nil
	case 400:
		var decoded ValidationErr
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode ListShows status 400 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status400 = &decoded
		return result, nil
	case 401:
		_ = res.Body.Close()
		result.Status401 = true
		return result, nil
	default:
		rawBody, readErr := io.ReadAll(io.LimitReader(res.Body, maxDiagnosticBodyBytes))
		_ = res.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read unexpected ListShows response status %d: %w", res.StatusCode, readErr)
		}
		return nil, &UnexpectedStatusError{
			Method:     req.Method,
			URL:        req.URL.String(),
			StatusCode: res.StatusCode,
			Body:       strings.TrimSpace(string(rawBody)),
		}
	}
}

type CreateShowParams struct {
	Body CreateShow
}

type CreateShowResponse struct {
	StatusCode int
	Raw        *http.Response
	Status201  *Show
	Status400  *ValidationErr
	Status401  bool
	Status403  bool
	Status409  bool
}

func (c *Client) NewCreateShowRequest(ctx context.Context, params CreateShowParams) (*http.Request, error) {
	if ctx == nil {
		return nil, fmt.Errorf("build CreateShow request: context must not be nil")
	}
	path := "/s/shows"

	endpoint, err := url.Parse(c.baseURL + path)
	if err != nil {
		return nil, fmt.Errorf("build CreateShow URL: %w", err)
	}
	var requestBody io.Reader
	encodedBody, err := json.Marshal(params.Body)
	if err != nil {
		return nil, fmt.Errorf("encode CreateShow JSON body: %w", err)
	}
	requestBody = bytes.NewReader(encodedBody)
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint.String(), requestBody)
	if err != nil {
		return nil, fmt.Errorf("build CreateShow request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func (c *Client) CreateShow(ctx context.Context, params CreateShowParams) (*CreateShowResponse, error) {

	req, err := c.NewCreateShowRequest(ctx, params)
	if err != nil {
		return nil, err
	}
	responseCtx, lifecycle := c.responseContext(ctx)
	req = req.Clone(responseCtx)
	keepLifecycle := false
	defer func() {
		if !keepLifecycle {
			lifecycle.close()
		}
	}()
	res, err := c.do(responseCtx, req)
	if err != nil {
		return nil, fmt.Errorf("execute CreateShow request: %w", err)
	}
	if res == nil {
		return nil, fmt.Errorf("execute CreateShow request: HTTP client returned nil response")
	}

	result := &CreateShowResponse{StatusCode: res.StatusCode, Raw: res}
	switch res.StatusCode {
	case 201:
		var decoded Show
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode CreateShow status 201 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status201 = &decoded
		return result, nil
	case 400:
		var decoded ValidationErr
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode CreateShow status 400 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status400 = &decoded
		return result, nil
	case 401:
		_ = res.Body.Close()
		result.Status401 = true
		return result, nil
	case 403:
		_ = res.Body.Close()
		result.Status403 = true
		return result, nil
	case 409:
		_ = res.Body.Close()
		result.Status409 = true
		return result, nil
	default:
		rawBody, readErr := io.ReadAll(io.LimitReader(res.Body, maxDiagnosticBodyBytes))
		_ = res.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read unexpected CreateShow response status %d: %w", res.StatusCode, readErr)
		}
		return nil, &UnexpectedStatusError{
			Method:     req.Method,
			URL:        req.URL.String(),
			StatusCode: res.StatusCode,
			Body:       strings.TrimSpace(string(rawBody)),
		}
	}
}

type CreateShowDeletionParams struct {
	ShowSlug ShowSlug
}

type CreateShowDeletionResponse struct {
	StatusCode int
	Raw        *http.Response
	Status202  *CreatedPublicShowDeletion
	Status400  *ValidationErr
	Status401  bool
	Status403  bool
	Status404  bool
}

func (c *Client) NewCreateShowDeletionRequest(ctx context.Context, params CreateShowDeletionParams) (*http.Request, error) {
	if ctx == nil {
		return nil, fmt.Errorf("build CreateShowDeletion request: context must not be nil")
	}
	if params.ShowSlug == "" {
		return nil, fmt.Errorf("build CreateShowDeletion request: required parameter show_slug is empty")
	}
	path := "/s/shows/{show_slug}/deletions"

	path = strings.ReplaceAll(path, "{show_slug}", url.PathEscape(fmt.Sprint(params.ShowSlug)))
	endpoint, err := url.Parse(c.baseURL + path)
	if err != nil {
		return nil, fmt.Errorf("build CreateShowDeletion URL: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build CreateShowDeletion request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	return req, nil
}

func (c *Client) CreateShowDeletion(ctx context.Context, params CreateShowDeletionParams) (*CreateShowDeletionResponse, error) {

	req, err := c.NewCreateShowDeletionRequest(ctx, params)
	if err != nil {
		return nil, err
	}
	responseCtx, lifecycle := c.responseContext(ctx)
	req = req.Clone(responseCtx)
	keepLifecycle := false
	defer func() {
		if !keepLifecycle {
			lifecycle.close()
		}
	}()
	res, err := c.do(responseCtx, req)
	if err != nil {
		return nil, fmt.Errorf("execute CreateShowDeletion request: %w", err)
	}
	if res == nil {
		return nil, fmt.Errorf("execute CreateShowDeletion request: HTTP client returned nil response")
	}

	result := &CreateShowDeletionResponse{StatusCode: res.StatusCode, Raw: res}
	switch res.StatusCode {
	case 202:
		var decoded CreatedPublicShowDeletion
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode CreateShowDeletion status 202 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status202 = &decoded
		return result, nil
	case 400:
		var decoded ValidationErr
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode CreateShowDeletion status 400 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status400 = &decoded
		return result, nil
	case 401:
		_ = res.Body.Close()
		result.Status401 = true
		return result, nil
	case 403:
		_ = res.Body.Close()
		result.Status403 = true
		return result, nil
	case 404:
		_ = res.Body.Close()
		result.Status404 = true
		return result, nil
	default:
		rawBody, readErr := io.ReadAll(io.LimitReader(res.Body, maxDiagnosticBodyBytes))
		_ = res.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read unexpected CreateShowDeletion response status %d: %w", res.StatusCode, readErr)
		}
		return nil, &UnexpectedStatusError{
			Method:     req.Method,
			URL:        req.URL.String(),
			StatusCode: res.StatusCode,
			Body:       strings.TrimSpace(string(rawBody)),
		}
	}
}

type ListEpisodesParams struct {
	ShowSlug ShowSlug
	Limit    *EpisodePageLimit
	Cursor   *EpisodePageCursor
}

type ListEpisodesResponse struct {
	StatusCode int
	Raw        *http.Response
	Status200  *EpisodePage
	Status400  *ValidationErr
	Status401  bool
	Status403  bool
	Status404  bool
}

func (c *Client) NewListEpisodesRequest(ctx context.Context, params ListEpisodesParams) (*http.Request, error) {
	if ctx == nil {
		return nil, fmt.Errorf("build ListEpisodes request: context must not be nil")
	}
	if params.ShowSlug == "" {
		return nil, fmt.Errorf("build ListEpisodes request: required parameter show_slug is empty")
	}
	path := "/s/shows/{show_slug}/episodes"

	path = strings.ReplaceAll(path, "{show_slug}", url.PathEscape(fmt.Sprint(params.ShowSlug)))
	endpoint, err := url.Parse(c.baseURL + path)
	if err != nil {
		return nil, fmt.Errorf("build ListEpisodes URL: %w", err)
	}
	query := endpoint.Query()
	if params.Limit != nil {
		query.Set("limit", fmt.Sprint(*params.Limit))
	}
	if params.Cursor != nil {
		query.Set("cursor", fmt.Sprint(*params.Cursor))
	}
	endpoint.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build ListEpisodes request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	return req, nil
}

func (c *Client) ListEpisodes(ctx context.Context, params ListEpisodesParams) (*ListEpisodesResponse, error) {

	req, err := c.NewListEpisodesRequest(ctx, params)
	if err != nil {
		return nil, err
	}
	responseCtx, lifecycle := c.responseContext(ctx)
	req = req.Clone(responseCtx)
	keepLifecycle := false
	defer func() {
		if !keepLifecycle {
			lifecycle.close()
		}
	}()
	res, err := c.do(responseCtx, req)
	if err != nil {
		return nil, fmt.Errorf("execute ListEpisodes request: %w", err)
	}
	if res == nil {
		return nil, fmt.Errorf("execute ListEpisodes request: HTTP client returned nil response")
	}

	result := &ListEpisodesResponse{StatusCode: res.StatusCode, Raw: res}
	switch res.StatusCode {
	case 200:
		var decoded EpisodePage
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode ListEpisodes status 200 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status200 = &decoded
		return result, nil
	case 400:
		var decoded ValidationErr
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode ListEpisodes status 400 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status400 = &decoded
		return result, nil
	case 401:
		_ = res.Body.Close()
		result.Status401 = true
		return result, nil
	case 403:
		_ = res.Body.Close()
		result.Status403 = true
		return result, nil
	case 404:
		_ = res.Body.Close()
		result.Status404 = true
		return result, nil
	default:
		rawBody, readErr := io.ReadAll(io.LimitReader(res.Body, maxDiagnosticBodyBytes))
		_ = res.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read unexpected ListEpisodes response status %d: %w", res.StatusCode, readErr)
		}
		return nil, &UnexpectedStatusError{
			Method:     req.Method,
			URL:        req.URL.String(),
			StatusCode: res.StatusCode,
			Body:       strings.TrimSpace(string(rawBody)),
		}
	}
}

type CreateShowInvitationParams struct {
	ShowSlug ShowSlug
	Body     CreateShowInvitation
}

type CreateShowInvitationResponse struct {
	StatusCode int
	Raw        *http.Response
	Status201  *PendingShowInvitation
	Status400  *ValidationErr
	Status401  bool
	Status403  bool
	Status404  bool
	Status409  bool
}

func (c *Client) NewCreateShowInvitationRequest(ctx context.Context, params CreateShowInvitationParams) (*http.Request, error) {
	if ctx == nil {
		return nil, fmt.Errorf("build CreateShowInvitation request: context must not be nil")
	}
	if params.ShowSlug == "" {
		return nil, fmt.Errorf("build CreateShowInvitation request: required parameter show_slug is empty")
	}
	path := "/s/shows/{show_slug}/invitations"

	path = strings.ReplaceAll(path, "{show_slug}", url.PathEscape(fmt.Sprint(params.ShowSlug)))
	endpoint, err := url.Parse(c.baseURL + path)
	if err != nil {
		return nil, fmt.Errorf("build CreateShowInvitation URL: %w", err)
	}
	var requestBody io.Reader
	encodedBody, err := json.Marshal(params.Body)
	if err != nil {
		return nil, fmt.Errorf("encode CreateShowInvitation JSON body: %w", err)
	}
	requestBody = bytes.NewReader(encodedBody)
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint.String(), requestBody)
	if err != nil {
		return nil, fmt.Errorf("build CreateShowInvitation request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func (c *Client) CreateShowInvitation(ctx context.Context, params CreateShowInvitationParams) (*CreateShowInvitationResponse, error) {

	req, err := c.NewCreateShowInvitationRequest(ctx, params)
	if err != nil {
		return nil, err
	}
	responseCtx, lifecycle := c.responseContext(ctx)
	req = req.Clone(responseCtx)
	keepLifecycle := false
	defer func() {
		if !keepLifecycle {
			lifecycle.close()
		}
	}()
	res, err := c.do(responseCtx, req)
	if err != nil {
		return nil, fmt.Errorf("execute CreateShowInvitation request: %w", err)
	}
	if res == nil {
		return nil, fmt.Errorf("execute CreateShowInvitation request: HTTP client returned nil response")
	}

	result := &CreateShowInvitationResponse{StatusCode: res.StatusCode, Raw: res}
	switch res.StatusCode {
	case 201:
		var decoded PendingShowInvitation
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode CreateShowInvitation status 201 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status201 = &decoded
		return result, nil
	case 400:
		var decoded ValidationErr
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode CreateShowInvitation status 400 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status400 = &decoded
		return result, nil
	case 401:
		_ = res.Body.Close()
		result.Status401 = true
		return result, nil
	case 403:
		_ = res.Body.Close()
		result.Status403 = true
		return result, nil
	case 404:
		_ = res.Body.Close()
		result.Status404 = true
		return result, nil
	case 409:
		_ = res.Body.Close()
		result.Status409 = true
		return result, nil
	default:
		rawBody, readErr := io.ReadAll(io.LimitReader(res.Body, maxDiagnosticBodyBytes))
		_ = res.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read unexpected CreateShowInvitation response status %d: %w", res.StatusCode, readErr)
		}
		return nil, &UnexpectedStatusError{
			Method:     req.Method,
			URL:        req.URL.String(),
			StatusCode: res.StatusCode,
			Body:       strings.TrimSpace(string(rawBody)),
		}
	}
}

type UpdateShowInvitationRoleParams struct {
	ShowSlug     ShowSlug
	InvitationId ShowInvitationID
	Body         UpdateShowMemberRole
}

type UpdateShowInvitationRoleResponse struct {
	StatusCode int
	Raw        *http.Response
	Status200  *PendingShowInvitation
	Status400  *ValidationErr
	Status401  bool
	Status403  bool
	Status404  bool
}

func (c *Client) NewUpdateShowInvitationRoleRequest(ctx context.Context, params UpdateShowInvitationRoleParams) (*http.Request, error) {
	if ctx == nil {
		return nil, fmt.Errorf("build UpdateShowInvitationRole request: context must not be nil")
	}
	if params.ShowSlug == "" {
		return nil, fmt.Errorf("build UpdateShowInvitationRole request: required parameter show_slug is empty")
	}
	if params.InvitationId == "" {
		return nil, fmt.Errorf("build UpdateShowInvitationRole request: required parameter invitation_id is empty")
	}
	path := "/s/shows/{show_slug}/invitations/{invitation_id}"

	path = strings.ReplaceAll(path, "{show_slug}", url.PathEscape(fmt.Sprint(params.ShowSlug)))
	path = strings.ReplaceAll(path, "{invitation_id}", url.PathEscape(fmt.Sprint(params.InvitationId)))
	endpoint, err := url.Parse(c.baseURL + path)
	if err != nil {
		return nil, fmt.Errorf("build UpdateShowInvitationRole URL: %w", err)
	}
	var requestBody io.Reader
	encodedBody, err := json.Marshal(params.Body)
	if err != nil {
		return nil, fmt.Errorf("encode UpdateShowInvitationRole JSON body: %w", err)
	}
	requestBody = bytes.NewReader(encodedBody)
	req, err := http.NewRequestWithContext(ctx, "PUT", endpoint.String(), requestBody)
	if err != nil {
		return nil, fmt.Errorf("build UpdateShowInvitationRole request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func (c *Client) UpdateShowInvitationRole(ctx context.Context, params UpdateShowInvitationRoleParams) (*UpdateShowInvitationRoleResponse, error) {

	req, err := c.NewUpdateShowInvitationRoleRequest(ctx, params)
	if err != nil {
		return nil, err
	}
	responseCtx, lifecycle := c.responseContext(ctx)
	req = req.Clone(responseCtx)
	keepLifecycle := false
	defer func() {
		if !keepLifecycle {
			lifecycle.close()
		}
	}()
	res, err := c.do(responseCtx, req)
	if err != nil {
		return nil, fmt.Errorf("execute UpdateShowInvitationRole request: %w", err)
	}
	if res == nil {
		return nil, fmt.Errorf("execute UpdateShowInvitationRole request: HTTP client returned nil response")
	}

	result := &UpdateShowInvitationRoleResponse{StatusCode: res.StatusCode, Raw: res}
	switch res.StatusCode {
	case 200:
		var decoded PendingShowInvitation
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode UpdateShowInvitationRole status 200 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status200 = &decoded
		return result, nil
	case 400:
		var decoded ValidationErr
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode UpdateShowInvitationRole status 400 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status400 = &decoded
		return result, nil
	case 401:
		_ = res.Body.Close()
		result.Status401 = true
		return result, nil
	case 403:
		_ = res.Body.Close()
		result.Status403 = true
		return result, nil
	case 404:
		_ = res.Body.Close()
		result.Status404 = true
		return result, nil
	default:
		rawBody, readErr := io.ReadAll(io.LimitReader(res.Body, maxDiagnosticBodyBytes))
		_ = res.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read unexpected UpdateShowInvitationRole response status %d: %w", res.StatusCode, readErr)
		}
		return nil, &UnexpectedStatusError{
			Method:     req.Method,
			URL:        req.URL.String(),
			StatusCode: res.StatusCode,
			Body:       strings.TrimSpace(string(rawBody)),
		}
	}
}

type RevokeShowInvitationParams struct {
	ShowSlug     ShowSlug
	InvitationId ShowInvitationID
}

type RevokeShowInvitationResponse struct {
	StatusCode int
	Raw        *http.Response
	Status204  bool
	Status400  *ValidationErr
	Status401  bool
	Status403  bool
	Status404  bool
}

func (c *Client) NewRevokeShowInvitationRequest(ctx context.Context, params RevokeShowInvitationParams) (*http.Request, error) {
	if ctx == nil {
		return nil, fmt.Errorf("build RevokeShowInvitation request: context must not be nil")
	}
	if params.ShowSlug == "" {
		return nil, fmt.Errorf("build RevokeShowInvitation request: required parameter show_slug is empty")
	}
	if params.InvitationId == "" {
		return nil, fmt.Errorf("build RevokeShowInvitation request: required parameter invitation_id is empty")
	}
	path := "/s/shows/{show_slug}/invitations/{invitation_id}"

	path = strings.ReplaceAll(path, "{show_slug}", url.PathEscape(fmt.Sprint(params.ShowSlug)))
	path = strings.ReplaceAll(path, "{invitation_id}", url.PathEscape(fmt.Sprint(params.InvitationId)))
	endpoint, err := url.Parse(c.baseURL + path)
	if err != nil {
		return nil, fmt.Errorf("build RevokeShowInvitation URL: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "DELETE", endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build RevokeShowInvitation request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	return req, nil
}

func (c *Client) RevokeShowInvitation(ctx context.Context, params RevokeShowInvitationParams) (*RevokeShowInvitationResponse, error) {

	req, err := c.NewRevokeShowInvitationRequest(ctx, params)
	if err != nil {
		return nil, err
	}
	responseCtx, lifecycle := c.responseContext(ctx)
	req = req.Clone(responseCtx)
	keepLifecycle := false
	defer func() {
		if !keepLifecycle {
			lifecycle.close()
		}
	}()
	res, err := c.do(responseCtx, req)
	if err != nil {
		return nil, fmt.Errorf("execute RevokeShowInvitation request: %w", err)
	}
	if res == nil {
		return nil, fmt.Errorf("execute RevokeShowInvitation request: HTTP client returned nil response")
	}

	result := &RevokeShowInvitationResponse{StatusCode: res.StatusCode, Raw: res}
	switch res.StatusCode {
	case 204:
		_ = res.Body.Close()
		result.Status204 = true
		return result, nil
	case 400:
		var decoded ValidationErr
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode RevokeShowInvitation status 400 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status400 = &decoded
		return result, nil
	case 401:
		_ = res.Body.Close()
		result.Status401 = true
		return result, nil
	case 403:
		_ = res.Body.Close()
		result.Status403 = true
		return result, nil
	case 404:
		_ = res.Body.Close()
		result.Status404 = true
		return result, nil
	default:
		rawBody, readErr := io.ReadAll(io.LimitReader(res.Body, maxDiagnosticBodyBytes))
		_ = res.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read unexpected RevokeShowInvitation response status %d: %w", res.StatusCode, readErr)
		}
		return nil, &UnexpectedStatusError{
			Method:     req.Method,
			URL:        req.URL.String(),
			StatusCode: res.StatusCode,
			Body:       strings.TrimSpace(string(rawBody)),
		}
	}
}

type ListShowMembersParams struct {
	ShowSlug ShowSlug
}

type ListShowMembersResponse struct {
	StatusCode int
	Raw        *http.Response
	Status200  *ShowMemberCollection
	Status400  *ValidationErr
	Status401  bool
	Status403  bool
	Status404  bool
}

func (c *Client) NewListShowMembersRequest(ctx context.Context, params ListShowMembersParams) (*http.Request, error) {
	if ctx == nil {
		return nil, fmt.Errorf("build ListShowMembers request: context must not be nil")
	}
	if params.ShowSlug == "" {
		return nil, fmt.Errorf("build ListShowMembers request: required parameter show_slug is empty")
	}
	path := "/s/shows/{show_slug}/members"

	path = strings.ReplaceAll(path, "{show_slug}", url.PathEscape(fmt.Sprint(params.ShowSlug)))
	endpoint, err := url.Parse(c.baseURL + path)
	if err != nil {
		return nil, fmt.Errorf("build ListShowMembers URL: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build ListShowMembers request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	return req, nil
}

func (c *Client) ListShowMembers(ctx context.Context, params ListShowMembersParams) (*ListShowMembersResponse, error) {

	req, err := c.NewListShowMembersRequest(ctx, params)
	if err != nil {
		return nil, err
	}
	responseCtx, lifecycle := c.responseContext(ctx)
	req = req.Clone(responseCtx)
	keepLifecycle := false
	defer func() {
		if !keepLifecycle {
			lifecycle.close()
		}
	}()
	res, err := c.do(responseCtx, req)
	if err != nil {
		return nil, fmt.Errorf("execute ListShowMembers request: %w", err)
	}
	if res == nil {
		return nil, fmt.Errorf("execute ListShowMembers request: HTTP client returned nil response")
	}

	result := &ListShowMembersResponse{StatusCode: res.StatusCode, Raw: res}
	switch res.StatusCode {
	case 200:
		var decoded ShowMemberCollection
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode ListShowMembers status 200 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status200 = &decoded
		return result, nil
	case 400:
		var decoded ValidationErr
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode ListShowMembers status 400 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status400 = &decoded
		return result, nil
	case 401:
		_ = res.Body.Close()
		result.Status401 = true
		return result, nil
	case 403:
		_ = res.Body.Close()
		result.Status403 = true
		return result, nil
	case 404:
		_ = res.Body.Close()
		result.Status404 = true
		return result, nil
	default:
		rawBody, readErr := io.ReadAll(io.LimitReader(res.Body, maxDiagnosticBodyBytes))
		_ = res.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read unexpected ListShowMembers response status %d: %w", res.StatusCode, readErr)
		}
		return nil, &UnexpectedStatusError{
			Method:     req.Method,
			URL:        req.URL.String(),
			StatusCode: res.StatusCode,
			Body:       strings.TrimSpace(string(rawBody)),
		}
	}
}

type UpdateShowMemberRoleParams struct {
	ShowSlug ShowSlug
	UserId   UserID
	Body     UpdateShowMemberRole
}

type UpdateShowMemberRoleResponse struct {
	StatusCode int
	Raw        *http.Response
	Status204  bool
	Status400  *ValidationErr
	Status401  bool
	Status403  bool
	Status404  bool
}

func (c *Client) NewUpdateShowMemberRoleRequest(ctx context.Context, params UpdateShowMemberRoleParams) (*http.Request, error) {
	if ctx == nil {
		return nil, fmt.Errorf("build UpdateShowMemberRole request: context must not be nil")
	}
	if params.ShowSlug == "" {
		return nil, fmt.Errorf("build UpdateShowMemberRole request: required parameter show_slug is empty")
	}
	if params.UserId == "" {
		return nil, fmt.Errorf("build UpdateShowMemberRole request: required parameter user_id is empty")
	}
	path := "/s/shows/{show_slug}/members/{user_id}"

	path = strings.ReplaceAll(path, "{show_slug}", url.PathEscape(fmt.Sprint(params.ShowSlug)))
	path = strings.ReplaceAll(path, "{user_id}", url.PathEscape(fmt.Sprint(params.UserId)))
	endpoint, err := url.Parse(c.baseURL + path)
	if err != nil {
		return nil, fmt.Errorf("build UpdateShowMemberRole URL: %w", err)
	}
	var requestBody io.Reader
	encodedBody, err := json.Marshal(params.Body)
	if err != nil {
		return nil, fmt.Errorf("encode UpdateShowMemberRole JSON body: %w", err)
	}
	requestBody = bytes.NewReader(encodedBody)
	req, err := http.NewRequestWithContext(ctx, "PUT", endpoint.String(), requestBody)
	if err != nil {
		return nil, fmt.Errorf("build UpdateShowMemberRole request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func (c *Client) UpdateShowMemberRole(ctx context.Context, params UpdateShowMemberRoleParams) (*UpdateShowMemberRoleResponse, error) {

	req, err := c.NewUpdateShowMemberRoleRequest(ctx, params)
	if err != nil {
		return nil, err
	}
	responseCtx, lifecycle := c.responseContext(ctx)
	req = req.Clone(responseCtx)
	keepLifecycle := false
	defer func() {
		if !keepLifecycle {
			lifecycle.close()
		}
	}()
	res, err := c.do(responseCtx, req)
	if err != nil {
		return nil, fmt.Errorf("execute UpdateShowMemberRole request: %w", err)
	}
	if res == nil {
		return nil, fmt.Errorf("execute UpdateShowMemberRole request: HTTP client returned nil response")
	}

	result := &UpdateShowMemberRoleResponse{StatusCode: res.StatusCode, Raw: res}
	switch res.StatusCode {
	case 204:
		_ = res.Body.Close()
		result.Status204 = true
		return result, nil
	case 400:
		var decoded ValidationErr
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode UpdateShowMemberRole status 400 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status400 = &decoded
		return result, nil
	case 401:
		_ = res.Body.Close()
		result.Status401 = true
		return result, nil
	case 403:
		_ = res.Body.Close()
		result.Status403 = true
		return result, nil
	case 404:
		_ = res.Body.Close()
		result.Status404 = true
		return result, nil
	default:
		rawBody, readErr := io.ReadAll(io.LimitReader(res.Body, maxDiagnosticBodyBytes))
		_ = res.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read unexpected UpdateShowMemberRole response status %d: %w", res.StatusCode, readErr)
		}
		return nil, &UnexpectedStatusError{
			Method:     req.Method,
			URL:        req.URL.String(),
			StatusCode: res.StatusCode,
			Body:       strings.TrimSpace(string(rawBody)),
		}
	}
}

type RemoveShowMemberParams struct {
	ShowSlug ShowSlug
	UserId   UserID
}

type RemoveShowMemberResponse struct {
	StatusCode int
	Raw        *http.Response
	Status204  bool
	Status400  *ValidationErr
	Status401  bool
	Status403  bool
	Status404  bool
}

func (c *Client) NewRemoveShowMemberRequest(ctx context.Context, params RemoveShowMemberParams) (*http.Request, error) {
	if ctx == nil {
		return nil, fmt.Errorf("build RemoveShowMember request: context must not be nil")
	}
	if params.ShowSlug == "" {
		return nil, fmt.Errorf("build RemoveShowMember request: required parameter show_slug is empty")
	}
	if params.UserId == "" {
		return nil, fmt.Errorf("build RemoveShowMember request: required parameter user_id is empty")
	}
	path := "/s/shows/{show_slug}/members/{user_id}"

	path = strings.ReplaceAll(path, "{show_slug}", url.PathEscape(fmt.Sprint(params.ShowSlug)))
	path = strings.ReplaceAll(path, "{user_id}", url.PathEscape(fmt.Sprint(params.UserId)))
	endpoint, err := url.Parse(c.baseURL + path)
	if err != nil {
		return nil, fmt.Errorf("build RemoveShowMember URL: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "DELETE", endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build RemoveShowMember request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	return req, nil
}

func (c *Client) RemoveShowMember(ctx context.Context, params RemoveShowMemberParams) (*RemoveShowMemberResponse, error) {

	req, err := c.NewRemoveShowMemberRequest(ctx, params)
	if err != nil {
		return nil, err
	}
	responseCtx, lifecycle := c.responseContext(ctx)
	req = req.Clone(responseCtx)
	keepLifecycle := false
	defer func() {
		if !keepLifecycle {
			lifecycle.close()
		}
	}()
	res, err := c.do(responseCtx, req)
	if err != nil {
		return nil, fmt.Errorf("execute RemoveShowMember request: %w", err)
	}
	if res == nil {
		return nil, fmt.Errorf("execute RemoveShowMember request: HTTP client returned nil response")
	}

	result := &RemoveShowMemberResponse{StatusCode: res.StatusCode, Raw: res}
	switch res.StatusCode {
	case 204:
		_ = res.Body.Close()
		result.Status204 = true
		return result, nil
	case 400:
		var decoded ValidationErr
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode RemoveShowMember status 400 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status400 = &decoded
		return result, nil
	case 401:
		_ = res.Body.Close()
		result.Status401 = true
		return result, nil
	case 403:
		_ = res.Body.Close()
		result.Status403 = true
		return result, nil
	case 404:
		_ = res.Body.Close()
		result.Status404 = true
		return result, nil
	default:
		rawBody, readErr := io.ReadAll(io.LimitReader(res.Body, maxDiagnosticBodyBytes))
		_ = res.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read unexpected RemoveShowMember response status %d: %w", res.StatusCode, readErr)
		}
		return nil, &UnexpectedStatusError{
			Method:     req.Method,
			URL:        req.URL.String(),
			StatusCode: res.StatusCode,
			Body:       strings.TrimSpace(string(rawBody)),
		}
	}
}

type CreateTeamInvitationParams struct {
	Body CreateTeamInvitation
}

type CreateTeamInvitationResponse struct {
	StatusCode int
	Raw        *http.Response
	Status201  *PendingTeamInvitation
	Status400  *ValidationErr
	Status401  bool
	Status403  bool
	Status409  bool
}

func (c *Client) NewCreateTeamInvitationRequest(ctx context.Context, params CreateTeamInvitationParams) (*http.Request, error) {
	if ctx == nil {
		return nil, fmt.Errorf("build CreateTeamInvitation request: context must not be nil")
	}
	path := "/s/team/invitations"

	endpoint, err := url.Parse(c.baseURL + path)
	if err != nil {
		return nil, fmt.Errorf("build CreateTeamInvitation URL: %w", err)
	}
	var requestBody io.Reader
	encodedBody, err := json.Marshal(params.Body)
	if err != nil {
		return nil, fmt.Errorf("encode CreateTeamInvitation JSON body: %w", err)
	}
	requestBody = bytes.NewReader(encodedBody)
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint.String(), requestBody)
	if err != nil {
		return nil, fmt.Errorf("build CreateTeamInvitation request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func (c *Client) CreateTeamInvitation(ctx context.Context, params CreateTeamInvitationParams) (*CreateTeamInvitationResponse, error) {

	req, err := c.NewCreateTeamInvitationRequest(ctx, params)
	if err != nil {
		return nil, err
	}
	responseCtx, lifecycle := c.responseContext(ctx)
	req = req.Clone(responseCtx)
	keepLifecycle := false
	defer func() {
		if !keepLifecycle {
			lifecycle.close()
		}
	}()
	res, err := c.do(responseCtx, req)
	if err != nil {
		return nil, fmt.Errorf("execute CreateTeamInvitation request: %w", err)
	}
	if res == nil {
		return nil, fmt.Errorf("execute CreateTeamInvitation request: HTTP client returned nil response")
	}

	result := &CreateTeamInvitationResponse{StatusCode: res.StatusCode, Raw: res}
	switch res.StatusCode {
	case 201:
		var decoded PendingTeamInvitation
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode CreateTeamInvitation status 201 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status201 = &decoded
		return result, nil
	case 400:
		var decoded ValidationErr
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode CreateTeamInvitation status 400 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status400 = &decoded
		return result, nil
	case 401:
		_ = res.Body.Close()
		result.Status401 = true
		return result, nil
	case 403:
		_ = res.Body.Close()
		result.Status403 = true
		return result, nil
	case 409:
		_ = res.Body.Close()
		result.Status409 = true
		return result, nil
	default:
		rawBody, readErr := io.ReadAll(io.LimitReader(res.Body, maxDiagnosticBodyBytes))
		_ = res.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read unexpected CreateTeamInvitation response status %d: %w", res.StatusCode, readErr)
		}
		return nil, &UnexpectedStatusError{
			Method:     req.Method,
			URL:        req.URL.String(),
			StatusCode: res.StatusCode,
			Body:       strings.TrimSpace(string(rawBody)),
		}
	}
}

type UpdateTeamInvitationRoleParams struct {
	InvitationId TeamInvitationID
	Body         UpdateTeamMemberRole
}

type UpdateTeamInvitationRoleResponse struct {
	StatusCode int
	Raw        *http.Response
	Status200  *PendingTeamInvitation
	Status400  *ValidationErr
	Status401  bool
	Status403  bool
	Status404  bool
}

func (c *Client) NewUpdateTeamInvitationRoleRequest(ctx context.Context, params UpdateTeamInvitationRoleParams) (*http.Request, error) {
	if ctx == nil {
		return nil, fmt.Errorf("build UpdateTeamInvitationRole request: context must not be nil")
	}
	if params.InvitationId == "" {
		return nil, fmt.Errorf("build UpdateTeamInvitationRole request: required parameter invitation_id is empty")
	}
	path := "/s/team/invitations/{invitation_id}"

	path = strings.ReplaceAll(path, "{invitation_id}", url.PathEscape(fmt.Sprint(params.InvitationId)))
	endpoint, err := url.Parse(c.baseURL + path)
	if err != nil {
		return nil, fmt.Errorf("build UpdateTeamInvitationRole URL: %w", err)
	}
	var requestBody io.Reader
	encodedBody, err := json.Marshal(params.Body)
	if err != nil {
		return nil, fmt.Errorf("encode UpdateTeamInvitationRole JSON body: %w", err)
	}
	requestBody = bytes.NewReader(encodedBody)
	req, err := http.NewRequestWithContext(ctx, "PUT", endpoint.String(), requestBody)
	if err != nil {
		return nil, fmt.Errorf("build UpdateTeamInvitationRole request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func (c *Client) UpdateTeamInvitationRole(ctx context.Context, params UpdateTeamInvitationRoleParams) (*UpdateTeamInvitationRoleResponse, error) {

	req, err := c.NewUpdateTeamInvitationRoleRequest(ctx, params)
	if err != nil {
		return nil, err
	}
	responseCtx, lifecycle := c.responseContext(ctx)
	req = req.Clone(responseCtx)
	keepLifecycle := false
	defer func() {
		if !keepLifecycle {
			lifecycle.close()
		}
	}()
	res, err := c.do(responseCtx, req)
	if err != nil {
		return nil, fmt.Errorf("execute UpdateTeamInvitationRole request: %w", err)
	}
	if res == nil {
		return nil, fmt.Errorf("execute UpdateTeamInvitationRole request: HTTP client returned nil response")
	}

	result := &UpdateTeamInvitationRoleResponse{StatusCode: res.StatusCode, Raw: res}
	switch res.StatusCode {
	case 200:
		var decoded PendingTeamInvitation
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode UpdateTeamInvitationRole status 200 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status200 = &decoded
		return result, nil
	case 400:
		var decoded ValidationErr
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode UpdateTeamInvitationRole status 400 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status400 = &decoded
		return result, nil
	case 401:
		_ = res.Body.Close()
		result.Status401 = true
		return result, nil
	case 403:
		_ = res.Body.Close()
		result.Status403 = true
		return result, nil
	case 404:
		_ = res.Body.Close()
		result.Status404 = true
		return result, nil
	default:
		rawBody, readErr := io.ReadAll(io.LimitReader(res.Body, maxDiagnosticBodyBytes))
		_ = res.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read unexpected UpdateTeamInvitationRole response status %d: %w", res.StatusCode, readErr)
		}
		return nil, &UnexpectedStatusError{
			Method:     req.Method,
			URL:        req.URL.String(),
			StatusCode: res.StatusCode,
			Body:       strings.TrimSpace(string(rawBody)),
		}
	}
}

type RevokeTeamInvitationParams struct {
	InvitationId TeamInvitationID
}

type RevokeTeamInvitationResponse struct {
	StatusCode int
	Raw        *http.Response
	Status204  bool
	Status400  *ValidationErr
	Status401  bool
	Status403  bool
	Status404  bool
}

func (c *Client) NewRevokeTeamInvitationRequest(ctx context.Context, params RevokeTeamInvitationParams) (*http.Request, error) {
	if ctx == nil {
		return nil, fmt.Errorf("build RevokeTeamInvitation request: context must not be nil")
	}
	if params.InvitationId == "" {
		return nil, fmt.Errorf("build RevokeTeamInvitation request: required parameter invitation_id is empty")
	}
	path := "/s/team/invitations/{invitation_id}"

	path = strings.ReplaceAll(path, "{invitation_id}", url.PathEscape(fmt.Sprint(params.InvitationId)))
	endpoint, err := url.Parse(c.baseURL + path)
	if err != nil {
		return nil, fmt.Errorf("build RevokeTeamInvitation URL: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "DELETE", endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build RevokeTeamInvitation request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	return req, nil
}

func (c *Client) RevokeTeamInvitation(ctx context.Context, params RevokeTeamInvitationParams) (*RevokeTeamInvitationResponse, error) {

	req, err := c.NewRevokeTeamInvitationRequest(ctx, params)
	if err != nil {
		return nil, err
	}
	responseCtx, lifecycle := c.responseContext(ctx)
	req = req.Clone(responseCtx)
	keepLifecycle := false
	defer func() {
		if !keepLifecycle {
			lifecycle.close()
		}
	}()
	res, err := c.do(responseCtx, req)
	if err != nil {
		return nil, fmt.Errorf("execute RevokeTeamInvitation request: %w", err)
	}
	if res == nil {
		return nil, fmt.Errorf("execute RevokeTeamInvitation request: HTTP client returned nil response")
	}

	result := &RevokeTeamInvitationResponse{StatusCode: res.StatusCode, Raw: res}
	switch res.StatusCode {
	case 204:
		_ = res.Body.Close()
		result.Status204 = true
		return result, nil
	case 400:
		var decoded ValidationErr
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode RevokeTeamInvitation status 400 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status400 = &decoded
		return result, nil
	case 401:
		_ = res.Body.Close()
		result.Status401 = true
		return result, nil
	case 403:
		_ = res.Body.Close()
		result.Status403 = true
		return result, nil
	case 404:
		_ = res.Body.Close()
		result.Status404 = true
		return result, nil
	default:
		rawBody, readErr := io.ReadAll(io.LimitReader(res.Body, maxDiagnosticBodyBytes))
		_ = res.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read unexpected RevokeTeamInvitation response status %d: %w", res.StatusCode, readErr)
		}
		return nil, &UnexpectedStatusError{
			Method:     req.Method,
			URL:        req.URL.String(),
			StatusCode: res.StatusCode,
			Body:       strings.TrimSpace(string(rawBody)),
		}
	}
}

type ListTeamMembersResponse struct {
	StatusCode int
	Raw        *http.Response
	Status200  *TeamMemberCollection
	Status400  *ValidationErr
	Status401  bool
	Status403  bool
}

func (c *Client) NewListTeamMembersRequest(ctx context.Context) (*http.Request, error) {
	if ctx == nil {
		return nil, fmt.Errorf("build ListTeamMembers request: context must not be nil")
	}
	path := "/s/team/members"

	endpoint, err := url.Parse(c.baseURL + path)
	if err != nil {
		return nil, fmt.Errorf("build ListTeamMembers URL: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build ListTeamMembers request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	return req, nil
}

func (c *Client) ListTeamMembers(ctx context.Context) (*ListTeamMembersResponse, error) {

	req, err := c.NewListTeamMembersRequest(ctx)
	if err != nil {
		return nil, err
	}
	responseCtx, lifecycle := c.responseContext(ctx)
	req = req.Clone(responseCtx)
	keepLifecycle := false
	defer func() {
		if !keepLifecycle {
			lifecycle.close()
		}
	}()
	res, err := c.do(responseCtx, req)
	if err != nil {
		return nil, fmt.Errorf("execute ListTeamMembers request: %w", err)
	}
	if res == nil {
		return nil, fmt.Errorf("execute ListTeamMembers request: HTTP client returned nil response")
	}

	result := &ListTeamMembersResponse{StatusCode: res.StatusCode, Raw: res}
	switch res.StatusCode {
	case 200:
		var decoded TeamMemberCollection
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode ListTeamMembers status 200 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status200 = &decoded
		return result, nil
	case 400:
		var decoded ValidationErr
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode ListTeamMembers status 400 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status400 = &decoded
		return result, nil
	case 401:
		_ = res.Body.Close()
		result.Status401 = true
		return result, nil
	case 403:
		_ = res.Body.Close()
		result.Status403 = true
		return result, nil
	default:
		rawBody, readErr := io.ReadAll(io.LimitReader(res.Body, maxDiagnosticBodyBytes))
		_ = res.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read unexpected ListTeamMembers response status %d: %w", res.StatusCode, readErr)
		}
		return nil, &UnexpectedStatusError{
			Method:     req.Method,
			URL:        req.URL.String(),
			StatusCode: res.StatusCode,
			Body:       strings.TrimSpace(string(rawBody)),
		}
	}
}

type UpdateTeamMemberRoleParams struct {
	UserId UserID
	Body   UpdateTeamMemberRole
}

type UpdateTeamMemberRoleResponse struct {
	StatusCode int
	Raw        *http.Response
	Status204  bool
	Status400  *ValidationErr
	Status401  bool
	Status403  bool
	Status404  bool
}

func (c *Client) NewUpdateTeamMemberRoleRequest(ctx context.Context, params UpdateTeamMemberRoleParams) (*http.Request, error) {
	if ctx == nil {
		return nil, fmt.Errorf("build UpdateTeamMemberRole request: context must not be nil")
	}
	if params.UserId == "" {
		return nil, fmt.Errorf("build UpdateTeamMemberRole request: required parameter user_id is empty")
	}
	path := "/s/team/members/{user_id}"

	path = strings.ReplaceAll(path, "{user_id}", url.PathEscape(fmt.Sprint(params.UserId)))
	endpoint, err := url.Parse(c.baseURL + path)
	if err != nil {
		return nil, fmt.Errorf("build UpdateTeamMemberRole URL: %w", err)
	}
	var requestBody io.Reader
	encodedBody, err := json.Marshal(params.Body)
	if err != nil {
		return nil, fmt.Errorf("encode UpdateTeamMemberRole JSON body: %w", err)
	}
	requestBody = bytes.NewReader(encodedBody)
	req, err := http.NewRequestWithContext(ctx, "PUT", endpoint.String(), requestBody)
	if err != nil {
		return nil, fmt.Errorf("build UpdateTeamMemberRole request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func (c *Client) UpdateTeamMemberRole(ctx context.Context, params UpdateTeamMemberRoleParams) (*UpdateTeamMemberRoleResponse, error) {

	req, err := c.NewUpdateTeamMemberRoleRequest(ctx, params)
	if err != nil {
		return nil, err
	}
	responseCtx, lifecycle := c.responseContext(ctx)
	req = req.Clone(responseCtx)
	keepLifecycle := false
	defer func() {
		if !keepLifecycle {
			lifecycle.close()
		}
	}()
	res, err := c.do(responseCtx, req)
	if err != nil {
		return nil, fmt.Errorf("execute UpdateTeamMemberRole request: %w", err)
	}
	if res == nil {
		return nil, fmt.Errorf("execute UpdateTeamMemberRole request: HTTP client returned nil response")
	}

	result := &UpdateTeamMemberRoleResponse{StatusCode: res.StatusCode, Raw: res}
	switch res.StatusCode {
	case 204:
		_ = res.Body.Close()
		result.Status204 = true
		return result, nil
	case 400:
		var decoded ValidationErr
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode UpdateTeamMemberRole status 400 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status400 = &decoded
		return result, nil
	case 401:
		_ = res.Body.Close()
		result.Status401 = true
		return result, nil
	case 403:
		_ = res.Body.Close()
		result.Status403 = true
		return result, nil
	case 404:
		_ = res.Body.Close()
		result.Status404 = true
		return result, nil
	default:
		rawBody, readErr := io.ReadAll(io.LimitReader(res.Body, maxDiagnosticBodyBytes))
		_ = res.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read unexpected UpdateTeamMemberRole response status %d: %w", res.StatusCode, readErr)
		}
		return nil, &UnexpectedStatusError{
			Method:     req.Method,
			URL:        req.URL.String(),
			StatusCode: res.StatusCode,
			Body:       strings.TrimSpace(string(rawBody)),
		}
	}
}

type RemoveTeamMemberParams struct {
	UserId UserID
}

type RemoveTeamMemberResponse struct {
	StatusCode int
	Raw        *http.Response
	Status204  bool
	Status400  *ValidationErr
	Status401  bool
	Status403  bool
	Status404  bool
}

func (c *Client) NewRemoveTeamMemberRequest(ctx context.Context, params RemoveTeamMemberParams) (*http.Request, error) {
	if ctx == nil {
		return nil, fmt.Errorf("build RemoveTeamMember request: context must not be nil")
	}
	if params.UserId == "" {
		return nil, fmt.Errorf("build RemoveTeamMember request: required parameter user_id is empty")
	}
	path := "/s/team/members/{user_id}"

	path = strings.ReplaceAll(path, "{user_id}", url.PathEscape(fmt.Sprint(params.UserId)))
	endpoint, err := url.Parse(c.baseURL + path)
	if err != nil {
		return nil, fmt.Errorf("build RemoveTeamMember URL: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "DELETE", endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build RemoveTeamMember request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	return req, nil
}

func (c *Client) RemoveTeamMember(ctx context.Context, params RemoveTeamMemberParams) (*RemoveTeamMemberResponse, error) {

	req, err := c.NewRemoveTeamMemberRequest(ctx, params)
	if err != nil {
		return nil, err
	}
	responseCtx, lifecycle := c.responseContext(ctx)
	req = req.Clone(responseCtx)
	keepLifecycle := false
	defer func() {
		if !keepLifecycle {
			lifecycle.close()
		}
	}()
	res, err := c.do(responseCtx, req)
	if err != nil {
		return nil, fmt.Errorf("execute RemoveTeamMember request: %w", err)
	}
	if res == nil {
		return nil, fmt.Errorf("execute RemoveTeamMember request: HTTP client returned nil response")
	}

	result := &RemoveTeamMemberResponse{StatusCode: res.StatusCode, Raw: res}
	switch res.StatusCode {
	case 204:
		_ = res.Body.Close()
		result.Status204 = true
		return result, nil
	case 400:
		var decoded ValidationErr
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode RemoveTeamMember status 400 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status400 = &decoded
		return result, nil
	case 401:
		_ = res.Body.Close()
		result.Status401 = true
		return result, nil
	case 403:
		_ = res.Body.Close()
		result.Status403 = true
		return result, nil
	case 404:
		_ = res.Body.Close()
		result.Status404 = true
		return result, nil
	default:
		rawBody, readErr := io.ReadAll(io.LimitReader(res.Body, maxDiagnosticBodyBytes))
		_ = res.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read unexpected RemoveTeamMember response status %d: %w", res.StatusCode, readErr)
		}
		return nil, &UnexpectedStatusError{
			Method:     req.Method,
			URL:        req.URL.String(),
			StatusCode: res.StatusCode,
			Body:       strings.TrimSpace(string(rawBody)),
		}
	}
}

type WhoamiResponse struct {
	StatusCode int
	Raw        *http.Response
	Status200  *APIKeyWhoami
	Status400  *ValidationErr
	Status401  bool
}

func (c *Client) NewWhoamiRequest(ctx context.Context) (*http.Request, error) {
	if ctx == nil {
		return nil, fmt.Errorf("build Whoami request: context must not be nil")
	}
	path := "/s/whoami"

	endpoint, err := url.Parse(c.baseURL + path)
	if err != nil {
		return nil, fmt.Errorf("build Whoami URL: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build Whoami request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	return req, nil
}

func (c *Client) Whoami(ctx context.Context) (*WhoamiResponse, error) {

	req, err := c.NewWhoamiRequest(ctx)
	if err != nil {
		return nil, err
	}
	responseCtx, lifecycle := c.responseContext(ctx)
	req = req.Clone(responseCtx)
	keepLifecycle := false
	defer func() {
		if !keepLifecycle {
			lifecycle.close()
		}
	}()
	res, err := c.do(responseCtx, req)
	if err != nil {
		return nil, fmt.Errorf("execute Whoami request: %w", err)
	}
	if res == nil {
		return nil, fmt.Errorf("execute Whoami request: HTTP client returned nil response")
	}

	result := &WhoamiResponse{StatusCode: res.StatusCode, Raw: res}
	switch res.StatusCode {
	case 200:
		var decoded APIKeyWhoami
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode Whoami status 200 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status200 = &decoded
		return result, nil
	case 400:
		var decoded ValidationErr
		if err := json.NewDecoder(io.LimitReader(res.Body, maxDecodedBodyBytes)).Decode(&decoded); err != nil {
			_ = res.Body.Close()
			return nil, fmt.Errorf("decode Whoami status 400 response: %w", err)
		}
		_ = res.Body.Close()
		result.Status400 = &decoded
		return result, nil
	case 401:
		_ = res.Body.Close()
		result.Status401 = true
		return result, nil
	default:
		rawBody, readErr := io.ReadAll(io.LimitReader(res.Body, maxDiagnosticBodyBytes))
		_ = res.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read unexpected Whoami response status %d: %w", res.StatusCode, readErr)
		}
		return nil, &UnexpectedStatusError{
			Method:     req.Method,
			URL:        req.URL.String(),
			StatusCode: res.StatusCode,
			Body:       strings.TrimSpace(string(rawBody)),
		}
	}
}

type SSEIdleTimeoutError struct {
	Duration time.Duration
}

func (err *SSEIdleTimeoutError) Error() string {
	return fmt.Sprintf("SSE stream was idle for %s", err.Duration)
}

func (err *SSEIdleTimeoutError) Timeout() bool { return true }

type idleTimeoutBody struct {
	body      io.ReadCloser
	duration  time.Duration
	onTimeout func()

	mu        sync.Mutex
	timer     *time.Timer
	timedOut  bool
	closed    bool
	closeOnce sync.Once
	closeErr  error
}

func newIdleTimeoutBody(body io.ReadCloser, duration time.Duration, onTimeout func()) io.ReadCloser {
	if duration == 0 {
		return body
	}
	wrapped := &idleTimeoutBody{
		body:      body,
		duration:  duration,
		onTimeout: onTimeout,
	}
	wrapped.timer = time.AfterFunc(duration, wrapped.expire)
	return wrapped
}

func (body *idleTimeoutBody) expire() {
	body.mu.Lock()
	if body.closed {
		body.mu.Unlock()
		return
	}
	body.timedOut = true
	body.mu.Unlock()
	if body.onTimeout != nil {
		body.onTimeout()
	}
	_ = body.body.Close()
}

func (body *idleTimeoutBody) Read(buffer []byte) (int, error) {
	read, err := body.body.Read(buffer)
	body.mu.Lock()
	if read > 0 && !body.timedOut && !body.closed {
		body.timer.Reset(body.duration)
	}
	timedOut := body.timedOut
	body.mu.Unlock()
	if timedOut && err != nil {
		return read, &SSEIdleTimeoutError{Duration: body.duration}
	}
	return read, err
}

func (body *idleTimeoutBody) Close() error {
	body.closeOnce.Do(func() {
		body.mu.Lock()
		body.closed = true
		body.timer.Stop()
		body.mu.Unlock()
		body.closeErr = body.body.Close()
	})
	return body.closeErr
}

type SSEStream[T any] struct {
	body         io.ReadCloser
	scanner      *bufio.Scanner
	closeOnce    sync.Once
	nextMu       sync.Mutex
	data         []string
	currentEvent string
	lastEvent    string
	finished     bool
	closeErr     error
	cleanup      func()
}

func NewSSEStream[T any](body io.ReadCloser) *SSEStream[T] {
	return newSSEStream[T](body, defaultSSEIdleTimeout, nil)
}

func newSSEStream[T any](body io.ReadCloser, idleTimeout time.Duration, cleanup func()) *SSEStream[T] {
	body = newIdleTimeoutBody(body, idleTimeout, cleanup)
	return newSSEStreamWithoutIdle[T](body, cleanup)
}

func newReconnectingSSEStream[T any](body io.ReadCloser, cleanup func()) *SSEStream[T] {
	return newSSEStreamWithoutIdle[T](body, cleanup)
}

func newSSEStreamWithoutIdle[T any](body io.ReadCloser, cleanup func()) *SSEStream[T] {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64<<10), maxSSEEventBytes)
	return &SSEStream[T]{body: body, scanner: scanner, cleanup: cleanup}
}

// EventName returns the event field associated with the item most recently
// returned by Next. An omitted event field produces an empty string.
func (stream *SSEStream[T]) EventName() string {
	stream.nextMu.Lock()
	defer stream.nextMu.Unlock()
	return stream.lastEvent
}

// Next returns one decoded SSE data payload without buffering the full stream.
// The boolean is false after clean EOF. Close ends consumption early.
func (stream *SSEStream[T]) Next(ctx context.Context) (T, bool, error) {
	stream.nextMu.Lock()
	defer stream.nextMu.Unlock()

	var zero T
	if stream.finished {
		return zero, false, nil
	}
	if err := ctx.Err(); err != nil {
		_ = stream.Close()
		return zero, false, err
	}
	wake := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = stream.Close()
		case <-wake:
		}
	}()
	defer close(wake)

	for stream.scanner.Scan() {
		line := stream.scanner.Text()
		if line == "" {
			decoded, ok, err := stream.decodeEvent()
			if ok || err != nil {
				return decoded, ok, err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, found := strings.Cut(line, ":")
		if !found {
			value = ""
		}
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "data":
			stream.data = append(stream.data, value)
		case "event":
			stream.currentEvent = value
		}
	}
	if err := ctx.Err(); err != nil {
		stream.finished = true
		_ = stream.Close()
		return zero, false, err
	}
	if err := stream.scanner.Err(); err != nil {
		stream.finished = true
		_ = stream.Close()
		return zero, false, fmt.Errorf("read SSE stream: %w", err)
	}
	stream.finished = true
	decoded, ok, err := stream.decodeEvent()
	_ = stream.Close()
	return decoded, ok, err
}

func (stream *SSEStream[T]) decodeEvent() (T, bool, error) {
	var zero T
	payload := strings.Join(stream.data, "\n")
	stream.data = nil
	stream.lastEvent = stream.currentEvent
	stream.currentEvent = ""
	if strings.TrimSpace(payload) == "" {
		return zero, false, nil
	}
	var decoded T
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		stream.finished = true
		_ = stream.Close()
		return zero, false, fmt.Errorf("decode SSE JSON event: %w", err)
	}
	return decoded, true, nil
}

func (stream *SSEStream[T]) Close() error {
	stream.closeOnce.Do(func() {
		stream.closeErr = stream.body.Close()
		if stream.cleanup != nil {
			stream.cleanup()
		}
	})
	return stream.closeErr
}
