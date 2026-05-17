#!/usr/bin/env bash
set -euo pipefail

repo="OrdalieTech/LazyBut"
bin="lazybut"
version="${LAZYBUT_VERSION:-latest}"

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m)"
case "$os" in
  darwin|linux) ;;
  *) echo "lazybut: unsupported OS: $os" >&2; exit 1 ;;
esac
case "$arch" in
  x86_64|amd64) arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *) echo "lazybut: unsupported architecture: $arch" >&2; exit 1 ;;
esac

asset="lazybut_${os}_${arch}.tar.gz"
if [[ "$version" == "latest" ]]; then
  url="https://github.com/${repo}/releases/latest/download/${asset}"
else
  url="https://github.com/${repo}/releases/download/${version}/${asset}"
fi

if [[ -n "${LAZYBUT_INSTALL_DIR:-}" ]]; then
  install_dir="$LAZYBUT_INSTALL_DIR"
elif [[ -d /usr/local/bin && -w /usr/local/bin ]]; then
  install_dir="/usr/local/bin"
else
  install_dir="${HOME}/.local/bin"
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "Downloading ${asset}..."
curl -fsSL "$url" -o "${tmp}/${asset}"
tar -xzf "${tmp}/${asset}" -C "$tmp"

mkdir -p "$install_dir"
install -m 0755 "${tmp}/${bin}" "${install_dir}/${bin}"

echo "lazybut installed to ${install_dir}/${bin}"
if [[ ":${PATH}:" != *":${install_dir}:"* ]]; then
  echo "Note: ${install_dir} is not in your PATH."
  echo "Add this to your shell profile:"
  echo "  export PATH=\"${install_dir}:\$PATH\""
fi
