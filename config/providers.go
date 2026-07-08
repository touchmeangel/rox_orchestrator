package config

type ModelSpec struct {
	ID                      string
	SupportsReasoningEffort bool
	DefaultTemperature      float64
	HasDefaultTemperature   bool
}

type Provider struct {
	Key                          string
	Label                        string
	Models                       []ModelSpec
	EnvKey                       string
	ProviderStr                  string
	BaseURL                      string
	EffortLevels                 []string
	DefaultEffort                string
	DefaultTemperature           float64
	AllModelsSupportsReasoning   bool
	AllModelsSupportsTemperature bool
}

var ProviderOrder = []string{"anthropic", "openai", "google", "huggingface", "openrouter", "cerebras", "naga", "ollama"}

var Providers = map[string]Provider{
	"anthropic": {
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
		Label: "OpenAI",
		Models: []ModelSpec{
			{ID: "gpt-5.5", SupportsReasoningEffort: true},
			{ID: "gpt-5", SupportsReasoningEffort: true},
			{ID: "gpt-4.1", DefaultTemperature: 0.2, HasDefaultTemperature: true},
			{ID: "gpt-4.1-mini", DefaultTemperature: 0.3, HasDefaultTemperature: true},
			{ID: "gpt-4o", DefaultTemperature: 0.3, HasDefaultTemperature: true},
		},
		EnvKey:             "OPENAI_API_KEY",
		EffortLevels:       []string{"none", "low", "medium", "high", "xhigh"},
		DefaultEffort:      "medium",
		DefaultTemperature: 0.3,
	},
	"google": {
		Label: "Google",
		Models: []ModelSpec{
			{ID: "gemini-3.5-flash", SupportsReasoningEffort: true},
			{ID: "gemini-3.1-pro", SupportsReasoningEffort: true},
			{ID: "gemini-3.1-flash-lite", SupportsReasoningEffort: true},
			{ID: "gemini-2.5-flash", SupportsReasoningEffort: true},
		},
		EnvKey:             "GOOGLE_API_KEY",
		BaseURL:            "https://generativelanguage.googleapis.com/v1beta/openai/",
		EffortLevels:       []string{"low", "medium", "high"},
		DefaultEffort:      "medium",
		DefaultTemperature: 0.3,
	},
	"huggingface": {
		Label:                      "Hugging Face",
		EnvKey:                     "HF_TOKEN",
		BaseURL:                    "https://router.huggingface.co/v1",
		EffortLevels:               []string{"none", "minimal", "low", "medium", "high", "xhigh"},
		DefaultEffort:              "medium",
		AllModelsSupportsReasoning: true,
	},
	"openrouter": {
		Label:                      "OpenRouter",
		EnvKey:                     "OPENROUTER_API_KEY",
		BaseURL:                    "https://openrouter.ai/api/v1",
		EffortLevels:               []string{"none", "minimal", "low", "medium", "high", "xhigh"},
		DefaultEffort:              "medium",
		AllModelsSupportsReasoning: true,
	},
	"cerebras": {
		Label:              "Cerebras",
		EnvKey:             "CEREBRAS_API_KEY",
		BaseURL:            "https://api.cerebras.ai/v1",
		EffortLevels:       []string{"none", "low", "medium", "high"},
		DefaultEffort:      "medium",
		DefaultTemperature: 0.3,
	},
	"naga": {
		Label:                        "Naga AI",
		EnvKey:                       "NAGA_API_KEY",
		BaseURL:                      "https://api.naga.ac/v1",
		DefaultTemperature:           0.2,
		AllModelsSupportsTemperature: true,
	},
	"ollama": {
		Label:              "Ollama (local)",
		EnvKey:             "OLLAMA_API_KEY",
		BaseURL:            "http://localhost:11434/v1",
		DefaultTemperature: 0.3,
	},
}

func init() {
	for key, p := range Providers {
		p.Key = key
		if p.ProviderStr == "" {
			p.ProviderStr = "openai"
		}
		Providers[key] = p
	}

	seen := map[string]bool{}
	for _, key := range ProviderOrder {
		if seen[key] {
			panic("config: duplicate provider key in ProviderOrder: " + key)
		}
		seen[key] = true
		if _, ok := Providers[key]; !ok {
			panic("config: ProviderOrder references unknown provider: " + key)
		}
	}
	for key := range Providers {
		if !seen[key] {
			panic("config: provider missing from ProviderOrder: " + key)
		}
	}
}

var TargetEnvKeys = func() []string {
	seen := map[string]bool{}
	keys := make([]string, 0, len(Providers))
	for _, key := range ProviderOrder {
		env := Providers[key].EnvKey
		if env == "" || seen[env] {
			continue
		}
		seen[env] = true
		keys = append(keys, env)
	}
	return keys
}()
