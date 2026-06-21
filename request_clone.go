package deebus

// CloneRequest returns a deep copy of req suitable for mutation by middleware,
// request policies, or provider adapters.
func CloneRequest(req *Request) Request {
	if req == nil {
		return Request{}
	}

	clone := *req
	clone.Messages = cloneMessages(req.Messages)
	clone.Tools = cloneTools(req.Tools)
	clone.Options = cloneAnyMap(req.Options)
	clone.Metadata = cloneStringMap(req.Metadata)

	if req.Seed != nil {
		v := *req.Seed
		clone.Seed = &v
	}
	if req.Store != nil {
		v := *req.Store
		clone.Store = &v
	}
	if req.ResponseFormat != nil {
		rf := *req.ResponseFormat
		rf.Schema = cloneAnyMap(req.ResponseFormat.Schema)
		clone.ResponseFormat = &rf
	}
	if req.Reasoning != nil {
		reasoning := *req.Reasoning
		clone.Reasoning = &reasoning
	}
	if req.ParallelToolCalls != nil {
		v := *req.ParallelToolCalls
		clone.ParallelToolCalls = &v
	}
	if req.Cache != nil {
		cache := *req.Cache
		if req.Cache.Control != nil {
			control := *req.Cache.Control
			cache.Control = &control
		}
		clone.Cache = &cache
	}

	return clone
}

func cloneMessages(src []Message) []Message {
	if len(src) == 0 {
		return nil
	}
	dst := make([]Message, len(src))
	for i, msg := range src {
		dst[i] = msg
		dst[i].Content = cloneContentBlocks(msg.Content)
		dst[i].ToolCalls = cloneToolCalls(msg.ToolCalls)
	}
	return dst
}

func cloneContentBlocks(src []ContentBlock) []ContentBlock {
	if len(src) == 0 {
		return nil
	}
	dst := make([]ContentBlock, len(src))
	for i, block := range src {
		dst[i] = cloneContentBlock(block)
	}
	return dst
}

func cloneContentBlock(block ContentBlock) ContentBlock {
	switch b := block.(type) {
	case TextContent:
		if b.CacheControl != nil {
			control := *b.CacheControl
			b.CacheControl = &control
		}
		return b
	case *TextContent:
		if b == nil {
			return b
		}
		cp := *b
		if b.CacheControl != nil {
			control := *b.CacheControl
			cp.CacheControl = &control
		}
		return &cp
	case ImageContent:
		if b.CacheControl != nil {
			control := *b.CacheControl
			b.CacheControl = &control
		}
		return b
	case *ImageContent:
		if b == nil {
			return b
		}
		cp := *b
		if b.CacheControl != nil {
			control := *b.CacheControl
			cp.CacheControl = &control
		}
		return &cp
	case AudioContent:
		return b
	case *AudioContent:
		if b == nil {
			return b
		}
		cp := *b
		return &cp
	case DocumentContent:
		if b.CacheControl != nil {
			control := *b.CacheControl
			b.CacheControl = &control
		}
		return b
	case *DocumentContent:
		if b == nil {
			return b
		}
		cp := *b
		if b.CacheControl != nil {
			control := *b.CacheControl
			cp.CacheControl = &control
		}
		return &cp
	default:
		return block
	}
}

func cloneTools(src []Tool) []Tool {
	if len(src) == 0 {
		return nil
	}
	dst := make([]Tool, len(src))
	for i, tool := range src {
		dst[i] = tool
		dst[i].Function.Parameters = cloneAnyMap(tool.Function.Parameters)
		if tool.CacheControl != nil {
			control := *tool.CacheControl
			dst[i].CacheControl = &control
		}
	}
	return dst
}

func cloneToolCalls(src []ToolCall) []ToolCall {
	if len(src) == 0 {
		return nil
	}
	dst := make([]ToolCall, len(src))
	copy(dst, src)
	return dst
}

func cloneStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func cloneAnyMap(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = cloneAny(v)
	}
	return dst
}

func cloneAny(v any) any {
	switch value := v.(type) {
	case map[string]any:
		return cloneAnyMap(value)
	case []any:
		out := make([]any, len(value))
		for i := range value {
			out[i] = cloneAny(value[i])
		}
		return out
	case []string:
		out := make([]string, len(value))
		copy(out, value)
		return out
	case []map[string]any:
		out := make([]map[string]any, len(value))
		for i := range value {
			out[i] = cloneAnyMap(value[i])
		}
		return out
	default:
		return value
	}
}
