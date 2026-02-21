package tools

import (
	"context"
	"fmt"

	"github.com/sipeed/picoclaw/pkg/utils"
)

// SMSSendTool sends an SMS message via termux-api.
type SMSSendTool struct{}

func NewSMSSendTool() *SMSSendTool { return &SMSSendTool{} }

func (t *SMSSendTool) Name() string { return "sms_send" }

func (t *SMSSendTool) Description() string {
	return "Send an SMS text message to a phone number. Requires Termux with termux-api installed on Android."
}

func (t *SMSSendTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"number": map[string]interface{}{
				"type":        "string",
				"description": "Phone number to send the SMS to (e.g. \"+1234567890\")",
			},
			"message": map[string]interface{}{
				"type":        "string",
				"description": "Text message content to send",
			},
		},
		"required": []string{"number", "message"},
	}
}

func (t *SMSSendTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	if !utils.IsTermux() {
		return ErrorResult("sms_send requires Termux with termux-api on Android")
	}

	number, ok := args["number"].(string)
	if !ok || number == "" {
		return ErrorResult("number is required")
	}
	message, ok := args["message"].(string)
	if !ok || message == "" {
		return ErrorResult("message is required")
	}

	return smsSend(ctx, number, message)
}

// SMSListTool lists SMS messages via termux-api.
type SMSListTool struct{}

func NewSMSListTool() *SMSListTool { return &SMSListTool{} }

func (t *SMSListTool) Name() string { return "sms_list" }

func (t *SMSListTool) Description() string {
	return "List SMS messages from the phone. Requires Termux with termux-api installed on Android."
}

func (t *SMSListTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"limit": map[string]interface{}{
				"type":        "integer",
				"description": "Maximum number of messages to return (default: 10)",
			},
			"type": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"inbox", "sent", "draft", "all"},
				"description": "Type of messages to list (default: inbox)",
			},
		},
	}
}

func (t *SMSListTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	if !utils.IsTermux() {
		return ErrorResult("sms_list requires Termux with termux-api on Android")
	}

	limit := 10
	if l, ok := args["limit"].(float64); ok && l > 0 {
		limit = int(l)
	}

	msgType := "inbox"
	if mt, ok := args["type"].(string); ok && mt != "" {
		msgType = mt
	}

	return smsList(ctx, limit, msgType)
}

// PhoneCallTool initiates a phone call via termux-api.
type PhoneCallTool struct{}

func NewPhoneCallTool() *PhoneCallTool { return &PhoneCallTool{} }

func (t *PhoneCallTool) Name() string { return "phone_call" }

func (t *PhoneCallTool) Description() string {
	return "Initiate a phone call to a number. Requires Termux with termux-api installed on Android."
}

func (t *PhoneCallTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"number": map[string]interface{}{
				"type":        "string",
				"description": "Phone number to call (e.g. \"+1234567890\")",
			},
		},
		"required": []string{"number"},
	}
}

func (t *PhoneCallTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	if !utils.IsTermux() {
		return ErrorResult("phone_call requires Termux with termux-api on Android")
	}

	number, ok := args["number"].(string)
	if !ok || number == "" {
		return ErrorResult("number is required")
	}

	return phoneCall(ctx, number)
}

// PhoneInfoTool gets device/SIM info via termux-api.
type PhoneInfoTool struct{}

func NewPhoneInfoTool() *PhoneInfoTool { return &PhoneInfoTool{} }

func (t *PhoneInfoTool) Name() string { return "phone_info" }

func (t *PhoneInfoTool) Description() string {
	return "Get phone device and SIM card information. Requires Termux with termux-api installed on Android."
}

func (t *PhoneInfoTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
	}
}

func (t *PhoneInfoTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	if !utils.IsTermux() {
		return ErrorResult("phone_info requires Termux with termux-api on Android")
	}

	return phoneInfo(ctx)
}

// runTermuxCommand executes a termux-api command and returns the output.
func runTermuxCommand(ctx context.Context, name string, args ...string) (string, error) {
	output, err := runTermuxCommandImpl(ctx, name, args...)
	if err != nil {
		return "", fmt.Errorf("%s failed: %w", name, err)
	}
	return output, nil
}
