//go:build linux

package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// runADBCommandImpl executes an adb command and returns its output.
func runADBCommandImpl(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "adb", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("adb %s: %w (output: %s)", strings.Join(args, " "), err, string(out))
	}
	return string(out), nil
}

func screenshotExecute(ctx context.Context, workspace string) *ToolResult {
	timestamp := time.Now().Format("20060102_150405")
	remotePath := fmt.Sprintf("/sdcard/picoclaw_screenshot_%s.png", timestamp)

	// Take screenshot via ADB
	_, err := runADBShell(ctx, "screencap", "-p", remotePath)
	if err != nil {
		return ErrorResult(fmt.Sprintf("Failed to take screenshot: %v", err))
	}

	// Pull to workspace tmp directory
	tmpDir := filepath.Join(workspace, "tmp")
	os.MkdirAll(tmpDir, 0755)
	localPath := filepath.Join(tmpDir, fmt.Sprintf("screenshot_%s.png", timestamp))

	_, err = runADB(ctx, "pull", remotePath, localPath)
	if err != nil {
		return ErrorResult(fmt.Sprintf("Failed to pull screenshot: %v", err))
	}

	// Clean up remote file
	runADBShell(ctx, "rm", remotePath)

	return SilentResult(fmt.Sprintf("Screenshot saved to %s — use send_file to send it to the user.", localPath))
}

func screenTap(ctx context.Context, x, y int) *ToolResult {
	_, err := runADBShell(ctx, "input", "tap", fmt.Sprintf("%d", x), fmt.Sprintf("%d", y))
	if err != nil {
		return ErrorResult(fmt.Sprintf("Failed to tap: %v", err))
	}
	return SilentResult(fmt.Sprintf("Tapped at (%d, %d)", x, y))
}

func screenSwipe(ctx context.Context, x1, y1, x2, y2, durationMs int) *ToolResult {
	_, err := runADBShell(ctx, "input", "swipe",
		fmt.Sprintf("%d", x1), fmt.Sprintf("%d", y1),
		fmt.Sprintf("%d", x2), fmt.Sprintf("%d", y2),
		fmt.Sprintf("%d", durationMs))
	if err != nil {
		return ErrorResult(fmt.Sprintf("Failed to swipe: %v", err))
	}
	return SilentResult(fmt.Sprintf("Swiped from (%d,%d) to (%d,%d) over %dms", x1, y1, x2, y2, durationMs))
}

func screenKey(ctx context.Context, keycode string) *ToolResult {
	_, err := runADBShell(ctx, "input", "keyevent", keycode)
	if err != nil {
		return ErrorResult(fmt.Sprintf("Failed to send key %s: %v", keycode, err))
	}
	return SilentResult(fmt.Sprintf("Sent key event: %s", keycode))
}

func screenText(ctx context.Context, text string) *ToolResult {
	// ADB input text uses %s for spaces and requires shell escaping
	escaped := strings.ReplaceAll(text, " ", "%s")
	// Escape other special shell characters
	escaped = strings.ReplaceAll(escaped, "'", "\\'")
	escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
	escaped = strings.ReplaceAll(escaped, "&", "\\&")
	escaped = strings.ReplaceAll(escaped, "(", "\\(")
	escaped = strings.ReplaceAll(escaped, ")", "\\)")
	escaped = strings.ReplaceAll(escaped, "<", "\\<")
	escaped = strings.ReplaceAll(escaped, ">", "\\>")
	escaped = strings.ReplaceAll(escaped, "|", "\\|")
	escaped = strings.ReplaceAll(escaped, ";", "\\;")

	_, err := runADBShell(ctx, "input", "text", escaped)
	if err != nil {
		return ErrorResult(fmt.Sprintf("Failed to type text: %v", err))
	}
	return SilentResult(fmt.Sprintf("Typed text: %s", text))
}

func appLaunch(ctx context.Context, pkg string) *ToolResult {
	// Use monkey to launch — doesn't require knowing the activity name
	_, err := runADBShell(ctx, "monkey", "-p", pkg, "-c", "android.intent.category.LAUNCHER", "1")
	if err != nil {
		return ErrorResult(fmt.Sprintf("Failed to launch %s: %v", pkg, err))
	}
	return SilentResult(fmt.Sprintf("Launched app: %s", pkg))
}

func screenInfo(ctx context.Context) *ToolResult {
	var info strings.Builder

	// Get screen size
	sizeOutput, err := runADBShell(ctx, "wm", "size")
	if err == nil {
		info.WriteString("Screen size: ")
		info.WriteString(strings.TrimSpace(sizeOutput))
		info.WriteString("\n")
	}

	// Get screen density
	densityOutput, err := runADBShell(ctx, "wm", "density")
	if err == nil {
		info.WriteString("Density: ")
		info.WriteString(strings.TrimSpace(densityOutput))
		info.WriteString("\n")
	}

	// Get current focus
	focusOutput, err := runADBShell(ctx, "dumpsys", "window", "displays")
	if err == nil {
		for _, line := range strings.Split(focusOutput, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.Contains(trimmed, "mCurrentFocus") || strings.Contains(trimmed, "mFocusedApp") {
				info.WriteString(trimmed)
				info.WriteString("\n")
			}
		}
	}

	result := info.String()
	if result == "" {
		return ErrorResult("Failed to get screen info — is ADB connected? Run: adb connect localhost:5555")
	}

	return SilentResult(result)
}
