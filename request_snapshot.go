package deebus

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"
)

// FingerprintOptions controls request hashing used by RequestSnapshot.
type FingerprintOptions struct {
	// Salt scopes fingerprints by deployment or tenant. It is optional but
	// recommended when snapshots leave the process boundary.
	Salt string `yaml:"salt"`

	// IncludeText controls whether text content contributes to the fingerprint.
	// The snapshot still stores only hashes and byte counts.
	IncludeText bool `yaml:"includeText"`
}

// Enabled reports whether non-default fingerprint options were set.
func (o FingerprintOptions) Enabled() bool {
	return o.Salt != "" || o.IncludeText
}

// RequestSnapshot is a prompt-safe description of an AI request.
type RequestSnapshot struct {
	Provider    string    `json:"provider,omitempty"`
	Model       string    `json:"model,omitempty"`
	Fingerprint string    `json:"fingerprint"`
	CreatedAt   time.Time `json:"created_at"`

	MessageCount int              `json:"message_count"`
	Messages     []MessageSummary `json:"messages,omitempty"`

	ToolCount       int      `json:"tool_count"`
	ToolNames       []string `json:"tool_names,omitempty"`
	ToolSchemaBytes int      `json:"tool_schema_bytes"`

	TextBytes     int `json:"text_bytes"`
	MediaBytes    int `json:"media_bytes"`
	ImageCount    int `json:"image_count"`
	AudioCount    int `json:"audio_count"`
	DocumentCount int `json:"document_count"`

	MetadataKeys []string       `json:"metadata_keys,omitempty"`
	OptionKeys   []string       `json:"option_keys,omitempty"`
	Cache        *CacheSnapshot `json:"cache,omitempty"`
	Store        *bool          `json:"store,omitempty"`
	UserIDHash   string         `json:"user_id_hash,omitempty"`
}

// MessageSummary describes one message without storing prompt content.
type MessageSummary struct {
	Role          string            `json:"role"`
	BlockCount    int               `json:"block_count"`
	TextBytes     int               `json:"text_bytes"`
	TextHash      string            `json:"text_hash,omitempty"`
	ImageCount    int               `json:"image_count"`
	AudioCount    int               `json:"audio_count"`
	DocumentCount int               `json:"document_count"`
	ToolCallCount int               `json:"tool_call_count"`
	Blocks        []BlockSummary    `json:"blocks,omitempty"`
	ToolCalls     []ToolCallSummary `json:"tool_calls,omitempty"`
}

// BlockSummary describes a content block type and size.
type BlockSummary struct {
	Type       string `json:"type"`
	Bytes      int    `json:"bytes,omitempty"`
	MediaType  string `json:"media_type,omitempty"`
	SourceType string `json:"source_type,omitempty"`
	Hash       string `json:"hash,omitempty"`
}

// ToolCallSummary describes a model-requested tool call without storing
// function arguments.
type ToolCallSummary struct {
	ID             string `json:"id,omitempty"`
	Type           string `json:"type,omitempty"`
	Name           string `json:"name,omitempty"`
	ArgumentsBytes int    `json:"arguments_bytes,omitempty"`
	ArgumentsHash  string `json:"arguments_hash,omitempty"`
}

// CacheSnapshot describes cache controls without exposing provider resource
// names directly.
type CacheSnapshot struct {
	HasControl        bool   `json:"has_control,omitempty"`
	HasKey            bool   `json:"has_key,omitempty"`
	KeyHash           string `json:"key_hash,omitempty"`
	Retention         string `json:"retention,omitempty"`
	HasCachedContent  bool   `json:"has_cached_content,omitempty"`
	CachedContentHash string `json:"cached_content_hash,omitempty"`
}

// SnapshotRequest returns a prompt-safe request snapshot and fingerprint.
func SnapshotRequest(provider, model string, req *Request, opts FingerprintOptions) RequestSnapshot {
	if req == nil {
		req = &Request{}
	}
	s := RequestSnapshot{
		Provider:     provider,
		Model:        model,
		CreatedAt:    time.Now().UTC(),
		MessageCount: len(req.Messages),
		ToolCount:    len(req.Tools),
		MetadataKeys: sortedStringMapKeys(req.Metadata),
		OptionKeys:   sortedAnyMapKeys(req.Options),
	}

	textBytes, mediaBytes := RequestPayloadBytes(req)
	s.TextBytes = textBytes
	s.MediaBytes = mediaBytes
	s.ToolSchemaBytes = ToolSchemaBytes(req.Tools)

	for _, tool := range req.Tools {
		if tool.Function.Name != "" {
			s.ToolNames = append(s.ToolNames, tool.Function.Name)
		}
	}
	sort.Strings(s.ToolNames)

	if req.Store != nil {
		store := *req.Store
		s.Store = &store
	}
	if req.UserID != "" {
		s.UserIDHash = hashWithSalt(opts.Salt, req.UserID)
	}
	if req.Cache != nil {
		s.Cache = snapshotCache(req.Cache, opts.Salt)
	}

	s.Messages = make([]MessageSummary, 0, len(req.Messages))
	for _, msg := range req.Messages {
		summary := snapshotMessage(msg, opts)
		s.ImageCount += summary.ImageCount
		s.AudioCount += summary.AudioCount
		s.DocumentCount += summary.DocumentCount
		s.Messages = append(s.Messages, summary)
	}

	s.Fingerprint = FingerprintRequest(provider, model, req, opts)
	return s
}

func snapshotMessage(msg Message, opts FingerprintOptions) MessageSummary {
	summary := MessageSummary{
		Role:          msg.Role,
		BlockCount:    len(msg.Content),
		ToolCallCount: len(msg.ToolCalls),
	}
	textHasher := sha256.New()
	if opts.Salt != "" {
		_, _ = textHasher.Write([]byte(opts.Salt))
	}

	for _, block := range msg.Content {
		blockSummary, text := snapshotBlock(block, opts)
		if text != "" {
			summary.TextBytes += len(text)
			if opts.IncludeText {
				_, _ = textHasher.Write([]byte(text))
			}
		}
		switch blockSummary.Type {
		case "image":
			summary.ImageCount++
		case "audio":
			summary.AudioCount++
		case "document":
			summary.DocumentCount++
		}
		summary.Blocks = append(summary.Blocks, blockSummary)
	}
	summary.ToolCalls = snapshotToolCalls(msg.ToolCalls, opts)

	if opts.IncludeText && summary.TextBytes > 0 {
		summary.TextHash = hex.EncodeToString(textHasher.Sum(nil))
	}
	return summary
}

func snapshotBlock(block ContentBlock, opts FingerprintOptions) (BlockSummary, string) {
	switch value := block.(type) {
	case TextContent:
		return BlockSummary{Type: "text", Bytes: len(value.Text), Hash: optionalHash(opts, value.Text)}, value.Text
	case *TextContent:
		if value == nil {
			return BlockSummary{Type: "unknown"}, ""
		}
		return BlockSummary{Type: "text", Bytes: len(value.Text), Hash: optionalHash(opts, value.Text)}, value.Text
	case ImageContent:
		return BlockSummary{Type: "image", Bytes: imageSourceBytes(value.Source), MediaType: value.Source.MediaType, SourceType: value.Source.Type}, ""
	case *ImageContent:
		if value == nil {
			return BlockSummary{Type: "unknown"}, ""
		}
		return BlockSummary{Type: "image", Bytes: imageSourceBytes(value.Source), MediaType: value.Source.MediaType, SourceType: value.Source.Type}, ""
	case AudioContent:
		return BlockSummary{Type: "audio", Bytes: len(value.Source.Data), SourceType: value.Source.Type}, ""
	case *AudioContent:
		if value == nil {
			return BlockSummary{Type: "unknown"}, ""
		}
		return BlockSummary{Type: "audio", Bytes: len(value.Source.Data), SourceType: value.Source.Type}, ""
	case DocumentContent:
		return BlockSummary{Type: "document", Bytes: documentSourceBytes(value.Source), MediaType: value.Source.MediaType, SourceType: value.Source.Type}, ""
	case *DocumentContent:
		if value == nil {
			return BlockSummary{Type: "unknown"}, ""
		}
		return BlockSummary{Type: "document", Bytes: documentSourceBytes(value.Source), MediaType: value.Source.MediaType, SourceType: value.Source.Type}, ""
	default:
		return BlockSummary{Type: "unknown"}, ""
	}
}

func snapshotCache(cache *CacheOptions, salt string) *CacheSnapshot {
	if cache == nil {
		return nil
	}
	s := &CacheSnapshot{
		HasControl: cache.Control != nil,
		Retention:  cache.Retention,
	}
	if cache.Key != "" {
		s.HasKey = true
		s.KeyHash = hashWithSalt(salt, cache.Key)
	}
	if cache.CachedContent != "" {
		s.HasCachedContent = true
		s.CachedContentHash = hashWithSalt(salt, cache.CachedContent)
	}
	return s
}

func snapshotToolCalls(calls []ToolCall, opts FingerprintOptions) []ToolCallSummary {
	if len(calls) == 0 {
		return nil
	}
	out := make([]ToolCallSummary, 0, len(calls))
	for _, call := range calls {
		summary := ToolCallSummary{
			ID:             call.ID,
			Type:           call.Type,
			Name:           call.Function.Name,
			ArgumentsBytes: len(call.Function.Arguments),
		}
		if opts.IncludeText && call.Function.Arguments != "" {
			summary.ArgumentsHash = hashWithSalt(opts.Salt, call.Function.Arguments)
		}
		out = append(out, summary)
	}
	return out
}

// FingerprintRequest returns a deterministic SHA-256 request fingerprint. It
// includes text only when opts.IncludeText is true. The hash is safe to store,
// but callers should add Salt when fingerprints cross trust boundaries.
func FingerprintRequest(provider, model string, req *Request, opts FingerprintOptions) string {
	if req == nil {
		req = &Request{}
	}
	canonical := map[string]any{
		"provider": provider,
		"model":    model,
		"request":  canonicalRequest(req, opts),
	}
	raw, _ := json.Marshal(canonical)
	return hashBytes(opts.Salt, raw)
}

func canonicalRequest(req *Request, opts FingerprintOptions) map[string]any {
	out := map[string]any{
		"max_tokens":          req.MaxTokens,
		"max_output_tokens":   req.MaxOutputTokens,
		"temperature":         req.Temperature,
		"top_p":               req.TopP,
		"stop":                append([]string(nil), req.Stop...),
		"stream":              req.Stream,
		"tool_choice":         req.ToolChoice,
		"metadata_keys":       sortedStringMapKeys(req.Metadata),
		"option_keys":         sortedAnyMapKeys(req.Options),
		"user_id_hash":        hashWithSalt(opts.Salt, req.UserID),
		"tools":               canonicalTools(req.Tools),
		"messages":            canonicalMessages(req.Messages, opts),
		"cache":               canonicalCache(req.Cache, opts.Salt),
		"response_format":     req.ResponseFormat,
		"reasoning":           req.Reasoning,
		"parallel_tool_calls": req.ParallelToolCalls,
	}
	if req.Seed != nil {
		out["seed"] = *req.Seed
	}
	if req.Store != nil {
		out["store"] = *req.Store
	}
	return out
}

func canonicalMessages(messages []Message, opts FingerprintOptions) []map[string]any {
	out := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
		out = append(out, map[string]any{
			"role":         msg.Role,
			"tool_call_id": msg.ToolCallID,
			"name":         msg.Name,
			"content":      canonicalBlocks(msg.Content, opts),
			"tool_calls":   snapshotToolCalls(msg.ToolCalls, opts),
		})
	}
	return out
}

func canonicalBlocks(blocks []ContentBlock, opts FingerprintOptions) []map[string]any {
	out := make([]map[string]any, 0, len(blocks))
	for _, block := range blocks {
		summary, text := snapshotBlock(block, opts)
		item := map[string]any{
			"type":        summary.Type,
			"bytes":       summary.Bytes,
			"media_type":  summary.MediaType,
			"source_type": summary.SourceType,
		}
		if opts.IncludeText && text != "" {
			item["text_hash"] = hashWithSalt(opts.Salt, text)
		}
		out = append(out, item)
	}
	return out
}

func canonicalTools(tools []Tool) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		out = append(out, map[string]any{
			"type":        tool.Type,
			"name":        tool.Function.Name,
			"description": tool.Function.Description,
			"strict":      tool.Function.Strict,
			"schema_hash": hashAny(tool.Function.Parameters),
		})
	}
	return out
}

func canonicalCache(cache *CacheOptions, salt string) map[string]any {
	if cache == nil {
		return nil
	}
	return map[string]any{
		"has_control":         cache.Control != nil,
		"key_hash":            hashWithSalt(salt, cache.Key),
		"retention":           cache.Retention,
		"cached_content_hash": hashWithSalt(salt, cache.CachedContent),
	}
}

func sortedStringMapKeys(values map[string]string) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedAnyMapKeys(values map[string]any) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func optionalHash(opts FingerprintOptions, value string) string {
	if !opts.IncludeText || value == "" {
		return ""
	}
	return hashWithSalt(opts.Salt, value)
}

func hashAny(value any) string {
	raw, _ := json.Marshal(value)
	return hashBytes("", raw)
}

func hashWithSalt(salt, value string) string {
	if value == "" {
		return ""
	}
	return hashBytes(salt, []byte(value))
}

func hashBytes(salt string, value []byte) string {
	h := sha256.New()
	if salt != "" {
		_, _ = h.Write([]byte(salt))
	}
	_, _ = h.Write(value)
	return hex.EncodeToString(h.Sum(nil))
}
