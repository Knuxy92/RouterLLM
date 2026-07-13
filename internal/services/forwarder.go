package services

import (
	"encoding/json"
	"io"
	"sort"

	"agentrouter/internal/model"
	"agentrouter/internal/util"
)

func bufferStream(body io.Reader) *model.ChatCompletionResponse {
	content := make(map[int]string)
	reasoning := make(map[int]string)
	finish := make(map[int]string)
	var usage json.RawMessage
	var resultID, modelName, systemFP string
	var created int64
	sawMeta := false

	_, _ = util.IterDataLines(body, func(payload string) bool {
		var chunk model.StreamChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			return true
		}
		if !sawMeta {
			resultID = chunk.ID
			modelName = chunk.Model
			systemFP = chunk.SystemFingerprint
			created = chunk.Created
			sawMeta = true
		}
		if len(chunk.Usage) > 0 {
			usage = chunk.Usage
		}
		for _, c := range chunk.Choices {
			idx := c.Index
			if c.Delta.Content != "" {
				content[idx] += c.Delta.Content
			}
			if c.Delta.ReasoningContent != "" {
				reasoning[idx] += c.Delta.ReasoningContent
			}
			if c.FinishReason != nil {
				finish[idx] = *c.FinishReason
			}
		}
		return true
	})

	if !sawMeta {
		return &model.ChatCompletionResponse{
			Object:  "chat.completion",
			Choices: []model.Choice{},
		}
	}

	indices := make(map[int]bool)
	for i := range content {
		indices[i] = true
	}
	for i := range finish {
		indices[i] = true
	}
	keysSorted := make([]int, 0, len(indices))
	for i := range indices {
		keysSorted = append(keysSorted, i)
	}
	sort.Ints(keysSorted)

	choices := make([]model.Choice, 0, len(keysSorted))
	for _, idx := range keysSorted {
		msg := model.Message{Role: "assistant", Content: content[idx]}
		if r := reasoning[idx]; r != "" {
			msg.ReasoningContent = r
		}
		fr := finish[idx]
		if fr == "" {
			fr = "stop"
		}
		choices = append(choices, model.Choice{
			Index:        idx,
			Message:      msg,
			FinishReason: fr,
		})
	}

	return &model.ChatCompletionResponse{
		ID:                resultID,
		Object:            "chat.completion",
		Created:           created,
		Model:             modelName,
		SystemFingerprint: systemFP,
		Choices:           choices,
		Usage:             usage,
	}
}
