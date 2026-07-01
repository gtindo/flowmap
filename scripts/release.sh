#!/usr/bin/env bash

set -euo pipefail

readonly expected_branch="main"
readonly release_remote="origin"

# print_usage explains the single required version argument.
print_usage() {
  cat <<"EOF"
Usage: scripts/release.sh <version>

Build, verify, tag, and push a Flowmap release.
The version may be written as 0.2.0 or v0.2.0.
EOF
}

# fail reports a release precondition failure and exits without tagging.
fail() {
  echo "release: $*" >&2
  exit 1
}

# normalize_version validates SemVer and returns its numeric and tag forms.
normalize_version() {
  local supplied_version="$1"
  version_number="${supplied_version#v}"
  if [[ ! "$version_number" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$ ]]; then
    fail "version must be SemVer, for example 0.2.0 or v0.2.0-rc.1"
  fi
  release_tag="v${version_number}"
}

# require_clean_main ensures the tag targets the committed remote main revision.
require_clean_main() {
  git rev-parse --show-toplevel >/dev/null 2>&1 || fail "run this script inside the Flowmap Git repository"

  local worktree_status
  worktree_status="$(git status --porcelain --untracked-files=normal)"
  [[ -z "$worktree_status" ]] || fail "working tree must be clean; commit or stash changes first"

  local current_branch
  current_branch="$(git branch --show-current)"
  [[ "$current_branch" == "$expected_branch" ]] || fail "release must run from ${expected_branch}, not ${current_branch:-detached HEAD}"

  git remote get-url "$release_remote" >/dev/null 2>&1 || fail "remote ${release_remote} is not configured"
  echo "Fetching ${release_remote}/${expected_branch}..."
  git fetch --quiet "$release_remote" "$expected_branch"

  local local_head remote_head
  local_head="$(git rev-parse HEAD)"
  remote_head="$(git rev-parse "refs/remotes/${release_remote}/${expected_branch}")"
  [[ "$local_head" == "$remote_head" ]] || fail "local ${expected_branch} must exactly match ${release_remote}/${expected_branch}"
}

# require_unused_tag prevents overwriting a local or published release tag.
require_unused_tag() {
  if git show-ref --verify --quiet "refs/tags/${release_tag}"; then
    fail "tag ${release_tag} already exists locally"
  fi

  set +e
  git ls-remote --exit-code --tags "$release_remote" "refs/tags/${release_tag}" >/dev/null 2>&1
  local remote_tag_status=$?
  set -e
  case "$remote_tag_status" in
    0) fail "tag ${release_tag} already exists on ${release_remote}" ;;
    2) ;;
    *) fail "could not query tags from ${release_remote}" ;;
  esac
}

# publish_release builds artifacts before creating and pushing the annotated tag.
publish_release() {
  echo "Building ${release_tag}..."
  make release VERSION="$version_number"

  echo "Creating annotated tag ${release_tag}..."
  git tag -a "$release_tag" -m "Flowmap ${release_tag}"

  echo "Pushing ${release_tag} to ${release_remote}..."
  if ! git push "$release_remote" "refs/tags/${release_tag}"; then
    fail "tag push failed; local tag ${release_tag} was retained for inspection"
  fi

  echo "Release tag ${release_tag} pushed. GitHub Actions will publish the release assets."
}

# main validates arguments and runs the guarded release sequence.
main() {
  if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
    print_usage
    return 0
  fi
  [[ $# -eq 1 ]] || { print_usage >&2; exit 2; }

  normalize_version "$1"
  require_clean_main
  require_unused_tag
  publish_release
}

main "$@"
