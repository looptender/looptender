# Installing looptender

How to install a release or a development build, and which name the executable has in each. The [README](../README.md#install) has the short version.

## Packages

With Homebrew on macOS or Linux:

```sh
brew install looptender/tap/looptender
looptender version
```

The [Homebrew formula](https://github.com/looptender/homebrew-tap) packages v0.2.0's `keepgoing` binary and provides `looptender` as a symlink to it. Both names run the same released code. Installation and upgrade fixtures passed on macOS arm64; Intel macOS and Linux remain unverified. Existing installations need their tap receipt and command conflicts checked before changing packages; see the tap's [installation guidance](https://github.com/looptender/homebrew-tap#existing-installations).

Or download a [release archive](https://github.com/looptender/looptender/releases/tag/v0.2.0) for macOS, Linux or Windows and verify it against the published SHA-256 checksums. The archives contain `keepgoing`. The [package verification record](release-verification.md) identifies the paths tested; not every OS/architecture has had a native installation walkthrough.

With Go 1.24 or newer:

```sh
go install github.com/connorburgess/keepgoing-agents/cmd/keepgoing@v0.2.0
```

The Go command above installs the released `keepgoing` executable. For unreleased source, `go install github.com/looptender/looptender/cmd/looptender@main` installs only `looptender`. That module's `@latest` needs a release declaring the same module path; use the pinned command above for v0.2.0.

The documentation's examples use `looptender`, which Homebrew and a source install provide. A v0.2.0 release archive and the pinned `go install` above provide only `keepgoing`: use that name in the examples, or link `looptender` to it. Existing schedulers need the executable named by their saved command, and a source install creates no `keepgoing` alias. Source-only features are marked **unreleased**.

`go install` with `@latest` selects a tagged release, not `main`. These builds report `dev` because they lack the release build label; inspect `go version -m /path/to/looptender` for the module version. Prebuilt archives carry an embedded version label.

[CLI v0.2.0 and BB plugin v0.3.0](releases/v0.2.0.md) are released from one tag. If your package source still provides v0.1.5, use a published v0.2.0 archive. The plugin has a separate [pinned installation path](../integrations/bb-plugin-keepgoing/README.md#install); Homebrew does not install it.

## Development build

On macOS/Linux, keep a source build separate from the installed release:

```sh
git clone https://github.com/looptender/looptender.git
cd looptender
revision=$(git rev-parse HEAD)
mkdir -p "$HOME/.local/bin"
go build -trimpath -ldflags "-X main.version=dev-$revision" \
  -o "$HOME/.local/bin/looptender-dev" ./cmd/looptender
"$HOME/.local/bin/looptender-dev" version
```

Substitute that absolute binary path for `looptender` in the examples. Record its revision when testing; building does not replace an existing loop's executable or timer. See [binary smoke checks](release-verification.md#check-an-actual-binary-without-provider-calls).

## The BB plugin

Plugin v0.3.0, the released one, runs the executable its `keepgoingPath` setting names, `keepgoing` by default. In source after it, the plugin runs `looptender` from the BB server's `PATH`, or `keepgoing` when only that is there; to name a path explicitly, set `looptenderPath`. A value saved under `keepgoingPath` is still used while `looptenderPath` is empty. The plugin's source is `integrations/bb-plugin-keepgoing`; see its [installation guide](../integrations/bb-plugin-keepgoing/README.md#install).
