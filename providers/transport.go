package providers

import (
	"bufio"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const (
	// maxBufferedBody caps a fully buffered provider response.
	// Streams are read incrementally and use maxStreamLine instead.
	maxBufferedBody = 32 << 20
	maxErrorBody    = 64 << 10
	maxStreamLine   = 4 << 20

	// maxRetryAfter caps Retry-After so a hostile or mistaken value cannot
	// stall a worker for hours.
	maxRetryAfter = 2 * time.Minute
)

var errResponseTooLarge = errors.New("response body exceeds limit")

var (
	querySecretPattern = regexp.MustCompile(`(?i)([?&](?:key|api_key|apikey|access_token|token|client_secret)=)[^&\s"]+`)
	bearerPattern      = regexp.MustCompile(`(?i)(bearer\s+)\S+`)
	skPattern          = regexp.MustCompile(`\bsk-[A-Za-z0-9_\-]{8,}`)
)

func newHTTPClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	var transport *http.Transport
	if base, ok := http.DefaultTransport.(*http.Transport); ok {
		transport = base.Clone()
	} else {
		transport = &http.Transport{}
	}
	transport.ForceAttemptHTTP2 = true
	transport.MaxIdleConns = 100
	transport.MaxIdleConnsPerHost = 20
	transport.IdleConnTimeout = 90 * time.Second
	transport.TLSHandshakeTimeout = 10 * time.Second
	transport.ResponseHeaderTimeout = timeout
	transport.ExpectContinueTimeout = time.Second
	transport.TLSClientConfig = &tls.Config{
		MinVersion: tls.VersionTLS12,
		NextProtos: []string{"h2", "http/1.1"},
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: userAgentTransport{base: transport},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("redirects are not followed")
		},
	}
}

type userAgentTransport struct {
	base http.RoundTripper
}

// RoundTrip sets a deebus User-Agent when the request does not already have one.
func (t userAgentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Header.Get("User-Agent") == "" {
		cloned := req.Clone(req.Context())
		cloned.Header.Set("User-Agent", "deebus")
		req = cloned
	}
	return t.base.RoundTrip(req)
}

func newStreamScanner(r io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64<<10), maxStreamLine)
	return scanner
}

func readResponseBody(r io.Reader) ([]byte, error) {
	body, err := readAtMost(r, maxBufferedBody)
	if errors.Is(err, errResponseTooLarge) {
		return nil, fmt.Errorf("response body exceeds %d bytes", maxBufferedBody)
	}
	return body, err
}

func readErrorBody(r io.Reader) []byte {
	body, _ := readAtMost(r, maxErrorBody)
	return body
}

func readAtMost(r io.Reader, limit int64) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if int64(len(body)) > limit {
		return body[:limit], errResponseTooLarge
	}
	return body, err
}

func redactSensitive(s string) string {
	s = querySecretPattern.ReplaceAllString(s, "${1}REDACTED")
	s = bearerPattern.ReplaceAllString(s, "${1}REDACTED")
	s = skPattern.ReplaceAllString(s, "sk-REDACTED")
	return s
}

// ValidateHeaderSafety rejects header and credential values that could split
// an HTTP header or smuggle a second header.
func ValidateHeaderSafety(headers map[string]string, fields map[string]string) error {
	for name, value := range fields {
		if err := rejectControlChars(name, value); err != nil {
			return err
		}
	}
	for name, value := range headers {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("header name is empty")
		}
		if strings.ContainsAny(name, " \t\r\n\x00") {
			return fmt.Errorf("header %q has an invalid name", name)
		}
		if err := rejectControlChars("header "+name, value); err != nil {
			return err
		}
	}
	return nil
}

func rejectControlChars(field, value string) error {
	if strings.ContainsAny(value, "\r\n\x00") {
		return fmt.Errorf("%s contains invalid control characters", field)
	}
	return nil
}
