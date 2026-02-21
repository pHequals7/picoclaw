//go:build linux

package tools

import (
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/utils"
)

// uiNode represents a single node in the Android UI hierarchy XML.
type uiNode struct {
	Index       string   `xml:"index,attr"`
	Text        string   `xml:"text,attr"`
	ResourceID  string   `xml:"resource-id,attr"`
	Class       string   `xml:"class,attr"`
	Package     string   `xml:"package,attr"`
	ContentDesc string   `xml:"content-desc,attr"`
	Clickable   string   `xml:"clickable,attr"`
	Enabled     string   `xml:"enabled,attr"`
	Focused     string   `xml:"focused,attr"`
	Scrollable  string   `xml:"scrollable,attr"`
	Selected    string   `xml:"selected,attr"`
	Bounds      string   `xml:"bounds,attr"`
	Children    []uiNode `xml:"node"`
}

// uiHierarchy is the root of the Android UI hierarchy XML.
type uiHierarchy struct {
	XMLName xml.Name `xml:"hierarchy"`
	Nodes   []uiNode `xml:"node"`
}

// parsedElement is a flattened, actionable UI element with computed coordinates.
type parsedElement struct {
	class       string
	text        string
	contentDesc string
	resourceID  string
	centerX     int
	centerY     int
	clickable   bool
	enabled     bool
	focused     bool
	scrollable  bool
	selected    bool
	priority    int // higher = more relevant
}

var boundsRegex = regexp.MustCompile(`\[(\d+),(\d+)\]\[(\d+),(\d+)\]`)

// parseBounds extracts center coordinates from bounds string like "[100,200][300,400]".
func parseBounds(bounds string) (centerX, centerY int, ok bool) {
	m := boundsRegex.FindStringSubmatch(bounds)
	if len(m) != 5 {
		return 0, 0, false
	}
	left, _ := strconv.Atoi(m[1])
	top, _ := strconv.Atoi(m[2])
	right, _ := strconv.Atoi(m[3])
	bottom, _ := strconv.Atoi(m[4])
	return (left + right) / 2, (top + bottom) / 2, true
}

// shortenClass turns "android.widget.Button" into "Button".
func shortenClass(class string) string {
	if idx := strings.LastIndex(class, "."); idx >= 0 {
		return class[idx+1:]
	}
	return class
}

// shortenResourceID strips the package prefix from resource IDs.
// "com.google.android.youtube:id/menu_search" → "menu_search"
func shortenResourceID(id string) string {
	if idx := strings.LastIndex(id, "/"); idx >= 0 {
		return id[idx+1:]
	}
	return id
}

// flattenNodes recursively walks the UI tree and collects actionable elements.
func flattenNodes(nodes []uiNode, out *[]parsedElement) {
	for _, n := range nodes {
		hasText := n.Text != ""
		hasDesc := n.ContentDesc != ""
		hasID := n.ResourceID != ""
		isClickable := n.Clickable == "true"
		isEnabled := n.Enabled == "true"
		isFocused := n.Focused == "true"
		isScrollable := n.Scrollable == "true"
		isSelected := n.Selected == "true"

		// Keep elements that are interactive or have identifying info
		if hasText || hasDesc || hasID || isClickable {
			cx, cy, ok := parseBounds(n.Bounds)
			if ok && cx > 0 && cy > 0 {
				// Compute priority for sorting (higher = more relevant)
				p := 0
				if isClickable && isEnabled {
					p += 10
				}
				if hasText {
					p += 5
				}
				if hasDesc {
					p += 3
				}
				if hasID {
					p += 1
				}

				*out = append(*out, parsedElement{
					class:       shortenClass(n.Class),
					text:        n.Text,
					contentDesc: n.ContentDesc,
					resourceID:  shortenResourceID(n.ResourceID),
					centerX:     cx,
					centerY:     cy,
					clickable:   isClickable,
					enabled:     isEnabled,
					focused:     isFocused,
					scrollable:  isScrollable,
					selected:    isSelected,
					priority:    p,
				})
			}
		}

		// Recurse into children
		flattenNodes(n.Children, out)
	}
}

// formatElements formats parsed elements into a compact single-line format for the LLM.
// Format: [1] Button "Search" (650,95) clickable [desc: Search]
func formatElements(pkg string, elements []parsedElement) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("UI Elements (%s, %d elements):\n\n", pkg, len(elements)))

	for i, el := range elements {
		sb.WriteString(fmt.Sprintf("[%d] %s", i+1, el.class))

		if el.text != "" {
			sb.WriteString(fmt.Sprintf(" %q", el.text))
		}

		sb.WriteString(fmt.Sprintf(" (%d,%d)", el.centerX, el.centerY))

		// Compact flags
		if el.clickable {
			sb.WriteString(" clickable")
		}
		if el.focused {
			sb.WriteString(" focused")
		}
		if el.scrollable {
			sb.WriteString(" scrollable")
		}
		if !el.enabled {
			sb.WriteString(" disabled")
		}

		// Only show content-desc; skip resource-id unless text and desc are both empty
		if el.contentDesc != "" {
			sb.WriteString(fmt.Sprintf(" [desc: %s]", el.contentDesc))
		} else if el.text == "" && el.resourceID != "" {
			sb.WriteString(fmt.Sprintf(" [id: %s]", el.resourceID))
		}

		sb.WriteString("\n")
	}

	sb.WriteString("\nUse screen_tap with coordinates to tap an element.")
	return sb.String()
}

// uiElementsDump runs uiautomator dump via ADB and returns a parsed element list.
func uiElementsDump(ctx context.Context) *ToolResult {
	// 4-second timeout for uiautomator dump
	dumpCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()

	// Use exec-out to get XML directly to stdout (avoids filesystem write on device)
	fullArgs := []string{"-s", adbSerial(), "exec-out", "uiautomator", "dump", "/dev/tty"}
	cmd := exec.CommandContext(dumpCtx, "adb", fullArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if dumpCtx.Err() == context.DeadlineExceeded {
			return ErrorResult("ui_elements timed out (4s) — this screen may contain WebViews, games, or animations that block UI dumping. Use screenshot instead.")
		}
		return ErrorResult(fmt.Sprintf("Failed to dump UI hierarchy: %v", err))
	}

	raw := string(out)

	// Strip the trailing status line: "UI hierchary dumped to: /dev/tty"
	if idx := strings.LastIndex(raw, "UI hierchary"); idx >= 0 {
		raw = raw[:idx]
	} else if idx := strings.LastIndex(raw, "UI hierarchy"); idx >= 0 {
		raw = raw[:idx]
	}
	raw = strings.TrimSpace(raw)

	if raw == "" || !strings.HasPrefix(raw, "<?xml") {
		return ErrorResult("ui_elements returned empty or invalid XML. The current screen may not support UI dumping. Use screenshot instead.")
	}

	// Parse XML
	var hierarchy uiHierarchy
	if err := xml.Unmarshal([]byte(raw), &hierarchy); err != nil {
		return ErrorResult(fmt.Sprintf("Failed to parse UI hierarchy XML: %v", err))
	}

	// Flatten into actionable elements
	var elements []parsedElement
	flattenNodes(hierarchy.Nodes, &elements)

	if len(elements) == 0 {
		return NewToolResult("No actionable UI elements found on screen. The app may use a custom rendering engine (game, Flutter, WebView). Use screenshot instead.")
	}

	// Sort by priority (most interactive first), then by Y coordinate (top to bottom)
	sort.SliceStable(elements, func(i, j int) bool {
		if elements[i].priority != elements[j].priority {
			return elements[i].priority > elements[j].priority
		}
		return elements[i].centerY < elements[j].centerY
	})

	// Cap at 30 elements to keep context compact
	if len(elements) > 30 {
		elements = elements[:30]
	}

	// Detect package from root node
	pkg := "unknown"
	if len(hierarchy.Nodes) > 0 {
		pkg = hierarchy.Nodes[0].Package
	}

	return NewToolResult(formatElements(pkg, elements))
}

// adbSerial is the ADB device serial to target. Defaults to localhost:5555
// (loopback ADB) but can be overridden via ANDROID_SERIAL env var.
func adbSerial() string {
	if s := os.Getenv("ANDROID_SERIAL"); s != "" {
		return s
	}
	return "localhost:5555"
}

// runADBCommandImpl executes an adb command and returns its output.
// It always targets a specific device via -s to avoid "more than one device" errors.
func runADBCommandImpl(ctx context.Context, args ...string) (string, error) {
	fullArgs := append([]string{"-s", adbSerial()}, args...)
	cmd := exec.CommandContext(ctx, "adb", fullArgs...)
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

	// Compress screenshot: downscale 50% + JPEG conversion for smaller context
	compressedPath, compErr := utils.CompressScreenshot(localPath)
	if compErr != nil {
		// Fall back to original if compression fails
		compressedPath = localPath
	}

	result := SilentResult(fmt.Sprintf("Screenshot saved to %s. I can see the screen contents via vision. Use send_file to share this image with the user if needed.", compressedPath))
	result.Images = []string{compressedPath}
	return result
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

func screenWait(ctx context.Context, seconds int) *ToolResult {
	if seconds < 1 {
		seconds = 1
	}
	if seconds > 120 {
		seconds = 120
	}
	select {
	case <-time.After(time.Duration(seconds) * time.Second):
		return SilentResult(fmt.Sprintf("Waited %d seconds", seconds))
	case <-ctx.Done():
		return ErrorResult("Wait cancelled")
	}
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
