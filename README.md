<h1 align="center">
  <img alt="saw" src="assets/saw-logo-card.png" width="300">
</h1>

<p align="center">
  <a href="https://github.com/davidamunga/saw/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/davidamunga/saw/actions/workflows/ci.yml/badge.svg"></a>
  <a href="https://github.com/davidamunga/saw/releases/latest"><img alt="Release" src="https://img.shields.io/github/v/release/davidamunga/saw"></a>
  <a href="https://pkg.go.dev/github.com/davidamunga/saw"><img alt="Go Reference" src="https://pkg.go.dev/badge/github.com/davidamunga/saw.svg"></a>
</p>

Stream a mobile app's logs from any Android device or emulator, iOS simulator, or iPhone/iPad, with
one command.

<p align="center">
  <img alt="saw streaming a demo app's logs from an Android phone" src="assets/saw-demo.gif" width="800">
</p>

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
saw stream --app com.example.app --scope OkHttp,SyncWorker   # only these tags (Android) or categories (iOS)
saw stream --app com.example.app --device pixel --level warn
saw stream --app com.example.app --json > session.jsonl
saw net --app com.example.app                # the app's network requests and connections
saw net --app com.example.app --full         # with the headers and bodies the app logs
saw doctor                                   # what's missing, and how to fix it
```

### `saw stream`

| Flag | Meaning |
|---|---|
| `--app <id or name>` | The app: its Android package name or iOS bundle id, or a name from `saw.json`. |
| `--scope <a,b>` | Only these scopes. Without `saw.json` they're logcat tags (Android) or Logger categories (iOS), matched in any case. Repeatable. |
| `--device <id or name>` | The device: its id, the start of its id, or all or part of its name (`pixel`, `"17 pro"`). |
| `--platform android\|ios` | Only consider devices of one platform. |
| `--level <level>` | The lowest level to show: `verbose`, `debug`, `info`, `warn`, `error` or `fatal`. Default `verbose`. |
| `--all` | Everything the app's process logs, system and framework lines included. |
| `--json` | One JSON object per line instead of the readable format. |
| `--config <path>` | A `saw.json` other than the nearest one. |

With no `--device`, saw uses the only running device; with several, it asks which one.

`saw devices` takes `--all` (to include shut-down simulators) and `--json`.

`saw net` takes `--app`, `--device`, `--platform`, `--json` and `--config`, the same as `saw stream`,
and `--full` for the headers and bodies of requests the app logs itself.

### What "the app's own logs" means

- **Android:** every line written by the app's process, matched by its user id: `Log`, Timber,
  `println`, Flutter, React Native, crashes.
- **iOS:** every line logged by code inside the app bundle (the app and the frameworks it ships):
  `Logger`/`os_log`, `NSLog`, CocoaLumberjack, React Native, Flutter. Apple's own framework lines are dropped.

To find tag or category names for `--scope`, run `saw stream` without it: the column after the level
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
| `scope` | The scope from `saw.json`, when the line belongs to one. |
| `message` | The log message. |

Empty fields are left out.

## Network requests: `saw net`

`saw net` shows an app's network activity, for any app, without changing it, without a proxy and
without installing certificates. What it can see depends on the platform:

| Device | Shows | From |
|---|---|---|
| iOS simulator | each request's **URL**, status, time, protocol and size | Apple's networking logs in the app's process |
| iPhone / iPad | each request's status, time and size, and the **hostname** of each new connection | the same logs (iOS hides URLs on devices), and a packet capture of the app (the TLS server name) |
| Android | each new **connection**, named by the hostnames the app's own log mentions (or reverse DNS), and the app's **HTTP log lines**: URLs, status codes, OkHttp's `-->`/`<--` | the kernel's socket table, which adb can read, and the app's log |

```
09:41:02.380 200  https://api.example.com/v1/orders  538ms h2 3.3 KB     (simulator)
09:41:02.114 TLS  api.example.com:443  203.0.113.7                         (iPhone)
09:41:02.918 200  (connection 1)  605ms h2 3.3 KB                         (iPhone)
09:41:03.002 TCP  api.example.com:443  203.0.113.7                         (Android)
09:41:03.010 LOG  OkHttp --> GET https://api.example.com/v1/orders         (Android)
```

HTTPS stays encrypted on the network: there, you see where an app connects and how requests went.
Several requests often share one connection, so on iPhone and Android a connection line can stand
for many requests.

### Full requests, from the app's own HTTP logging

Debug builds often log every request: OkHttp's `HttpLoggingInterceptor` on Android, and loggers
like it on iOS. `saw net` assembles those logs into one line per request, on every platform and
under any tag or category, and `--full` adds the headers and bodies:

```
09:41:02.380 POST https://api.example.com/v1/orders  201  412ms
    → Content-Type: application/json
    → {"item":"1042"}
    ← content-type: application/json
    ← {"id":"ord_88","status":"created"}
```

It knows three formats:

| Format | Used by | Looks like |
|---|---|---|
| OkHttp-style | OkHttp and Retrofit, and the many loggers that copy it | `--> POST https://…`, headers, body, `<-- 201 https://… (412ms)` |
| Ktor | Ktor's `Logging` plugin | `REQUEST: https://…`, `METHOD: …`, `RESPONSE: 200 OK` |
| Dio | Flutter's Dio `LogInterceptor` | `*** Request ***`, `uri: …`, `statusCode: …` |

Other HTTP-looking lines the app logs (a URL, or a status code) appear as `LOG` lines. What you see
is what the app logs: its log level decides whether headers and bodies are included (OkHttp's
`Level.BODY`), and its own redaction (`Authorization: ██`) stays. iOS caps each log message at
about 1 KB, so long bodies there are cut short. An app that doesn't log its requests shows only the
network view above.

## saw.json

Optional. It gives your apps short names and lists the project's log scopes, so a team can run
`saw stream --app main --scope auth,api` without knowing package ids, tags or categories. Put it at
the repo root; saw finds the nearest one above the working directory.

```json
{
  "apps": {
    "main": { "android": "com.example.app", "ios": "com.example.app" },
    "admin": { "android": "com.example.admin", "ios": "com.example.admin" }
  },
  "scopes": ["api", "auth", "payments", "push", "ui"],
  "android": { "tag": "Example{Scope}" },
  "ios": { "subsystem": "{bundle}" }
}
```

| Key | Meaning |
|---|---|
| `apps.<name>.android` | The app's Android package name. |
| `apps.<name>.ios` | The app's iOS bundle id. |
| `apps.<name>.process` | Optional. The iOS executable name; read from the device when left out. |
| `scopes` | The app's log scopes, for `--scope`. On iOS each is a Logger category. |
| `android.tag` | The logcat tag for a scope: `{Scope}` → `Api`, `{scope}` → `api`, `{SCOPE}` → `API`. |
| `ios.subsystem` | The app's Logger subsystem. Default `{bundle}`, the bundle id. |

With `scopes` set, saw shows only lines from those scopes by default: logcat tags built from
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
| Nothing appears | Is the app running and doing something? Lower `--level`, drop `--scope`, or try `--all`. |
| `saw net` shows nothing on Android | Connections are reported when they open, so make the app do something new. Long-lived connections opened before `saw net` started appear on its first look. |
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
