package adapter

import (
	"encoding/json"
	"testing"
)

func TestTranslateOpenAIImageDataURLToAnthropic(t *testing.T) {
	body := map[string]any{"messages": []any{map[string]any{"role": "user", "content": []any{
		map[string]any{"type": "text", "text": "describe"},
		map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,aGk="}},
	}}}}
	data, _, err := TranslateRequest(body, "claude")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	msg := got["messages"].([]any)[0].(map[string]any)
	image := msg["content"].([]any)[1].(map[string]any)
	source := image["source"].(map[string]any)
	if source["media_type"] != "image/png" || source["data"] != "aGk=" {
		t.Fatalf("unexpected image source: %#v", source)
	}
}

func TestTranslateAnthropicImageAndDocumentToOpenAI(t *testing.T) {
	raw := []byte(`{"messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/jpeg","data":"aGk="}},{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"cGRm"}}]}]}`)
	data, err := AnthropicRequestToOpenAI(raw)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	content := got["messages"].([]any)[0].(map[string]any)["content"].([]any)
	if content[0].(map[string]any)["type"] != "image_url" || content[1].(map[string]any)["type"] != "input_file" {
		t.Fatalf("unexpected content: %#v", content)
	}
}

func TestTranslateRejectsInvalidImageDataURL(t *testing.T) {
	body := map[string]any{"messages": []any{map[string]any{"role": "user", "content": []any{
		map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,!!!"}},
	}}}}
	if _, _, err := TranslateRequest(body, "claude"); err == nil {
		t.Fatal("expected invalid image error")
	}
}

func TestTranslateResolvesRemoteImageInMemory(t *testing.T) {
	body := map[string]any{"messages": []any{map[string]any{"role": "user", "content": []any{
		map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://example.test/image.jpg"}},
	}}}}
	resolve := func(reference string) ([]byte, string, error) {
		if reference != "https://example.test/image.jpg" {
			t.Fatalf("reference = %q", reference)
		}
		return []byte("image"), "image/jpeg", nil
	}
	data, _, err := TranslateRequestWithResolver(body, "claude", resolve)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("expected translated body")
	}
}

func TestAnthropicReferencesRemainRepresentable(t *testing.T) {
	raw := []byte(`{"messages":[{"role":"user","content":[{"type":"image","source":{"type":"url","url":"https://example.test/image.jpg"}},{"type":"document","source":{"type":"file","file_id":"file_1"}}]}]}`)
	data, err := AnthropicRequestToOpenAI(raw)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	content := got["messages"].([]any)[0].(map[string]any)["content"].([]any)
	if content[0].(map[string]any)["image_url"].(map[string]any)["url"] != "https://example.test/image.jpg" {
		t.Fatal("image URL was not preserved")
	}
	if content[1].(map[string]any)["file_id"] != "file_1" {
		t.Fatal("file ID was not preserved")
	}
}
