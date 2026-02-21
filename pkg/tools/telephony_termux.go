//go:build linux

package tools

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
)

// runTermuxCommandImpl executes a termux-api binary and returns its stdout.
func runTermuxCommandImpl(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %w (output: %s)", name, err, string(out))
	}
	return string(out), nil
}

func smsSend(ctx context.Context, number, message string) *ToolResult {
	_, err := runTermuxCommand(ctx, "termux-sms-send", "-n", number, message)
	if err != nil {
		return ErrorResult(fmt.Sprintf("Failed to send SMS: %v", err))
	}
	return SilentResult(fmt.Sprintf("SMS sent to %s", number))
}

func smsList(ctx context.Context, limit int, msgType string) *ToolResult {
	output, err := runTermuxCommand(ctx, "termux-sms-list", "-l", strconv.Itoa(limit), "-t", msgType)
	if err != nil {
		return ErrorResult(fmt.Sprintf("Failed to list SMS: %v", err))
	}
	return SilentResult(output)
}

func phoneCall(ctx context.Context, number string) *ToolResult {
	_, err := runTermuxCommand(ctx, "termux-telephony-call", number)
	if err != nil {
		return ErrorResult(fmt.Sprintf("Failed to initiate call: %v", err))
	}
	return SilentResult(fmt.Sprintf("Phone call initiated to %s", number))
}

func phoneInfo(ctx context.Context) *ToolResult {
	output, err := runTermuxCommand(ctx, "termux-telephony-deviceinfo")
	if err != nil {
		return ErrorResult(fmt.Sprintf("Failed to get phone info: %v", err))
	}
	return SilentResult(output)
}
