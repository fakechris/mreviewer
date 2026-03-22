package reviewlang

import "strings"

const DefaultOutputLanguage = "zh-CN"

func Normalize(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return DefaultOutputLanguage
	}

	switch strings.ToLower(value) {
	case "zh", "zh-cn", "zh_hans", "zh-hans", "zh-sg":
		return "zh-CN"
	case "en", "en-us", "en_us":
		return "en-US"
	default:
		return value
	}
}

func IsChinese(value string) bool {
	return strings.HasPrefix(strings.ToLower(Normalize(value)), "zh")
}
