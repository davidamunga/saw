# Contributing

```sh
go test ./...
go build -o bin/saw ./cmd/saw
```

Tests run saw against fake `adb`, `xcrun` and `pymobiledevice3` (see `internal/runtest`), so they
need no devices; `testdata/` holds real captured output for the parsers.

## Changesets

Every pull request that changes saw's behavior adds a changeset: a short note in `.changeset/` that
says what changed and how much, for the changelog. CI checks for one when a PR touches `cmd/`,
`internal/` or `go.mod`.

Add one with [go-changesets](https://github.com/jakoblorz/go-changesets):

```sh
go install github.com/jakoblorz/go-changesets/cmd/changeset@latest
changeset add
```

or by hand, as `.changeset/<any-name>.md`:

```markdown
---
saw: minor
---

`saw stream` reconnects when a device drops.
```

Use `patch` for fixes, `minor` for new features, and `major` for changes that break existing
commands, flags, output or saw.json. Write it for people using saw: what they can now do, or what
works differently.

## Releasing

1. Version: this bumps `version.txt`, adds the changesets to `CHANGELOG.md` and removes them.
   ```sh
   git checkout -b release/vX.Y.Z
   changeset version --project saw
   ```
2. Open a pull request with that and merge it.
3. Tag the merge on main with the new version:
   ```sh
   git tag -a vX.Y.Z -m vX.Y.Z && git push origin vX.Y.Z
   ```

The release workflow checks the tag matches `version.txt`, builds every platform with GoReleaser,
publishes a GitHub release with that version's `CHANGELOG.md` section as its notes, and updates the
Homebrew tap. (saw tags releases `vX.Y.Z` for Go and GoReleaser, so `changeset publish`, which tags
`saw@vX.Y.Z`, isn't used.)
