# Install

Hades is two static binaries, `hades` and `hades-port`, with no runtime dependencies. Both must be on `PATH`.

## Platforms

| Platform | Status |
|---|---|
| darwin/arm64 | first class, tested in CI |
| linux/amd64 | first class, tested in CI |
| linux/arm64 | built, untested |
| darwin/amd64 | built, untested |

## Server

The infra playbook installs a pinned release: it downloads the archive for the host, puts `hades` and `hades-port` in `/usr/local/bin` (root-owned, mode 0755), writes `/etc/hades/host.toml` and installs the sudoers rule for `hades caddy-reload`. Hades never writes the host config itself. See [configuration](configuration.md) for its keys.

## Laptop

Download the archive for your platform from the GitHub Releases page (`hades_<version>_<os>_<arch>.tar.gz`, with `checksums.txt` beside it). The repository is private for now, so use `gh release download` or a browser signed in to GitHub. Verify the checksum, extract, and put both binaries on `PATH`:

```sh
tar xzf hades_<version>_darwin_arm64.tar.gz
install -m 0755 hades hades-port ~/.local/bin/
hades version
```

Or build from source with Go 1.25 or newer:

```sh
go install github.com/quotedo/hades/cmd/...@latest
```

`go install` puts both binaries in `$(go env GOPATH)/bin`. A source build reports its version as `dev`.

Without a host config, hades runs in laptop mode: no URL, no Caddy site, no sudo.

## Building a release

Releases are cut by pushing a tag `v*`, which runs `.github/workflows/release.yml` (goreleaser). To try the build locally without publishing anything:

```sh
make release-snapshot
```

The archives and `checksums.txt` land in `dist/`. The first run downloads goreleaser.
