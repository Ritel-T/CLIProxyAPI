package claude

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/cache"
	"github.com/tidwall/gjson"
)

// ============================================================================
// Signature Caching Tests
// ============================================================================

func TestConvertAntigravityResponseToClaude_ParamsInitialized(t *testing.T) {
	cache.ClearSignatureCache("")

	// Request with user message - should initialize params
	requestJSON := []byte(`{
		"messages": [
			{"role": "user", "content": [{"type": "text", "text": "Hello world"}]}
		]
	}`)

	// First response chunk with thinking
	responseJSON := []byte(`{
		"response": {
			"candidates": [{
				"content": {
					"parts": [{"text": "Let me think...", "thought": true}]
				}
			}]
		}
	}`)

	var param any
	ctx := context.Background()
	ConvertAntigravityResponseToClaude(ctx, "claude-sonnet-4-5-thinking", requestJSON, requestJSON, responseJSON, &param)

	params := param.(*Params)
	if !params.HasFirstResponse {
		t.Error("HasFirstResponse should be set after first chunk")
	}
	if params.CurrentThinkingText.Len() == 0 {
		t.Error("Thinking text should be accumulated")
	}
}

func TestConvertAntigravityResponseToClaude_ThinkingTextAccumulated(t *testing.T) {
	cache.ClearSignatureCache("")

	requestJSON := []byte(`{
		"messages": [{"role": "user", "content": [{"type": "text", "text": "Test"}]}]
	}`)

	// First thinking chunk
	chunk1 := []byte(`{
		"response": {
			"candidates": [{
				"content": {
					"parts": [{"text": "First part of thinking...", "thought": true}]
				}
			}]
		}
	}`)

	// Second thinking chunk (continuation)
	chunk2 := []byte(`{
		"response": {
			"candidates": [{
				"content": {
					"parts": [{"text": " Second part of thinking...", "thought": true}]
				}
			}]
		}
	}`)

	var param any
	ctx := context.Background()

	// Process first chunk - starts new thinking block
	ConvertAntigravityResponseToClaude(ctx, "claude-sonnet-4-5-thinking", requestJSON, requestJSON, chunk1, &param)
	params := param.(*Params)

	if params.CurrentThinkingText.Len() == 0 {
		t.Error("Thinking text should be accumulated after first chunk")
	}

	// Process second chunk - continues thinking block
	ConvertAntigravityResponseToClaude(ctx, "claude-sonnet-4-5-thinking", requestJSON, requestJSON, chunk2, &param)

	text := params.CurrentThinkingText.String()
	if !strings.Contains(text, "First part") || !strings.Contains(text, "Second part") {
		t.Errorf("Thinking text should accumulate both parts, got: %s", text)
	}
}

func TestConvertAntigravityResponseToClaude_SignatureCached(t *testing.T) {
	cache.ClearSignatureCache("")

	requestJSON := []byte(`{
		"model": "claude-sonnet-4-5-thinking",
		"messages": [{"role": "user", "content": [{"type": "text", "text": "Cache test"}]}]
	}`)

	// Thinking chunk
	thinkingChunk := []byte(`{
		"response": {
			"candidates": [{
				"content": {
					"parts": [{"text": "My thinking process here", "thought": true}]
				}
			}]
		}
	}`)

	// Signature chunk
	validSignature := "abc123validSignature1234567890123456789012345678901234567890"
	signatureChunk := []byte(`{
		"response": {
			"candidates": [{
				"content": {
					"parts": [{"text": "", "thought": true, "thoughtSignature": "` + validSignature + `"}]
				}
			}]
		}
	}`)

	var param any
	ctx := context.Background()

	// Process thinking chunk
	ConvertAntigravityResponseToClaude(ctx, "claude-sonnet-4-5-thinking", requestJSON, requestJSON, thinkingChunk, &param)
	params := param.(*Params)
	thinkingText := params.CurrentThinkingText.String()

	if thinkingText == "" {
		t.Fatal("Thinking text should be accumulated")
	}

	// Process signature chunk - should cache the signature
	ConvertAntigravityResponseToClaude(ctx, "claude-sonnet-4-5-thinking", requestJSON, requestJSON, signatureChunk, &param)

	// Verify signature was cached
	cachedSig := cache.GetCachedSignature("claude-sonnet-4-5-thinking", thinkingText)
	if cachedSig != validSignature {
		t.Errorf("Expected cached signature '%s', got '%s'", validSignature, cachedSig)
	}

	// Verify thinking text was reset after caching
	if params.CurrentThinkingText.Len() != 0 {
		t.Error("Thinking text should be reset after signature is cached")
	}
}

func TestConvertAntigravityResponseToClaude_MultipleThinkingBlocks(t *testing.T) {
	cache.ClearSignatureCache("")

	requestJSON := []byte(`{
		"model": "claude-sonnet-4-5-thinking",
		"messages": [{"role": "user", "content": [{"type": "text", "text": "Multi block test"}]}]
	}`)

	validSig1 := "signature1_12345678901234567890123456789012345678901234567"
	validSig2 := "signature2_12345678901234567890123456789012345678901234567"

	// First thinking block with signature
	block1Thinking := []byte(`{
		"response": {
			"candidates": [{
				"content": {
					"parts": [{"text": "First thinking block", "thought": true}]
				}
			}]
		}
	}`)
	block1Sig := []byte(`{
		"response": {
			"candidates": [{
				"content": {
					"parts": [{"text": "", "thought": true, "thoughtSignature": "` + validSig1 + `"}]
				}
			}]
		}
	}`)

	// Text content (breaks thinking)
	textBlock := []byte(`{
		"response": {
			"candidates": [{
				"content": {
					"parts": [{"text": "Regular text output"}]
				}
			}]
		}
	}`)

	// Second thinking block with signature
	block2Thinking := []byte(`{
		"response": {
			"candidates": [{
				"content": {
					"parts": [{"text": "Second thinking block", "thought": true}]
				}
			}]
		}
	}`)
	block2Sig := []byte(`{
		"response": {
			"candidates": [{
				"content": {
					"parts": [{"text": "", "thought": true, "thoughtSignature": "` + validSig2 + `"}]
				}
			}]
		}
	}`)

	var param any
	ctx := context.Background()

	// Process first thinking block
	ConvertAntigravityResponseToClaude(ctx, "claude-sonnet-4-5-thinking", requestJSON, requestJSON, block1Thinking, &param)
	params := param.(*Params)
	firstThinkingText := params.CurrentThinkingText.String()

	ConvertAntigravityResponseToClaude(ctx, "claude-sonnet-4-5-thinking", requestJSON, requestJSON, block1Sig, &param)

	// Verify first signature cached
	if cache.GetCachedSignature("claude-sonnet-4-5-thinking", firstThinkingText) != validSig1 {
		t.Error("First thinking block signature should be cached")
	}

	// Process text (transitions out of thinking)
	ConvertAntigravityResponseToClaude(ctx, "claude-sonnet-4-5-thinking", requestJSON, requestJSON, textBlock, &param)

	// Process second thinking block
	ConvertAntigravityResponseToClaude(ctx, "claude-sonnet-4-5-thinking", requestJSON, requestJSON, block2Thinking, &param)
	secondThinkingText := params.CurrentThinkingText.String()

	ConvertAntigravityResponseToClaude(ctx, "claude-sonnet-4-5-thinking", requestJSON, requestJSON, block2Sig, &param)

	// Verify second signature cached
	if cache.GetCachedSignature("claude-sonnet-4-5-thinking", secondThinkingText) != validSig2 {
		t.Error("Second thinking block signature should be cached")
	}
}

func TestConvertAntigravityResponseToClaude_TextAndSignatureInSameChunk(t *testing.T) {
	cache.ClearSignatureCache("")

	requestJSON := []byte(`{
		"model": "claude-sonnet-4-5-thinking",
		"messages": [{"role": "user", "content": [{"type": "text", "text": "Test"}]}]
	}`)

	validSignature := "RtestSig1234567890123456789012345678901234567890123456789"

	// Chunk 1: thinking text only (no signature)
	chunk1 := []byte(`{
		"response": {
			"candidates": [{
				"content": {
					"parts": [{"text": "First part.", "thought": true}]
				}
			}]
		}
	}`)

	// Chunk 2: thinking text AND signature in the same part
	chunk2 := []byte(`{
		"response": {
			"candidates": [{
				"content": {
					"parts": [{"text": " Second part.", "thought": true, "thoughtSignature": "` + validSignature + `"}]
				}
			}]
		}
	}`)

	var param any
	ctx := context.Background()

	result1 := ConvertAntigravityResponseToClaude(ctx, "claude-sonnet-4-5-thinking", requestJSON, requestJSON, chunk1, &param)
	result2 := ConvertAntigravityResponseToClaude(ctx, "claude-sonnet-4-5-thinking", requestJSON, requestJSON, chunk2, &param)

	allOutput := string(bytes.Join(result1, nil)) + string(bytes.Join(result2, nil))

	// The text " Second part." must appear as a thinking_delta, not be silently dropped
	if !strings.Contains(allOutput, "Second part.") {
		t.Error("Text co-located with signature must be emitted as thinking_delta before the signature")
	}

	// The signature must also be emitted
	if !strings.Contains(allOutput, "signature_delta") {
		t.Error("Signature delta must still be emitted")
	}

	// Verify the cached signature covers the FULL text (both parts)
	fullText := "First part. Second part."
	cachedSig := cache.GetCachedSignature("claude-sonnet-4-5-thinking", fullText)
	if cachedSig != validSignature {
		t.Errorf("Cached signature should cover full text %q, got sig=%q", fullText, cachedSig)
	}
}

func TestConvertAntigravityResponseToClaude_SignatureOnlyChunk(t *testing.T) {
	cache.ClearSignatureCache("")

	requestJSON := []byte(`{
		"model": "claude-sonnet-4-5-thinking",
		"messages": [{"role": "user", "content": [{"type": "text", "text": "Test"}]}]
	}`)

	validSignature := "RtestSig1234567890123456789012345678901234567890123456789"

	// Chunk 1: thinking text
	chunk1 := []byte(`{
		"response": {
			"candidates": [{
				"content": {
					"parts": [{"text": "Full thinking text.", "thought": true}]
				}
			}]
		}
	}`)

	// Chunk 2: signature only (empty text) — the normal case
	chunk2 := []byte(`{
		"response": {
			"candidates": [{
				"content": {
					"parts": [{"text": "", "thought": true, "thoughtSignature": "` + validSignature + `"}]
				}
			}]
		}
	}`)

	var param any
	ctx := context.Background()

	ConvertAntigravityResponseToClaude(ctx, "claude-sonnet-4-5-thinking", requestJSON, requestJSON, chunk1, &param)
	ConvertAntigravityResponseToClaude(ctx, "claude-sonnet-4-5-thinking", requestJSON, requestJSON, chunk2, &param)

	cachedSig := cache.GetCachedSignature("claude-sonnet-4-5-thinking", "Full thinking text.")
	if cachedSig != validSignature {
		t.Errorf("Signature-only chunk should still cache correctly, got %q", cachedSig)
	}
}

func TestConvertAntigravityResponseToClaudeNonStream_SimulatedCacheFirstTurn(t *testing.T) {
	requestJSON := []byte(`{
		"model": "claude-sonnet-4-5-thinking",
		"messages": [{"role": "user", "content": [{"type": "text", "text": "hello"}]}]
	}`)
	responseJSON := []byte(`{
		"response": {
			"responseId": "resp_1",
			"modelVersion": "claude-sonnet-4-5-thinking",
			"usageMetadata": {
				"promptTokenCount": 120,
				"candidatesTokenCount": 40,
				"totalTokenCount": 160,
				"cachedContentTokenCount": 10
			},
			"candidates": [{
				"finishReason": "STOP",
				"content": {"parts": [{"text": "hello"}]}
			}]
		}
	}`)

	ctx := cache.WithSimulatedCacheOverride(context.Background(), &cache.SimulatedCacheOverride{IsFirstTurn: true})
	converted := ConvertAntigravityResponseToClaudeNonStream(ctx, "claude-sonnet-4-5-thinking", requestJSON, requestJSON, responseJSON, nil)

	if got := gjson.GetBytes(converted, "usage.input_tokens").Int(); got != 1 {
		t.Fatalf("usage.input_tokens = %d, want 1", got)
	}
	if got := gjson.GetBytes(converted, "usage.cache_read_input_tokens").Int(); got != 0 {
		t.Fatalf("usage.cache_read_input_tokens = %d, want 0", got)
	}
	if got := gjson.GetBytes(converted, "usage.cache_creation_input_tokens").Int(); got != 120 {
		t.Fatalf("usage.cache_creation_input_tokens = %d, want 120", got)
	}
}

func TestConvertAntigravityResponseToClaudeNonStream_SimulatedCacheHit(t *testing.T) {
	requestJSON := []byte(`{
		"model": "claude-sonnet-4-5-thinking",
		"messages": [{"role": "user", "content": [{"type": "text", "text": "hello"}]}]
	}`)
	responseJSON := []byte(`{
		"response": {
			"responseId": "resp_1",
			"modelVersion": "claude-sonnet-4-5-thinking",
			"usageMetadata": {
				"promptTokenCount": 120,
				"candidatesTokenCount": 40,
				"totalTokenCount": 160,
				"cachedContentTokenCount": 5
			},
			"candidates": [{
				"finishReason": "STOP",
				"content": {"parts": [{"text": "hello"}]}
			}]
		}
	}`)

	ctx := cache.WithSimulatedCacheOverride(context.Background(), &cache.SimulatedCacheOverride{HistoryCachedTokenCount: 70})
	converted := ConvertAntigravityResponseToClaudeNonStream(ctx, "claude-sonnet-4-5-thinking", requestJSON, requestJSON, responseJSON, nil)

	if got := gjson.GetBytes(converted, "usage.input_tokens").Int(); got != 1 {
		t.Fatalf("usage.input_tokens = %d, want 1", got)
	}
	if got := gjson.GetBytes(converted, "usage.cache_read_input_tokens").Int(); got != 70 {
		t.Fatalf("usage.cache_read_input_tokens = %d, want 70", got)
	}
	if got := gjson.GetBytes(converted, "usage.cache_creation_input_tokens").Int(); got != 50 {
		t.Fatalf("usage.cache_creation_input_tokens = %d, want 50", got)
	}
}

func TestConvertAntigravityResponseToClaude_StreamSimulatedCacheUsage(t *testing.T) {
	requestJSON := []byte(`{
		"model": "claude-sonnet-4-5-thinking",
		"messages": [{"role": "user", "content": [{"type": "text", "text": "hello"}]}]
	}`)
	chunk := []byte(`{
		"response": {
			"responseId": "resp_1",
			"modelVersion": "claude-sonnet-4-5-thinking",
			"usageMetadata": {
				"promptTokenCount": 120,
				"candidatesTokenCount": 40,
				"totalTokenCount": 160,
				"cachedContentTokenCount": 5
			},
			"candidates": [{
				"finishReason": "STOP",
				"content": {"parts": [{"text": "hello"}]}
			}]
		}
	}`)

	var param any
	ctx := cache.WithSimulatedCacheOverride(context.Background(), &cache.SimulatedCacheOverride{HistoryCachedTokenCount: 70})
	parts := ConvertAntigravityResponseToClaude(ctx, "claude-sonnet-4-5-thinking", requestJSON, requestJSON, chunk, &param)
	joined := string(bytes.Join(parts, nil))

	if !strings.Contains(joined, `"cache_read_input_tokens":70`) {
		t.Fatalf("stream output missing cache_read_input_tokens override: %s", joined)
	}
	if !strings.Contains(joined, `"input_tokens":1`) {
		t.Fatalf("stream output missing input_tokens override: %s", joined)
	}
	if !strings.Contains(joined, `"cache_creation_input_tokens":50`) {
		t.Fatalf("stream output missing cache_creation_input_tokens override: %s", joined)
	}
}

func TestConvertAntigravityResponseToClaude_StreamSimulatedCacheFullCoverageKeepsInputZero(t *testing.T) {
	requestJSON := []byte(`{
		"model": "claude-sonnet-4-5-thinking",
		"messages": [{"role": "user", "content": [{"type": "text", "text": "hello"}]}]
	}`)
	chunk := []byte(`{
		"response": {
			"responseId": "resp_1",
			"modelVersion": "claude-sonnet-4-5-thinking",
			"usageMetadata": {
				"promptTokenCount": 120,
				"candidatesTokenCount": 40,
				"totalTokenCount": 160,
				"cachedContentTokenCount": 0
			},
			"candidates": [{
				"finishReason": "STOP",
				"content": {"parts": [{"text": "hello"}]}
			}]
		}
	}`)

	var param any
	ctx := cache.WithSimulatedCacheOverride(context.Background(), &cache.SimulatedCacheOverride{HistoryCachedTokenCount: 120})
	parts := ConvertAntigravityResponseToClaude(ctx, "claude-sonnet-4-5-thinking", requestJSON, requestJSON, chunk, &param)
	joined := string(bytes.Join(parts, nil))

	if !strings.Contains(joined, `"cache_read_input_tokens":120`) {
		t.Fatalf("stream output missing full cache read override: %s", joined)
	}
	if strings.Contains(joined, `"cache_creation_input_tokens":120`) {
		t.Fatalf("stream output incorrectly reports full prompt as cache creation: %s", joined)
	}
	if !strings.Contains(joined, `"input_tokens":1`) {
		t.Fatalf("stream output should keep input_tokens at 1 when cache covers full prompt: %s", joined)
	}
}

func TestConvertAntigravityResponseToClaude_MessageStartRespectsSimulatedCacheSplit(t *testing.T) {
	requestJSON := []byte(`{
		"model": "claude-sonnet-4-5-thinking",
		"messages": [{"role": "user", "content": [{"type": "text", "text": "hello"}]}]
	}`)
	chunk := []byte(`{
		"response": {
			"responseId": "resp_1",
			"modelVersion": "claude-sonnet-4-5-thinking",
			"cpaUsageMetadata": {
				"promptTokenCount": 120,
				"candidatesTokenCount": 40
			},
			"usageMetadata": {
				"promptTokenCount": 120,
				"candidatesTokenCount": 40,
				"totalTokenCount": 160,
				"cachedContentTokenCount": 0
			},
			"candidates": [{
				"finishReason": "STOP",
				"content": {"parts": [{"text": "hello"}]}
			}]
		}
	}`)

	var param any
	ctx := cache.WithSimulatedCacheOverride(context.Background(), &cache.SimulatedCacheOverride{IsFirstTurn: true})
	parts := ConvertAntigravityResponseToClaude(ctx, "claude-sonnet-4-5-thinking", requestJSON, requestJSON, chunk, &param)
	joined := string(bytes.Join(parts, nil))

	if !strings.Contains(joined, `"type": "message_start"`) {
		t.Fatalf("stream output missing message_start: %s", joined)
	}
	if !strings.Contains(joined, `"usage": {"input_tokens": 1, "output_tokens": 40}`) {
		t.Fatalf("message_start usage should reflect split input_tokens=1: %s", joined)
	}
	if !strings.Contains(joined, `"cache_creation_input_tokens":120`) {
		t.Fatalf("final usage missing cache_creation_input_tokens: %s", joined)
	}
}

func TestConvertAntigravityResponseToClaude_MessageStartClampsZeroInputToOne(t *testing.T) {
	requestJSON := []byte(`{
		"model": "claude-sonnet-4-5-thinking",
		"messages": [{"role": "user", "content": [{"type": "text", "text": "hello"}]}]
	}`)
	chunk := []byte(`{
		"response": {
			"responseId": "resp_1",
			"modelVersion": "claude-sonnet-4-5-thinking",
			"cpaUsageMetadata": {
				"promptTokenCount": 0,
				"candidatesTokenCount": 40
			},
			"usageMetadata": {
				"promptTokenCount": 0,
				"candidatesTokenCount": 40,
				"totalTokenCount": 40,
				"cachedContentTokenCount": 0
			},
			"candidates": [{
				"finishReason": "STOP",
				"content": {"parts": [{"text": "hello"}]}
			}]
		}
	}`)

	var param any
	parts := ConvertAntigravityResponseToClaude(context.Background(), "claude-sonnet-4-5-thinking", requestJSON, requestJSON, chunk, &param)
	joined := string(bytes.Join(parts, nil))

	if !strings.Contains(joined, `"usage": {"input_tokens": 1, "output_tokens": 40}`) {
		t.Fatalf("message_start usage should clamp zero input_tokens to 1: %s", joined)
	}
	if !strings.Contains(joined, `"usage":{"input_tokens":1,"output_tokens":40}`) {
		t.Fatalf("message_delta usage should clamp zero input_tokens to 1: %s", joined)
	}
}
