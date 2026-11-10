---
saw: minor
---

`saw net` reads two more request log formats, both common on iOS: Alamofire's `response.debugDescription` and Moya's `NetworkLoggerPlugin`, when the app sends them to the unified log (`NSLog`, `os_log` or `Logger`).
