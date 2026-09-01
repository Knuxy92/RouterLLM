package adapter

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
)

type MediaResolver func(reference string) (data []byte, mediaType string, err error)

func translateOpenAIContent(content any, resolve MediaResolver) ([]any, error) {
	if content == nil {
		return nil, nil
	}
	if text, ok := content.(string); ok {
		return []any{map[string]any{"type": "text", "text": text}}, nil
	}
	parts, ok := content.([]any)
	if !ok {
		return nil, fmt.Errorf("content must be a string or array")
	}

	result := make([]any, 0, len(parts))
	for _, part := range parts {
		block, ok := part.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("content part must be an object")
		}
		typeName, _ := block["type"].(string)
		switch typeName {
		case "text", "input_text":
			text, _ := block["text"].(string)
			result = append(result, map[string]any{"type": "text", "text": text})
		case "image_url":
			image, ok := block["image_url"].(map[string]any)
			if !ok {
				return nil, fmt.Errorf("image_url must be an object")
			}
			ref, _ := image["url"].(string)
			block, err := mediaBlock(ref, "image", resolve)
			if err != nil {
				return nil, err
			}
			result = append(result, block)
		case "input_image":
			ref, _ := block["image_url"].(string)
			block, err := mediaBlock(ref, "image", resolve)
			if err != nil {
				return nil, err
			}
			result = append(result, block)
		case "image_file", "input_file", "file":
			ref := firstString(block, "file_id", "file_url", "file_data", "url")
			block, err := mediaBlock(ref, "document", resolve)
			if err != nil {
				return nil, err
			}
			result = append(result, block)
		case "image", "document":
			result = append(result, block)
		default:
			return nil, fmt.Errorf("unsupported content part type %q", typeName)
		}
	}
	return result, nil
}

func mediaBlock(reference, blockType string, resolve MediaResolver) (map[string]any, error) {
	data, mediaType, err := decodeMediaReference(reference)
	if err != nil && resolve != nil {
		data, mediaType, err = resolve(reference)
	}
	if err != nil {
		return nil, err
	}
	if blockType == "image" && !strings.HasPrefix(mediaType, "image/") {
		return nil, fmt.Errorf("media type %q is not an image", mediaType)
	}
	if blockType == "document" && mediaType == "" {
		mediaType = "application/pdf"
	}
	return map[string]any{
		"type": blockType,
		"source": map[string]any{
			"type":       "base64",
			"media_type": mediaType,
			"data":       base64.StdEncoding.EncodeToString(data),
		},
	}, nil
}

func decodeMediaReference(reference string) ([]byte, string, error) {
	if strings.HasPrefix(reference, "data:") {
		meta, encoded, ok := strings.Cut(reference, ",")
		if !ok || !strings.Contains(meta, ";base64") {
			return nil, "", fmt.Errorf("invalid base64 data URL")
		}
		mediaType := strings.TrimPrefix(strings.Split(meta, ";")[0], "data:")
		data, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, "", fmt.Errorf("invalid base64 data URL: %w", err)
		}
		return data, mediaType, nil
	}
	if parsed, err := url.Parse(reference); err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") {
		return nil, "", fmt.Errorf("remote media requires resolver")
	}
	return nil, "", fmt.Errorf("unsupported media reference")
}

func firstString(block map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := block[key].(string); ok && value != "" {
			return value
		}
	}
	return ""
}

func anthropicContentToOpenAI(content any) []any {
	blocks, ok := content.([]any)
	if !ok {
		return nil
	}
	result := make([]any, 0, len(blocks))
	for _, raw := range blocks {
		block, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		switch block["type"] {
		case "text":
			result = append(result, map[string]any{"type": "text", "text": block["text"]})
		case "image":
			if source, ok := block["source"].(map[string]any); ok {
				if sourceType, _ := source["type"].(string); sourceType == "url" {
					result = append(result, map[string]any{"type": "image_url", "image_url": map[string]any{"url": source["url"]}})
					continue
				}
				if data, ok := source["data"].(string); ok {
					mediaType, _ := source["media_type"].(string)
					result = append(result, map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:" + mediaType + ";base64," + data}})
					continue
				}
				if fileID, ok := source["file_id"].(string); ok && fileID != "" {
					result = append(result, map[string]any{"type": "image_file", "image_file": map[string]any{"file_id": fileID}})
				}
			}
		case "document":
			if source, ok := block["source"].(map[string]any); ok {
				if sourceType, _ := source["type"].(string); sourceType == "url" {
					result = append(result, map[string]any{"type": "input_file", "file_url": source["url"]})
					continue
				}
				data, _ := source["data"].(string)
				mediaType, _ := source["media_type"].(string)
				if data != "" {
					result = append(result, map[string]any{"type": "input_file", "file_data": "data:" + mediaType + ";base64," + data})
					continue
				}
				if fileID, ok := source["file_id"].(string); ok && fileID != "" {
					result = append(result, map[string]any{"type": "input_file", "file_id": fileID})
				}
			}
		}
	}
	return result
}

func hasNonTextContent(content []any) bool {
	for _, part := range content {
		block, ok := part.(map[string]any)
		if ok && block["type"] != "text" {
			return true
		}
	}
	return false
}
