package providers

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestReadAtMostRejectsOversizedBody(t *testing.T) {
	body, err := readAtMost(strings.NewReader("abcdef"), 3)
	if !errors.Is(err, errResponseTooLarge) {
		t.Fatalf("err = %v", err)
	}
	if string(body) != "abc" {
		t.Fatalf("body = %q", body)
	}
}

func TestRedactSensitiveHidesCredentials(t *testing.T) {
	got := redactSensitive("Get https://example.com/v1?key=super-secret&x=1 failed, token sk-abcdefghijklmnopqrst")
	if strings.Contains(got, "super-secret") || strings.Contains(got, "sk-abcdefghijklmnopqrst") {
		t.Fatalf("redaction missed secrets: %s", got)
	}
	if !strings.Contains(got, "key=REDACTED") || !strings.Contains(got, "sk-REDACTED") {
		t.Fatalf("redaction markers missing: %s", got)
	}
}

func TestParseRetryAfterIsCapped(t *testing.T) {
	h := make(http.Header)
	h.Set("Retry-After", "86400")
	if got := parseRetryAfter(h); got != maxRetryAfter {
		t.Fatalf("retry-after = %s, want %s", got, maxRetryAfter)
	}
}

func TestHTTPClientRefusesRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1/secret", http.StatusFound)
	}))
	defer srv.Close()

	client := newHTTPClient(time.Second)
	resp, err := client.Get(srv.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatal("expected redirect to be refused")
	}
	if !strings.Contains(err.Error(), "redirects are not followed") {
		t.Fatalf("err = %v", err)
	}
}

func TestNetworkErrorRedactsURLQuery(t *testing.T) {
	err := networkError("gemini", errors.New(`Get "https://generativelanguage.googleapis.com/v1?key=api-key": dial failed`))
	if strings.Contains(err.Error(), "api-key") {
		t.Fatalf("error leaked api key: %s", err.Error())
	}
}

func TestValidateHeaderSafetyRejectsCRLF(t *testing.T) {
	err := ValidateHeaderSafety(map[string]string{"X-Test": "a\r\nb"}, nil)
	if err == nil {
		t.Fatal("expected header error")
	}
}

func TestReadResponseBodyWithinLimit(t *testing.T) {
	body, err := readResponseBody(strings.NewReader("ok"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "ok" {
		t.Fatalf("body = %q", body)
	}
	if _, err := readResponseBody(io.LimitReader(strings.NewReader(strings.Repeat("x", 8)), 8)); err != nil {
		t.Fatal(err)
	}
}
