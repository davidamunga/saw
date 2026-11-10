<h1 align="center">
  <img alt="saw" src="assets/saw-logo-card.png" width="300">
</h1>

<p align="center">
  <a href="https://github.com/davidamunga/saw/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/davidamunga/saw/actions/workflows/ci.yml/badge.svg"></a>
  <a href="https://github.com/davidamunga/saw/releases/latest"><img alt="Release" src="https://img.shields.io/github/v/release/davidamunga/saw"></a>
  <a href="https://pkg.go.dev/github.com/davidamunga/saw"><img alt="Go Reference" src="https://pkg.go.dev/badge/github.com/davidamunga/saw.svg"></a>
</p>

saw shows a mobile app's logs and network requests, live, from any Android device or emulator, iOS
simulator, or iPhone/iPad, with one command.

<p align="center">
  <img alt="saw net showing a demo app's requests live on an Android phone, then the same requests as curl commands" src="assets/saw-demo.gif" width="800">
</p>

- **One command for every device.** Android and iOS, emulators, simulators and physical devices.
- **Your app only.** No filters to write: saw keeps your app's lines and drops the system's.
- **Full HTTP requests.** Method, URL, status, headers and bodies, from the logging debug builds
  already do, ready to copy as plain HTTP or curl. No proxy, no certificates, no app changes.
- **Survives restarts and crashes.** Relaunch the app and the stream carries on.
- **Looks back.** `--since 5m` shows what already happened: the crash on launch, the request that
  just failed.
- **Every crash.** `saw crashes` finds the crashes the app's log misses: native crashes and ANRs
  on Android, crash reports on iOS.
- **Every device at once.** `--device all` follows the app on Android and iOS together.
- **Made for terminals and scripts.** Aligned, copiable output for people; `--json`, `--grep`,
  `--until` and `--for` for scripts, CI and agents.

## Install

```sh
brew install davidamunga/tap/saw                      # macOS
go install github.com/davidamunga/saw/cmd/saw@latest  # anywhere with Go
```

Or download a binary for macOS, Linux or Windows from the
[releases](https://github.com/davidamunga/saw/releases).

saw drives the tools that already talk to devices. Install the ones for the devices you use:

| To read | You need | Install |
|---|---|---|
| Android devices and emulators | adb | `brew install --cask android-platform-tools`, or the Android SDK's platform-tools |
| iOS simulators | Xcode (macOS only) | the Mac App Store |
| iPhones and iPads | pymobiledevice3 | `pipx install pymobiledevice3`; on Linux also usbmuxd, on Windows the Apple Devices app |

Then run `saw doctor`: it checks each tool and every connected device, and says how to fix
anything missing.

## Quick start

```sh
saw devices                                                  # what's connected
saw stream --app com.example.app                             # the app's logs
saw net --app com.example.app                                # the app's network requests
saw net --app com.example.app --full                         # ... with headers and bodies, as plain HTTP
saw stream --app com.example.app --since 5m --grep checkout  # the last 5 minutes, about checkout
saw net --app com.example.app --device all                   # requests from every device at once
saw crashes --app com.example.app                            # crash reports, newest first
```

`--app` takes an Android package name or iOS bundle id, or a short name from [`saw.json`](#sawjson).
With no `--device`, saw uses the only running device; with several, it asks which one (or pass
`--device all`).

## Logs: `saw stream`

```
09:41:02.114 I MainActivity       onCreate
09:41:02.380 D OkHttp             --> GET https://api.example.com/v1/orders
09:41:03.002 W SyncWorker         upload failed, retrying (attempt 2 of 5)
09:41:03.441 E Checkout           payment failed: card declined
```

saw shows the app's own lines:

- **Android:** every line written by the app's process, matched by its user id: `Log`, Timber,
  `println`, Flutter, React Native, and crashes.
- **iOS:** every line logged by code inside the app bundle (the app and the frameworks it ships):
  `Logger`/`os_log`, `NSLog`, CocoaLumberjack, React Native, Flutter. Apple's framework lines are
  dropped.

`--scope` narrows it to some tags (Android) or Logger categories (iOS), matched in any case:
`--scope okhttp,syncworker`. To find the names, run `saw stream` once without it: the column after
the level letter is each line's tag or category. `--level warn` hides everything below warnings,
and `--all` shows everything the process logs, system lines included.

## Network: `saw net`

### Requests the app logs

Debug builds usually log every request they make: OkHttp's `HttpLoggingInterceptor` on Android,
and loggers like it on iOS. `saw net` assembles that logging into one line per request, on every
platform, under any tag or category:

```
09:41:02.792  GET    200   538ms   163 B  https://api.example.com/v1/orders
09:41:03.380  POST   402   412ms    61 B  https://api.example.com/v1/payments
09:41:05.020  POST   201   301ms    51 B  https://api.example.com/v1/payments
```

Every line has the same columns (time, method, status, duration, size) and the full URL last, so it
stays aligned and copies cleanly. Lines are stamped when each request finished.

`--full` prints each request as plain HTTP, in the `.http` file format that VS Code's REST Client
and JetBrains' HTTP Client can send again. JSON bodies are indented; everything else is a `#`
comment:

```
### 09:41:05.020  POST 201  301ms
POST https://api.example.com/v1/payments
Content-Type: application/json
Authorization: Bearer ****

{
  "order": "1042",
  "amount": 2400
}

HTTP 201
content-type: application/json

{
  "id": "pay_88",
  "status": "succeeded"
}
```

`--curl` prints each request as a curl command, to paste and send again:

```sh
curl -X POST 'https://api.example.com/v1/payments' \
  -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer ****' \
  --data-raw '{"order":"1042","amount":2400}'
```

`--har session.har` also saves the requests to a [HAR](https://en.wikipedia.org/wiki/HAR_(file_format))
file as they come, while printing as usual. Open it in Chrome DevTools (Network tab, Import HAR),
Proxyman, Charles or Insomnia, or attach it to a bug report:

```sh
saw net --app com.example.app --for 1m --har session.har
```

saw knows three logging formats:

| Format | Used by | Looks like |
|---|---|---|
| OkHttp-style | OkHttp and Retrofit, and the many loggers that copy it | `--> POST https://…`, headers, body, `<-- 201 https://… (301ms)` |
| Ktor | Ktor's `Logging` plugin | `REQUEST: https://…`, `METHOD: …`, `RESPONSE: 200 OK` |
| Dio | Flutter's Dio `LogInterceptor` | `*** Request ***`, `uri: …`, `statusCode: …` |

Other HTTP-looking lines the app logs (with a URL or a status code in them) appear as `LOG` lines.

What you see is what the app logs: its log level decides whether headers and bodies are included
(OkHttp's `Level.BODY`), and its own redaction of tokens stays. Logs also cap each message's size,
at about 4 KB on Android and 1 KB on iOS, so long bodies can be cut short; `--full` says so when a
body is shorter than its `Content-Length`.

### The network itself

For apps that don't log their requests, and for everything before an app logs its first one,
`saw net` shows what each platform exposes about the app's traffic, without changing anything on
the device:

| Device | Shows | From |
|---|---|---|
| iOS simulator | each request's URL, status, time, protocol and size | Apple's networking logs in the app's process |
| iPhone / iPad | each request's status, time and size, and each new connection's hostname | the same logs (iOS hides URLs on devices), and a packet capture of the app (the TLS server name) |
| Android | each new connection, named by hostnames the app's log mentions, or reverse DNS | the kernel's socket table, which adb can read |

```
09:41:02.114  TLS                         api.example.com:443  203.0.113.7
09:41:02.918  REQ    200   605ms  3.3 KB  (connection 1)  h2
09:41:03.002  TCP                         api.example.com:443  203.0.113.7
```

HTTPS stays encrypted on the network, so this view shows where the app connects and how requests
went, not their contents. Once the app has logged a request, these lines stop, since the logged
requests say more; `--connections` keeps them.

## Crashes: `saw crashes`

Lists the app's crash reports on the device, newest first, including the crashes its own log
doesn't show: native crashes and ANRs on Android, and every crash on iOS, which writes them to
a report file rather than the log.

```
$ saw crashes --app com.example.app
2026-11-10 15:02:12  native  SIGSEGV in libexample.so (parse_frame+88)
2026-11-10 14:51:07  crash   java.lang.IllegalStateException: cart is empty
2026-11-09 18:20:05  anr     Input dispatching timed out: waited 5002ms for MotionEvent
```

`--last` prints the newest report in full, with its stack trace; `--json` prints every report in
full, one per line; `--since 24h` leaves out older ones. `--device all` reads every device.

| Device | Reports | From |
|---|---|---|
| Android | Java/Kotlin crashes, native crashes, ANRs (the app not responding) | the system's dropbox, via `adb shell dumpsys dropbox` |
| iOS simulator (macOS) | crashes, with symbol names | the `.ips` reports macOS writes in `~/Library/Logs/DiagnosticReports` |
| iPhone / iPad | crashes, including iOS killing the app (watchdog) | the device's `.ips` reports, copied with `pymobiledevice3 crash` |

For a crash with a message (an uncaught exception, a Swift `fatalError`, a native abort), the
summary is that message; otherwise it's the signal and the app's own frame closest to the crash.
Devices keep a limited number of reports, so old ones drop off.

## Look back, filter, stop, and several devices

These work with both `saw stream` and `saw net`.

**`--since`** starts back in time: `--since 5m`, `--since 90s`, or a clock time, `--since 09:40`.
saw prints what the device still has from then, then carries on live.

- **Android:** all of it, as far back as the device's log buffer goes (usually the last few
  minutes to hours).
- **iOS simulator:** info level and above. iOS keeps debug-level lines only while something is
  streaming them, so start `saw stream` before the bug if you need those.
- **iPhone/iPad:** not available, since they only stream live.

**`--grep`** shows only what matches a regular expression: in `saw stream`, a line's message or
tag; in `saw net`, a request's method, status, URL, host or bodies. A pattern in lower case ignores
case (`--grep checkout`), and one with capitals matches case exactly (`--grep Checkout`).
`--exclude-tag` (`saw stream`) hides chatty tags or categories: `--exclude-tag choreographer,ViewRootImpl`.

**`--until`** stops after the first match, and exits with an error if nothing matches.
**`--for`** stops after a while. Together they make saw usable from scripts, CI and coding agents:

```sh
# Did the payment go through within 30 seconds of launch?
saw stream --until "payment (succeeded|failed)" --for 30s

# Record 10 seconds of requests as JSON.
saw net --for 10s --json > requests.ndjson
```

`--for` counts from when live lines start on every device, after any `--since` history.

**`--device all`** follows the app on every running device it's configured for, with a column
naming each line's device. `--device pixel,"17 pro"` picks several by name. With `--since`, the
history from every device is printed in time order.

```
09:41:05.020 Pixel 8   I checkout           payment succeeded: order #1042
09:41:05.410 iPhone 17 E checkout           payment failed: card declined
```

If one device can't be read (the app isn't installed, or it's an iPhone with `--since`), saw says
so and carries on with the others.

## saw.json

Optional. It gives a project's apps short names and lists its log scopes, so a team can run
`saw stream --app main --scope auth` without knowing package ids, tags or categories. Put it at the
repo root; saw finds the nearest one above the working directory, and `--config` points at another.

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

With `scopes` set, `saw stream` shows only those scopes by default: the logcat tags `android.tag`
builds, and Logger lines in the app's subsystem on iOS. `--all` shows everything. With one app
listed, `--app` can be left out. A misspelled key or scope is an error, not silently ignored.

## Commands

| Command | Flags |
|---|---|
| `saw devices` | `--all` includes shut-down simulators; `--json` |
| `saw stream` | `--app`, `--scope a,b` (repeatable), `--level verbose\|debug\|info\|warn\|error\|fatal`, `--exclude-tag a,b`, `--all`, `--json`, and the shared flags |
| `saw net` | `--app`, one of `--full`, `--curl` or `--json`; `--har <file>`, `--connections`, and the shared flags |
| `saw crashes` | `--app`, `--device`, `--platform`, `--since`, and one of `--last` or `--json` |
| `saw doctor` | |
| `saw version` | |

Shared flags for `stream` and `net`: `--device`, `--platform android|ios`, `--since`, `--grep`,
`--until`, `--for` (see [above](#look-back-filter-stop-and-several-devices)).

Every command takes `--config <path>`. `--device` takes a device's id, the start of its id, or all
or part of its name (`pixel`, `"17 pro"`); several separated by commas; or `all`.

### JSON

`--json` prints one object per line, leaving out empty fields. `saw stream`:

```json
{"time":"2026-11-09T09:41:02.38+03:00","platform":"android","device":"Pixel 8","process":"com.example.app","pid":4821,"tid":4870,"level":"debug","tag":"OkHttp","message":"--> GET https://api.example.com/v1/orders"}
```

| Field | Meaning |
|---|---|
| `time` | When the line was logged. |
| `platform`, `device` | `android` or `ios`, and the device's name. |
| `process`, `pid`, `tid`, `sender` | The app's process, its ids, and on iOS the binary or library that logged the line. |
| `level` | `verbose`, `debug`, `info`, `warn`, `error` or `fatal`. |
| `tag`, `subsystem`, `category`, `scope` | The logcat tag (Android), Logger subsystem and category (iOS), and the `saw.json` scope. |
| `message` | The log message. |

`saw net`:

```json
{"time":"2026-11-09T09:41:05.02+03:00","started":"2026-11-09T09:41:04.72+03:00","platform":"android","device":"Pixel 8","kind":"logged","url":"https://api.example.com/v1/payments","method":"POST","status":201,"duration_ms":301,"request_headers":[{"name":"Content-Type","value":"application/json"}],"request_body":"{\"order\":\"1042\"}","response_body":"{\"id\":\"pay_88\"}"}
```

| Field | Meaning |
|---|---|
| `kind` | `logged` (a request the app logged), `request` (from Apple's networking logs), `http` (plain HTTP seen on the network), `tls`, `tcp`, `udp`, `dns` (connections), or `log` (another HTTP-looking log line). |
| `time`, `started` | When it happened, or for requests when they finished; and when a logged request was sent. |
| `method`, `url`, `status`, `duration_ms`, `failed`, `error` | The request, its outcome and timing. |
| `request_headers`, `request_body`, `response_headers`, `response_body` | For logged requests: what the app's logging included. |
| `host`, `ip`, `port`, `protocol`, `bytes`, `connection`, `cached` | Network details, where the platform gives them. |
| `log`, `tag`, `level` | For `log` lines: the line, its tag or category, and its level. |

`saw crashes --json`, one report per line:

| Field | Meaning |
|---|---|
| `time`, `platform`, `device`, `process` | When and where the app crashed. |
| `kind` | `crash`, `native` (Android native code) or `anr` (Android: the app stopped responding). |
| `summary` | The exception, message or signal, in one line. |
| `report` | The whole report, readable, with stack traces. |
| `file` | iOS simulator: the `.ips` report on this computer. |

## How it works

saw starts the tool that reads each kind of device, turns its output into one format, filters it
to the app, and prints it:

| Device | Logs | Network |
|---|---|---|
| Android device or emulator | `adb logcat`, filtered by the app's user id | the app's log, and its sockets in `/proc/net` (via adb) |
| iOS simulator (macOS) | `xcrun simctl spawn … log stream` | the app's log, and Apple's networking logs |
| iPhone / iPad | `pymobiledevice3 syslog live`, keeping each line's subsystem and category | the app's log, Apple's networking logs, and `pymobiledevice3 pcap` for TLS server names |

It never installs anything on the device or changes its settings.

## Troubleshooting

| You see | Do this |
|---|---|
| A device shows as `unauthorized` | Unlock it and accept "Allow USB debugging". |
| An iPhone shows as not trusted | Unlock it and tap "Trust This Computer". |
| `more than one device` | Pick one: `--platform android`, or `--device pixel`. |
| `… is not installed on …` | Check the package or bundle id: `adb shell pm list packages \| grep example` on Android. |
| Nothing appears | Is the app running and doing something? Lower `--level`, drop `--scope`, or try `--all`. |
| `saw net` shows only `TCP`, `TLS` or `REQ` lines | The app doesn't log its requests (or not in a format saw knows). Turn on its HTTP logging in debug builds, e.g. OkHttp's `HttpLoggingInterceptor`. |
| `(connection 1)` instead of a URL on an iPhone | iOS hides URLs on devices; the `TLS` line for that connection names its host. |
| `# body cut short in the app's log` | The log's message size limit cut the body. On Android, log long bodies in pieces under 4000 characters, as OkHttp's own logger does. |
| iOS values show as `<private>` | Mark them `privacy: .public` in the `Logger` call. saw points this out when it sees one. |

Known limits:

- iOS: Swift `print` and `printf` write to stdout, not the system log, so no tool can read them
  from a device; only Xcode, attached at launch, sees them. Use `Logger`, `os_log` or `NSLog`.
- iOS app extensions (widgets, notification services) run as their own processes and aren't included.
- Release builds often strip logging; saw shows only what the app emits.

## Develop

```sh
go test ./...
go build -o bin/saw ./cmd/saw
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for how tests work, changesets and releasing, and
[CHANGELOG.md](CHANGELOG.md) for what changed in each version.

## License

[MIT](LICENSE)
