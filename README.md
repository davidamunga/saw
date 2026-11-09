# saw

[![CI](https://github.com/davidamunga/saw/actions/workflows/ci.yml/badge.svg)](https://github.com/davidamunga/saw/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/davidamunga/saw)](https://github.com/davidamunga/saw/releases/latest)
[![Go Reference](https://pkg.go.dev/badge/github.com/davidamunga/saw.svg)](https://pkg.go.dev/github.com/davidamunga/saw)

Stream a mobile app's logs from any Android device or emulator, iOS simulator, or iPhone/iPad, with
one command.

```sh
saw stream --app com.example.app
```

```
saw: com.example.app on Pixel 8 Android 14 (Ctrl-C to stop)
09:41:02.114 I MainActivity onCreate
09:41:02.380 D OkHttp       --> GET https://api.example.com/v1/orders
09:41:02.918 D OkHttp       <-- 200 https://api.example.com/v1/orders (538ms)
09:41:03.002 W SyncWorker   retrying upload (attempt 2 of 5)
09:41:03.441 E Checkout     payment failed: card declined
```

- **One command for every device.** Android and iOS, emulators, simulators and physical devices.
- **Your app's lines only.** No setup and no filters to write: saw keeps your app's own logs and
  drops the system's.
- **Survives restarts and crashes.** Relaunch the app and the stream carries on.
- **Works anywhere a terminal does.** Pretty output for people, `--json` for scripts, CI and agents.

## Install

```sh
brew install davidamunga/tap/saw                      # macOS
go install github.com/davidamunga/saw/cmd/saw@latest  # anywhere with Go
```

Or download a binary for macOS, Linux or Windows from the
[releases](https://github.com/davidamunga/saw/releases).

### Requirements

saw drives the tools that already talk to devices. Install the ones for the devices you use:

| To read | You need | Install |
|---|---|---|
| Android devices and emulators | adb | `brew install --cask android-platform-tools`, or the Android SDK's platform-tools |
| iOS simulators | Xcode (macOS only) | the Mac App Store |
| iPhones and iPads | pymobiledevice3 | `pipx install pymobiledevice3`; on Linux also usbmuxd, on Windows the Apple Devices app |

Then run `saw doctor`. It checks each one, and your connected devices, and says how to fix what's missing.

## Usage

```sh
saw devices                                  # what's connected
saw stream --app com.example.app             # an app's own logs, by package or bundle id
saw stream --app com.example.app --area OkHttp,SyncWorker   # only these tags (Android) or categories (iOS)
saw stream --app com.example.app --device pixel --level warn
saw stream --app com.example.app --json > session.jsonl
saw doctor                                   # what's missing, and how to fix it
```

### `saw stream`

| Flag | Meaning |
|---|---|
| `--app <id or name>` | The app: its Android package name or iOS bundle id, or a name from `saw.json`. |
| `--area <a,b>` | Only these areas. Without `saw.json` they're logcat tags (Android) or Logger categories (iOS), matched in any case. Repeatable. |
| `--device <id or name>` | The device: its id, the start of its id, or all or part of its name (`pixel`, `"17 pro"`). |
| `--platform android\|ios` | Only consider devices of one platform. |
| `--level <level>` | The lowest level to show: `verbose`, `debug`, `info`, `warn`, `error` or `fatal`. Default `verbose`. |
| `--all` | Everything the app's process logs, system and framework lines included. |
| `--json` | One JSON object per line instead of the readable format. |
| `--config <path>` | A `saw.json` other than the nearest one. |

With no `--device`, saw uses the only running device; with several, it asks which one.

`saw devices` takes `--all` (to include shut-down simulators) and `--json`.

### What "the app's own logs" means

- **Android:** every line written by the app's process, matched by its user id: `Log`, Timber,
  `println`, Flutter, React Native, crashes.
- **iOS:** every line logged by code inside the app bundle (the app and the frameworks it ships):
  `Logger`/`os_log`, `NSLog`, CocoaLumberjack, React Native, Flutter. Apple's own framework lines are dropped.

To find tag or category names for `--area`, run `saw stream` without it: the column after the level
letter shows each line's tag or category.

### JSON output

`--json` prints one object per line:

```json
{"time":"2026-11-09T09:41:02.38+03:00","platform":"android","device":"Pixel 8","process":"com.example.app","pid":4821,"tid":4870,"level":"debug","tag":"OkHttp","message":"--> GET https://api.example.com/v1/orders"}
```

| Field | Meaning |
|---|---|
| `time` | When the line was logged, with the time zone offset. |
| `platform`, `device` | `android` or `ios`, and the device's name. |
| `process`, `pid`, `tid` | The app's process, process id and thread id. |
| `sender` | iOS: the binary or library that logged the line. |
| `level` | `verbose`, `debug`, `info`, `warn`, `error` or `fatal`. |
| `tag` | Android: the logcat tag. |
| `subsystem`, `category` | iOS: the Logger subsystem and category. |
| `area` | The area from `saw.json`, when the line belongs to one. |
| `message` | The log message. |

Empty fields are left out.

## saw.json

Optional. It gives your apps short names and lists the project's log areas, so a team can run
`saw stream --app main --area auth,api` without knowing package ids, tags or categories. Put it at
the repo root; saw finds the nearest one above the working directory.

```json
{
  "apps": {
    "main": { "android": "com.example.app", "ios": "com.example.app" },
    "admin": { "android": "com.example.admin", "ios": "com.example.admin" }
  },
  "areas": ["api", "auth", "payments", "push", "ui"],
  "android": { "tag": "Example{Area}" },
  "ios": { "subsystem": "{bundle}" }
}
```

| Key | Meaning |
|---|---|
| `apps.<name>.android` | The app's Android package name. |
| `apps.<name>.ios` | The app's iOS bundle id. |
| `apps.<name>.process` | Optional. The iOS executable name; read from the device when left out. |
| `areas` | The app's log areas, for `--area`. On iOS each is a Logger category. |
| `android.tag` | The logcat tag for an area: `{Area}` → `Api`, `{area}` → `api`, `{AREA}` → `API`. |
| `ios.subsystem` | The app's Logger subsystem. Default `{bundle}`, the bundle id. |

With `areas` set, saw shows only lines from those areas by default: logcat tags built from
`android.tag`, and Logger lines in the app's subsystem on iOS. `--all` shows everything. With only
one app listed, `--app` can be left out. A misspelled key is an error, not silently ignored.

## How it works

saw starts the tool that reads each kind of device, turns its output into one format, filters it
and prints it:

| Target | Tool | Host |
|---|---|---|
| Android device or emulator | `adb logcat`, filtered by the app's user id, so it survives restarts and crashes | any |
| iOS simulator | `xcrun simctl spawn … log stream` | macOS |
| iPhone / iPad | `pymobiledevice3 syslog live`, which keeps each line's subsystem and category | any |

## Troubleshooting

| You see | Do this |
|---|---|
| A device shows as `unauthorized` | Unlock it and accept "Allow USB debugging". |
| An iPhone shows as not trusted | Unlock it and tap "Trust This Computer". |
| `more than one device` | Pick one: `--platform android`, or `--device pixel`. |
| `… is not installed on …` | Check the package or bundle id: `adb shell pm list packages \| grep example` on Android. |
| Nothing appears | Is the app running and doing something? Lower `--level`, drop `--area`, or try `--all`. |
| iOS values show as `<private>` | Mark them `privacy: .public` in the `Logger` call. saw points this out when it sees one. |

Known limits:

- iOS: Swift `print` and `printf` write to stdout, not the system log, so no tool can read them
  from a device; only Xcode, attached at launch, sees them. Use `Logger`, `os_log` or `NSLog`.
- iOS app extensions (widgets, notification services) run as their own processes and aren't included.
- Release builds may strip logging; saw only shows what the app emits.

## Develop

```sh
go test ./...
go build -o bin/saw ./cmd/saw
```

`testdata/` holds real captured log output that the parser tests run against.

To release, tag and push: `git tag -a vX.Y.Z -m vX.Y.Z && git push origin vX.Y.Z`. The release
workflow builds every platform with GoReleaser, publishes a GitHub release and updates the Homebrew tap.

## License

[MIT](LICENSE)
