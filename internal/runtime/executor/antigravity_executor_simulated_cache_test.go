package executor

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/cache"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v6/sdk/translator"
	"github.com/tidwall/gjson"
)

func TestAntigravityExecute_SimulatedCacheProgressesAcrossTurns(t *testing.T) {
	cache.SetSimulatedCacheConfig(config.SimulatedCacheConfig{
		Enabled:         true,
		MissProbability: 0,
		TTLSeconds:      300,
		RetentionRatio:  0.7,
	})
	cache.ClearSignatureCache("")
	t.Cleanup(func() {
		cache.SetSimulatedCacheConfig(config.SimulatedCacheConfig{
			Enabled:         true,
			MissProbability: 0,
			TTLSeconds:      300,
			RetentionRatio:  0.7,
		})
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"response":{"responseId":"resp_1","modelVersion":"claude-sonnet-4-5-thinking","candidates":[{"finishReason":"STOP","content":{"role":"model","parts":[{"text":"ok"}]}}],"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":20,"totalTokenCount":120}}}`))
	}))
	defer server.Close()

	exec := NewAntigravityExecutor(&config.Config{SimulatedCache: config.SimulatedCacheConfig{Enabled: true, MissProbability: 0, TTLSeconds: 300, RetentionRatio: 0.7}})
	auth := testAntigravityAuth(server.URL)
	payload := []byte(`{"model":"claude-sonnet-4-5-thinking","messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`)
	ctx := context.WithValue(context.Background(), "gin", ginContextWithAPIKey("sim-cache-api-key"))
	opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("claude"), OriginalRequest: payload}
	req := cliproxyexecutor.Request{Model: "claude-sonnet-4-5-thinking", Payload: payload}

	first, err := exec.Execute(ctx, auth, req, opts)
	if err != nil {
		t.Fatalf("first Execute() error = %v", err)
	}
	if got := gjson.GetBytes(first.Payload, "usage.cache_creation_input_tokens").Int(); got != 100 {
		t.Fatalf("first cache_creation_input_tokens = %d, want 100", got)
	}
	if got := gjson.GetBytes(first.Payload, "usage.cache_read_input_tokens").Int(); got != 0 {
		t.Fatalf("first cache_read_input_tokens = %d, want 0", got)
	}

	second, err := exec.Execute(ctx, auth, req, opts)
	if err != nil {
		t.Fatalf("second Execute() error = %v", err)
	}
	if got := gjson.GetBytes(second.Payload, "usage.cache_read_input_tokens").Int(); got != 70 {
		t.Fatalf("second cache_read_input_tokens = %d, want 70", got)
	}
	if got := gjson.GetBytes(second.Payload, "usage.cache_creation_input_tokens").Int(); got != 30 {
		t.Fatalf("second cache_creation_input_tokens = %d, want 30", got)
	}
	if got := gjson.GetBytes(second.Payload, "usage.input_tokens").Int(); got != 1 {
		t.Fatalf("second input_tokens = %d, want 1", got)
	}
}

func TestAntigravityExecuteStream_SimulatedCacheUpdatesState(t *testing.T) {
	cache.SetSimulatedCacheConfig(config.SimulatedCacheConfig{
		Enabled:         true,
		MissProbability: 0,
		TTLSeconds:      300,
		RetentionRatio:  0.5,
	})
	t.Cleanup(func() {
		cache.SetSimulatedCacheConfig(config.SimulatedCacheConfig{
			Enabled:         true,
			MissProbability: 0,
			TTLSeconds:      300,
			RetentionRatio:  0.7,
		})
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"response\":{\"responseId\":\"resp_1\",\"modelVersion\":\"claude-sonnet-4-5-thinking\",\"candidates\":[{\"finishReason\":\"STOP\",\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"ok\"}]}}],\"usageMetadata\":{\"promptTokenCount\":80,\"candidatesTokenCount\":20,\"totalTokenCount\":100}}}\n\n"))
	}))
	defer server.Close()

	exec := NewAntigravityExecutor(&config.Config{SimulatedCache: config.SimulatedCacheConfig{Enabled: true, MissProbability: 0, TTLSeconds: 300, RetentionRatio: 0.5}})
	auth := testAntigravityAuth(server.URL)
	payload := []byte(`{"model":"claude-sonnet-4-5-thinking","messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`)
	ctx := context.WithValue(context.Background(), "gin", ginContextWithAPIKey("sim-cache-stream-key"))
	opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("claude"), OriginalRequest: payload, Stream: true}
	req := cliproxyexecutor.Request{Model: "claude-sonnet-4-5-thinking", Payload: payload}

	streamResult, err := exec.ExecuteStream(ctx, auth, req, opts)
	if err != nil {
		t.Fatalf("ExecuteStream() error = %v", err)
	}
	var builder strings.Builder
	for chunk := range streamResult.Chunks {
		if chunk.Err != nil {
			t.Fatalf("stream chunk error = %v", chunk.Err)
		}
		builder.Write(chunk.Payload)
	}
	if !strings.Contains(builder.String(), `"cache_creation_input_tokens":80`) {
		t.Fatalf("first stream missing first-turn cache creation tokens: %s", builder.String())
	}

	nonStream, err := exec.Execute(ctx, auth, req, cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("claude"), OriginalRequest: payload})
	if err != nil {
		t.Fatalf("follow-up Execute() error = %v", err)
	}
	if got := gjson.GetBytes(nonStream.Payload, "usage.cache_read_input_tokens").Int(); got != 40 {
		t.Fatalf("follow-up cache_read_input_tokens = %d, want 40", got)
	}
	if got := gjson.GetBytes(nonStream.Payload, "usage.cache_creation_input_tokens").Int(); got != 40 {
		t.Fatalf("follow-up cache_creation_input_tokens = %d, want 40", got)
	}
}

func ginContextWithAPIKey(apiKey string) *gin.Context {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(nil)
	ctx.Set("apiKey", apiKey)
	return ctx
}

func TestAntigravityExecuteStream_SimulatedCacheOutputTerminates(t *testing.T) {
	cache.SetSimulatedCacheConfig(config.SimulatedCacheConfig{Enabled: true, MissProbability: 0, TTLSeconds: 300, RetentionRatio: 0.7})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"response\":{\"responseId\":\"resp_1\",\"modelVersion\":\"claude-sonnet-4-5-thinking\",\"candidates\":[{\"finishReason\":\"STOP\",\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"ok\"}]}}],\"usageMetadata\":{\"promptTokenCount\":10,\"candidatesTokenCount\":2,\"totalTokenCount\":12}}}\n\n"))
	}))
	defer server.Close()

	exec := NewAntigravityExecutor(&config.Config{SimulatedCache: config.SimulatedCacheConfig{Enabled: true, MissProbability: 0, TTLSeconds: 300, RetentionRatio: 0.7}})
	auth := testAntigravityAuth(server.URL)
	payload := []byte(`{"model":"claude-sonnet-4-5-thinking","messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`)
	ctx := context.WithValue(context.Background(), "gin", ginContextWithAPIKey("sim-cache-stream-terminate"))
	streamResult, err := exec.ExecuteStream(ctx, auth, cliproxyexecutor.Request{Model: "claude-sonnet-4-5-thinking", Payload: payload}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("claude"), OriginalRequest: payload, Stream: true})
	if err != nil {
		t.Fatalf("ExecuteStream() error = %v", err)
	}

	seen := false
	for chunk := range streamResult.Chunks {
		if chunk.Err != nil {
			t.Fatalf("stream chunk error = %v", chunk.Err)
		}
		if strings.Contains(string(chunk.Payload), `"message_stop"`) {
			seen = true
		}
	}
	if !seen {
		t.Fatal("stream output missing message_stop")
	}
}

func TestGinContextWithAPIKey_HelpsCompatibility(t *testing.T) {
	ctx := ginContextWithAPIKey("abc")
	if ctx == nil {
		t.Fatal("ctx = nil")
	}
	if got, _ := ctx.Get("apiKey"); got != "abc" {
		t.Fatalf("apiKey = %v, want abc", got)
	}
	_ = bufio.ErrFinalToken
	_ = time.Second
}
