---
saw: patch
---

iOS lines logged with `NSLog` now show in `saw stream` (without a Logger subsystem in saw.json) and count for `saw net`. They were dropped, because `NSLog` logs from Foundation rather than from the app's own code.
