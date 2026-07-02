package config

// ModelSpec is one known model under a provider.
type ModelSpec struct {
	ID                      string
	SupportsReasoningEffort bool
	// DefaultTemperature overrides the provider's default_temperature for
	// this specific model. Only meaningful when HasDefaultTemperature.
	DefaultTemperature    float64
	HasDefaultTemperature bool
}

// Provider mirrors one entry of the Python PROVIDERS dict.
type Provider struct {
	Key                 string
	Label               string
	Models              []ModelSpec
	EnvKey              string
	ProviderStr         string
	BaseURL             string
	EffortLevels        []string
	DefaultEffort       string
	DefaultTemperature  float64
	// AllModelsSupportsReasoning / AllModelsSupportsTemperature: for
	// providers with an open-ended model list (openrouter, naga) where you
	// type the model ID rather than pick from a fixed set.
	AllModelsSupportsReasoning   bool
	AllModelsSupportsTemperature bool
}

// ProviderOrder fixes the menu order (Go maps don't preserve one).
var ProviderOrder = []string{"anthropic", "openai", "google", "openrouter", "naga", "ollama"}

var Providers = map[string]Provider{
	"anthropic": {
		Key:   "anthropic",
		Label: "Anthropic",
		Models: []ModelSpec{
			{ID: "claude-opus-4-8", SupportsReasoningEffort: true},
			{ID: "claude-opus-4-7", SupportsReasoningEffort: true},
			{ID: "claude-opus-4-6", SupportsReasoningEffort: true},
			{ID: "claude-sonnet-4-6", SupportsReasoningEffort: true},
		},
		EnvKey:             "ANTHROPIC_API_KEY",
		ProviderStr:        "anthropic",
		EffortLevels:       []string{"low", "medium", "high", "xhigh", "max"},
		DefaultEffort:      "high",
		DefaultTemperature: 0.3,
	},
	"openai": {
		Key:   "openai",
		Label: "OpenAI",
		Models: []ModelSpec{
			{ID: "gpt-5.5", SupportsReasoningEffort: true},
			{ID: "gpt-5", SupportsReasoningEffort: true},
			{ID: "gpt-4.1", SupportsReasoningEffort: false, DefaultTemperature: 0.2, HasDefaultTemperature: true},
			{ID: "gpt-4.1-mini", SupportsReasoningEffort: false, DefaultTemperature: 0.3, HasDefaultTemperature: true},
			{ID: "gpt-4o", SupportsReasoningEffort: false, DefaultTemperature: 0.3, HasDefaultTemperature: true},
		},
		EnvKey:             "OPENAI_API_KEY",
		ProviderStr:        "openai",
		EffortLevels:       []string{"none", "low", "medium", "high", "xhigh"},
		DefaultEffort:      "medium",
		DefaultTemperature: 0.3,
	},
	"google": {
		Key:   "google",
		Label: "Google",
		Models: []ModelSpec{
			{ID: "gemini-3.5-flash", SupportsReasoningEffort: true},
			{ID: "gemini-3.1-pro", SupportsReasoningEffort: true},
			{ID: "gemini-3.1-flash-lite", SupportsReasoningEffort: true},
			{ID: "gemini-2.5-flash", SupportsReasoningEffort: true},
		},
		EnvKey:             "GOOGLE_API_KEY",
		ProviderStr:        "openai",
		BaseURL:            "https://generativelanguage.googleapis.com/v1beta/openai/",
		EffortLevels:       []string{"low", "medium", "high"},
		DefaultEffort:      "medium",
		DefaultTemperature: 0.3,
	},
	"openrouter": {
		Key:                        "openrouter",
		Label:                      "OpenRouter",
		EnvKey:                     "OPENROUTER_API_KEY",
		ProviderStr:                "openai",
		BaseURL:                    "https://openrouter.ai/api/v1",
		EffortLevels:               []string{"none", "minimal", "low", "medium", "high", "xhigh"},
		DefaultEffort:              "medium",
		DefaultTemperature:         0.3,
		AllModelsSupportsReasoning: true,
	},
	"naga": {
		Key:                          "naga",
		Label:                        "Naga AI",
		EnvKey:                       "NAGA_API_KEY",
		ProviderStr:                  "openai",
		BaseURL:                      "https://api.naga.ac/v1",
		DefaultTemperature:           0.2,
		AllModelsSupportsTemperature: true,
	},
	"ollama": {
		Key:                "ollama",
		Label:              "Ollama (local)",
		EnvKey:             "OLLAMA_API_KEY",
		ProviderStr:        "openai",
		BaseURL:            "http://localhost:11434/v1",
		DefaultTemperature: 0.3,
	},
}

// TargetEnvKeys is every env var the CLI knows how to forward into
// containers — used by LoadEnvVars and by the .env writer.
var TargetEnvKeys = []string{
	"ANTHROPIC_API_KEY",
	"OPENAI_API_KEY",
	"GOOGLE_API_KEY",
	"OLLAMA_API_KEY",
	"OPENROUTER_API_KEY",
	"NAGA_API_KEY",
}

const MaxChainLength = 5