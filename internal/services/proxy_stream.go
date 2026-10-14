package services

import (
	"encoding/json"
	"io"
	"sort"

	"routerllm/internal/model"
	"routerllm/internal/util"
)

// bufferStream folds an upstream SSE body into one chat completion. Gateways
// that answer 200 and only then fail the model call stream an error frame
// instead; the raw payload of the first one is returned so the caller can
// surface the failure rather than hand the client an empty completion.
func bufferStream(body io.Reader) (*model.ChatCompletionResponse, string) {
	content := make(map[int]string)
	reasoning := make(map[int]string)
	finish := make(map[int]string)
	tools := make(map[int]map[int]*model.ToolCall)

	var usage json.RawMessage
	var resultID, modelName, systemFP string
	var created int64
	sawMeta := false
	upstreamErr := ""

	_, _ = util.IterDataLines(body, func(payload string) bool {
		if _, isError := util.ParseErrorFrame(payload); isError {
			if upstreamErr == "" {
				upstreamErr = payload
			}
			return true
		}

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
			if len(c.Delta.ToolCalls) > 0 {
				calls := tools[idx]
				if calls == nil {
					calls = make(map[int]*model.ToolCall)
					tools[idx] = calls
				}
				for _, fragment := range c.Delta.ToolCalls {
					mergeToolCallFragment(calls, fragment)
				}
			}
			if c.FinishReason != nil {
				finish[idx] = *c.FinishReason
			}
		}
		return true
	})

	if !sawMeta {
		if upstreamErr != "" {
			return nil, upstreamErr
		}

		return &model.ChatCompletionResponse{
			Object:  "chat.completion",
			Choices: []model.Choice{},
		}, ""
	}

	indices := make(map[int]bool)
	for i := range content {
		indices[i] = true
	}
	for i := range finish {
		indices[i] = true
	}
	for i := range tools {
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
		if calls := tools[idx]; len(calls) > 0 {
			msg.ToolCalls = assembleToolCalls(calls)
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

	// An error frame only decides the outcome when the stream carried no content
	// at all: real output that arrived before the failure is still the answer.
	if upstreamErr != "" && len(choices) == 0 {
		return nil, upstreamErr
	}

	return &model.ChatCompletionResponse{
		ID:                resultID,
		Object:            "chat.completion",
		Created:           created,
		Model:             modelName,
		SystemFingerprint: systemFP,
		Choices:           choices,
		Usage:             usage,
	}, ""
}

// mergeToolCallFragment folds one streaming tool_call delta into the
// per-choice accumulator: identity fields are set once, argument fragments
// concatenate in arrival order.
func mergeToolCallFragment(calls map[int]*model.ToolCall, fragment model.ToolCall) {
	existing := calls[fragment.Index]
	if existing == nil {
		existing = &model.ToolCall{Index: fragment.Index}
		calls[fragment.Index] = existing
	}

	if fragment.ID != "" {
		existing.ID = fragment.ID
	}
	if fragment.Type != "" {
		existing.Type = fragment.Type
	}
	if fragment.Function.Name != "" {
		existing.Function.Name = fragment.Function.Name
	}
	existing.Function.Arguments += fragment.Function.Arguments
}

func assembleToolCalls(fragments map[int]*model.ToolCall) []model.ToolCall {
	indexes := make([]int, 0, len(fragments))
	for i := range fragments {
		indexes = append(indexes, i)
	}
	sort.Ints(indexes)

	calls := make([]model.ToolCall, 0, len(indexes))
	for _, i := range indexes {
		calls = append(calls, *fragments[i])
	}

	return calls
}
