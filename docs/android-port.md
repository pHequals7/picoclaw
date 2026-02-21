# PicoClaw Android Port Specification

## Overview

Port PicoClaw to run on Android phones (starting with POCO C71, ARM64, 6GB RAM) via Termux, replacing the current Oracle Cloud ARM64 VM deployment.

## Phase 1: Termux Deployment

### Architecture

PicoClaw runs as a standard Linux ARM64 binary inside Termux's Linux userland. No Android NDK or gomobile required — Termux provides a full POSIX environment with `GOOS=linux GOARCH=arm64`.

```
┌─────────────────────────────────┐
│         Android Phone           │
│  ┌───────────────────────────┐  │
│  │         Termux             │  │
│  │  ┌─────────────────────┐  │  │
│  │  │     PicoClaw         │  │  │
│  │  │  (linux/arm64)       │  │  │
│  │  └───────┬─────────────┘  │  │
│  │          │                 │  │
│  │  ┌───────▼─────────────┐  │  │
│  │  │   termux-api         │  │  │
│  │  │  (SMS, Calls, etc.)  │  │  │
│  │  └─────────────────────┘  │  │
│  └───────────────────────────┘  │
│                                  │
│  ┌───────────────────────────┐  │
│  │  Termux:API Android App   │  │
│  │  (bridges to Android OS)  │  │
│  └───────────────────────────┘  │
└─────────────────────────────────┘
```

### Build

Cross-compile on any machine:

```bash
make build-android
# Produces: build/picoclaw-android-arm64
```

Uses `GOOS=linux GOARCH=arm64` — identical to the existing Oracle VM target. No CGO, no NDK.

### Deployment

```bash
# Transfer binary
adb push build/picoclaw-android-arm64 /sdcard/Download/

# In Termux
cp /sdcard/Download/picoclaw-android-arm64 ~/bin/picoclaw
chmod +x ~/bin/picoclaw

# Install termux-api package (for telephony tools)
pkg install termux-api

# Also install Termux:API app from F-Droid

# Run with wake lock to prevent sleep
termux-wake-lock
picoclaw gateway
```

### Platform Detection

`pkg/utils/platform.go` provides:
- `IsTermux()` — checks `TERMUX_VERSION` env var or home path containing `com.termux`
- `IsAndroid()` — checks for `/system/build.prop`

### Telephony Tools

New tools that wrap `termux-api` commands:

| Tool | Command | Purpose |
|------|---------|---------|
| `sms_send` | `termux-sms-send` | Send SMS to a number |
| `sms_list` | `termux-sms-list` | Read SMS inbox/sent/draft |
| `phone_call` | `termux-telephony-call` | Initiate a phone call |
| `phone_info` | `termux-telephony-deviceinfo` | Get device/SIM info |

#### Safety

Telephony tools check `IsTermux()` at runtime and return clear errors on non-Termux platforms. All tools are registered unconditionally but gracefully degrade.

### Hardware Tools Compatibility

Existing I2C/SPI/USB tools compile fine (they use `GOOS=linux`). They return runtime errors when `/dev/i2c-*` etc. are not found — no code changes needed.

### OAuth

The `openBrowser` function in `pkg/auth/oauth.go` adds Termux support via `termux-open-url` within the `linux` case, since `GOOS` will be `linux` on Android/Termux.

### Configuration

No config changes needed for basic operation. The same `~/.picoclaw/config.json` works. In Termux, `~` resolves to `/data/data/com.termux/files/home`.

### Stability

- Use `termux-wake-lock` to prevent Android from killing the process
- Termux notification shows PicoClaw is running
- Standard Go signal handling works in Termux

---

## Phase 2: Standalone APK (Future)

Deferred until Phase 1 is validated. Considerations:

### Framework Options
- **Gio** — Pure Go UI toolkit, good for simple status/config screens
- **gomobile bind** — Expose Go as AAR, write thin Kotlin/Java wrapper
- **Kotlin shell** — Kotlin app that bundles and launches the Go binary

### Android Service Lifecycle
- Foreground service with persistent notification
- `WAKE_LOCK` to prevent process death
- `BOOT_COMPLETED` receiver for auto-start

### Permissions Required
- `SEND_SMS`, `READ_SMS` — telephony
- `CALL_PHONE` — phone calls
- `READ_PHONE_STATE` — device info
- `INTERNET` — Telegram/LLM API access
- `FOREGROUND_SERVICE` — keep-alive
- `WAKE_LOCK` — prevent sleep

### Distribution
- Direct APK download (simplest)
- F-Droid (open source store)
- Play Store (requires review, restrictive on SMS/call permissions)
