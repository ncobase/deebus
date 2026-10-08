package providers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestKlingTokenUsesAccessKeyAndSecret(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	token, err := KlingToken("ak-test", "secret-test", now)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token parts = %d", len(parts))
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims klingClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatal(err)
	}
	if claims.Issuer != "ak-test" || claims.ExpiresAt != now.Add(30*time.Minute).Unix() {
		t.Fatalf("claims = %#v", claims)
	}
	mac := hmac.New(sha256.New, []byte("secret-test"))
	_, _ = mac.Write([]byte(parts[0] + "." + parts[1]))
	if base64.RawURLEncoding.EncodeToString(mac.Sum(nil)) != parts[2] {
		t.Fatal("signature does not match secret")
	}
	if strings.Contains(token, "secret-test") {
		t.Fatal("secret was embedded in the token")
	}
}

func TestKlingVideoUsesBearerJWT(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		if r.URL.Path != "/v1/videos/text2video" {
			t.Errorf("path = %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"duration":"5"`) {
			t.Errorf("body = %s", body)
		}
		_, _ = w.Write([]byte(`{"code":0,"message":"SUCCEED","data":{"task_id":"task1","task_status":"submitted"}}`))
	}))
	defer srv.Close()

	p := NewKling(Config{AccessKey: "ak", Secret: "sek", BaseURL: srv.URL, Timeout: time.Second})
	op, err := p.SubmitOperation(context.Background(), &OperationRequest{
		Model: "kling-v1", Kind: OperationVideo, Prompt: "a lake", DurationSeconds: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if op.ID != "text2video/task1" || op.Status != OperationQueued {
		t.Fatalf("op = %#v", op)
	}
	if !strings.HasPrefix(auth, "Bearer ") || strings.Contains(auth, "sek") {
		t.Fatalf("authorization = %s", auth)
	}
}

func TestValidateAuthChoosesOneCredentialStyle(t *testing.T) {
	if err := ValidateAuth("kling", "", "", "ak", "sek", nil, false); err != nil {
		t.Fatal(err)
	}
	if err := ValidateAuth("openai", "sk", "", "ak", "sek", nil, false); err == nil {
		t.Fatal("expected mutual exclusion")
	}
	if err := ValidateAuth("openai", "", "", "ak", "sek", nil, false); err == nil {
		t.Fatal("openai must reject access key")
	}
	if err := ValidateAuth("kling", "sk", "", "", "", nil, false); err == nil {
		t.Fatal("kling must reject api key")
	}
}
