# Building from source

## Requirements

- Go 1.22 or newer. Check with `go version`.
- Git.

No C compiler is needed: pulse builds with `CGO_ENABLED=0` for every platform.

## Build

```sh
git clone https://github.com/Aadilkaifi/brynko-pulse.git
cd brynko-pulse
go build -o pulse ./cmd/pulse        # Windows: -o pulse.exe
./pulse version
```

## Build for another system

Go builds for any supported system from any other, for example a Linux server binary on a Windows
PC.

```sh
# Linux or macOS shell
GOOS=linux   GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o pulse         ./cmd/pulse
GOOS=linux   GOARCH=arm64 go build -trimpath -ldflags "-s -w" -o pulse-arm64   ./cmd/pulse
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o pulse.exe     ./cmd/pulse
```

```powershell
# Windows PowerShell
$env:GOOS="linux"; $env:GOARCH="amd64"
go build -trimpath -ldflags "-s -w" -o pulse ./cmd/pulse
Remove-Item Env:GOOS, Env:GOARCH
```

`-trimpath` and `-ldflags "-s -w"` make the binary smaller (about 8 MB) and independent of where it
was built. Copy it to the server (for example with `scp`) and run `sudo ./pulse install`.

## Test

```sh
gofmt -l .                     # must print nothing
go vet ./...
go run honnef.co/go/tools/cmd/staticcheck@2025.1.1 ./...
go test -race ./...
```

CI runs the same on Linux and Windows for every push and pull request.

## Docs that are generated

Two pages are generated from the code, and a test fails when they are out of date:

| Page | Generated from |
|---|---|
| [cli.md](cli.md) | The command table in `cmd/pulse/main.go` and each command's flags |
| [config-reference.md](config-reference.md) | `schema/pulse.schema.json` |

After changing a command, a flag or the schema, regenerate them:

```sh
go test ./cmd/pulse ./schema -run Docs -update
```

A test also checks that [monitors.md](monitors.md) and [channels.md](channels.md) have a section
for every monitor and channel type in the schema.

## Packages

The `.deb` and `.rpm` packages are built with [nfpm](https://nfpm.goreleaser.com/) from
`packaging/nfpm.yaml`. nfpm is only used to build them; nothing extra is installed on servers.

```sh
go install github.com/goreleaser/nfpm/v2/cmd/nfpm@v2.41.1
VERSION=0.0.0-dev ARCH=amd64 BINARY=./pulse nfpm package --config packaging/nfpm.yaml --packager deb --target dist/
```

## Releases

Pushing a tag such as `v0.1.0` runs `.github/workflows/release.yml`. It tests, builds the
binaries for linux/amd64, linux/arm64 and windows/amd64 with the version, commit and commit time
embedded, builds the `.deb` and `.rpm` packages, adds `install.sh`, writes `SHA256SUMS`, and
creates a draft release. A maintainer checks the draft and publishes it. Builds are reproducible:
the same tag always gives the same files.
