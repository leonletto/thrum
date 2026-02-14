#!/bin/sh
# Thrum install script
# Usage: curl -fsSL https://raw.githubusercontent.com/leonletto/thrum/main/scripts/install.sh | sh
#
# Environment variables:
#   VERSION      - Specific version to install (default: latest)
#   INSTALL_DIR  - Installation directory (default: ~/.local/bin)

set -e

REPO="leonletto/thrum"
BINARY_NAME="thrum"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"
VERSION="${VERSION:-latest}"
GITHUB_API="https://api.github.com/repos/$REPO"
GITHUB_RELEASE="https://github.com/$REPO/releases/download"

# --- Helpers ---

log() {
  printf '%s\n' "$1"
}

err() {
  printf 'Error: %s\n' "$1" >&2
  exit 1
}

need_cmd() {
  if ! command -v "$1" > /dev/null 2>&1; then
    err "required command not found: $1"
  fi
}

# --- Platform detection ---

detect_os() {
  os=$(uname -s | tr '[:upper:]' '[:lower:]')
  case "$os" in
    linux)  echo "linux" ;;
    darwin) echo "darwin" ;;
    *)      err "Unsupported operating system: $os" ;;
  esac
}

detect_arch() {
  arch=$(uname -m)
  case "$arch" in
    x86_64)        echo "amd64" ;;
    aarch64|arm64) echo "arm64" ;;
    *)             err "Unsupported architecture: $arch" ;;
  esac
}

# --- Version resolution ---

get_latest_version() {
  if command -v curl > /dev/null 2>&1; then
    curl -fsSL --proto '=https' --tlsv1.2 "$GITHUB_API/releases/latest" 2>/dev/null |
      grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/'
  elif command -v wget > /dev/null 2>&1; then
    wget -qO- "$GITHUB_API/releases/latest" 2>/dev/null |
      grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/'
  else
    err "curl or wget is required"
  fi
}

# --- Download helper ---

download() {
  url="$1"
  output="$2"
  if command -v curl > /dev/null 2>&1; then
    curl -fsSL --proto '=https' --tlsv1.2 -o "$output" "$url"
  elif command -v wget > /dev/null 2>&1; then
    wget -qO "$output" "$url"
  else
    err "curl or wget is required"
  fi
}

# --- Checksum verification ---

verify_checksum() {
  archive="$1"
  checksums_file="$2"
  expected=$(grep "$(basename "$archive")" "$checksums_file" | awk '{print $1}')

  if [ -z "$expected" ]; then
    err "No checksum found for $(basename "$archive") in checksums.txt"
  fi

  if command -v sha256sum > /dev/null 2>&1; then
    actual=$(sha256sum "$archive" | awk '{print $1}')
  elif command -v shasum > /dev/null 2>&1; then
    actual=$(shasum -a 256 "$archive" | awk '{print $1}')
  else
    log "Warning: sha256sum/shasum not found, skipping checksum verification"
    return 0
  fi

  if [ "$actual" != "$expected" ]; then
    err "Checksum mismatch!\n  Expected: $expected\n  Actual:   $actual\n\nThe downloaded file may have been tampered with."
  fi

  log "  Checksum verified (SHA-256)"
}

# --- macOS re-signing ---

macos_resign() {
  binary="$1"
  if [ "$(uname -s)" = "Darwin" ]; then
    # Remove quarantine attribute (macOS adds this to downloaded files)
    xattr -d com.apple.quarantine "$binary" 2>/dev/null || true
    # Ad-hoc codesign for Gatekeeper
    if command -v codesign > /dev/null 2>&1; then
      codesign --force --sign - "$binary" 2>/dev/null || true
    fi
  fi
}

# --- Installation methods ---

install_from_release() {
  os="$1"
  arch="$2"
  version="$3"

  # Strip leading 'v' for archive naming
  version_num="${version#v}"
  archive_name="thrum_${version_num}_${os}_${arch}.tar.gz"
  download_url="$GITHUB_RELEASE/$version/$archive_name"
  checksums_url="$GITHUB_RELEASE/$version/checksums.txt"

  tmp_dir=$(mktemp -d)
  trap 'rm -rf "$tmp_dir"' EXIT

  log "Downloading thrum $version for $os/$arch..."
  download "$download_url" "$tmp_dir/$archive_name" || return 1

  log "Downloading checksums..."
  download "$checksums_url" "$tmp_dir/checksums.txt" || return 1

  log "Verifying integrity..."
  verify_checksum "$tmp_dir/$archive_name" "$tmp_dir/checksums.txt"

  log "Extracting..."
  tar -xzf "$tmp_dir/$archive_name" -C "$tmp_dir"

  mkdir -p "$INSTALL_DIR"
  mv "$tmp_dir/$BINARY_NAME" "$INSTALL_DIR/$BINARY_NAME"
  chmod +x "$INSTALL_DIR/$BINARY_NAME"

  macos_resign "$INSTALL_DIR/$BINARY_NAME"

  return 0
}

install_with_go() {
  if ! command -v go > /dev/null 2>&1; then
    return 1
  fi

  log "Installing via 'go install'..."
  go install "github.com/$REPO/cmd/$BINARY_NAME@latest"

  macos_resign "$(go env GOPATH)/bin/$BINARY_NAME"

  return 0
}

install_from_source() {
  if ! command -v go > /dev/null 2>&1; then
    return 1
  fi
  if ! command -v git > /dev/null 2>&1; then
    return 1
  fi

  log "Building from source..."
  tmp_dir=$(mktemp -d)
  trap 'rm -rf "$tmp_dir"' EXIT

  git clone --depth 1 "https://github.com/$REPO.git" "$tmp_dir/thrum"
  cd "$tmp_dir/thrum"
  go build -o "$BINARY_NAME" ./cmd/$BINARY_NAME

  mkdir -p "$INSTALL_DIR"
  mv "$BINARY_NAME" "$INSTALL_DIR/$BINARY_NAME"
  chmod +x "$INSTALL_DIR/$BINARY_NAME"

  macos_resign "$INSTALL_DIR/$BINARY_NAME"

  return 0
}

# --- PATH check ---

check_path() {
  case ":$PATH:" in
    *":$INSTALL_DIR:"*) return 0 ;;
  esac

  shell_name=$(basename "$SHELL" 2>/dev/null || echo "sh")
  case "$shell_name" in
    zsh)  rc="~/.zshrc" ;;
    bash) rc="~/.bashrc" ;;
    fish) rc="~/.config/fish/config.fish" ;;
    *)    rc="your shell profile" ;;
  esac

  log ""
  log "  $INSTALL_DIR is not in your PATH."
  log "  Add it by running:"
  log ""
  if [ "$shell_name" = "fish" ]; then
    log "    fish_add_path $INSTALL_DIR"
  else
    log "    echo 'export PATH=\"$INSTALL_DIR:\$PATH\"' >> $rc"
    log "    export PATH=\"$INSTALL_DIR:\$PATH\""
  fi
}

# --- Duplicate detection ---

check_duplicates() {
  count=0
  IFS=:
  for dir in $PATH; do
    if [ -x "$dir/$BINARY_NAME" ]; then
      count=$((count + 1))
    fi
  done
  unset IFS

  if [ "$count" -gt 1 ]; then
    log ""
    log "  Warning: Found $count copies of '$BINARY_NAME' in PATH."
    log "  Run 'which -a $BINARY_NAME' to see all locations."
  fi
}

# --- Main ---

main() {
  log "Thrum installer"
  log ""

  os=$(detect_os)
  arch=$(detect_arch)

  # Resolve version
  if [ "$VERSION" = "latest" ]; then
    log "Resolving latest version..."
    VERSION=$(get_latest_version)
    if [ -z "$VERSION" ]; then
      err "Failed to determine latest version. Set VERSION explicitly or check network."
    fi
  fi

  log "  Version:  $VERSION"
  log "  Platform: $os/$arch"
  log "  Target:   $INSTALL_DIR"
  log ""

  # Method 1: GitHub release (preferred)
  if install_from_release "$os" "$arch" "$VERSION"; then
    log ""
    log "  thrum installed to $INSTALL_DIR/$BINARY_NAME"
    check_path
    check_duplicates
    log ""
    log "  Run 'thrum --help' to get started."
    return 0
  fi

  log "  Release binary not available, trying fallback methods..."
  log ""

  # Method 2: go install
  if install_with_go; then
    log ""
    log "  thrum installed via 'go install'"
    check_duplicates
    log ""
    log "  Run 'thrum --help' to get started."
    return 0
  fi

  # Method 3: Build from source
  if install_from_source; then
    log ""
    log "  thrum built from source and installed to $INSTALL_DIR/$BINARY_NAME"
    check_path
    check_duplicates
    log ""
    log "  Run 'thrum --help' to get started."
    return 0
  fi

  err "All installation methods failed. Please install Go (https://go.dev) and try again."
}

main "$@"
