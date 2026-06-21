package deebus

import (
	"fmt"
	"strings"
)

// RequestLimits defines provider-neutral request size guards for gateway use.
// Zero fields are disabled.
type RequestLimits struct {
	MaxMessages        int `yaml:"maxMessages"`
	MaxTextBytes       int `yaml:"maxTextBytes"`
	MaxMediaBytes      int `yaml:"maxMediaBytes"`
	MaxTools           int `yaml:"maxTools"`
	MaxToolSchemaBytes int `yaml:"maxToolSchemaBytes"`
	MaxMetadataKeys    int `yaml:"maxMetadataKeys"`
	MaxOptionKeys      int `yaml:"maxOptionKeys"`
}

// Enabled reports whether any limit is active.
func (l RequestLimits) Enabled() bool {
	return l.MaxMessages > 0 ||
		l.MaxTextBytes > 0 ||
		l.MaxMediaBytes > 0 ||
		l.MaxTools > 0 ||
		l.MaxToolSchemaBytes > 0 ||
		l.MaxMetadataKeys > 0 ||
		l.MaxOptionKeys > 0
}

// Validate rejects requests exceeding configured limits.
func (l RequestLimits) Validate(req *Request) error {
	if req == nil {
		return fmt.Errorf("request limits: request is nil")
	}
	if l.MaxMessages > 0 && len(req.Messages) > l.MaxMessages {
		return fmt.Errorf("request limits: messages=%d exceeds maxMessages=%d", len(req.Messages), l.MaxMessages)
	}
	if l.MaxTools > 0 && len(req.Tools) > l.MaxTools {
		return fmt.Errorf("request limits: tools=%d exceeds maxTools=%d", len(req.Tools), l.MaxTools)
	}
	if l.MaxMetadataKeys > 0 && len(req.Metadata) > l.MaxMetadataKeys {
		return fmt.Errorf("request limits: metadata keys=%d exceeds maxMetadataKeys=%d", len(req.Metadata), l.MaxMetadataKeys)
	}
	if l.MaxOptionKeys > 0 && len(req.Options) > l.MaxOptionKeys {
		return fmt.Errorf("request limits: option keys=%d exceeds maxOptionKeys=%d", len(req.Options), l.MaxOptionKeys)
	}

	textBytes, mediaBytes := RequestPayloadBytes(req)
	if l.MaxTextBytes > 0 && textBytes > l.MaxTextBytes {
		return fmt.Errorf("request limits: text bytes=%d exceeds maxTextBytes=%d", textBytes, l.MaxTextBytes)
	}
	if l.MaxMediaBytes > 0 && mediaBytes > l.MaxMediaBytes {
		return fmt.Errorf("request limits: media bytes=%d exceeds maxMediaBytes=%d", mediaBytes, l.MaxMediaBytes)
	}

	schemaBytes := ToolSchemaBytes(req.Tools)
	if l.MaxToolSchemaBytes > 0 && schemaBytes > l.MaxToolSchemaBytes {
		return fmt.Errorf("request limits: tool schema bytes=%d exceeds maxToolSchemaBytes=%d", schemaBytes, l.MaxToolSchemaBytes)
	}

	return nil
}

// RequestPayloadBytes returns approximate text and inline-media payload sizes.
// URL media contributes only URL length; base64 media contributes data length.
func RequestPayloadBytes(req *Request) (textBytes, mediaBytes int) {
	if req == nil {
		return 0, 0
	}
	for _, msg := range req.Messages {
		for _, block := range msg.Content {
			t, m := contentBlockBytes(block)
			textBytes += t
			mediaBytes += m
		}
		for _, call := range msg.ToolCalls {
			textBytes += len(call.Function.Name)
			textBytes += len(call.Function.Arguments)
		}
		textBytes += len(msg.ToolCallID) + len(msg.Name)
	}
	return textBytes, mediaBytes
}

func contentBlockBytes(block ContentBlock) (textBytes, mediaBytes int) {
	switch value := block.(type) {
	case TextContent:
		return len(value.Text), 0
	case *TextContent:
		if value == nil {
			return 0, 0
		}
		return len(value.Text), 0
	case ImageContent:
		return 0, imageSourceBytes(value.Source)
	case *ImageContent:
		if value == nil {
			return 0, 0
		}
		return 0, imageSourceBytes(value.Source)
	case AudioContent:
		return 0, len(value.Source.Data)
	case *AudioContent:
		if value == nil {
			return 0, 0
		}
		return 0, len(value.Source.Data)
	case DocumentContent:
		return 0, documentSourceBytes(value.Source)
	case *DocumentContent:
		if value == nil {
			return 0, 0
		}
		return 0, documentSourceBytes(value.Source)
	default:
		return 0, 0
	}
}

func imageSourceBytes(src ImageSource) int {
	return len(src.Data) + len(src.URL) + len(src.FileID)
}

func documentSourceBytes(src DocumentSource) int {
	return len(src.Data) + len(src.URL)
}

// ToolSchemaBytes returns the approximate serialized size of tool definitions.
func ToolSchemaBytes(tools []Tool) int {
	total := 0
	for _, tool := range tools {
		total += len(tool.Type)
		total += len(tool.Function.Name)
		total += len(tool.Function.Description)
		total += anySize(tool.Function.Parameters)
	}
	return total
}

func anySize(value any) int {
	switch v := value.(type) {
	case nil:
		return 0
	case string:
		return len(v)
	case map[string]any:
		total := 2
		for key, val := range v {
			total += len(key) + anySize(val) + 4
		}
		return total
	case []any:
		total := 2
		for _, item := range v {
			total += anySize(item) + 1
		}
		return total
	case []string:
		return len(strings.Join(v, ",")) + len(v)*2
	case bool:
		if v {
			return 4
		}
		return 5
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return len(fmt.Sprint(v))
	default:
		return len(fmt.Sprint(v))
	}
}
