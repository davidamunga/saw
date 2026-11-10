# Changelog

All notable changes to this project will be documented in this file.


## 0.7.1 (2026-11-10)
### Patch Changes
- Publishes 0.7.0's changes (0.7.0 was tagged but its release build failed). Tests no longer depend on the clock or time zone.

## 0.7.0 (2026-11-10)
### Minor Changes
- `saw crashes` lists the app's crash reports, newest first: Java crashes, native crashes and ANRs on Android, and crash reports on iOS simulators and devices. `--last` prints the newest in full.
- `saw net --har <file>` also saves requests to a HAR file, for Chrome DevTools, Proxyman, Charles or Insomnia.

## 0.6.0 (2026-11-09)
### Minor Changes
- `--since` starts with the device's recent history (Android, iOS simulator), then carries on live.
- `--grep`, `--exclude-tag`, `--until` and `--for` filter output and stop it, for scripts, CI and agents.
- `--device all`, or several names with commas, follows the app on several devices at once.

### Patch Changes
- Streams stopped from inside saw no longer hang on a simulator.

## 0.5.1 (2026-11-09)
### Patch Changes
- Documentation and demo updates.

## 0.5.0 (2026-11-09)
### Minor Changes
- `saw net` output is aligned and copiable; `--full` prints requests as plain HTTP (.http files), `--curl` as curl commands.

## 0.4.0 (2026-11-09)
### Minor Changes
- `saw net` assembles the HTTP requests apps log (OkHttp-style, Ktor, Dio), with `--full` for headers and bodies.

## 0.3.0 (2026-11-09)
### Major Changes
- "Areas" are renamed "scopes": `--scope`, and `scopes` in saw.json.

### Minor Changes
- `saw net` names Android connections and shows the app's HTTP log lines.

## 0.2.0 (2026-11-09)
### Minor Changes
- `saw net` shows an app's network requests on any device.

## 0.1.2 (2026-11-09)
### Patch Changes
- Documentation updates.

## 0.1.1 (2026-11-09)
### Patch Changes
- `--area` filters logcat tags without a saw.json.

## 0.1.0 (2026-11-09)
### Minor Changes
- First release: `saw devices`, `saw stream` and `saw doctor`, for Android devices and emulators, iOS simulators and iPhones.
