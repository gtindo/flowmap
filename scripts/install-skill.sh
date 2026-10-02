#!/usr/bin/env bash

set -euo pipefail

readonly repository="gtindo/flowmap"
readonly skill_name="flowmap-views"
readonly script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly skill_source="${script_dir}/../skills/${skill_name}"
readonly skills_dir="${CLAUDE_SKILLS_DIR:-${HOME}/.claude/skills}"
readonly destination="${skills_dir}/${skill_name}"

# fail reports an installation failure and exits without touching the destination.
fail() {
  echo "install-skill: $*" >&2
  exit 1
}

# platform maps the host to a published release archive suffix.
platform() {
  local os arch
  case "$(uname -s)" in
    Darwin) os="darwin" ;;
    Linux) os="linux" ;;
    *) fail "unsupported operating system $(uname -s)" ;;
  esac
  case "$(uname -m)" in
    arm64 | aarch64) arch="arm64" ;;
    x86_64 | amd64) arch="amd64" ;;
    *) fail "unsupported architecture $(uname -m)" ;;
  esac
  [[ "${os}_${arch}" != "linux_arm64" ]] || fail "no linux_arm64 release is published"
  echo "${os}_${arch}"
}

# download_binary fetches the pinned release and verifies it against SHA256SUMS.
download_binary() {
  local version="$1" work_dir="$2"
  local archive="flowmap_${version}_$(platform).tar.gz"
  local base_url="https://github.com/${repository}/releases/download/v${version}"

  echo "Downloading ${archive}..." >&2
  curl -fsSL -o "${work_dir}/${archive}" "${base_url}/${archive}" || fail "download failed: ${base_url}/${archive}"
  curl -fsSL -o "${work_dir}/SHA256SUMS" "${base_url}/SHA256SUMS" || fail "download failed: ${base_url}/SHA256SUMS"

  local expected actual
  expected="$(awk -v name="${archive}" '$2 == name || $2 == "*" name { print $1 }' "${work_dir}/SHA256SUMS")"
  [[ -n "$expected" ]] || fail "SHA256SUMS has no entry for ${archive}"
  actual="$(shasum -a 256 "${work_dir}/${archive}" | awk '{ print $1 }')"
  [[ "$expected" == "$actual" ]] || fail "checksum mismatch for ${archive}"

  tar -xzf "${work_dir}/${archive}" -C "$work_dir"
  find "$work_dir" -type f -name flowmap -perm -u+x | head -n 1
}

# main installs the skill files and the verified pinned binary.
main() {
  [[ -f "${skill_source}/SKILL.md" ]] || fail "skill source not found at ${skill_source}"
  local version
  version="$(tr -d '[:space:]' < "${skill_source}/FLOWMAP_VERSION")"
  version="${version#v}"

  # Global so the EXIT trap can still see it after main returns.
  work_dir="$(mktemp -d)"
  trap 'rm -rf "$work_dir"' EXIT

  local binary
  binary="$(download_binary "$version" "$work_dir")"
  [[ -n "$binary" ]] || fail "release archive did not contain a flowmap binary"

  local installed_version
  installed_version="$("$binary" version)"
  [[ "${installed_version#v}" == "$version" ]] || fail "binary reports ${installed_version}, expected ${version}"

  echo "Installing ${skill_name} into ${destination}..."
  mkdir -p "$skills_dir"
  rm -rf "${destination}.tmp"
  mkdir -p "${destination}.tmp/bin"
  cp -R "${skill_source}/." "${destination}.tmp/"
  cp "$binary" "${destination}.tmp/bin/flowmap"
  chmod +x "${destination}.tmp/bin/flowmap" "${destination}.tmp/scripts/fm"
  rm -rf "$destination"
  mv "${destination}.tmp" "$destination"

  echo "Installed ${skill_name} with Flowmap ${version}."
}

main "$@"
