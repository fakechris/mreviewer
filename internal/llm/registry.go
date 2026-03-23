package llm

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	ctxpkg "github.com/mreviewer/mreviewer/internal/context"
)

type FallbackProvider struct {
	primary        Provider
	secondary      Provider
	primaryRoute   string
	secondaryRoute string
	logger         *slog.Logger
}

// ProviderRegistry maps route names to Provider instances, enabling
// runtime selection of the correct provider based on effective policy.
type ProviderRegistry struct {
	providers     map[string]Provider
	defaultRoute  string
	fallbackRoute string
	logger        *slog.Logger
}

// NewProviderRegistry creates a registry with a default and optional
// fallback route. Additional routes can be registered via Register.
func NewProviderRegistry(logger *slog.Logger, defaultRoute string, defaultProvider Provider) *ProviderRegistry {
	if logger == nil {
		logger = slog.Default()
	}
	r := &ProviderRegistry{
		providers:    make(map[string]Provider),
		defaultRoute: strings.TrimSpace(defaultRoute),
		logger:       logger,
	}
	r.providers[r.defaultRoute] = defaultProvider
	return r
}

// Register adds a provider under the given route name.
func (r *ProviderRegistry) Register(route string, provider Provider) {
	route = strings.TrimSpace(route)
	if route == "" || provider == nil {
		return
	}
	r.providers[route] = provider
}

// SetFallbackRoute designates a route to try when the primary route
// for a run fails with a retryable/fallback-eligible error.
func (r *ProviderRegistry) SetFallbackRoute(route string) {
	r.fallbackRoute = strings.TrimSpace(route)
}

// Resolve returns the Provider registered for the given route.
// If the route is unknown, it falls back to the default route.
func (r *ProviderRegistry) Resolve(route string) (Provider, string) {
	route = strings.TrimSpace(route)
	if route == "" {
		route = r.defaultRoute
	}
	if p, ok := r.providers[route]; ok {
		return p, route
	}
	r.logger.Warn("unknown provider route, falling back to default", "requested_route", route, "default_route", r.defaultRoute)
	return r.providers[r.defaultRoute], r.defaultRoute
}

// ResolveWithFallback returns a FallbackProvider that uses the
// requested route as primary and the registry's fallback route as
// secondary. If the requested route equals the fallback route (or
// no fallback is configured), a plain provider is returned.
func (r *ProviderRegistry) ResolveWithFallback(route string) Provider {
	primary, resolvedRoute := r.Resolve(route)
	if r.fallbackRoute == "" || r.fallbackRoute == resolvedRoute {
		return primary
	}
	secondary, secondaryRoute := r.Resolve(r.fallbackRoute)
	return NewFallbackProvider(r.logger, primary, resolvedRoute, secondary, secondaryRoute)
}

// Routes returns the list of registered route names.
func (r *ProviderRegistry) Routes() []string {
	routes := make([]string, 0, len(r.providers))
	for route := range r.providers {
		routes = append(routes, route)
	}
	sort.Strings(routes)
	return routes
}

func NewFallbackProvider(logger *slog.Logger, primary Provider, primaryRoute string, secondary Provider, secondaryRoute string) *FallbackProvider {
	if logger == nil {
		logger = slog.Default()
	}
	return &FallbackProvider{primary: primary, secondary: secondary, primaryRoute: strings.TrimSpace(primaryRoute), secondaryRoute: strings.TrimSpace(secondaryRoute), logger: logger}
}

func (p *FallbackProvider) RequestPayload(request ctxpkg.ReviewRequest) map[string]any {
	if p == nil || p.primary == nil {
		return map[string]any{}
	}
	payload := p.primary.RequestPayload(request)
	if payload == nil {
		payload = map[string]any{}
	}
	payload["provider_route"] = p.primaryRoute
	if p.secondary != nil && p.secondaryRoute != "" {
		payload["secondary_provider_route"] = p.secondaryRoute
	}
	return payload
}

func (p *FallbackProvider) RequestPayloadWithSystemPrompt(request ctxpkg.ReviewRequest, systemPrompt string) map[string]any {
	if p == nil || p.primary == nil {
		return map[string]any{}
	}
	payload := requestPayloadWithSystemPrompt(p.primary, request, systemPrompt)
	if payload == nil {
		payload = map[string]any{}
	}
	payload["provider_route"] = p.primaryRoute
	if p.secondary != nil && p.secondaryRoute != "" {
		payload["secondary_provider_route"] = p.secondaryRoute
	}
	return payload
}

func (p *FallbackProvider) Review(ctx context.Context, request ctxpkg.ReviewRequest) (ProviderResponse, error) {
	if p == nil || p.primary == nil {
		return ProviderResponse{}, fmt.Errorf("llm: primary provider is required")
	}
	response, err := p.primary.Review(ctx, request)
	if err == nil || p.secondary == nil || !shouldFallbackToSecondary(err) {
		return response, err
	}
	p.logger.WarnContext(ctx, "primary provider failed, retrying with secondary provider", "primary_provider_route", p.primaryRoute, "secondary_provider_route", p.secondaryRoute, "error", err)
	secondaryResponse, secondaryErr := p.secondary.Review(ctx, request)
	if secondaryErr != nil {
		return ProviderResponse{}, fmt.Errorf("llm: primary provider %q failed: %w; secondary provider %q failed: %v", p.primaryRoute, err, p.secondaryRoute, secondaryErr)
	}
	if secondaryResponse.ResponsePayload == nil {
		secondaryResponse.ResponsePayload = map[string]any{}
	}
	secondaryResponse.ResponsePayload["fallback_from_provider_route"] = p.primaryRoute
	secondaryResponse.ResponsePayload["provider_route"] = p.secondaryRoute
	secondaryResponse.FallbackStage = strings.TrimSpace(joinNonEmpty(secondaryResponse.FallbackStage, "secondary_provider"))
	if secondaryResponse.Model == "" {
		secondaryResponse.Model = p.secondaryRoute
	}
	return secondaryResponse, nil
}

func (p *FallbackProvider) ReviewWithSystemPrompt(ctx context.Context, request ctxpkg.ReviewRequest, systemPrompt string) (ProviderResponse, error) {
	if p == nil || p.primary == nil {
		return ProviderResponse{}, fmt.Errorf("llm: primary provider is required")
	}
	response, err := reviewWithSystemPrompt(p.primary, ctx, request, systemPrompt)
	if err == nil || p.secondary == nil || !shouldFallbackToSecondary(err) {
		return response, err
	}
	p.logger.WarnContext(ctx, "primary provider failed, retrying with secondary provider", "primary_provider_route", p.primaryRoute, "secondary_provider_route", p.secondaryRoute, "error", err)
	secondaryResponse, secondaryErr := reviewWithSystemPrompt(p.secondary, ctx, request, systemPrompt)
	if secondaryErr != nil {
		return ProviderResponse{}, fmt.Errorf("llm: primary provider %q failed: %w; secondary provider %q failed: %v", p.primaryRoute, err, p.secondaryRoute, secondaryErr)
	}
	if secondaryResponse.ResponsePayload == nil {
		secondaryResponse.ResponsePayload = map[string]any{}
	}
	secondaryResponse.ResponsePayload["fallback_from_provider_route"] = p.primaryRoute
	secondaryResponse.ResponsePayload["provider_route"] = p.secondaryRoute
	secondaryResponse.FallbackStage = strings.TrimSpace(joinNonEmpty(secondaryResponse.FallbackStage, "secondary_provider"))
	if secondaryResponse.Model == "" {
		secondaryResponse.Model = p.secondaryRoute
	}
	return secondaryResponse, nil
}

func shouldFallbackToSecondary(err error) bool {
	if err == nil {
		return false
	}
	if isTimeoutError(err) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "provider_request_failed") || strings.Contains(message, "5xx") || strings.Contains(message, "status 500") || strings.Contains(message, "status 502") || strings.Contains(message, "status 503") || strings.Contains(message, "status 504")
}

func joinNonEmpty(parts ...string) string {
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		filtered = append(filtered, part)
	}
	return strings.Join(filtered, ":")
}
