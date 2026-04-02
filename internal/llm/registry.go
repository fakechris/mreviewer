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
	primary      Provider
	fallbacks    []namedProvider
	primaryRoute string
	logger       *slog.Logger
}

type namedProvider struct {
	route    string
	provider Provider
}

// ProviderRegistry maps route names to Provider instances, enabling
// runtime selection of the correct provider based on effective policy.
type ProviderRegistry struct {
	providers       map[string]Provider
	defaultRoute    string
	fallbackRoutes  []string
	logger          *slog.Logger
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

// SetFallbackRoutes designates routes to try when the primary route
// for a run fails with a retryable/fallback-eligible error.
func (r *ProviderRegistry) SetFallbackRoutes(routes []string) {
	seen := map[string]struct{}{}
	r.fallbackRoutes = r.fallbackRoutes[:0]
	for _, route := range routes {
		route = strings.TrimSpace(route)
		if route == "" {
			continue
		}
		if _, ok := seen[route]; ok {
			continue
		}
		seen[route] = struct{}{}
		r.fallbackRoutes = append(r.fallbackRoutes, route)
	}
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
// requested route as primary and the registry's configured fallback routes.
// If no usable fallback routes remain, a plain provider is returned.
func (r *ProviderRegistry) ResolveWithFallback(route string) Provider {
	return r.ResolveWithFallbackRoutes(route, r.fallbackRoutes)
}

// ResolveWithFallbackRoutes returns a Provider using the requested route as
// primary and the provided ordered fallback routes as alternates.
func (r *ProviderRegistry) ResolveWithFallbackRoutes(route string, fallbackRoutes []string) Provider {
	primary, resolvedRoute := r.Resolve(route)
	fallbackProviders := make([]namedProvider, 0, len(fallbackRoutes))
	seen := map[string]struct{}{resolvedRoute: {}}
	for _, fallbackRoute := range fallbackRoutes {
		fallbackRoute = strings.TrimSpace(fallbackRoute)
		if fallbackRoute == "" {
			continue
		}
		if _, ok := seen[fallbackRoute]; ok {
			continue
		}
		fallback, resolvedFallbackRoute := r.Resolve(fallbackRoute)
		if _, ok := seen[resolvedFallbackRoute]; ok {
			continue
		}
		seen[resolvedFallbackRoute] = struct{}{}
		fallbackProviders = append(fallbackProviders, namedProvider{
			route:    resolvedFallbackRoute,
			provider: fallback,
		})
	}
	if len(fallbackProviders) == 0 {
		return primary
	}
	return NewFallbackProvider(r.logger, primary, resolvedRoute, fallbackProviders)
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

func NewFallbackProvider(logger *slog.Logger, primary Provider, primaryRoute string, fallbacks []namedProvider) *FallbackProvider {
	if logger == nil {
		logger = slog.Default()
	}
	return &FallbackProvider{primary: primary, fallbacks: fallbacks, primaryRoute: strings.TrimSpace(primaryRoute), logger: logger}
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
	if len(p.fallbacks) > 0 {
		payload["secondary_provider_route"] = p.fallbacks[0].route
		if len(p.fallbacks) > 1 {
			routes := make([]string, 0, len(p.fallbacks))
			for _, fallback := range p.fallbacks {
				routes = append(routes, fallback.route)
			}
			payload["secondary_provider_routes"] = routes
		}
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
	if len(p.fallbacks) > 0 {
		payload["secondary_provider_route"] = p.fallbacks[0].route
		if len(p.fallbacks) > 1 {
			routes := make([]string, 0, len(p.fallbacks))
			for _, fallback := range p.fallbacks {
				routes = append(routes, fallback.route)
			}
			payload["secondary_provider_routes"] = routes
		}
	}
	return payload
}

func (p *FallbackProvider) Review(ctx context.Context, request ctxpkg.ReviewRequest) (ProviderResponse, error) {
	if p == nil || p.primary == nil {
		return ProviderResponse{}, fmt.Errorf("llm: primary provider is required")
	}
	response, err := p.primary.Review(ctx, request)
	if err == nil || len(p.fallbacks) == 0 || !shouldFallbackToSecondary(err) {
		return response, err
	}
	return p.reviewFallbackChain(ctx, request, err, func(provider Provider) (ProviderResponse, error) {
		return provider.Review(ctx, request)
	})
}

func (p *FallbackProvider) reviewFallbackChain(ctx context.Context, request ctxpkg.ReviewRequest, primaryErr error, call func(Provider) (ProviderResponse, error)) (ProviderResponse, error) {
	previousRoute := p.primaryRoute
	previousErr := primaryErr
	failures := make([]string, 0, len(p.fallbacks)+1)
	failures = append(failures, fmt.Sprintf("%s: %v", p.primaryRoute, primaryErr))
	for _, fallback := range p.fallbacks {
		if fallback.provider == nil || fallback.route == "" {
			continue
		}
		p.logger.WarnContext(ctx, "provider failed, retrying with fallback provider", "failed_provider_route", previousRoute, "fallback_provider_route", fallback.route, "error", previousErr)
		response, err := call(fallback.provider)
		if err == nil {
			if response.ResponsePayload == nil {
				response.ResponsePayload = map[string]any{}
			}
			response.ResponsePayload["fallback_from_provider_route"] = previousRoute
			response.ResponsePayload["provider_route"] = fallback.route
			response.FallbackStage = strings.TrimSpace(joinNonEmpty(response.FallbackStage, "fallback_provider"))
			if response.Model == "" {
				response.Model = fallback.route
			}
			return response, nil
		}
		failures = append(failures, fmt.Sprintf("%s: %v", fallback.route, err))
		if !shouldFallbackToSecondary(err) {
			return ProviderResponse{}, fmt.Errorf("llm: provider chain failed (%s): %w", strings.Join(failures, "; "), err)
		}
		previousRoute = fallback.route
		previousErr = err
	}
	return ProviderResponse{}, fmt.Errorf("llm: provider chain failed (%s): %w", strings.Join(failures, "; "), previousErr)
}

func (p *FallbackProvider) ReviewWithSystemPrompt(ctx context.Context, request ctxpkg.ReviewRequest, systemPrompt string) (ProviderResponse, error) {
	if p == nil || p.primary == nil {
		return ProviderResponse{}, fmt.Errorf("llm: primary provider is required")
	}
	response, err := reviewWithSystemPrompt(p.primary, ctx, request, systemPrompt)
	if err == nil || len(p.fallbacks) == 0 || !shouldFallbackToSecondary(err) {
		return response, err
	}
	return p.reviewFallbackChain(ctx, request, err, func(provider Provider) (ProviderResponse, error) {
		return reviewWithSystemPrompt(provider, ctx, request, systemPrompt)
	})
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
