---
name: mobile-automation
description: Android intent shortcuts, device state queries, media control, and system tuning to skip visual navigation. Use before resorting to screenshot + tap workflows.
metadata: {"nanobot":{"emoji":"📱","os":["linux"],"requires":{"bins":["adb"]}}}
---

# Mobile Automation Skill

Use Android intents and shell commands to skip visual UI navigation. Every command here saves 2-15 LLM round-trips compared to the screenshot + tap approach.

Run all commands via the **exec** tool. They execute through ADB shell on the loopback connection.

---

## 1. Intent Shortcuts — Jump Directly to Content

### Core Pattern

```bash
am start -a android.intent.action.VIEW -d "<uri>"
```

### YouTube

```bash
# Search for a video
am start -a android.intent.action.VIEW -d "https://www.youtube.com/results?search_query=lofi+hip+hop"

# Play a specific video by ID
am start -a android.intent.action.VIEW -d "https://www.youtube.com/watch?v=VIDEO_ID"

# Open a channel
am start -a android.intent.action.VIEW -d "https://www.youtube.com/@ChannelName"
```

After launching a search, take one screenshot to identify the right video, tap it, done. ~3 round-trips instead of ~15.

### Chrome / Browser

```bash
# Google search directly
am start -a android.intent.action.VIEW -d "https://www.google.com/search?q=your+query"

# Open any URL (uses default browser)
am start -a android.intent.action.VIEW -d "https://example.com"
```

### Google Maps

```bash
# Search for a place
am start -a android.intent.action.VIEW -d "geo:0,0?q=coffee+near+me"

# Turn-by-turn navigation
am start -a android.intent.action.VIEW -d "google.navigation:q=1600+Amphitheatre+Parkway"

# Open coordinates
am start -a android.intent.action.VIEW -d "geo:37.7749,-122.4194?z=15"
```

### Phone / Dialer

```bash
# Open dialer with number pre-filled (does NOT call)
am start -a android.intent.action.DIAL -d "tel:+1234567890"
```

For actually placing calls, use the `phone_call` tool instead.

### Spotify

```bash
am start -a android.intent.action.VIEW -d "spotify:search:lofi+beats"
am start -a android.intent.action.VIEW -d "spotify:track:TRACK_ID"
```

### WhatsApp

```bash
# Open chat with a number
am start -a android.intent.action.VIEW -d "https://wa.me/1234567890"

# Pre-filled message
am start -a android.intent.action.VIEW -d "https://wa.me/1234567890?text=Hello"
```

### Email

```bash
am start -a android.intent.action.SENDTO -d "mailto:user@example.com" \
  --es android.intent.extra.SUBJECT "Subject" --es android.intent.extra.TEXT "Body"
```

### Settings (direct jump to any settings page)

```bash
am start -a android.settings.WIFI_SETTINGS
am start -a android.settings.BLUETOOTH_SETTINGS
am start -a android.settings.DISPLAY_SETTINGS
am start -a android.settings.SOUND_SETTINGS
am start -a android.settings.LOCATION_SOURCE_SETTINGS
am start -a android.settings.ACCESSIBILITY_SETTINGS
am start -a android.intent.action.POWER_USAGE_SUMMARY
am start -a android.settings.APPLICATION_DETAILS_SETTINGS -d "package:com.google.android.youtube"
```

### Play Store

```bash
am start -a android.intent.action.VIEW -d "market://details?id=com.app.package"
am start -a android.intent.action.VIEW -d "market://search?q=weather+app"
```

### Camera

```bash
am start -a android.media.action.STILL_IMAGE_CAMERA
am start -a android.media.action.VIDEO_CAMERA
```

### SMS Composer

```bash
am start -a android.intent.action.SENDTO -d "smsto:+15551234567" --es sms_body "Hey there"
```

---

## 2. Device State Queries — Know Before You Look

These let you answer user questions WITHOUT taking a screenshot.

### What app is in the foreground?

```bash
dumpsys activity activities | grep ResumedActivity
```

Check this before any screen interaction — you already know what's on screen.

### What's currently playing? (track, artist, app)

```bash
dumpsys media_session | grep -A 10 "metadata:"
```

User asks "what song is playing?" → read metadata directly. Zero UI interaction.

### Battery level and charging state

```bash
dumpsys battery | grep -E "level|powered"
```

### Is the screen on or off?

```bash
dumpsys power | grep mWakefulness
```

Returns `Awake`, `Asleep`, or `Dozing`. Wake it with `input keyevent KEYCODE_WAKEUP` if needed.

### WiFi connection info

```bash
dumpsys wifi | grep mWifiInfo
```

### Phone call state

```bash
dumpsys telephony.registry | grep mCallState
```

0=idle, 1=ringing, 2=offhook (in call).

### Read all notifications (full text content)

```bash
dumpsys notification --noredact
```

User asks "do I have any messages?" → read notification titles/text directly. Can extract OTP codes from SMS notifications without opening any app.

---

## 3. Media Control — No UI Needed

### Transport controls

```bash
cmd media_session dispatch play
cmd media_session dispatch pause
cmd media_session dispatch play-pause
cmd media_session dispatch next
cmd media_session dispatch previous
cmd media_session dispatch stop
cmd media_session dispatch fast-forward
cmd media_session dispatch rewind
```

User says "pause the music" → one command, done. No screenshots, no tapping.

### Volume control

```bash
# Get current volume
cmd media_session volume --get

# Adjust (shows volume UI overlay)
cmd media_session volume --show --adj raise
cmd media_session volume --show --adj lower

# Set exact level (stream 3 = music)
cmd media_session volume --show --stream 3 --set 10
```

Streams: 0=voice call, 1=system, 2=ring, 3=music, 4=alarm, 5=notification.

### Key-based media control (alternative)

```bash
input keyevent KEYCODE_MEDIA_PLAY_PAUSE
input keyevent KEYCODE_MEDIA_NEXT
input keyevent KEYCODE_MEDIA_PREVIOUS
input keyevent KEYCODE_VOLUME_UP
input keyevent KEYCODE_VOLUME_DOWN
input keyevent KEYCODE_VOLUME_MUTE
```

---

## 4. System Toggles — Instant Control

### WiFi

```bash
svc wifi enable
svc wifi disable
```

### Mobile data

```bash
svc data enable
svc data disable
```

### Bluetooth

```bash
cmd bluetooth_manager enable
cmd bluetooth_manager disable
```

### Airplane mode

```bash
cmd connectivity airplane-mode enable
cmd connectivity airplane-mode disable
```

### Do Not Disturb

```bash
settings put global zen_mode 0   # Off
settings put global zen_mode 1   # Priority only
settings put global zen_mode 2   # Total silence
settings put global zen_mode 3   # Alarms only
```

### Brightness

```bash
settings put system screen_brightness_mode 0   # Manual
settings put system screen_brightness 200      # 0-255
settings put system screen_brightness_mode 1   # Auto
```

### Screen timeout

```bash
settings put system screen_off_timeout 300000   # 5 minutes (in ms)
settings put system screen_off_timeout 600000   # 10 minutes
```

### Screen rotation

```bash
wm set-user-rotation lock 0    # Lock portrait
wm set-user-rotation lock 1    # Lock landscape
wm set-user-rotation free      # Auto-rotate
```

### Keep screen on while charging (essential for agent stability)

```bash
settings put global stay_on_while_plugged_in 3   # 1=USB, 2=AC, 3=both
```

### Power mode

```bash
cmd power set-mode 0   # Normal
cmd power set-mode 1   # Power saver
```

### Notification shade

```bash
cmd statusbar expand-notifications   # Pull down
cmd statusbar expand-settings        # Quick settings
cmd statusbar collapse               # Close
```

---

## 5. Speed Hacks — Make the Agent Faster

### Disable all animations (huge speedup)

```bash
settings put global window_animation_scale 0
settings put global transition_animation_scale 0
settings put global animator_duration_scale 0
```

Eliminates 300ms transitions between every screen. The agent doesn't need to wait for animations before screenshotting. To restore: set all three to `1.0`.

### Wait for app launch to complete

```bash
am start -W -n com.package.name/.MainActivity
```

The `-W` flag blocks until the activity is fully resumed. Returns timing info. Use this instead of launching + sleeping + screenshotting.

### Check which app handles a URL (plan before launching)

```bash
cmd package resolve-activity --brief -a android.intent.action.VIEW -d "https://youtube.com"
```

The agent can ask Android "who handles this?" before launching — no guessing.

### Force-stop a misbehaving app

```bash
am force-stop com.example.app
```

### Clear app data (factory reset one app)

```bash
pm clear com.example.app
```

### Long press simulation

```bash
input swipe 540 1200 540 1200 1500
```

Touch-and-hold at (540,1200) for 1.5 seconds. Same start/end coordinates with long duration = long press.

---

## 6. App Intelligence

### List installed apps

```bash
pm list packages -3              # Third-party only
pm list packages | grep spotify  # Search
```

### Get app version

```bash
pm dump com.example.app | grep versionName
```

### Grant permissions silently (no popup)

```bash
pm grant com.example.app android.permission.READ_CONTACTS
pm grant com.example.app android.permission.READ_CALL_LOG
pm grant com.example.app android.permission.POST_NOTIFICATIONS
```

### Disable bloatware (reversible, no root)

```bash
pm disable-user --user 0 com.bloatware.app
pm enable com.bloatware.app   # Re-enable
```

---

## 7. Data Access via Content Providers

### Read SMS inbox

```bash
content query --uri content://sms/inbox \
  --projection address:date:body --sort "date DESC"
```

### Read call log

```bash
content query --uri content://call_log/calls \
  --projection number:date:duration:type --sort "date DESC"
```

type: 1=incoming, 2=outgoing, 3=missed.

### Read contacts

```bash
content query --uri content://contacts/phones/ \
  --projection display_name:number
```

### Calendar events

```bash
content query --uri content://com.android.calendar/events \
  --projection title:dtstart:dtend:description:eventLocation
```

Note: content providers may require permissions pre-granted via `pm grant`.

---

## Decision Flow

1. **Can the user's request be answered with a query?** (what's playing, battery, notifications) → Use `dumpsys` → done, zero UI
2. **Can I open it with a URL/URI?** → Use `am start -a VIEW -d "<uri>"` → done in 1 step
3. **Is it a system toggle?** (wifi, brightness, DND) → Use `svc`/`settings`/`cmd` → done in 1 step
4. **Is it media control?** → Use `cmd media_session dispatch` → done in 1 step
5. **Need to interact with app UI?** → Launch with intent to get close, then screen tools for last mile
6. **No shortcut available?** → Fall back to `app_launch` + `ui_elements` + `screen_tap`

## URL Encoding

Spaces in queries must be encoded as `+` or `%20`:
```bash
# Correct
am start -a android.intent.action.VIEW -d "https://www.youtube.com/results?search_query=lofi+hip+hop"

# Wrong — will fail
am start -a android.intent.action.VIEW -d "https://www.youtube.com/results?search_query=lofi hip hop"
```

## Troubleshooting

- **"Error: Activity not started"** — App not installed or URI scheme not registered. Fall back to screen tools.
- **App opens but wrong screen** — Some apps don't handle all URI patterns. Screenshot and navigate from there.
- **"Permission Denial"** — Try adding `--user 0` flag.
- **Content provider empty** — Grant the required permission first: `pm grant <pkg> <permission>`.
