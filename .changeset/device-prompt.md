---
saw: patch
---

saw asks "Which device?" only when someone at a terminal can answer. From scripts, CI and coding agents (whose input is often `/dev/null`, which saw took for a terminal), it now lists the devices and says to pass `--device`, instead of failing with "no device chosen". Colors are also off when output goes to `/dev/null`.
