#!/usr/bin/env bash
# Resolve a compatible installed Go toolchain or install a verified, pinned
# Linux distribution inside the checkout's ignored dist directory.
# Source this file from build.sh; do not change the user's global PATH or GOROOT.

go_usable() {
  local candidate="$1" required="$2" version
  [[ -x "$candidate" ]] || return 1
  version="$(env -u GOROOT GOTOOLCHAIN=local "$candidate" version 2>/dev/null)" || return 1
  [[ "$version" =~ ^go[[:space:]]version[[:space:]]go([0-9]+)\.([0-9]+)(\.([0-9]+))?[[:space:]] ]] || return 1
  local major="$((10#${BASH_REMATCH[1]}))" minor="$((10#${BASH_REMATCH[2]}))"
  local patch="$((10#${BASH_REMATCH[4]:-0}))"
  [[ "$required" =~ ^([0-9]+)\.([0-9]+)(\.([0-9]+))?$ ]] || return 1
  local req_major="$((10#${BASH_REMATCH[1]}))" req_minor="$((10#${BASH_REMATCH[2]}))"
  local req_patch="$((10#${BASH_REMATCH[4]:-0}))"
  (( major > req_major || (major == req_major && (minor > req_minor || (minor == req_minor && patch >= req_patch))) ))
}

bootstrap_go() (
  set -euo pipefail
  umask 077
  local root="$1" required="$2" arch checksum
  local version="go1.26.11"
  if [[ "$(uname -s)" != Linux ]]; then
    echo "Automatic Go installation supports Linux amd64/arm64. Install Go $required or newer from https://go.dev/dl/." >&2
    return 1
  fi
  case "$(uname -m)" in
    x86_64|amd64) arch=amd64; checksum=9849f4e04ee35303714e34fbc3ac8ab44ea3e448a95aa182f91e3b9b79c710ec ;;
    aarch64|arm64) arch=arm64; checksum=6c2f29b458b5add0835f18b776f69c8881a6d88428f1484a6da55755e620757e ;;
    *) echo "Unsupported CPU for automatic Go installation. Install Go $required or newer from https://go.dev/dl/." >&2; return 1 ;;
  esac
  # The SHA-256 values are published by https://go.dev/dl/.
  local cache="$root/dist/toolchains" target stage archive url
  target="$cache/$version-linux-$arch"
  if go_usable "$target/bin/go" "$required"; then
    printf '%s\n' "$target/bin/go"
    return 0
  fi
  for tool in tar sha256sum mktemp; do
    if ! command -v "$tool" >/dev/null; then
      echo "Cannot install Go: missing $tool." >&2
      return 1
    fi
  done
  mkdir -p "$cache"
  stage="$(mktemp -d "$cache/.install.XXXXXX")"
  trap 'rm -rf -- "$stage"' EXIT
  archive="$stage/$version.linux-$arch.tar.gz"
  url="https://go.dev/dl/$version.linux-$arch.tar.gz"
  echo "[build] Go not found or too old; downloading $version (linux/$arch)..." >&2
  if command -v curl >/dev/null; then
    if ! curl --fail --show-error --location --retry 2 --connect-timeout 15 --max-time 300 --proto '=https' --proto-redir '=https' --output "$archive" "$url"; then
      echo "[build] Go download failed. Check network access to go.dev and dl.google.com." >&2
      return 1
    fi
  elif command -v wget >/dev/null; then
    if ! wget --https-only --timeout=30 --tries=3 -O "$archive" "$url"; then
      echo "[build] Go download failed. Check network access to go.dev and dl.google.com." >&2
      return 1
    fi
  else
    echo "Cannot download Go: install curl or wget and retry." >&2
    return 1
  fi
  if ! printf '%s  %s\n' "$checksum" "$archive" | sha256sum -c - >&2; then
    echo "[build] Go archive checksum verification failed; nothing was installed." >&2
    return 1
  fi
  tar -xzf "$archive" -C "$stage"
  if ! go_usable "$stage/go/bin/go" "$required"; then
    echo "[build] Downloaded Go does not satisfy go.mod ($required)." >&2
    return 1
  fi
  # Publish the complete toolchain atomically. Never overwrite an installed
  # toolchain; another concurrent build may have completed the same download.
  if ! mv -T -- "$stage/go" "$target" 2>/dev/null; then
    if ! go_usable "$target/bin/go" "$required"; then
      echo "[build] Cannot publish verified Go toolchain at $target." >&2
      return 1
    fi
  fi
  echo "[build] Go is ready: $target" >&2
  printf '%s\n' "$target/bin/go"
)

ensure_go() {
  local root="$1" required candidate installed
  required="$(awk '$1 == "go" { print $2; exit }' "$root/go.mod")"
  if [[ ! "$required" =~ ^[0-9]+\.[0-9]+(\.[0-9]+)?$ ]]; then
    echo "[build] Cannot determine required Go version from go.mod." >&2
    return 1
  fi
  installed="$(type -P go || true)"
  for candidate in "$installed" /usr/local/go/bin/go /usr/lib/go/bin/go "${HOME:-/nonexistent}/.local/go/bin/go"; do
    if go_usable "$candidate" "$required"; then
      printf '%s\n' "$candidate"
      return 0
    fi
  done
  bootstrap_go "$root" "$required"
}
