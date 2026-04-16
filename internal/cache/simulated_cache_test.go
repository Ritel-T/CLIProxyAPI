package cache

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
)

func TestApplySimulatedCacheOverride_FirstTurn(t *testing.T) {
	split, applied := ApplySimulatedCacheOverride(&SimulatedCacheOverride{IsFirstTurn: true}, 120)
	if !applied {
		t.Fatal("applied = false, want true")
	}
	if split.CacheReadInputTokens != 0 {
		t.Fatalf("CacheReadInputTokens = %d, want 0", split.CacheReadInputTokens)
	}
	if split.CacheCreationInputTokens != 120 {
		t.Fatalf("CacheCreationInputTokens = %d, want 120", split.CacheCreationInputTokens)
	}
	if split.InputTokens != 0 {
		t.Fatalf("InputTokens = %d, want 0", split.InputTokens)
	}
}

func TestApplySimulatedCacheOverride_Miss(t *testing.T) {
	split, applied := ApplySimulatedCacheOverride(&SimulatedCacheOverride{IsMiss: true}, 88)
	if !applied {
		t.Fatal("applied = false, want true")
	}
	if split.CacheReadInputTokens != 0 || split.CacheCreationInputTokens != 88 || split.InputTokens != 0 {
		t.Fatalf("unexpected split: %+v", split)
	}
}

func TestApplySimulatedCacheOverride_Hit(t *testing.T) {
	split, applied := ApplySimulatedCacheOverride(&SimulatedCacheOverride{HistoryCachedTokenCount: 70}, 100)
	if !applied {
		t.Fatal("applied = false, want true")
	}
	if split.CacheReadInputTokens != 70 {
		t.Fatalf("CacheReadInputTokens = %d, want 70", split.CacheReadInputTokens)
	}
	if split.CacheCreationInputTokens != 30 {
		t.Fatalf("CacheCreationInputTokens = %d, want 30", split.CacheCreationInputTokens)
	}
	if split.InputTokens != 0 {
		t.Fatalf("InputTokens = %d, want 0", split.InputTokens)
	}
}

func TestApplySimulatedCacheOverride_HistoryClamped(t *testing.T) {
	split, applied := ApplySimulatedCacheOverride(&SimulatedCacheOverride{HistoryCachedTokenCount: 500}, 100)
	if !applied {
		t.Fatal("applied = false, want true")
	}
	if split.CacheReadInputTokens != 100 {
		t.Fatalf("CacheReadInputTokens = %d, want 100", split.CacheReadInputTokens)
	}
	if split.CacheCreationInputTokens != 0 {
		t.Fatalf("CacheCreationInputTokens = %d, want 0", split.CacheCreationInputTokens)
	}
}

func TestGenerateSimulatedCacheKey_ExecutionSessionHighestPriority(t *testing.T) {
	resetSimulatedCacheForTest()
	key := GenerateSimulatedCacheKey(context.Background(), nil, cliproxyexecutor.Request{}, cliproxyexecutor.Options{
		Metadata: map[string]any{cliproxyexecutor.ExecutionSessionMetadataKey: "exec-123"},
	})
	if key != "exec:exec-123" {
		t.Fatalf("key = %q, want %q", key, "exec:exec-123")
	}
}

func TestGenerateSimulatedCacheKey_ClaudeMetadataSessionPreferred(t *testing.T) {
	resetSimulatedCacheForTest()
	payload := []byte(`{"metadata":{"user_id":"{\"device_id\":\"abc\",\"account_uuid\":\"\",\"session_id\":\"c72554f2-1234-5678-abcd-123456789abc\"}"},"messages":[{"role":"user","content":"hello"}]}`)
	key := GenerateSimulatedCacheKey(context.Background(), nil, cliproxyexecutor.Request{Payload: payload}, cliproxyexecutor.Options{OriginalRequest: payload})
	if key != "claude:c72554f2-1234-5678-abcd-123456789abc" {
		t.Fatalf("key = %q", key)
	}
}

func TestGenerateSimulatedCacheKey_StableAcrossHeaderNoise(t *testing.T) {
	resetSimulatedCacheForTest()
	ctx := context.WithValue(context.Background(), "gin", nil)
	payload := []byte(`{"system":"You are a helpful assistant.","messages":[{"role":"user","content":"hello"}]}`)
	key1 := GenerateSimulatedCacheKey(ctx, nil, cliproxyexecutor.Request{Payload: payload}, cliproxyexecutor.Options{OriginalRequest: payload, Headers: http.Header{"User-Agent": []string{"ua-1"}}})
	key2 := GenerateSimulatedCacheKey(ctx, nil, cliproxyexecutor.Request{Payload: payload}, cliproxyexecutor.Options{OriginalRequest: payload, Headers: http.Header{"User-Agent": []string{"ua-2"}}})
	if key1 == "" || key2 == "" {
		t.Fatalf("keys must not be empty: %q %q", key1, key2)
	}
	if key1 != key2 {
		t.Fatalf("keys differ: %q vs %q", key1, key2)
	}
}

func TestGenerateSimulatedCacheKey_StringSystemAffectsFallbackHash(t *testing.T) {
	resetSimulatedCacheForTest()
	payload1 := []byte(`{"system":"You are a helpful assistant.","messages":[{"role":"user","content":"hello"}]}`)
	payload2 := []byte(`{"system":"You are a debugging assistant.","messages":[{"role":"user","content":"hello"}]}`)
	key1 := GenerateSimulatedCacheKey(context.Background(), nil, cliproxyexecutor.Request{Payload: payload1}, cliproxyexecutor.Options{OriginalRequest: payload1})
	key2 := GenerateSimulatedCacheKey(context.Background(), nil, cliproxyexecutor.Request{Payload: payload2}, cliproxyexecutor.Options{OriginalRequest: payload2})
	if key1 == key2 {
		t.Fatalf("keys equal, want different: %q", key1)
	}
}

func TestGenerateSimulatedCacheKey_ArraySystemExcludedFromFallbackHash(t *testing.T) {
	resetSimulatedCacheForTest()
	payload1 := []byte(`{"system":[{"type":"text","text":"You are OpenCode. Files: a b c.","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"user","content":"hello"}]}`)
	payload2 := []byte(`{"system":[{"type":"text","text":"You are OpenCode. Files: a b c d.","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"user","content":"hello"}]}`)
	key1 := GenerateSimulatedCacheKey(context.Background(), nil, cliproxyexecutor.Request{Payload: payload1}, cliproxyexecutor.Options{OriginalRequest: payload1})
	key2 := GenerateSimulatedCacheKey(context.Background(), nil, cliproxyexecutor.Request{Payload: payload2}, cliproxyexecutor.Options{OriginalRequest: payload2})
	if key1 != key2 {
		t.Fatalf("keys differ, want equal: %q vs %q", key1, key2)
	}
}

func TestComputeAndUpdateSimulatedCacheState(t *testing.T) {
	resetSimulatedCacheForTest()
	SetSimulatedCacheConfig(configForTest(true, 0, 300, 0.7))
	key := "session-key"

	override := ComputeSimulatedCacheOverride(key)
	if override == nil || !override.IsFirstTurn {
		t.Fatalf("override = %+v, want first turn", override)
	}

	UpdateSimulatedCacheState(key, 100)
	override = ComputeSimulatedCacheOverride(key)
	if override == nil {
		t.Fatal("override = nil, want non-nil")
	}
	if override.IsFirstTurn {
		t.Fatal("override.IsFirstTurn = true, want false")
	}
	if override.HistoryCachedTokenCount != 70 {
		t.Fatalf("HistoryCachedTokenCount = %d, want 70", override.HistoryCachedTokenCount)
	}
}

func TestComputeSimulatedCacheOverride_Disabled(t *testing.T) {
	resetSimulatedCacheForTest()
	SetSimulatedCacheConfig(configForTest(false, 0, 300, 0.7))
	if override := ComputeSimulatedCacheOverride("session-key"); override != nil {
		t.Fatalf("override = %+v, want nil", override)
	}
}

func TestSimulatedCacheEntryExpires(t *testing.T) {
	resetSimulatedCacheForTest()
	setSimulatedCacheEntry("expiring", simulatedCacheEntry{CachedTokenCount: 9, TurnCount: 1, Expire: time.Now().Add(-time.Second)})
	if _, ok := getSimulatedCacheEntry("expiring", time.Now()); ok {
		t.Fatal("entry still present after expiry")
	}
}

func TestPurgeExpiredSimulatedCacheEntriesAt(t *testing.T) {
	resetSimulatedCacheForTest()
	now := time.Now()
	setSimulatedCacheEntry("expired", simulatedCacheEntry{Expire: now.Add(-time.Second)})
	setSimulatedCacheEntry("fresh", simulatedCacheEntry{Expire: now.Add(time.Second)})
	purgeExpiredSimulatedCacheEntriesAt(now)
	if _, ok := getSimulatedCacheEntry("expired", now); ok {
		t.Fatal("expired entry still present")
	}
	if _, ok := getSimulatedCacheEntry("fresh", now); !ok {
		t.Fatal("fresh entry missing")
	}
}

func configForTest(enabled bool, missProbability float64, ttlSeconds int, retentionRatio float64) config.SimulatedCacheConfig {
	return config.SimulatedCacheConfig{
		Enabled:         enabled,
		MissProbability: missProbability,
		TTLSeconds:      ttlSeconds,
		RetentionRatio:  retentionRatio,
	}
}
