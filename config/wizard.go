package config

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/touchmeangel/ignite_orchestrator/ui"
)

const (
	customModelLabel = "[ enter custom model id ]"
	backLabel        = "← Go back"
)

type Profile struct {
	ProviderKey        string
	ProviderStr        string
	Model              string
	BaseURL            string
	EnvKey             string
	Caps               ModelSpec
	Effort             string
	DefaultTemperature float64
}

func ResolveModelCaps(modelID string, prov Provider) (ModelSpec, error) {
	for _, m := range prov.Models {
		if m.ID == modelID {
			return m, nil
		}
	}
	fmt.Println("  " + ui.Dim(fmt.Sprintf("No capability info for '%s' — please declare:", modelID)))
	choice, err := ui.Select("This model accepts:", []string{"temperature", "reasoning_effort"}, 0)
	if err != nil {
		return ModelSpec{}, err
	}
	return ModelSpec{ID: modelID, SupportsReasoningEffort: choice == 1}, nil
}

func AskEffort(prov Provider) (string, error) {
	fmt.Println("  " + ui.Dim("Controls thinking depth."))
	defaultIdx := 0
	for i, lvl := range prov.EffortLevels {
		if lvl == prov.DefaultEffort {
			defaultIdx = i
			break
		}
	}
	idx, err := ui.Select("Reasoning effort:", prov.EffortLevels, defaultIdx)
	if err != nil {
		return "", err
	}
	return prov.EffortLevels[idx], nil
}

func AskEnvKeyOverride(defaultEnvKey string) (string, error) {
	v, err := ui.Text("API key env var for this model (enter to reuse, change for a separate key)", defaultEnvKey)
	if err != nil {
		return "", err
	}
	if v == "" {
		return defaultEnvKey, nil
	}
	return v, nil
}

func fetchOllamaModels(baseURL string) []string {
	apiBase := strings.TrimSuffix(strings.TrimSuffix(baseURL, "/"), "/v1")
	if !strings.HasPrefix(apiBase, "http://") && !strings.HasPrefix(apiBase, "https://") {
		return nil
	}
	client := http.Client{Timeout: 4 * time.Second}
	resp, err := client.Get(apiBase + "/api/tags")
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	var data struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil
	}
	names := make([]string, 0, len(data.Models))
	for _, m := range data.Models {
		names = append(names, m.Name)
	}
	return names
}

func AskModelProfile(existing ModelEntry) (Profile, error) {
	isFreshEntry := existing.Model == ""

	for {
		labels := make([]string, len(ProviderOrder))
		for i, k := range ProviderOrder {
			labels[i] = Providers[k].Label
		}

		labels = append(labels, backLabel)
		backIdx := len(labels) - 1

		defaultIdx := 0
		if !isFreshEntry {
			for i, k := range ProviderOrder {
				if k == existing.ProviderKey {
					defaultIdx = i
					break
				}
			}
		}

		pIdx, err := ui.Select("Select provider:", labels, defaultIdx)
		if err != nil {
			return Profile{}, err
		}
		if pIdx == backIdx {
			return Profile{}, fmt.Errorf("back")
		}

		provKey := ProviderOrder[pIdx]
		prov := Providers[provKey]
		sameProvider := existing.ProviderKey == provKey

		switch {
		case provKey == "ollama":
			prevURL := prov.BaseURL
			if sameProvider && existing.BaseURL != "" {
				prevURL = existing.BaseURL
			}
			prevURL = strings.NewReplacer("host.docker.internal", "localhost", "127.0.0.1", "localhost").Replace(prevURL)

			hostURL, err := ui.Text("Ollama endpoint address (type < to go back)", prevURL)
			if err != nil {
				return Profile{}, err
			}
			if hostURL == "<" {
				continue
			}
			if hostURL == "" {
				hostURL = prevURL
			}
			containerURL := strings.NewReplacer("localhost", "host.docker.internal", "127.0.0.1", "host.docker.internal").Replace(hostURL)

			models := fetchOllamaModels(hostURL)
			choices := append(append([]string{}, models...), customModelLabel, backLabel)
			idx, err := ui.Select("Select model tag:", choices, 0)
			if err != nil {
				return Profile{}, err
			}
			picked := choices[idx]
			if picked == backLabel {
				continue
			}
			if picked == customModelLabel {
				picked, err = ui.Text("Model tag", "")
				if err != nil {
					return Profile{}, err
				}
				if picked == "<" || picked == "" {
					continue
				}
			}
			caps, err := ResolveModelCaps(picked, prov)
			if err != nil {
				return Profile{}, err
			}
			return Profile{
				ProviderKey: provKey, ProviderStr: prov.ProviderStr, Model: picked,
				BaseURL: containerURL, EnvKey: prov.EnvKey, Caps: caps,
				DefaultTemperature: ModelDefaultTemp(caps, prov),
			}, nil

		case prov.AllModelsSupportsReasoning || prov.AllModelsSupportsTemperature:
			def := ""
			if sameProvider {
				def = existing.Model
			}
			model, err := ui.Text("Model ID (type < to go back)", def)
			if err != nil {
				return Profile{}, err
			}
			if model == "" || model == "<" {
				continue
			}
			caps := ModelSpec{ID: model, SupportsReasoningEffort: prov.AllModelsSupportsReasoning}
			defaultTemp := prov.DefaultTemperature
			effort := ""
			if caps.SupportsReasoningEffort && len(prov.EffortLevels) > 0 {
				effort, err = AskEffort(prov)
				if err != nil {
					return Profile{}, err
				}
			}
			envKey, err := AskEnvKeyOverride(prov.EnvKey)
			if err != nil {
				return Profile{}, err
			}
			return Profile{ProviderKey: provKey, ProviderStr: prov.ProviderStr, Model: model, BaseURL: prov.BaseURL, EnvKey: envKey, Caps: caps, Effort: effort, DefaultTemperature: defaultTemp}, nil

		default:
			choices := make([]string, 0, len(prov.Models)+2)
			for _, m := range prov.Models {
				choices = append(choices, m.ID)
			}
			choices = append(choices, customModelLabel, backLabel)
			idx, err := ui.Select("Select a model variant:", choices, 0)
			if err != nil {
				return Profile{}, err
			}
			picked := choices[idx]
			if picked == backLabel {
				continue
			}
			if picked == customModelLabel {
				picked, err = ui.Text("Model signature string", "")
				if err != nil {
					return Profile{}, err
				}
				if picked == "" || picked == "<" {
					continue
				}
			}
			caps, err := ResolveModelCaps(picked, prov)
			if err != nil {
				return Profile{}, err
			}
			defaultTemp := ModelDefaultTemp(caps, prov)
			effort := ""
			if caps.SupportsReasoningEffort {
				effort, err = AskEffort(prov)
				if err != nil {
					return Profile{}, err
				}
			}
			envKey, err := AskEnvKeyOverride(prov.EnvKey)
			if err != nil {
				return Profile{}, err
			}
			return Profile{ProviderKey: provKey, ProviderStr: prov.ProviderStr, Model: picked, BaseURL: prov.BaseURL, EnvKey: envKey, Caps: caps, Effort: effort, DefaultTemperature: defaultTemp}, nil
		}
	}
}

func entryParamsHint(e ModelEntry) string {
	if e.ReasoningEffort != "" {
		return "effort=" + e.ReasoningEffort
	}
	if e.Temperature != nil {
		return "temp=" + strconv.FormatFloat(*e.Temperature, 'g', -1, 64)
	}
	return ""
}

func PrintChain(chain []ModelEntry) {
	t := ui.Table{Headers: []string{"#", "Role", "Model", "Provider", "Key env", "Params"}}
	for i, e := range chain {
		role := "primary"
		if i > 0 {
			role = fmt.Sprintf("fallback %d", i)
		}
		keyEnv := e.APIKeyEnv
		if keyEnv == "" {
			keyEnv = "—"
		}
		t.Rows = append(t.Rows, []string{strconv.Itoa(i + 1), role, e.Model, e.ProviderKey, keyEnv, entryParamsHint(e)})
	}
	t.Print()
}

func CollectKeyForEntry(entry ModelEntry, collected map[string]string) error {
	envKey := entry.APIKeyEnv
	if envKey == "" || envKey == "OLLAMA_API_KEY" {
		if envKey == "OLLAMA_API_KEY" {
			collected[envKey] = "ollama"
		}
		return nil
	}
	if collected[envKey] != "" {
		return nil
	}
	fmt.Println()
	fmt.Println("  " + ui.Dim("API key needed:"))
	token, err := ui.Password(envKey)
	if err != nil {
		return err
	}
	if token != "" {
		collected[envKey] = token
	}
	return nil
}

func entryFromProfile(p Profile) ModelEntry {
	return EntryFromProfile(p.ProviderKey, p.ProviderStr, p.Model, p.BaseURL, p.EnvKey, p.Caps, p.Effort, p.DefaultTemperature)
}

func ManageChain(chain []ModelEntry, collected map[string]string) ([]ModelEntry, error) {
	for {
		fmt.Println()
		PrintChain(chain)

		var choices []string
		for i, e := range chain {
			role := "primary"
			if i > 0 {
				role = fmt.Sprintf("fallback %d", i)
			}
			choices = append(choices, fmt.Sprintf("Edit %d. %-24s [%s]", i+1, e.Model, role))
		}

		addIdx, removeIdx := -1, -1
		choices = append(choices, "＋ Add fallback model")
		addIdx = len(choices) - 1

		if len(chain) > 1 {
			choices = append(choices, "－ Remove a model")
			removeIdx = len(choices) - 1
		}
		choices = append(choices, "✓ Save & continue")
		doneIdx := len(choices) - 1

		choice, err := ui.Select("Manage fallback chain:", choices, doneIdx)
		if err != nil {
			return nil, err
		}

		switch {
		case choice < len(chain):
			idx := choice
			fmt.Println()
			fmt.Println("  " + ui.Dim(fmt.Sprintf("Reconfiguring model %d …", idx+1)))
			profile, err := AskModelProfile(chain[idx])
			if err != nil {
				if err.Error() == "back" || err.Error() == "cancelled by user" {
					continue
				}
				return nil, err
			}
			chain[idx] = entryFromProfile(profile)
			if err := CollectKeyForEntry(chain[idx], collected); err != nil {
				return nil, err
			}

		case choice == addIdx:
			fmt.Println()
			fmt.Println("  " + ui.Dim(fmt.Sprintf("Adding fallback %d …", len(chain))))
			profile, err := AskModelProfile(ModelEntry{})
			if err != nil {
				if err.Error() == "back" || err.Error() == "cancelled by user" {
					continue
				}
				return nil, err
			}
			entry := entryFromProfile(profile)
			if err := CollectKeyForEntry(entry, collected); err != nil {
				return nil, err
			}
			chain = append(chain, entry)

		case choice == removeIdx:
			var removeChoices []string
			for i, e := range chain {
				role := "primary"
				if i > 0 {
					role = fmt.Sprintf("fallback %d", i)
				}
				removeChoices = append(removeChoices, fmt.Sprintf("%s [%s]", e.Model, role))
			}
			removeChoices = append(removeChoices, "← Cancel")
			target, err := ui.Select("Remove which model?", removeChoices, len(removeChoices)-1)
			if err != nil {
				return nil, err
			}
			if target == len(removeChoices)-1 {
				continue
			}
			if target == 0 && len(chain) > 1 {
				ok, err := ui.Confirm(fmt.Sprintf("Remove primary (%s)? %s will become the new primary.", chain[0].Model, chain[1].Model), false)
				if err != nil {
					return nil, err
				}
				if !ok {
					continue
				}
			}
			chain = append(chain[:target], chain[target+1:]...)

		default:
			return chain, nil
		}
	}
}

func RunSetup() (*Config, error) {
	if err := os.MkdirAll(IgniteHome, 0o755); err != nil {
		return nil, err
	}

	var existingChain []ModelEntry
	if Exists() {
		if cfg, err := Load(); err == nil {
			existingChain = ChainFromConfig(cfg)
		}
	}
	storedKeys := LoadEnvFile()
	collected := map[string]string{}
	for k, v := range storedKeys {
		collected[k] = v
	}

	fmt.Println()
	ui.Rule("STEP 1 — Primary model")
	var existingPrimary ModelEntry
	if len(existingChain) > 0 {
		existingPrimary = existingChain[0]
	}
	profile, err := AskModelProfile(existingPrimary)
	if err != nil {
		return nil, err
	}
	primary := entryFromProfile(profile)

	fmt.Println()
	ui.Rule("STEP 2 — API key")
	if primary.APIKeyEnv != "" && primary.APIKeyEnv != "OLLAMA_API_KEY" {
		if storedKeys[primary.APIKeyEnv] != "" {
			keep, err := ui.Confirm(fmt.Sprintf("Keep existing %s?", primary.APIKeyEnv), true)
			if err != nil {
				return nil, err
			}
			if !keep {
				token, err := ui.Password(primary.APIKeyEnv)
				if err != nil {
					return nil, err
				}
				if token != "" {
					collected[primary.APIKeyEnv] = token
				}
			}
		} else {
			token, err := ui.Password(primary.APIKeyEnv)
			if err != nil {
				return nil, err
			}
			if token != "" {
				collected[primary.APIKeyEnv] = token
			}
		}
	} else if primary.APIKeyEnv == "OLLAMA_API_KEY" {
		collected[primary.APIKeyEnv] = "ollama"
	}

	fmt.Println()
	ui.Rule("STEP 3 — Fallback chain")
	fmt.Println("  " + ui.Dim("Add fallback models that will be tried if the primary fails."))
	fmt.Println("  " + ui.Dim("Useful for rate limits, outages, or cost optimization. Optional."))
	fmt.Println()

	chain := []ModelEntry{primary}
	if len(existingChain) > 1 {
		chain = append(chain, existingChain[1:]...)
	}

	manage, err := ui.Confirm(fmt.Sprintf("Manage fallback chain? (currently %d model(s))", len(chain)), len(chain) > 1)
	if err != nil {
		return nil, err
	}
	if manage {
		chain, err = ManageChain(chain, collected)
		if err != nil {
			return nil, err
		}
	}

	cfg := ChainToConfig(chain)
	if err := Save(cfg); err != nil {
		return nil, err
	}
	if err := SaveEnvFile(collected); err != nil {
		return nil, err
	}

	fmt.Println()
	fmt.Println("  " + ui.Cyan("✔") + "  config.json  →  " + ConfigPath())
	fmt.Println("  " + ui.Cyan("✔") + "  .env         →  " + EnvPath())

	return cfg, nil
}

func RunReconfigure() (*Config, error) {
	if !Exists() {
		fmt.Println("  " + ui.Dim("No existing configuration — running first-time setup."))
		return RunSetup()
	}
	cfg, err := Load()
	if err != nil {
		return nil, err
	}
	chain := ChainFromConfig(cfg)
	if len(chain) == 0 {
		fmt.Println("  " + ui.Dim("Config exists but has no models — running first-time setup."))
		return RunSetup()
	}

	storedKeys := LoadEnvFile()
	collected := map[string]string{}
	for k, v := range storedKeys {
		collected[k] = v
	}

	var missing []ModelEntry
	for _, e := range chain {
		if e.APIKeyEnv != "" && e.APIKeyEnv != "OLLAMA_API_KEY" && collected[e.APIKeyEnv] == "" {
			missing = append(missing, e)
		}
	}
	if len(missing) > 0 {
		fmt.Println()
		ui.Rule("Missing API keys")
		for _, e := range missing {
			fmt.Printf("  %s  No stored key for %s (used by %s)\n", ui.Yellow("⚠"), e.APIKeyEnv, e.Model)
			if err := CollectKeyForEntry(e, collected); err != nil {
				return nil, err
			}
		}
	}

	fmt.Println()
	ui.Rule("Current configuration")
	chain, err = ManageChain(chain, collected)
	if err != nil {
		return nil, err
	}

	newCfg := ChainToConfig(chain)
	if err := Save(newCfg); err != nil {
		return nil, err
	}
	if err := SaveEnvFile(collected); err != nil {
		return nil, err
	}
	return newCfg, nil
}
