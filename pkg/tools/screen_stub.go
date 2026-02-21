//go:build !linux

package tools

import (
	"context"
	"fmt"
)

func runADBCommandImpl(ctx context.Context, args ...string) (string, error) {
	return "", fmt.Errorf("ADB commands are only available on Linux/Android")
}

func screenshotExecute(ctx context.Context, workspace string) *ToolResult {
	return ErrorResult("screenshot is only available on Android/Termux")
}

func screenTap(ctx context.Context, x, y int) *ToolResult {
	return ErrorResult("screen_tap is only available on Android/Termux")
}

func screenSwipe(ctx context.Context, x1, y1, x2, y2, durationMs int) *ToolResult {
	return ErrorResult("screen_swipe is only available on Android/Termux")
}

func screenKey(ctx context.Context, keycode string) *ToolResult {
	return ErrorResult("screen_key is only available on Android/Termux")
}

func screenText(ctx context.Context, text string) *ToolResult {
	return ErrorResult("screen_text is only available on Android/Termux")
}

func appLaunch(ctx context.Context, pkg string) *ToolResult {
	return ErrorResult("app_launch is only available on Android/Termux")
}

func screenWait(ctx context.Context, seconds int) *ToolResult {
	return ErrorResult("screen_wait is only available on Android/Termux")
}

func screenInfo(ctx context.Context) *ToolResult {
	return ErrorResult("screen_info is only available on Android/Termux")
}

func uiElementsDump(ctx context.Context) *ToolResult {
	return ErrorResult("ui_elements is only available on Android/Termux")
}
