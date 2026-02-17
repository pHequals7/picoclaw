package providers

import "strings"

// InferProviderFromModel infers a provider label from a model identifier.
// This is used for usage reporting and does not affect routing.
func InferProviderFromModel(model string) string {
	m := strings.TrimSpace(strings.ToLower(model))
	if m == "" {
		return "unknown"
	}

	if idx := strings.Index(m, "/"); idx > 0 {
		prefix := m[:idx]
		switch prefix {
		case "openrouter":
			return "openrouter"
		case "anthropic":
			return "openrouter"
		case "openai":
			return "openrouter"
		case "google":
			return "openrouter"
		case "deepseek":
			return "openrouter"
		case "meta-llama":
			return "openrouter"
		case "moonshot":
			return "moonshot"
		case "groq":
			return "groq"
		case "nvidia":
			return "nvidia"
		case "zhipu", "glm", "zai":
			return "zhipu"
		case "vllm":
			return "vllm"
		}
	}

	switch {
	case strings.Contains(m, "claude"):
		return "anthropic"
	case strings.Contains(m, "kimi") || strings.Contains(m, "moonshot"):
		return "moonshot"
	case strings.Contains(m, "gpt") || strings.Contains(m, "o1") || strings.Contains(m, "o3") || strings.Contains(m, "o4"):
		return "openai"
	case strings.Contains(m, "gemini"):
		return "gemini"
	case strings.Contains(m, "glm") || strings.Contains(m, "zhipu") || strings.Contains(m, "zai"):
		return "zhipu"
	case strings.Contains(m, "groq"):
		return "groq"
	case strings.Contains(m, "deepseek"):
		return "deepseek"
	case strings.Contains(m, "nvidia"):
		return "nvidia"
	default:
		return "unknown"
	}
}
