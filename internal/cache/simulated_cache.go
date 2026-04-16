package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/runtime/executor/helps"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	"github.com/tidwall/gjson"
)

const (
	defaultSimulatedCacheTTLSeconds = 300
	simulatedCacheCleanupInterval   = 15 * time.Minute
)

type simulatedCacheContextKey string

const SimulatedCacheOverrideContextKey simulatedCacheContextKey = "simulated_cache_override"

var (
	simulatedCacheConfigValue atomic.Value
	simulatedCacheEntries     = make(map[string]simulatedCacheEntry)
	simulatedCacheMu          sync.RWMutex
	simulatedCacheCleanupOnce sync.Once
	sessionPattern            = regexp.MustCompile(`_session_([A-Za-z0-9-]+)$`)
)

type simulatedCacheEntry struct {
	CachedTokenCount int64
	TurnCount        int
	Expire           time.Time
}

// SimulatedCacheOverride carries simulated-cache hit/miss state for one request.
type SimulatedCacheOverride struct {
	HistoryCachedTokenCount int64
	IsMiss                  bool
	IsFirstTurn             bool
	TTLSeconds              int
}

// SimulatedCacheUsageSplit is the client-facing usage split produced by simulated cache billing.
type SimulatedCacheUsageSplit struct {
	CacheReadInputTokens     int64
	CacheCreationInputTokens int64
	InputTokens              int64
}

// DisplayInputTokens returns the externally displayed input token count.
// For simulated-cache responses, downstream CPA UX expects a minimum of 1
// instead of 0 even when prompt tokens are fully represented by cache fields.
func (s SimulatedCacheUsageSplit) DisplayInputTokens() int64 {
	if s.InputTokens <= 0 && (s.CacheReadInputTokens > 0 || s.CacheCreationInputTokens > 0) {
		return 1
	}
	return s.InputTokens
}

func init() {
	simulatedCacheConfigValue.Store(defaultSimulatedCacheConfig())
}

func defaultSimulatedCacheConfig() config.SimulatedCacheConfig {
	return config.SimulatedCacheConfig{
		Enabled:         true,
		MissProbability: 0,
		TTLSeconds:      defaultSimulatedCacheTTLSeconds,
		RetentionRatio:  0.7,
	}
}

func currentSimulatedCacheConfig() config.SimulatedCacheConfig {
	if value := simulatedCacheConfigValue.Load(); value != nil {
		if cfg, ok := value.(config.SimulatedCacheConfig); ok {
			return cfg
		}
	}
	return defaultSimulatedCacheConfig()
}

// SetSimulatedCacheConfig updates the runtime simulated-cache configuration.
func SetSimulatedCacheConfig(cfg config.SimulatedCacheConfig) {
	simulatedCacheConfigValue.Store(sanitizeSimulatedCacheConfig(cfg))
}

func sanitizeSimulatedCacheConfig(cfg config.SimulatedCacheConfig) config.SimulatedCacheConfig {
	if cfg.MissProbability < 0 {
		cfg.MissProbability = 0
	}
	if cfg.MissProbability > 1 {
		cfg.MissProbability = 1
	}
	if cfg.TTLSeconds < 0 {
		cfg.TTLSeconds = 0
	}
	if cfg.RetentionRatio < 0 {
		cfg.RetentionRatio = 0
	}
	if cfg.RetentionRatio > 1 {
		cfg.RetentionRatio = 1
	}
	return cfg
}

// SimulatedCacheEnabled reports whether runtime simulated-cache billing is enabled.
func SimulatedCacheEnabled() bool {
	return currentSimulatedCacheConfig().Enabled
}

// ResolveSimulatedCacheTTLSeconds normalizes the configured TTL.
func ResolveSimulatedCacheTTLSeconds(ttlSeconds int) int {
	if ttlSeconds <= 0 {
		return 3600
	}
	return ttlSeconds
}

// ApplySimulatedCacheOverride rewrites prompt usage according to the override.
func ApplySimulatedCacheOverride(override *SimulatedCacheOverride, totalPromptTokens int64) (SimulatedCacheUsageSplit, bool) {
	if override == nil {
		return SimulatedCacheUsageSplit{}, false
	}
	if totalPromptTokens < 0 {
		totalPromptTokens = 0
	}
	if override.IsFirstTurn || override.IsMiss {
		return SimulatedCacheUsageSplit{
			CacheReadInputTokens:     0,
			CacheCreationInputTokens: totalPromptTokens,
			InputTokens:              0,
		}, true
	}
	cacheRead := override.HistoryCachedTokenCount
	if cacheRead > totalPromptTokens {
		cacheRead = totalPromptTokens
	}
	return SimulatedCacheUsageSplit{
		CacheReadInputTokens:     cacheRead,
		CacheCreationInputTokens: totalPromptTokens - cacheRead,
		InputTokens:              0,
	}, true
}

// ComputeSimulatedCacheOverride returns simulated-cache state for the current request.
func ComputeSimulatedCacheOverride(cacheKey string) *SimulatedCacheOverride {
	cfg := currentSimulatedCacheConfig()
	if !cfg.Enabled || strings.TrimSpace(cacheKey) == "" {
		return nil
	}
	ttlSeconds := ResolveSimulatedCacheTTLSeconds(cfg.TTLSeconds)
	entry, ok := getSimulatedCacheEntry(cacheKey, time.Now())
	if !ok || entry.TurnCount <= 0 {
		return &SimulatedCacheOverride{IsFirstTurn: true, TTLSeconds: ttlSeconds}
	}
	return &SimulatedCacheOverride{
		HistoryCachedTokenCount: entry.CachedTokenCount,
		IsMiss:                  rand.Float64() < cfg.MissProbability,
		IsFirstTurn:             false,
		TTLSeconds:              ttlSeconds,
	}
}

// WithSimulatedCacheOverride stores the override in context for translator access.
func WithSimulatedCacheOverride(ctx context.Context, override *SimulatedCacheOverride) context.Context {
	if override == nil {
		return ctx
	}
	return context.WithValue(ctx, SimulatedCacheOverrideContextKey, override)
}

// SimulatedCacheOverrideFromContext reads the override from context.
func SimulatedCacheOverrideFromContext(ctx context.Context) *SimulatedCacheOverride {
	if ctx == nil {
		return nil
	}
	override, _ := ctx.Value(SimulatedCacheOverrideContextKey).(*SimulatedCacheOverride)
	return override
}

// UpdateSimulatedCacheState updates the session state after a successful request.
func UpdateSimulatedCacheState(cacheKey string, totalPromptTokens int64) {
	cfg := currentSimulatedCacheConfig()
	if !cfg.Enabled || strings.TrimSpace(cacheKey) == "" {
		return
	}
	if totalPromptTokens < 0 {
		totalPromptTokens = 0
	}
	retained := int64(math.Trunc(float64(totalPromptTokens) * cfg.RetentionRatio))
	now := time.Now()
	entry, _ := getSimulatedCacheEntry(cacheKey, now)
	entry.CachedTokenCount = retained
	entry.TurnCount++
	entry.Expire = now.Add(time.Duration(ResolveSimulatedCacheTTLSeconds(cfg.TTLSeconds)) * time.Second)
	setSimulatedCacheEntry(cacheKey, entry)
}

// GenerateSimulatedCacheKey derives a stable simulated-cache key for one request.
func GenerateSimulatedCacheKey(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) string {
	payload := req.Payload
	if len(opts.OriginalRequest) > 0 {
		payload = opts.OriginalRequest
	}
	if sessionID := explicitSimulatedSessionID(payload, opts.Headers, opts.Metadata); sessionID != "" {
		return sessionID
	}
	identity := simulatedCacheIdentity(ctx, auth)
	if identity == "" && len(payload) == 0 {
		return ""
	}
	components := []string{
		identity,
		extractStableSystemText(payload),
		extractStableFirstUserText(payload),
	}
	return stableSimulatedCacheHash(strings.Join(components, "\n"))
}

func explicitSimulatedSessionID(payload []byte, headers http.Header, metadata map[string]any) string {
	if metadata != nil {
		if executionSession, ok := metadata[cliproxyexecutor.ExecutionSessionMetadataKey].(string); ok {
			if trimmed := strings.TrimSpace(executionSession); trimmed != "" {
				return "exec:" + trimmed
			}
		}
	}
	if len(payload) > 0 {
		userID := gjson.GetBytes(payload, "metadata.user_id").String()
		if userID != "" {
			if matches := sessionPattern.FindStringSubmatch(userID); len(matches) >= 2 {
				return "claude:" + matches[1]
			}
			if len(userID) > 0 && userID[0] == '{' {
				if sid := gjson.Get(userID, "session_id").String(); sid != "" {
					return "claude:" + sid
				}
			}
		}
	}
	if headers != nil {
		if sid := strings.TrimSpace(headers.Get("X-Session-ID")); sid != "" {
			return "header:" + sid
		}
	}
	if len(payload) > 0 {
		if userID := strings.TrimSpace(gjson.GetBytes(payload, "metadata.user_id").String()); userID != "" {
			return "user:" + userID
		}
		if convID := strings.TrimSpace(gjson.GetBytes(payload, "conversation_id").String()); convID != "" {
			return "conv:" + convID
		}
	}
	return ""
}

func simulatedCacheIdentity(ctx context.Context, auth *cliproxyauth.Auth) string {
	if apiKey := strings.TrimSpace(helps.APIKeyFromContext(ctx)); apiKey != "" {
		return "api:" + stableSimulatedCacheHash(apiKey)
	}
	if auth != nil {
		if id := strings.TrimSpace(auth.ID); id != "" {
			return "auth:" + id
		}
		if idx := strings.TrimSpace(auth.EnsureIndex()); idx != "" {
			return "auth-index:" + idx
		}
	}
	return ""
}

func extractStableSystemText(payload []byte) string {
	if len(payload) == 0 {
		return ""
	}
	system := gjson.GetBytes(payload, "system")
	if system.Type == gjson.String {
		return strings.TrimSpace(system.String())
	}
	if instructions := strings.TrimSpace(gjson.GetBytes(payload, "instructions").String()); instructions != "" {
		return instructions
	}
	return ""
}

func extractStableFirstUserText(payload []byte) string {
	if len(payload) == 0 {
		return ""
	}
	messages := gjson.GetBytes(payload, "messages")
	if messages.Exists() && messages.IsArray() {
		for _, msg := range messages.Array() {
			if msg.Get("role").String() != "user" {
				continue
			}
			if text := extractMessageText(msg.Get("content")); text != "" {
				return text
			}
		}
	}
	contents := gjson.GetBytes(payload, "contents")
	if contents.Exists() && contents.IsArray() {
		for _, msg := range contents.Array() {
			if msg.Get("role").String() != "user" {
				continue
			}
			parts := msg.Get("parts")
			if !parts.Exists() || !parts.IsArray() {
				continue
			}
			for _, part := range parts.Array() {
				if text := strings.TrimSpace(part.Get("text").String()); text != "" {
					return text
				}
			}
		}
	}
	input := gjson.GetBytes(payload, "input")
	if input.Exists() && input.IsArray() {
		for _, item := range input.Array() {
			if item.Get("role").String() != "user" {
				continue
			}
			if text := extractMessageText(item.Get("content")); text != "" {
				return text
			}
		}
	}
	return ""
}

func extractMessageText(content gjson.Result) string {
	if !content.Exists() {
		return ""
	}
	if content.Type == gjson.String {
		return strings.TrimSpace(content.String())
	}
	if content.IsArray() {
		for _, item := range content.Array() {
			if text := strings.TrimSpace(item.Get("text").String()); text != "" {
				return text
			}
		}
	}
	return ""
}

func stableSimulatedCacheHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:8])
}

func startSimulatedCacheCleanup() {
	go func() {
		ticker := time.NewTicker(simulatedCacheCleanupInterval)
		defer ticker.Stop()
		for range ticker.C {
			purgeExpiredSimulatedCacheEntries()
		}
	}()
}

func purgeExpiredSimulatedCacheEntries() {
	purgeExpiredSimulatedCacheEntriesAt(time.Now())
}

func purgeExpiredSimulatedCacheEntriesAt(now time.Time) {
	simulatedCacheMu.Lock()
	defer simulatedCacheMu.Unlock()
	for key, entry := range simulatedCacheEntries {
		if !entry.Expire.After(now) {
			delete(simulatedCacheEntries, key)
		}
	}
}

func getSimulatedCacheEntry(key string, now time.Time) (simulatedCacheEntry, bool) {
	simulatedCacheCleanupOnce.Do(startSimulatedCacheCleanup)
	simulatedCacheMu.RLock()
	entry, ok := simulatedCacheEntries[key]
	simulatedCacheMu.RUnlock()
	if !ok {
		return simulatedCacheEntry{}, false
	}
	if !entry.Expire.After(now) {
		simulatedCacheMu.Lock()
		if current, exists := simulatedCacheEntries[key]; exists && !current.Expire.After(now) {
			delete(simulatedCacheEntries, key)
		}
		simulatedCacheMu.Unlock()
		return simulatedCacheEntry{}, false
	}
	return entry, true
}

func setSimulatedCacheEntry(key string, entry simulatedCacheEntry) {
	simulatedCacheCleanupOnce.Do(startSimulatedCacheCleanup)
	simulatedCacheMu.Lock()
	simulatedCacheEntries[key] = entry
	simulatedCacheMu.Unlock()
}

func resetSimulatedCacheForTest() {
	simulatedCacheMu.Lock()
	clear(simulatedCacheEntries)
	simulatedCacheMu.Unlock()
	SetSimulatedCacheConfig(defaultSimulatedCacheConfig())
}

func describeSimulatedCacheKeyForTest(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) string {
	return fmt.Sprintf("%s", GenerateSimulatedCacheKey(ctx, auth, req, opts))
}
