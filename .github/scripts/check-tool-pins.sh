#!/usr/bin/env bash
# Compares the tool versions pinned in the workflow `env:` blocks with the
# latest stable GitHub release of each tool. Dependabot does not update these.
#
# Prints a Markdown list of stale pins on stdout, or nothing when every pin is
# current. Exits non-zero only on an error (a pin or a release it cannot read).
#
# Needs: gh (authenticated, e.g. GH_TOKEN), grep, awk.
# WORKFLOWS_DIR overrides the directory that holds ci.yml and release.yml.
set -euo pipefail

dir="${WORKFLOWS_DIR:-.github/workflows}"
ci="${dir}/ci.yml"
release="${dir}/release.yml"

# pin FILE NAME -> the value of `NAME:` in FILE's env block.
pin() {
  local value
  value="$(grep -E "^[[:space:]]+$2:" "$1" | head -n1 | awk '{print $2}')"
  if [[ -z "${value}" ]]; then
    echo "error: no $2 pin in $1" >&2
    return 1
  fi
  printf '%s' "${value}"
}

# latest OWNER/REPO -> tag of the release GitHub marks "latest" (never a
# draft or a pre-release).
latest() {
  gh release view --repo "$1" --json tagName --jq .tagName
}

stale=()

# check NAME PINNED OWNER/REPO [PREFIX]: PREFIX is put before the pin when the
# pin omits the tag's leading "v".
check() {
  local name="$1" pinned="$2" repo="$3" prefix="${4:-}" want
  want="$(latest "${repo}")"
  if [[ "${prefix}${pinned}" != "${want}" ]]; then
    stale+=("- \`${name}\`: pinned \`${pinned}\`, latest is \`${want}\` (https://github.com/${repo}/releases/tag/${want})")
  fi
}

# Assigned first: `set -e` does not see a failed $(...) inside a command's arguments.
helm_ci="$(pin "${ci}" HELM_VERSION)"
helm_release="$(pin "${release}" HELM_VERSION)"
unittest="$(pin "${ci}" HELM_UNITTEST_VERSION)"
golangci="$(pin "${ci}" GOLANGCI_LINT_VERSION)"
cosign="$(pin "${ci}" COSIGN_VERSION)"
if [[ "${helm_ci}" != "${helm_release}" ]]; then
  stale+=("- \`HELM_VERSION\` differs: \`${helm_ci}\` in ci.yml, \`${helm_release}\` in release.yml")
fi

check "HELM_VERSION (ci.yml)" "${helm_ci}" helm/helm
if [[ "${helm_release}" != "${helm_ci}" ]]; then
  check "HELM_VERSION (release.yml)" "${helm_release}" helm/helm
fi
check "HELM_UNITTEST_VERSION (update HELM_UNITTEST_SHA256 with it)" "${unittest}" helm-unittest/helm-unittest v
check GOLANGCI_LINT_VERSION "${golangci}" golangci/golangci-lint
check COSIGN_VERSION "${cosign}" sigstore/cosign

if ((${#stale[@]})); then
  printf '%s\n' "${stale[@]}"
fi
