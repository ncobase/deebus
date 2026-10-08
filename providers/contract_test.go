package providers

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestOfficialTaskStatusSamples(t *testing.T) {
	dash := []struct {
		status string
		want   OperationStatus
	}{
		{"PENDING", OperationQueued},
		{"RUNNING", OperationRunning},
		{"SUCCEEDED", OperationSucceeded},
		{"FAILED", OperationFailed},
		{"CANCELED", OperationCancelled},
		{"UNKNOWN", OperationFailed},
	}
	for _, sample := range dash {
		if got := mapDashScopeStatus(sample.status); got != sample.want {
			t.Fatalf("dashscope %s = %s", sample.status, got)
		}
	}
	kling := []struct {
		status string
		want   OperationStatus
	}{
		{"submitted", OperationQueued},
		{"processing", OperationRunning},
		{"succeed", OperationSucceeded},
		{"failed", OperationFailed},
	}
	for _, sample := range kling {
		if got := mapKlingStatus(sample.status); got != sample.want {
			t.Fatalf("kling %s = %s", sample.status, got)
		}
	}
	jimeng := []struct {
		status string
		want   OperationStatus
	}{
		{"in_queue", OperationQueued},
		{"generating", OperationRunning},
		{"done", OperationSucceeded},
		{"not_found", OperationFailed},
		{"expired", OperationFailed},
	}
	for _, sample := range jimeng {
		if got := mapJimengStatus(sample.status); got != sample.want {
			t.Fatalf("jimeng %s = %s", sample.status, got)
		}
	}
	doubao := []struct {
		status string
		want   OperationStatus
	}{
		{"queued", OperationQueued},
		{"running", OperationRunning},
		{"succeeded", OperationSucceeded},
		{"failed", OperationFailed},
		{"cancelled", OperationCancelled},
	}
	for _, sample := range doubao {
		if got := mapDoubaoStatus(sample.status); got != sample.want {
			t.Fatalf("doubao %s = %s", sample.status, got)
		}
	}
	batches := []struct {
		status string
		want   OperationStatus
	}{
		{"validating", OperationRunning},
		{"in_progress", OperationRunning},
		{"finalizing", OperationRunning},
		{"cancelling", OperationRunning},
		{"completed", OperationSucceeded},
		{"failed", OperationFailed},
		{"expired", OperationFailed},
		{"cancelled", OperationCancelled},
	}
	for _, sample := range batches {
		if got := mapOpenAIBatchStatus(sample.status); got != sample.want {
			t.Fatalf("openai batch %s = %s", sample.status, got)
		}
	}
	anthropic := []struct {
		status string
		want   OperationStatus
	}{
		{"in_progress", OperationRunning},
		{"canceling", OperationRunning},
		{"ended", OperationSucceeded},
	}
	for _, sample := range anthropic {
		if got := mapAnthropicBatchStatus(sample.status); got != sample.want {
			t.Fatalf("anthropic batch %s = %s", sample.status, got)
		}
	}
}

func TestOfficialResultPayloads(t *testing.T) {
	var dash dashScopeTask
	if err := json.Unmarshal([]byte(`{"output":{"task_id":"t","task_status":"SUCCEEDED","video_url":"https://cdn.example/v.mp4"}}`), &dash); err != nil {
		t.Fatal(err)
	}
	if op := dash.asOperation("qwen", ""); op.Status != OperationSucceeded || op.Result.Videos[0].URL == "" {
		t.Fatalf("dashscope video = %#v", op)
	}

	var kling klingEnvelope
	if err := json.Unmarshal([]byte(`{"code":0,"data":{"task_status":"failed","task_status_msg":"rejected","task_result":{"videos":[]}}}`), &kling); err != nil {
		t.Fatal(err)
	}
	if op := kling.asOperation(); op.Status != OperationFailed || op.Error != "rejected" {
		t.Fatalf("kling = %#v", op)
	}

	var jimeng jimengEnvelope
	if err := json.Unmarshal([]byte(`{"code":10000,"data":{"status":"expired"}}`), &jimeng); err != nil {
		t.Fatal(err)
	}
	if op := jimeng.asOperation("key/task"); op.Status != OperationFailed {
		t.Fatalf("jimeng = %#v", op)
	}
}

func TestBatchPartialResults(t *testing.T) {
	openai, err := parseOpenAIBatchResults([]byte(
		"{\"custom_id\":\"ok\",\"response\":{\"status_code\":200,\"body\":{\"choices\":[{\"message\":{\"content\":\"yes\"}}]}}}\n" +
			"{\"custom_id\":\"bad\",\"response\":{\"status_code\":400,\"body\":{\"error\":{\"message\":\"invalid\"}}}}\n",
	))
	if err != nil {
		t.Fatal(err)
	}
	if openai[0].Content != "yes" || openai[1].Error != "invalid" {
		t.Fatalf("openai results = %#v", openai)
	}
	anthropic, err := parseAnthropicBatchResults([]byte(
		"{\"custom_id\":\"ok\",\"result\":{\"type\":\"succeeded\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"yes\"}]}}}\n" +
			"{\"custom_id\":\"old\",\"result\":{\"type\":\"expired\"}}\n" +
			"{\"custom_id\":\"stop\",\"result\":{\"type\":\"canceled\",\"error\":{\"message\":\"canceled by user\"}}}\n",
	))
	if err != nil {
		t.Fatal(err)
	}
	if anthropic[0].Content != "yes" || anthropic[1].Error != "expired" || anthropic[2].Error != "canceled by user" {
		t.Fatalf("anthropic results = %#v", anthropic)
	}
}

func TestSignatureVectors(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "https://hunyuan.tencentcloudapi.com/", strings.NewReader(`{"Limit":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := signTC3(req, []byte(`{"Limit":1}`), "AKIDEXAMPLE", "secret", "hunyuan", "ChatCompletions", hunyuanAPIVersion, "ap-guangzhou", time.Unix(1551113065, 0)); err != nil {
		t.Fatal(err)
	}
	const wantTC3 = "TC3-HMAC-SHA256 Credential=AKIDEXAMPLE/2019-02-25/hunyuan/tc3_request, SignedHeaders=content-type;host;x-tc-action, Signature=991f703d2eb838a0b0598c06eae314587d27910ae5c43c36f21f2648e958f5fd"
	if req.Header.Get("Authorization") != wantTC3 {
		t.Fatalf("tc3 = %s", req.Header.Get("Authorization"))
	}

	volc, err := http.NewRequest(http.MethodPost, "https://visual.volcengineapi.com/?Action=CVSync2AsyncSubmitTask&Version=2022-08-31", strings.NewReader(`{"prompt":"a"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := signVolcengine(volc, []byte(`{"prompt":"a"}`), "AK", "sek", jimengRegion, jimengService, time.Unix(1700000000, 0)); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(volc.Header.Get("Authorization"), "Signature=afbde79f0fec5195ec2e203340a7689f3a74d8de2e076398de3f291efc72a28a") {
		t.Fatalf("volc = %s", volc.Header.Get("Authorization"))
	}
}
