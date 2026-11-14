// Package restapi is the little JSON-over-HTTP client the sandbox clouds
// without a Go SDK (Runloop, E2B) are called with: one request, a JSON
// body each way, and an error that says whether the cloud answered.
package restapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
)

// maxBody bounds any response body read.
const maxBody = 8 << 20

// Error is a response the cloud answered with an error status.
type Error struct {
	Op      string
	Status  int
	Message string
	// Code is the cloud's machine-readable error code, when it sends one.
	Code string
	// Header is the response's headers (Retry-After, Should-Retry).
	Header http.Header
}

func (e *Error) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("%s: the cloud answered %d", e.Op, e.Status)
	}

	return fmt.Sprintf("%s: %s (%d)", e.Op, e.Message, e.Status)
}

// Client calls one API. Header sets the authentication on every request.
type Client struct {
	BaseURL string
	HTTP    *http.Client
	Header  http.Header
}

// Do sends method path with in as the JSON body (nil for none) and decodes
// the answer into out (nil to discard it). It returns the response headers.
func (c *Client) Do(ctx context.Context, op, method, path string, in, out any) (http.Header, error) {
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return nil, fmt.Errorf("%s: encode request: %w", op, err)
		}
		body = bytes.NewReader(data)
	}

	return c.DoRaw(ctx, op, method, path, "application/json", body, out)
}

// DoRaw is Do with a body of any content type.
func (c *Client) DoRaw(ctx context.Context, op, method, path, contentType string, body io.Reader, out any) (http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.BaseURL, "/")+path, body)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	for k, vs := range c.Header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if body != nil {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "airship")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return resp.Header, fmt.Errorf("%s: read response: %w", op, err)
	}
	if resp.StatusCode >= http.StatusMultipleChoices {
		return resp.Header, &Error{Op: op, Status: resp.StatusCode, Message: message(data), Code: code(data), Header: resp.Header}
	}
	if out != nil && len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return resp.Header, fmt.Errorf("%s: unreadable response: %w", op, err)
		}
	}

	return resp.Header, nil
}

// message pulls a human message out of an error body, whatever its shape.
func message(body []byte) string {
	var parsed struct {
		Message any    `json:"message"`
		Error   any    `json:"error"`
		Detail  string `json:"detail"`
	}
	if json.Unmarshal(body, &parsed) == nil {
		for _, candidate := range []any{parsed.Message, parsed.Error, parsed.Detail} {
			switch m := candidate.(type) {
			case string:
				if m != "" {
					return m
				}
			case map[string]any:
				if s, ok := m["message"].(string); ok && s != "" {
					return s
				}
			}
		}
	}
	text := strings.TrimSpace(string(body))
	if len(text) > 300 {
		text = text[:300]
	}

	return text
}

func code(body []byte) string {
	var parsed struct {
		ErrorCode string `json:"error_code"`
	}
	_ = json.Unmarshal(body, &parsed)

	return parsed.ErrorCode
}

// Status is the HTTP status of an Error, or 0.
func Status(err error) int {
	var apiErr *Error
	if errors.As(err, &apiErr) {
		return apiErr.Status
	}

	return 0
}

// IsNotFound reports a 404.
func IsNotFound(err error) bool { return Status(err) == http.StatusNotFound }

// IsAuth reports a refused credential.
func IsAuth(err error) bool {
	s := Status(err)

	return s == http.StatusUnauthorized || s == http.StatusForbidden
}

// IsTransport reports an error where the cloud's answer, if any, was lost:
// the request may or may not have been acted on.
func IsTransport(err error) bool {
	if err == nil || Status(err) != 0 {
		return false
	}
	var netErr net.Error

	return errors.As(err, &netErr) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF)
}

// IsTransient reports an error worth trying again as is: lost answers,
// throttling and server errors.
func IsTransient(err error) bool {
	s := Status(err)

	return IsTransport(err) || s == http.StatusTooManyRequests || s >= http.StatusInternalServerError
}

// Message is the cloud's message for an Error, or the error's text.
func Message(err error) string {
	var apiErr *Error
	if errors.As(err, &apiErr) && apiErr.Message != "" {
		return apiErr.Message
	}

	return err.Error()
}
