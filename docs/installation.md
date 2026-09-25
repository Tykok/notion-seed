# Installation

## Requirements

`notion-seed` does not handle authentication: it delegates the calls to
[`ntn`](https://www.npmjs.com/package/ntn), which stores the token in the OS
keychain.

```sh
npm i -g ntn@0.22.11
ntn login
```

::: warning The version is pinned
`notion-seed` refuses to run below `ntn` 0.22.11: it is the only version on
which the output format of `ntn` was measured.
:::

## Install notion-seed

Published binary — Linux, macOS and Windows, on amd64 and arm64 — from the
[Releases](https://github.com/tykok/notion-seed/releases) page:

```sh
# macOS arm64, adapt to your platform
curl -fsSL https://github.com/tykok/notion-seed/releases/latest/download/notion-seed_<version>_darwin_arm64.tar.gz \
  | tar -xz notion-seed
```

### macOS, Linux (Homebrew)

```sh
brew install tykok/tap/notion-seed
```

### Debian, Ubuntu

apt has no per-user tap mechanism, but an apt repository is only a tree of
static files: this one is hosted on GitHub Pages and signed.

```sh
sudo install -d /etc/apt/keyrings
curl -fsSL https://tykok.github.io/notion-seed/apt/gpg.key \
  | sudo tee /etc/apt/keyrings/notion-seed.asc > /dev/null
echo "deb [signed-by=/etc/apt/keyrings/notion-seed.asc] https://tykok.github.io/notion-seed/apt stable main" \
  | sudo tee /etc/apt/sources.list.d/notion-seed.list > /dev/null
sudo apt update && sudo apt install notion-seed
```

`signed-by` restricts the key to this repository alone: without it, the key
would be trusted for every apt source on the machine.

A `.deb`, `.rpm` or `.apk` is also attached to each release, if you would
rather not add anything to your sources:

```sh
sudo apt install ./notion-seed_<version>_linux_amd64.deb
```

### From source

```sh
go install github.com/tykok/notion-seed@latest
```

The binary installed by `go install` reports `0.0.0-dev`: the version is only
injected by the release build.

## Getting started

```sh
notion-seed init    # checks that ntn is present, recent enough and authenticated
notion-seed plan    # shows the changes
```

Next: the [commands](/commands), then how to describe your databases in [YAML](/yaml).
