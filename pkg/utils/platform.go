package utils

import (
	"os"
	"strings"
)

// IsTermux returns true if running inside the Termux terminal emulator on Android.
func IsTermux() bool {
	if os.Getenv("TERMUX_VERSION") != "" {
		return true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	return strings.Contains(home, "com.termux")
}

// IsAndroid returns true if running on an Android device.
func IsAndroid() bool {
	_, err := os.Stat("/system/build.prop")
	return err == nil
}
