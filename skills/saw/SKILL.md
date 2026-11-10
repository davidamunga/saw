---
name: saw
description: Read a mobile app's logs, network requests and crash reports from Android devices, emulators, iOS simulators and iPhones with the saw CLI. Use when debugging app behavior on a device, checking what an API call sent and got back, confirming a fix in the running app, or finding why the app crashed.
---

# saw: device logs, requests and crashes

`saw` reads what a running app logs, the HTTP requests it makes, and its crash reports, from any
connected Android device or emulator, iOS simulator, or iPhone. Run `saw doctor` if a command
fails because a tool or device is missing.

## Rules

- **Always bound a command** with `--for <duration>`, `--until <regexp>` or both. `saw stream`
  and `saw net` otherwise run until Ctrl-C, and the command never returns.
- **Always pass `--device`** (a name like `pixel`, or an id from `saw devices --json`) or
  `--platform android|ios`. With several devices connected, saw otherwise asks which one, and
  the command fails.
- **Look back instead of waiting.** After reproducing a problem, `--since 2m` reads what already
  happened. This works on Android and iOS simulators, not iPhones (live only), and on iOS only
  info level and above is kept.
- Pass `--app <package or bundle id>` unless the project has a `saw.json` with one app in it.
- Prefer `--json` when parsing output; one object per line.

## Commands

```sh
saw devices --json                                              # what's connected, with ids

# Logs
saw stream --device pixel --since 2m --level warn --for 1s      # warnings and errors from the last 2 minutes
saw stream --device pixel --since 5m --grep checkout --for 1s   # lines about checkout (lowercase = any case)
saw stream --device pixel --until "payment (succeeded|failed)" --for 60s   # wait for an outcome; exit 1 if it never comes

# Network requests (from the app's own HTTP logging in debug builds)
saw net --device pixel --since 2m --for 1s                      # one line per request: method, status, time, size, URL
saw net --device pixel --since 2m --grep " 4[0-9][0-9] | 5[0-9][0-9] " --full --for 1s   # failing requests, with headers and bodies
saw net --device pixel --since 2m --curl --grep /v1/orders --for 1s       # a request as a curl command, to replay
saw net --device pixel --for 30s --har session.har              # record 30 seconds to a HAR file

# Crashes, including native crashes, ANRs and iOS crash reports
saw crashes --device pixel                                      # newest first, one line each
saw crashes --device pixel --last                               # the newest report in full, with its stack trace
```

`--for` counts from when live lines start, after any `--since` history; with `--since` alone,
`--for 1s` just reads the history and stops.

## A debugging loop

1. Reproduce the problem in the app (drive it, or ask the user to).
2. `saw stream --device <d> --since 2m --level warn --for 1s` for errors.
3. `saw net --device <d> --since 2m --full --grep <endpoint> --for 1s` for the request and response.
4. `saw crashes --device <d> --last` if the app died.
5. After a fix, rebuild, then confirm with `--until "<expected line>" --for 60s`.

## Limits

- Requests show in full only if the app logs them (OkHttp-style, Ktor, Dio, Alamofire's
  `debugDescription`, Moya's logger). Otherwise `saw net` shows connections and timings only.
- Long bodies are cut at the log's message size (about 4 KB on Android, 1 KB on iOS); `--full`
  says when.
- On iOS, Swift `print` goes to stdout, which saw can't read; apps need `Logger`, `os_log` or
  `NSLog`, with `privacy: .public` for values to show instead of `<private>`.
