# Contributing

Thanks for helping. A few rules keep the project safe to run on production servers.

## Before you start

- For anything larger than a small fix, open an issue first so we can agree on the approach.
- New dependencies need a clear reason. Prefer the Go standard library.
- You must sign the [Contributor Licence Agreement](CLA.md) before a pull request can be merged. A bot
  asks you on your first pull request.

## Build and test

```sh
go build ./...
go vet ./...
go run honnef.co/go/tools/cmd/staticcheck@2025.1.1 ./...
go test -race ./...
```

`gofmt` must report nothing. CI runs all of this on Linux and Windows.

## Code

- Code that differs by operating system lives only in `internal/platform`, selected by build tags.
- Every source file starts with:

  ```go
  // Copyright 2026 Brynko Technologies
  // SPDX-License-Identifier: AGPL-3.0-only
  ```

- Keep comments short and only where the reason is not obvious from the code.
- Every change comes with tests. A bug fix includes a test that fails without it.
- Never log secrets. Never run a command through a shell string.
- Changing any `--json` output in a way that breaks readers requires a new `api_version`.

## Writing

Messages and docs use plain language: say what happened and what to do next. Do not use the em dash
character; use commas, colons or full stops.
