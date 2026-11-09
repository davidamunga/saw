# saw

Stream a mobile app's logs from any Android device or emulator, iOS simulator, or iPhone/iPad.

```sh
saw devices                         # everything connected (--all adds shut-down simulators)
saw stream --app com.example.app    # any app, by package or bundle id: its own log lines
saw stream --app main --area auth,api --device iPhone --level debug   # apps and areas from saw.json
saw stream --app main --all --json > session.jsonl                     # everything, as JSON lines
saw doctor                          # what's missing, and how to fix it
```

## Install

```sh
brew install davidamunga/tap/saw                     # macOS, Linux
go install github.com/davidamunga/saw/cmd/saw@latest  # anywhere with Go
```

Or download a binary for macOS, Linux or Windows from the
[releases](https://github.com/davidamunga/saw/releases). Then run `saw doctor`: it says which of
adb, Xcode and pymobiledevice3 you need for your devices.

## How it works

`saw` wraps the tools that already read device logs and gives them one interface and one output:

| Target | Tool | Host |
|---|---|---|
| Android device or emulator | `adb logcat`, filtered by the app's uid, so it survives restarts and crashes | any |
| iOS simulator | `xcrun simctl spawn … log stream` | macOS |
| iPhone / iPad | `pymobiledevice3 syslog live` (keeps subsystem and category) | any (usbmuxd on Linux, Apple Devices on Windows) |

With no `--device`, `saw` uses the only running device, or asks which one.

## saw.json

Optional. It names your apps and maps the project's log areas to iOS Logger categories and Android
logcat tags. Put it at the repo root; `saw` finds the nearest one above the working directory, and
`--config` points at another. See [saw.example.json](saw.example.json).

| Key | Meaning |
|---|---|
| `apps.<name>.android` | The app's Android package name. |
| `apps.<name>.ios` | The app's iOS bundle id. |
| `apps.<name>.process` | Optional. The iOS executable name; read from the device when left out. |
| `areas` | The app's log areas, for `--area`. On iOS each is a Logger category. |
| `android.tag` | The logcat tag for an area: `{Area}` → `Api`, `{area}` → `api`, `{AREA}` → `API`. |
| `ios.subsystem` | The app's Logger subsystem. Default `{bundle}`, the bundle id. |

A misspelled key is an error, not silently ignored.

Without one, pass `--app <package or bundle id>`; it works for any app, however it logs:

- **Android:** every line the app's process writes: `Log`, Timber, `println`, Flutter, React Native, crashes.
- **iOS:** every line sent from code inside the app bundle (the app and the frameworks it ships):
  `Logger`/`os_log`, `NSLog`, CocoaLumberjack, React Native, Flutter. Apple's own framework lines are dropped.

`--all` shows everything the process logs, Apple's and the system's lines included.

## Notes

- iOS: Swift `print` and `printf` go to stdout, not the system log, so no tool can read them from a
  device; only a debugger attached at launch (Xcode) sees them. Use `Logger`, `os_log` or `NSLog`.
- iOS hides interpolated values as `<private>` unless they're marked `privacy: .public` in the
  `Logger` call; `saw` warns when it sees them.
- iOS app extensions (widgets, notification services) run as their own processes and aren't included.
- Release builds may strip logging; `saw` only shows what the app emits.

## Develop

```sh
go test ./...
go build -o bin/saw ./cmd/saw
```

To release, tag and push: `git tag v0.1.0 && git push origin v0.1.0`. The release workflow builds
every platform with GoReleaser, publishes a GitHub release and updates the Homebrew tap.

`testdata/` holds real captured log output that the parser tests run against.

## License

[MIT](LICENSE)
