#!/usr/bin/env bash
# Scorecard's SAST check counts a merged commit as scanned only when its pull request's head
# commit carries a code-scanning check run. CodeQL runs here from GitHub's default setup, which
# analyses pull requests opened against the default branch; a stacked pull request retargeted by
# automatic_base_change_succeeded keeps a head no analysis ever covered. With a pull request
# number this checks that one head before it merges; with none it audits the window Scorecard
# samples (SAST_WINDOW commits of the default branch, default 30).
set -euo pipefail

window=${SAST_WINDOW:-30}
# The check-run apps Scorecard accepts, and the conclusions it counts (ossf/scorecard sast.go).
apps='["github-advanced-security","github-code-scanning","lgtm-com","sonarcloud"]'

# scanned <head sha>: names the accepted check runs on that commit, empty if there are none.
scanned() {
  gh api "repos/{owner}/{repo}/commits/$1/check-runs?per_page=100" |
    jq -r --argjson apps "$apps" '[.check_runs[]
      | select(.status == "completed" and (.conclusion == "success" or .conclusion == "neutral"))
      | select(IN(.app.slug; $apps[]))
      | .name] | unique | join(", ")'
}

if [ "$#" -gt 0 ]; then
  head=$(gh api "repos/{owner}/{repo}/pulls/$1" --jq .head.sha)
  names=$(scanned "$head")
  if [ -z "$names" ]; then
    echo "::error::pull request $1 head $head carries no code-scanning analysis: push a default-branch catch-up so default setup analyses the final head" >&2
    exit 1
  fi
  echo "pull request $1 head $head: $names"
  exit 0
fi

branch=$(gh api "repos/{owner}/{repo}" --jq .default_branch)
mapfile -t commits < <(gh api "repos/{owner}/{repo}/commits?sha=$branch&per_page=$window" --jq '.[].sha')
declare -A names_of
gaps=()
merged=0
covered=0
for sha in "${commits[@]}"; do
  pair=$(gh api "repos/{owner}/{repo}/commits/$sha/pulls" \
    --jq 'map(select(.merged_at != null)) | if length > 0 then "\(.[0].number) \(.[0].head.sha)" else "" end')
  [ -n "$pair" ] || continue # Scorecard scores merged pull requests only
  merged=$((merged + 1))
  head=${pair#* }
  [ -n "${names_of[$head]+set}" ] || names_of[$head]=$(scanned "$head")
  if [ -n "${names_of[$head]}" ]; then
    covered=$((covered + 1))
  else
    gaps+=("$sha from pull request ${pair% *}, head $head")
  fi
done

echo "$covered of $merged merged commits in the last ${#commits[@]} are checked with a SAST tool"
if [ "${#gaps[@]}" -gt 0 ]; then
  echo "::error::these merged commits have no code-scanning analysis on their pull request head:" >&2
  printf '  %s\n' "${gaps[@]}" >&2
  exit 1
fi
