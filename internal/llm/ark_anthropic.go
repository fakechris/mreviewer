package llm

type ArkAnthropicProvider struct {
	*MiniMaxProvider
}

func NewArkAnthropicProvider(cfg ProviderConfig) (*ArkAnthropicProvider, error) {
	arkProfile := anthropicToolProfile{kind: ProviderKindArkAnthropic, outputMode: openAIOutputModeToolCall}
	prov, err := newAnthropicToolProvider(cfg, arkProfile)
	if err != nil {
		return nil, err
	}
	if prov.timeoutRetries <= 0 {
		prov.timeoutRetries = 3
	}
	return &ArkAnthropicProvider{MiniMaxProvider: prov}, nil
}

var (
	_ Provider              = (*ArkAnthropicProvider)(nil)
	_ DynamicPromptProvider = (*ArkAnthropicProvider)(nil)
)
