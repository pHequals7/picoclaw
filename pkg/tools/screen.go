package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/sipeed/picoclaw/pkg/utils"
)

// runADBShell executes a command via `adb shell` and returns the output.
// This is needed because Android restricts screencap, input, etc. to the
// ADB shell user. Termux can proxy through loopback ADB (localhost:5555).
func runADBShell(ctx context.Context, args ...string) (string, error) {
	fullArgs := append([]string{"shell"}, args...)
	output, err := runADBCommandImpl(ctx, fullArgs...)
	if err != nil {
		return "", fmt.Errorf("adb shell: %w", err)
	}
	return output, nil
}

// runADB executes an adb command (without "shell" prefix) and returns the output.
func runADB(ctx context.Context, args ...string) (string, error) {
	output, err := runADBCommandImpl(ctx, args...)
	if err != nil {
		return "", fmt.Errorf("adb: %w", err)
	}
	return output, nil
}

// ScreenshotTool takes a screenshot via ADB and returns the file path.
type ScreenshotTool struct {
	workspace string
}

func NewScreenshotTool(workspace string) *ScreenshotTool {
	return &ScreenshotTool{workspace: workspace}
}

func (t *ScreenshotTool) Name() string { return "screenshot" }

func (t *ScreenshotTool) Description() string {
	return "Take a screenshot of the Android device screen. Returns the file path of the saved screenshot. Use send_file to send the image to the user. Requires ADB loopback setup on Android/Termux."
}

func (t *ScreenshotTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
	}
}

func (t *ScreenshotTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	if !utils.IsTermux() {
		return ErrorResult("screenshot requires Termux with ADB on Android")
	}
	return screenshotExecute(ctx, t.workspace)
}

// ScreenTapTool taps at screen coordinates.
type ScreenTapTool struct{}

func NewScreenTapTool() *ScreenTapTool { return &ScreenTapTool{} }

func (t *ScreenTapTool) Name() string { return "screen_tap" }

func (t *ScreenTapTool) Description() string {
	return "Tap at specific screen coordinates on the Android device. Requires ADB loopback setup on Android/Termux."
}

func (t *ScreenTapTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"x": map[string]interface{}{
				"type":        "integer",
				"description": "X coordinate to tap",
			},
			"y": map[string]interface{}{
				"type":        "integer",
				"description": "Y coordinate to tap",
			},
		},
		"required": []string{"x", "y"},
	}
}

func (t *ScreenTapTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	if !utils.IsTermux() {
		return ErrorResult("screen_tap requires Termux with ADB on Android")
	}

	x, ok := args["x"].(float64)
	if !ok {
		return ErrorResult("x coordinate is required")
	}
	y, ok := args["y"].(float64)
	if !ok {
		return ErrorResult("y coordinate is required")
	}

	return screenTap(ctx, int(x), int(y))
}

// ScreenSwipeTool performs a swipe gesture.
type ScreenSwipeTool struct{}

func NewScreenSwipeTool() *ScreenSwipeTool { return &ScreenSwipeTool{} }

func (t *ScreenSwipeTool) Name() string { return "screen_swipe" }

func (t *ScreenSwipeTool) Description() string {
	return "Perform a swipe gesture on the Android device screen. Requires ADB loopback setup on Android/Termux."
}

func (t *ScreenSwipeTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"x1": map[string]interface{}{
				"type":        "integer",
				"description": "Start X coordinate",
			},
			"y1": map[string]interface{}{
				"type":        "integer",
				"description": "Start Y coordinate",
			},
			"x2": map[string]interface{}{
				"type":        "integer",
				"description": "End X coordinate",
			},
			"y2": map[string]interface{}{
				"type":        "integer",
				"description": "End Y coordinate",
			},
			"duration_ms": map[string]interface{}{
				"type":        "integer",
				"description": "Duration of swipe in milliseconds (default: 300)",
			},
		},
		"required": []string{"x1", "y1", "x2", "y2"},
	}
}

func (t *ScreenSwipeTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	if !utils.IsTermux() {
		return ErrorResult("screen_swipe requires Termux with ADB on Android")
	}

	x1, ok := args["x1"].(float64)
	if !ok {
		return ErrorResult("x1 coordinate is required")
	}
	y1, ok := args["y1"].(float64)
	if !ok {
		return ErrorResult("y1 coordinate is required")
	}
	x2, ok := args["x2"].(float64)
	if !ok {
		return ErrorResult("x2 coordinate is required")
	}
	y2, ok := args["y2"].(float64)
	if !ok {
		return ErrorResult("y2 coordinate is required")
	}

	durationMs := 300
	if d, ok := args["duration_ms"].(float64); ok && d > 0 {
		durationMs = int(d)
	}

	return screenSwipe(ctx, int(x1), int(y1), int(x2), int(y2), durationMs)
}

// ScreenKeyTool sends a key event.
type ScreenKeyTool struct{}

func NewScreenKeyTool() *ScreenKeyTool { return &ScreenKeyTool{} }

func (t *ScreenKeyTool) Name() string { return "screen_key" }

func (t *ScreenKeyTool) Description() string {
	return "Send a key event to the Android device (e.g. BACK, HOME, ENTER, VOLUME_UP, POWER, TAB, DPAD_UP, DPAD_DOWN, DPAD_LEFT, DPAD_RIGHT, DEL). Requires ADB loopback setup on Android/Termux."
}

func (t *ScreenKeyTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"key": map[string]interface{}{
				"type":        "string",
				"description": "Key name (e.g. BACK, HOME, ENTER, VOLUME_UP) or numeric keycode",
			},
		},
		"required": []string{"key"},
	}
}

// keycodeLookup maps common key names to Android keycodes.
var keycodeLookup = map[string]string{
	"HOME":         "KEYCODE_HOME",
	"BACK":         "KEYCODE_BACK",
	"ENTER":        "KEYCODE_ENTER",
	"TAB":          "KEYCODE_TAB",
	"DEL":          "KEYCODE_DEL",
	"DELETE":       "KEYCODE_FORWARD_DEL",
	"POWER":        "KEYCODE_POWER",
	"VOLUME_UP":    "KEYCODE_VOLUME_UP",
	"VOLUME_DOWN":  "KEYCODE_VOLUME_DOWN",
	"MENU":         "KEYCODE_MENU",
	"SEARCH":       "KEYCODE_SEARCH",
	"DPAD_UP":      "KEYCODE_DPAD_UP",
	"DPAD_DOWN":    "KEYCODE_DPAD_DOWN",
	"DPAD_LEFT":    "KEYCODE_DPAD_LEFT",
	"DPAD_RIGHT":   "KEYCODE_DPAD_RIGHT",
	"DPAD_CENTER":  "KEYCODE_DPAD_CENTER",
	"APP_SWITCH":   "KEYCODE_APP_SWITCH",
	"RECENT_APPS":  "KEYCODE_APP_SWITCH",
	"SPACE":        "KEYCODE_SPACE",
	"ESCAPE":       "KEYCODE_ESCAPE",
	"CAMERA":       "KEYCODE_CAMERA",
	"MEDIA_PLAY":   "KEYCODE_MEDIA_PLAY",
	"MEDIA_PAUSE":  "KEYCODE_MEDIA_PAUSE",
	"MEDIA_NEXT":   "KEYCODE_MEDIA_NEXT",
	"MEDIA_PREV":   "KEYCODE_MEDIA_PREVIOUS",
	"WAKEUP":       "KEYCODE_WAKEUP",
	"SLEEP":        "KEYCODE_SLEEP",
}

func (t *ScreenKeyTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	if !utils.IsTermux() {
		return ErrorResult("screen_key requires Termux with ADB on Android")
	}

	key, ok := args["key"].(string)
	if !ok || key == "" {
		return ErrorResult("key is required")
	}

	// Look up short name in keycode table
	keycode := key
	if mapped, exists := keycodeLookup[strings.ToUpper(key)]; exists {
		keycode = mapped
	} else if !strings.HasPrefix(strings.ToUpper(key), "KEYCODE_") {
		// If it's not a number and doesn't have KEYCODE_ prefix, add it
		if _, err := fmt.Sscanf(key, "%d", new(int)); err != nil {
			keycode = "KEYCODE_" + strings.ToUpper(key)
		}
	}

	return screenKey(ctx, keycode)
}

// ScreenTextTool types text on the device.
type ScreenTextTool struct{}

func NewScreenTextTool() *ScreenTextTool { return &ScreenTextTool{} }

func (t *ScreenTextTool) Name() string { return "screen_text" }

func (t *ScreenTextTool) Description() string {
	return "Type text on the Android device. Note: only ASCII text is supported by ADB input. Requires ADB loopback setup on Android/Termux."
}

func (t *ScreenTextTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"text": map[string]interface{}{
				"type":        "string",
				"description": "Text to type on the device",
			},
		},
		"required": []string{"text"},
	}
}

func (t *ScreenTextTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	if !utils.IsTermux() {
		return ErrorResult("screen_text requires Termux with ADB on Android")
	}

	text, ok := args["text"].(string)
	if !ok || text == "" {
		return ErrorResult("text is required")
	}

	return screenText(ctx, text)
}

// AppLaunchTool launches an Android app by package name.
type AppLaunchTool struct{}

func NewAppLaunchTool() *AppLaunchTool { return &AppLaunchTool{} }

func (t *AppLaunchTool) Name() string { return "app_launch" }

func (t *AppLaunchTool) Description() string {
	return "Launch an Android app by package name (e.g. com.android.chrome, com.google.android.youtube, com.whatsapp). Requires ADB loopback setup on Android/Termux."
}

func (t *AppLaunchTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"package": map[string]interface{}{
				"type":        "string",
				"description": "Android package name (e.g. com.android.chrome)",
			},
		},
		"required": []string{"package"},
	}
}

func (t *AppLaunchTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	if !utils.IsTermux() {
		return ErrorResult("app_launch requires Termux with ADB on Android")
	}

	pkg, ok := args["package"].(string)
	if !ok || pkg == "" {
		return ErrorResult("package is required")
	}

	return appLaunch(ctx, pkg)
}

// ScreenInfoTool gets screen state and active app info.
type ScreenInfoTool struct{}

func NewScreenInfoTool() *ScreenInfoTool { return &ScreenInfoTool{} }

func (t *ScreenInfoTool) Name() string { return "screen_info" }

func (t *ScreenInfoTool) Description() string {
	return "Get Android screen information including dimensions and currently focused app. Useful for determining tap coordinates. Requires ADB loopback setup on Android/Termux."
}

func (t *ScreenInfoTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
	}
}

func (t *ScreenInfoTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	if !utils.IsTermux() {
		return ErrorResult("screen_info requires Termux with ADB on Android")
	}

	return screenInfo(ctx)
}

// UIElementsTool dumps the Android UI hierarchy and returns a structured element list.
type UIElementsTool struct{}

func NewUIElementsTool() *UIElementsTool { return &UIElementsTool{} }

func (t *UIElementsTool) Name() string { return "ui_elements" }

func (t *UIElementsTool) Description() string {
	return "Get a structured list of all UI elements currently on the Android screen with their exact tap coordinates, text labels, and IDs. Returns clickable buttons, text fields, labels, and other interactive elements. Use this to find precise coordinates before tapping instead of guessing from screenshots. Falls back with an error if the screen contains WebViews or games that don't expose UI elements. Requires ADB loopback setup on Android/Termux."
}

func (t *UIElementsTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
	}
}

func (t *UIElementsTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	if !utils.IsTermux() {
		return ErrorResult("ui_elements requires Termux with ADB on Android")
	}

	return uiElementsDump(ctx)
}
