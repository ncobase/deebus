package providers

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestJimengSubmitsSignedVisualTask(t *testing.T) {
	var action, auth, bodyText string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		action = r.URL.Query().Get("Action")
		auth = r.Header.Get("Authorization")
		if r.URL.Query().Get("Version") != jimengVersion {
			t.Errorf("version = %s", r.URL.Query().Get("Version"))
		}
		if strings.Contains(auth, "sek") {
			t.Fatal("secret was sent")
		}
		body, _ := io.ReadAll(r.Body)
		bodyText = string(body)
		if action == "CVSync2AsyncSubmitTask" {
			_, _ = w.Write([]byte(`{"code":10000,"message":"Success","data":{"task_id":"task1"}}`))
			return
		}
		if action == "CVSync2AsyncGetResult" {
			if !strings.Contains(bodyText, `return_url`) || !strings.Contains(bodyText, `task1`) {
				t.Errorf("get body = %s", bodyText)
			}
			_, _ = w.Write([]byte(`{"code":10000,"data":{"status":"done","image_urls":["https://example.com/a.png"]}}`))
			return
		}
		t.Errorf("action = %s", action)
		http.NotFound(w, r)
	}))
	defer srv.Close()

	p := NewJimeng(Config{AccessKey: "AK", Secret: "sek", BaseURL: srv.URL, Timeout: time.Second})
	op, err := p.SubmitOperation(context.Background(), &OperationRequest{
		Kind: OperationImage, Prompt: "a lake",
	})
	if err != nil {
		t.Fatal(err)
	}
	if op.ID != "jimeng_t2i_v40/task1" || !strings.HasPrefix(auth, "HMAC-SHA256 ") {
		t.Fatalf("op=%#v auth=%s", op, auth)
	}
	if !strings.Contains(bodyText, `"req_key":"jimeng_t2i_v40"`) {
		t.Fatalf("body = %s", bodyText)
	}
	got, err := p.GetOperation(context.Background(), op.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != OperationSucceeded || got.Result.Images[0].URL != "https://example.com/a.png" {
		t.Fatalf("get = %#v", got)
	}
}

func TestVolcengineSignatureChangesWithBody(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	first := volcAuthorization(t, []byte(`{"prompt":"a"}`), now)
	second := volcAuthorization(t, []byte(`{"prompt":"b"}`), now)
	if first == second || !strings.Contains(first, "Credential=AK/") || !strings.Contains(first, "/cn-north-1/cv/request") {
		t.Fatalf("authorization = %s", first)
	}
}

func volcAuthorization(t *testing.T, body []byte, now time.Time) string {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "https://visual.volcengineapi.com/?Action=CVSync2AsyncSubmitTask&Version=2022-08-31", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	if err := signVolcengine(req, body, "AK", "sek", jimengRegion, jimengService, now); err != nil {
		t.Fatal(err)
	}
	return req.Header.Get("Authorization")
}
