#!/usr/bin/env bash
set -euo pipefail

repo="${BORZ_REPO:-leolin310148/borz}"
version="${BORZ_VERSION:-latest}"
install_dir="${BORZ_INSTALL_DIR:-/usr/local/bin}"
binary_name="${BORZ_BINARY_NAME:-borz}"

need() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "error: required command not found: $1" >&2
    exit 1
  fi
}

detect_os() {
  case "$(uname -s)" in
    Darwin) echo "darwin" ;;
    Linux) echo "linux" ;;
    *) echo "error: unsupported OS $(uname -s); use install.ps1 on Windows" >&2; exit 1 ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64) echo "amd64" ;;
    arm64|aarch64) echo "arm64" ;;
    *) echo "error: unsupported architecture $(uname -m)" >&2; exit 1 ;;
  esac
}

release_path() {
  if [ "$version" = "latest" ]; then
    echo "latest/download"
    return
  fi
  case "$version" in
    v*) echo "download/$version" ;;
    *) echo "download/v$version" ;;
  esac
}

install_binary() {
  local src="$1"
  local dst="$install_dir/$binary_name"
  if [ -d "$install_dir" ] && [ -w "$install_dir" ]; then
    install -m 0755 "$src" "$dst"
  else
    if [ ! -d "$install_dir" ]; then
      sudo install -d -m 0755 "$install_dir"
    fi
    sudo install -m 0755 "$src" "$dst"
  fi
}

verify_checksum() {
  local checksum_file="$1"
  local asset="$2"
  local line_file="$3"

  awk -v name="$asset" '$2 == name || $2 == "*" name { print; found=1 } END { exit found ? 0 : 1 }' \
    "$checksum_file" > "$line_file" || {
      echo "error: checksums.txt does not contain $asset" >&2
      exit 1
    }

  if command -v sha256sum >/dev/null 2>&1; then
    (cd "$(dirname "$line_file")" && sha256sum -c "$(basename "$line_file")")
  elif command -v shasum >/dev/null 2>&1; then
    (cd "$(dirname "$line_file")" && shasum -a 256 -c "$(basename "$line_file")")
  else
    echo "error: sha256sum or shasum is required for checksum verification" >&2
    exit 1
  fi
}

need curl
need awk

os="$(detect_os)"
arch="$(detect_arch)"
asset="borz-$os-$arch"
base_url="https://github.com/$repo/releases/$(release_path)"
tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT

echo "Installing borz from $repo ($version) for $os/$arch..."
curl -fsSL "$base_url/checksums.txt" -o "$tmp_dir/checksums.txt"
curl -fL "$base_url/$asset" -o "$tmp_dir/$asset"
verify_checksum "$tmp_dir/checksums.txt" "$asset" "$tmp_dir/$asset.sha256"
chmod +x "$tmp_dir/$asset"
install_binary "$tmp_dir/$asset"

echo "Installed $binary_name to $install_dir"
if command -v "$binary_name" >/dev/null 2>&1; then
  "$binary_name" version
else
  echo "Add $install_dir to PATH, then run: $binary_name version"
fi

