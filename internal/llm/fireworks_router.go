package llm

import (
	"net/http"
	"time"
)

type xApiKeyTransport struct {
	base   http.RoundTripper
	apiKey string
}

func (t *xApiKeyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header.Del("Authorization")
	clone.Header.Set("x-api-key", t.apiKey)
	return t.base.RoundTrip(clone)
}

func newFireworksHTTPClient(apiKey string, base *http.Client) *http.Client {
	if base != nil {
		client := *base
		transport := client.Transport
		if transport == nil {
			transport = http.DefaultTransport
		}
		client.Transport = &xApiKeyTransport{base: transport, apiKey: apiKey}
		if client.Timeout == 0 {
			client.Timeout = 180 * time.Second
		}
		return &client
	}
	return &http.Client{
		Transport: &xApiKeyTransport{base: http.DefaultTransport, apiKey: apiKey},
		Timeout:   180 * time.Second,
	}
}

type FireworksRouterProvider struct {
	*MiniMaxProvider
}

var fireworksRouterProfile = anthropicToolProfile{
	kind:       ProviderKindFireworksRouter,
	outputMode: openAIOutputModeToolCall,
}

func NewFireworksRouterProvider(cfg ProviderConfig) (*FireworksRouterProvider, error) {
	if cfg.TimeoutRetries <= 0 {
		cfg.TimeoutRetries = 3
	}
	cfg.HTTPClient = newFireworksHTTPClient(cfg.APIKey, cfg.HTTPClient)
	prov, err := newAnthropicToolProvider(cfg, fireworksRouterProfile)
	if err != nil {
		return nil, err
	}
	return &FireworksRouterProvider{MiniMaxProvider: prov}, nil
}

var (
	_ Provider              = (*FireworksRouterProvider)(nil)
	_ DynamicPromptProvider = (*FireworksRouterProvider)(nil)
)
