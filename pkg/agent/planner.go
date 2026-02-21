package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/usage"
	"github.com/sipeed/picoclaw/pkg/utils"
)

const plannerSystemPrompt = `You are an execution planner for PicoClaw, a personal AI agent running on an Android phone via Termux.

Environment:
- You run directly on the device (ARM64 Linux userland via Termux)
- You have ADB loopback access (localhost:5555) for screen automation
- You can: take screenshots, tap/swipe/type on screen, launch apps, send SMS, make calls, execute shell commands, read/write files, search the web, and interact with the user via Telegram
- You can query the UI element tree (ui_elements) to get exact coordinates for every button, text field, and interactive element — prefer this over screenshots for finding tap targets
- Screen tools let you operate any Android app — navigate UIs, fill forms, tap buttons, read screen content via screenshots

Return only a numbered list of concrete execution steps.
Guidance:
- Prefer 4-6 steps, but fewer steps are allowed when the task is simple.
- Use imperative action language (e.g., "Take screenshot to see current screen", "Tap search icon at coordinates", "Type query into search field").
- For UI automation tasks, start with ui_elements to get exact tap coordinates, or screenshot for visual context.
- For multi-step app interactions, include screenshot checkpoints between actions to verify state.
- Ground steps in the provided request and candidate tool actions.
- Do not include headings, notes, explanations, or markdown fences.
- Do not mention policies.`

func (al *AgentLoop) generateExecutionPlanBullets(ctx context.Context, opts processOptions, activeModel string, activeProvider providers.LLMProvider, toolCalls []providers.ToolCall) ([]string, string) {
	fallback := buildExecutionPlanBullets(toolCalls)
	plannerCfg := al.config.Agents.Planner
	if !plannerCfg.Enabled {
		return fallback, activeModel
	}

	plannerModel := strings.TrimSpace(plannerCfg.Model)
	if plannerModel == "" {
		return fallback, activeModel
	}

	plannerProvider := activeProvider
	if plannerModel != activeModel {
		providerForPlan, err := providers.CreateProviderForModel(al.config, plannerModel)
		if err != nil {
			logger.WarnCF("agent", "Planner provider initialization failed; using fallback plan",
				map[string]interface{}{
					"planner_model": plannerModel,
					"error":         err.Error(),
				})
			return fallback, activeModel
		}
		plannerProvider = providerForPlan
	}

	requestText := strings.TrimSpace(opts.UserMessage)
	if requestText == "" {
		requestText = "(empty)"
	}
	requestText = utils.Truncate(requestText, 1200)

	candidateSteps := make([]string, 0, len(toolCalls))
	seen := make(map[string]struct{})
	for _, tc := range toolCalls {
		step := summarizeToolCallForPlan(tc)
		if step == "" {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(step))
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		candidateSteps = append(candidateSteps, step)
		if len(candidateSteps) >= 10 {
			break
		}
	}
	var toolList strings.Builder
	for i, step := range candidateSteps {
		toolList.WriteString(fmt.Sprintf("%d. %s\n", i+1, step))
	}

	plannerUserPrompt := fmt.Sprintf("User request:\n%s\n\nCandidate tool actions:\n%s\nReturn only the numbered list.", requestText, strings.TrimSpace(toolList.String()))
	plannerMessages := []providers.Message{
		{Role: "system", Content: plannerSystemPrompt},
		{Role: "user", Content: plannerUserPrompt},
	}

	response, err := plannerProvider.Chat(ctx, plannerMessages, nil, plannerModel, map[string]interface{}{
		"max_tokens":  4096,
		"temperature": 0.1,
	})
	if err != nil {
		logger.WarnCF("agent", "Planner model call failed; using fallback plan",
			map[string]interface{}{
				"planner_model": plannerModel,
				"error":         err.Error(),
			})
		return fallback, activeModel
	}

	if al.usageStore != nil {
		usageKnown := response.Usage != nil
		promptTokens := 0
		completionTokens := 0
		totalTokens := 0
		if usageKnown {
			promptTokens = response.Usage.PromptTokens
			completionTokens = response.Usage.CompletionTokens
			totalTokens = response.Usage.TotalTokens
		}
		if totalTokens == 0 {
			totalTokens = promptTokens + completionTokens
		}
		al.usageStore.Add(usage.Record{
			Timestamp:        time.Now().UTC(),
			SessionKey:       opts.SessionKey,
			DayKey:           time.Now().UTC().Format("2006-01-02"),
			Provider:         providerFromModel(plannerModel),
			Model:            plannerModel,
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
			TotalTokens:      totalTokens,
			UsageKnown:       usageKnown,
			Reason:           "planner_call",
		})
	}

	parsed := parseExecutionPlanBullets(response.Content)
	if len(parsed) == 0 {
		logger.WarnCF("agent", "Planner returned unparsable plan; using fallback plan",
			map[string]interface{}{
				"planner_model": plannerModel,
				"raw_preview":   utils.Truncate(response.Content, 200),
			})
		return fallback, plannerModel
	}
	return parsed, plannerModel
}
