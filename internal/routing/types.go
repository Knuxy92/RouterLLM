package routing

type RequestDefaults struct {
	ReasoningEffort string `json:"reasoning_effort"`
	EnableThinking  *bool  `json:"enable_thinking,omitempty"`
	ThinkingBudget  int    `json:"thinking_budget"`
}

type Spec struct {
	Provider string          `json:"provider"`
	Model    string          `json:"model"`
	Defaults RequestDefaults `json:"defaults,omitempty"`
}

type Rule struct {
	Model  string `json:"model"`
	Routes []Spec `json:"routes"`
}

type Config struct {
	Version int    `json:"version"`
	Routes  []Rule `json:"routes"`
}
