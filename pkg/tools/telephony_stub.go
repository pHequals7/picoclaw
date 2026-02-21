//go:build !linux

package tools

import (
	"context"
	"fmt"
)

func runTermuxCommandImpl(ctx context.Context, name string, args ...string) (string, error) {
	return "", fmt.Errorf("termux commands are only available on Linux/Android")
}

func smsSend(ctx context.Context, number, message string) *ToolResult {
	return ErrorResult("SMS send is only available on Android/Termux")
}

func smsList(ctx context.Context, limit int, msgType string) *ToolResult {
	return ErrorResult("SMS list is only available on Android/Termux")
}

func phoneCall(ctx context.Context, number string) *ToolResult {
	return ErrorResult("Phone calls are only available on Android/Termux")
}

func phoneInfo(ctx context.Context) *ToolResult {
	return ErrorResult("Phone info is only available on Android/Termux")
}
