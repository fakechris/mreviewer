package config

import (
	"fmt"
	"strings"

	"github.com/mreviewer/mreviewer/internal/llm"
)

func BuildProviderConfigs(cfg *Config) (map[string]llm.ProviderConfig, error) {
	if cfg == nil {
		return nil, fmt.Errorf("configuration is required")
	}
	if len(cfg.Models) == 0 {
		return nil, fmt.Errorf("models configuration is required")
	}

	providers := make(map[string]llm.ProviderConfig)
	for modelName, model := range cfg.Models {
		modelName = strings.TrimSpace(modelName)
		if modelName == "" {
			return nil, fmt.Errorf("models contains an empty model id")
		}
		kind := strings.TrimSpace(model.Provider)
		if kind == "" {
			return nil, fmt.Errorf("models.%s.provider is required", modelName)
		}
		modelNameValue := strings.TrimSpace(model.Model)
		if modelNameValue == "" {
			return nil, fmt.Errorf("models.%s.model is required", modelName)
		}
		providers[modelName] = llm.ProviderConfig{
			Kind:                kind,
			BaseURL:             strings.TrimSpace(model.BaseURL),
			APIKey:              strings.TrimSpace(model.APIKey),
			Model:               modelNameValue,
			RouteName:           modelName,
			OutputMode:          strings.TrimSpace(model.OutputMode),
			MaxTokens:           model.MaxTokens,
			MaxCompletionTokens: model.MaxCompletionTokens,
			ReasoningEffort:     strings.TrimSpace(model.ReasoningEffort),
			Temperature:         model.Temperature,
		}
	}
	return providers, nil
}

func ResolveProviderReference(cfg *Config, ref string) (string, []string, error) {
	if cfg == nil {
		return "", nil, fmt.Errorf("configuration is required")
	}
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", nil, fmt.Errorf("provider reference is required")
	}

	if chain, ok := cfg.ModelChains[ref]; ok {
		primary := strings.TrimSpace(chain.Primary)
		if primary == "" {
			return "", nil, fmt.Errorf("model_chains.%s.primary is required", ref)
		}
		if _, ok := cfg.Models[primary]; !ok {
			return "", nil, fmt.Errorf("models.%s is not configured", primary)
		}
		fallbacks := make([]string, 0, len(chain.Fallbacks))
		for _, fallback := range chain.Fallbacks {
			fallback = strings.TrimSpace(fallback)
			if fallback == "" || fallback == primary {
				continue
			}
			if _, ok := cfg.Models[fallback]; !ok {
				return "", nil, fmt.Errorf("models.%s is not configured", fallback)
			}
			fallbacks = append(fallbacks, fallback)
		}
		return primary, fallbacks, nil
	}

	if _, ok := cfg.Models[ref]; ok {
		return ref, nil, nil
	}
	return "", nil, fmt.Errorf("provider reference %q is not configured", ref)
}

func ResolveModelChain(cfg *Config, chainName string) (string, []string, map[string]llm.ProviderConfig, error) {
	if cfg == nil {
		return "", nil, nil, fmt.Errorf("configuration is required")
	}
	chainName = strings.TrimSpace(chainName)
	if chainName == "" {
		return "", nil, nil, fmt.Errorf("model chain is required")
	}
	providers, err := BuildProviderConfigs(cfg)
	if err != nil {
		return "", nil, nil, err
	}
	primary, fallbacks, err := ResolveProviderReference(cfg, chainName)
	if err != nil {
		return "", nil, nil, err
	}
	chainProviders := map[string]llm.ProviderConfig{}
	chainProviders[primary] = providers[primary]
	for _, fallback := range fallbacks {
		fallback = strings.TrimSpace(fallback)
		chainProviders[fallback] = providers[fallback]
	}
	return primary, fallbacks, chainProviders, nil
}

func ResolveReviewCatalog(cfg *Config) (string, []string, map[string]llm.ProviderConfig, error) {
	if cfg == nil {
		return "", nil, nil, fmt.Errorf("configuration is required")
	}
	providers, err := BuildProviderConfigs(cfg)
	if err != nil {
		return "", nil, nil, err
	}
	primary, fallbacks, err := ResolveProviderReference(cfg, strings.TrimSpace(cfg.Review.ModelChain))
	if err != nil {
		return "", nil, nil, err
	}
	return primary, fallbacks, providers, nil
}

func ResolveProvider(cfg *Config, registry *llm.ProviderRegistry, defaultPrimary string, defaultFallbacks []string, ref string) llm.Provider {
	if registry == nil {
		return nil
	}
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return registry.ResolveWithFallbackRoutes(defaultPrimary, defaultFallbacks)
	}
	if cfg != nil {
		if primary, fallbacks, err := ResolveProviderReference(cfg, ref); err == nil {
			return registry.ResolveWithFallbackRoutes(primary, fallbacks)
		}
	}
	return registry.ResolveWithFallbackRoutes(ref, defaultFallbacks)
}
