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

func TestGeminiVideoOperation(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, ":predictLongRunning"):
			body, _ := io.ReadAll(r.Body)
			gotBody = string(body)
			_, _ = w.Write([]byte(`{"name":"models/veo/operations/op1"}`))
		case strings.HasSuffix(r.URL.Path, ":cancel"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		case strings.Contains(r.URL.Path, "/operations/op1"):
			_, _ = w.Write([]byte(`{"done":true,"response":{"generateVideoResponse":{"generatedSamples":[{"video":{"uri":"` + srvURLPlaceholder(r) + `/video.mp4","mimeType":"video/mp4"}}]}}}`))
		case strings.HasSuffix(r.URL.Path, "/video.mp4"):
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write([]byte("MP4"))
		default:
			t.Errorf("path = %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p := NewGemini(Config{APIKey: "key", BaseURL: srv.URL, Timeout: time.Second})
	op, err := p.SubmitOperation(context.Background(), &OperationRequest{
		Model: "veo", Kind: OperationVideo, Prompt: "a lake", AspectRatio: "16:9",
	})
	if err != nil {
		t.Fatal(err)
	}
	if op.ID != "models/veo/operations/op1" || !strings.Contains(gotBody, `"aspectRatio":"16:9"`) {
		t.Fatalf("submit = %#v body=%s", op, gotBody)
	}
	got, err := p.GetOperation(context.Background(), op.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != OperationSucceeded || len(got.Result.Videos) != 1 {
		t.Fatalf("get = %#v", got)
	}
	asset, err := p.ReadOperation(context.Background(), op.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(asset.Data) != "MP4" {
		t.Fatalf("asset = %q", asset.Data)
	}
	if err := p.CancelOperation(context.Background(), op.ID); err != nil {
		t.Fatal(err)
	}
}

func srvURLPlaceholder(r *http.Request) string {
	return "http://" + r.Host
}

func TestOpenAIVideoOperation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/videos":
			_, _ = w.Write([]byte(`{"id":"video_1","model":"sora-2","status":"in_progress","progress":10}`))
		case "/v1/videos/video_1":
			_, _ = w.Write([]byte(`{"id":"video_1","model":"sora-2","status":"completed","progress":100}`))
		case "/v1/videos/video_1/content":
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write([]byte("VID"))
		default:
			t.Errorf("path = %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p := NewOpenAI(Config{APIKey: "sk-test", BaseURL: srv.URL, Timeout: time.Second})
	op, err := p.SubmitOperation(context.Background(), &OperationRequest{
		Model: "sora-2", Kind: OperationVideo, Prompt: "a cat", DurationSeconds: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if op.Status != OperationRunning || op.Progress != 10 {
		t.Fatalf("submit = %#v", op)
	}
	got, err := p.GetOperation(context.Background(), "video_1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != OperationSucceeded || !strings.HasSuffix(got.Result.Videos[0].URL, "/v1/videos/video_1/content") {
		t.Fatalf("get = %#v", got)
	}
	asset, err := p.ReadOperation(context.Background(), "video_1")
	if err != nil {
		t.Fatal(err)
	}
	if string(asset.Data) != "VID" {
		t.Fatalf("asset = %q", asset.Data)
	}
	if err := p.CancelOperation(context.Background(), "video_1"); err == nil || IsFallback(err) {
		t.Fatal("openai video cancel must not fall back")
	}
}

func TestDashScopeImageAndVideoTasks(t *testing.T) {
	var imageBody, videoBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/text2image/image-synthesis"):
			if r.Header.Get("X-DashScope-Async") != "enable" {
				t.Errorf("async header = %s", r.Header.Get("X-DashScope-Async"))
			}
			body, _ := io.ReadAll(r.Body)
			imageBody = string(body)
			_, _ = w.Write([]byte(`{"output":{"task_id":"img1","task_status":"PENDING"}}`))
		case strings.HasSuffix(r.URL.Path, "/video-synthesis"):
			body, _ := io.ReadAll(r.Body)
			videoBody = string(body)
			_, _ = w.Write([]byte(`{"output":{"task_id":"vid1","task_status":"PENDING"}}`))
		case strings.HasSuffix(r.URL.Path, "/tasks/img1"):
			_, _ = w.Write([]byte(`{"output":{"task_id":"img1","task_status":"SUCCEEDED","results":[{"url":"` + "http://" + r.Host + `/out.png"}]}}`))
		case strings.HasSuffix(r.URL.Path, "/out.png"):
			_, _ = w.Write([]byte("PNG"))
		default:
			t.Errorf("path = %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p := NewOpenAICompatible("qwen", Config{APIKey: "sk", BaseURL: srv.URL + "/compatible-mode/v1", Timeout: time.Second})
	image, err := p.SubmitOperation(context.Background(), &OperationRequest{
		Model: "wan2.6-t2i", Kind: OperationImage, Prompt: "a shop", Size: "1024x1024",
	})
	if err != nil {
		t.Fatal(err)
	}
	if image.ID != "img1" || image.Status != OperationQueued || !strings.Contains(imageBody, "1024*1024") {
		t.Fatalf("image = %#v body=%s", image, imageBody)
	}
	got, err := p.GetOperation(context.Background(), "img1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != OperationSucceeded || got.Result.Images[0].URL == "" {
		t.Fatalf("get = %#v", got)
	}
	asset, err := p.ReadOperation(context.Background(), "img1")
	if err != nil {
		t.Fatal(err)
	}
	if string(asset.Data) != "PNG" {
		t.Fatalf("asset = %q", asset.Data)
	}
	video, err := p.SubmitOperation(context.Background(), &OperationRequest{
		Model: "wan2.6-t2v", Kind: OperationVideo, Prompt: "a road", DurationSeconds: 5, AspectRatio: "16:9",
	})
	if err != nil {
		t.Fatal(err)
	}
	if video.ID != "vid1" || !strings.Contains(videoBody, `"duration":5`) || !strings.Contains(videoBody, `"ratio":"16:9"`) {
		t.Fatalf("video = %#v body=%s", video, videoBody)
	}
}

func TestZhipuVideoOperation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/paas/v4/videos/generations":
			_, _ = w.Write([]byte(`{"id":"task1","task_status":"PROCESSING","model":"cogvideox-3"}`))
		case "/api/paas/v4/async-result/task1":
			_, _ = w.Write([]byte(`{"id":"task1","task_status":"SUCCESS","video_result":[{"url":"https://example.com/a.mp4"}]}`))
		default:
			t.Errorf("path = %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p := NewOpenAICompatible("zhipu", Config{APIKey: "key", BaseURL: srv.URL + "/api/paas/v4", Timeout: time.Second})
	op, err := p.SubmitOperation(context.Background(), &OperationRequest{
		Model: "cogvideox-3", Kind: OperationVideo, Prompt: "a city",
	})
	if err != nil {
		t.Fatal(err)
	}
	if op.Status != OperationRunning || op.ID != "task1" {
		t.Fatalf("submit = %#v", op)
	}
	got, err := p.GetOperation(context.Background(), "task1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != OperationSucceeded || got.Result.Videos[0].URL != "https://example.com/a.mp4" {
		t.Fatalf("get = %#v", got)
	}
}

func TestReadOperationDoesNotSendCredentialOffHost(t *testing.T) {
	var sawAuth bool
	assetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("api-key") != "" {
			sawAuth = true
		}
		_, _ = w.Write([]byte("OK"))
	}))
	defer assetSrv.Close()
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"task1","task_status":"SUCCESS","video_result":[{"url":"` + assetSrv.URL + `/a.mp4"}]}`))
	}))
	defer apiSrv.Close()

	p := NewOpenAICompatible("zhipu", Config{APIKey: "key", BaseURL: apiSrv.URL + "/api/paas/v4", Timeout: time.Second})
	asset, err := p.ReadOperation(context.Background(), "task1")
	if err != nil {
		t.Fatal(err)
	}
	if string(asset.Data) != "OK" || sawAuth {
		t.Fatalf("asset=%q leaked=%v", asset.Data, sawAuth)
	}
}
