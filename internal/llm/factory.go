package llm

import (
	"fmt"
	"log/slog"
	"strings"
)

const (
	ProviderKindMiniMax             = "minimax"
	ProviderKindAnthropicCompatible = "anthropic_compatible"
	ProviderKindAnthropic           = "anthropic"
	ProviderKindOpenAI              = "openai"
	ProviderKindArkAnthropic        = "ark_anthropic"
	ProviderKindArkOpenAI           = "ark_openai"
	ProviderKindFireworksRouter     = "fireworks_router"
)

func NewProviderFromConfig(cfg ProviderConfig) (Provider, error) {
	kind := strings.ToLower(strings.TrimSpace(cfg.Kind))
	if kind == "" {
		kind = ProviderKindMiniMax
	}
	switch kind {
	case ProviderKindMiniMax, ProviderKindAnthropicCompatible:
		return NewMiniMaxProvider(cfg)
	case ProviderKindAnthropic:
		return NewAnthropicProvider(cfg)
	case ProviderKindOpenAI:
		return NewOpenAIProvider(cfg)
	case ProviderKindArkAnthropic:
		return NewArkAnthropicProvider(cfg)
	case ProviderKindArkOpenAI:
		return NewArkOpenAIProvider(cfg)
	case ProviderKindFireworksRouter:
		return NewFireworksRouterProvider(cfg)
	default:
		return nil, fmt.Errorf("llm: unknown provider kind %q", cfg.Kind)
	}
}

func BuildProviderRegistryFromRouteConfigs(logger *slog.Logger, defaultRoute string, fallbackRoute string, routes map[string]ProviderConfig) (*ProviderRegistry, error) {
	defaultRoute = strings.TrimSpace(defaultRoute)
	if defaultRoute == "" {
		return nil, fmt.Errorf("llm: default route is required")
	}
	defaultCfg, ok := routes[defaultRoute]
	if !ok {
		return nil, fmt.Errorf("llm: missing default route config %q", defaultRoute)
	}
	defaultCfg.RouteName = defaultRoute
	defaultProvider, err := NewProviderFromConfig(defaultCfg)
	if err != nil {
		return nil, fmt.Errorf("llm: build default route %q: %w", defaultRoute, err)
	}
	registry := NewProviderRegistry(logger, defaultRoute, defaultProvider)
	for route, cfg := range routes {
		if route == defaultRoute {
			continue
		}
		cfg.RouteName = route
		provider, err := NewProviderFromConfig(cfg)
		if err != nil {
			return nil, fmt.Errorf("llm: build route %q: %w", route, err)
		}
		registry.Register(route, provider)
	}
	if strings.TrimSpace(fallbackRoute) != "" {
		trimmedFallback := strings.TrimSpace(fallbackRoute)
		if trimmedFallback != defaultRoute {
			if _, ok := routes[trimmedFallback]; !ok {
				return nil, fmt.Errorf("llm: missing fallback route config %q", trimmedFallback)
			}
		}
		registry.SetFallbackRoute(trimmedFallback)
	}
	return registry, nil
}
