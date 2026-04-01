package reviewpack

func BuiltinPrompts() map[string]string {
	prompts := map[string]string{}
	for _, pack := range DefaultPacks() {
		prompts[pack.ID] = pack.SystemPrompt()
	}
	return prompts
}
