package model

import "encoding/json"

type Model struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

type ModelList struct {
	Object string  `json:"object"`
	Data   []Model `json:"data"`
}

type StreamChunk struct {
	ID                string          `json:"id"`
	Object            string          `json:"object"`
	Created           int64           `json:"created"`
	Model             string          `json:"model"`
	SystemFingerprint string          `json:"system_fingerprint"`
	Choices           []StreamChoice  `json:"choices"`
	Usage             json.RawMessage `json:"usage,omitempty"`
}

type StreamChoice struct {
	Index        int     `json:"index"`
	Delta        Delta   `json:"delta"`
	FinishReason *string `json:"finish_reason"`
}

type Delta struct {
	Role             string `json:"role,omitempty"`
	Content          string `json:"content,omitempty"`
	ReasoningContent string `json:"reasoning_content,omitempty"`
}

type ChatCompletionResponse struct {
	ID                string          `json:"id"`
	Object            string          `json:"object"`
	Created           int64           `json:"created"`
	Model             string          `json:"model"`
	SystemFingerprint string          `json:"system_fingerprint"`
	Choices           []Choice        `json:"choices"`
	Usage             json.RawMessage `json:"usage"`
}

type Choice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

type Message struct {
	Role             string `json:"role"`
	Content          string `json:"content"`
	ReasoningContent string `json:"reasoning_content,omitempty"`
}

type RequestDefaults struct {
	ReasoningEffort string `yaml:"reasoning_effort" json:"reasoning_effort"`
	EnableThinking  *bool  `yaml:"enable_thinking,omitempty" json:"enable_thinking,omitempty"`
	ThinkingBudget  int    `yaml:"thinking_budget" json:"thinking_budget"`
}

type Spec struct {
	Provider string          `yaml:"provider" json:"provider"`
	Model    string          `yaml:"model" json:"model"`
	Defaults RequestDefaults `yaml:"defaults,omitzero" json:"defaults,omitzero"`
}

type Rule struct {
	Model  string `yaml:"model" json:"model"`
	Routes []Spec `yaml:"routes" json:"routes"`
}
