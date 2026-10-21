#!/usr/bin/env bash
# Attempt to port upstream commits into this repository.
#
# The tracked upstreams share no history with this repository, so a merge cannot
# succeed. Each changed file is applied independently with `git apply --3way`
# inside a throwaway worktree; clean applies go into a draft pull request and
# everything else is reported to an issue carrying the upstream-sync label.
set -euo pipefail

STATE_FILE="project-context/upstream-state.json"
REPORT_DIR="project-context/upstream-sync"
LABEL="upstream-sync"
DRY_RUN="${DRY_RUN:-false}"
FILTER="${UPSTREAM_FILTER:-all}"
ARTIFACT_DIR="${ARTIFACT_DIR:-}"
# Overridable so a local run can use SSH when HTTPS to GitHub is blocked.
UPSTREAM_BASE="${UPSTREAM_BASE:-https://github.com}"

upstream_url() { # repo
  case "$UPSTREAM_BASE" in
    *:) printf '%s%s.git' "$UPSTREAM_BASE" "$1" ;;
    *) printf '%s/%s.git' "${UPSTREAM_BASE%/}" "$1" ;;
  esac
}

log() { printf '[upstream-sync] %s\n' "$*"; }

JQ=$(command -v jq || true)
PY=$(command -v python3 || command -v python || true)
if [ -z "$JQ" ] && [ -z "$PY" ]; then
  log "需要 jq 或 python 才能读写状态文件"
  exit 1
fi

state_value() { # name field
  if [ -n "$JQ" ]; then
    "$JQ" -r ".upstreams[\"$1\"][\"$2\"] // \"\"" "$STATE_FILE"
    return
  fi
  "$PY" - "$STATE_FILE" "$1" "$2" <<'PY'
import json, sys
path, name, field = sys.argv[1:4]
value = json.load(open(path, encoding="utf-8"))["upstreams"][name].get(field, "")
print("" if value is None else value)
PY
}

set_state() { # name field value
  local tmp
  tmp=$(mktemp)
  if [ -n "$JQ" ]; then
    "$JQ" --arg n "$1" --arg f "$2" --arg v "$3" '.upstreams[$n][$f] = $v' "$STATE_FILE" >"$tmp"
  else
    "$PY" - "$STATE_FILE" "$tmp" "$1" "$2" "$3" <<'PY'
import json, sys
source, target, name, field, value = sys.argv[1:6]
data = json.load(open(source, encoding="utf-8"))
data["upstreams"][name][field] = value
json.dump(data, open(target, "w", encoding="utf-8"), indent=2, ensure_ascii=False)
open(target, "a", encoding="utf-8").write("\n")
PY
  fi
  mv "$tmp" "$STATE_FILE"
}

set_state_number() { # name field number
  local tmp
  tmp=$(mktemp)
  if [ -n "$JQ" ]; then
    "$JQ" --arg n "$1" --arg f "$2" --argjson v "$3" '.upstreams[$n][$f] = $v' "$STATE_FILE" >"$tmp"
  else
    "$PY" - "$STATE_FILE" "$tmp" "$1" "$2" "$3" <<'PY'
import json, sys
source, target, name, field, value = sys.argv[1:6]
data = json.load(open(source, encoding="utf-8"))
data["upstreams"][name][field] = int(value)
json.dump(data, open(target, "w", encoding="utf-8"), indent=2, ensure_ascii=False)
open(target, "a", encoding="utf-8").write("\n")
PY
  fi
  mv "$tmp" "$STATE_FILE"
}

# commit_state writes the state file on main. It refuses to run from any other
# branch so a sync branch can never be pushed onto main by accident.
commit_state() { # message
  local branch
  branch=$(git rev-parse --abbrev-ref HEAD)
  if [ "$branch" != "main" ]; then
    log "refusing to commit state from branch $branch"
    return 1
  fi
  if [ "$DRY_RUN" = true ]; then
    return 0
  fi
  git add "$STATE_FILE"
  if git diff --cached --quiet; then
    return 0
  fi
  git commit -q -m "$1"
  git pull -q --rebase origin main
  git push -q origin HEAD:main
}

semver_bump() { # previous latest -> patch|minor
  local previous="${1#v}" latest="${2#v}"
  if [ -z "$previous" ] || [ -z "$latest" ]; then
    echo patch
    return 0
  fi
  local previous_major previous_minor latest_major latest_minor
  IFS=. read -r previous_major previous_minor _ <<<"$previous" || true
  IFS=. read -r latest_major latest_minor _ <<<"$latest" || true
  if [ "${latest_major:-0}" != "${previous_major:-0}" ] || [ "${latest_minor:-0}" != "${previous_minor:-0}" ]; then
    echo minor
    return 0
  fi
  echo patch
}

is_binary_diff() { # sha path
  git diff --numstat "$1^" "$1" -- "$2" | awk '{print $1}' | grep -qx -- '-'
}

# Paths this repository owns. A mechanical patch must never rewrite the CI
# pipeline, the licensing files or the sync state.
protected_path() { # path
  case "$1" in
    .github/* | project-context/* | AGENTS.md | CLAUDE.md | LICENSE* | COPYING* | NOTICE |     go.mod | go.sum | Dockerfile | compose.yaml | .gitignore | .gitattributes)
      return 0
      ;;
  esac
  return 1
}

# apply_one applies the diff for one upstream file in place. The module root is
# the repository root, so upstream paths need no rewriting.
apply_one() { # worktree sha path
  local worktree="$1" sha="$2" path="$3"
  (cd "$worktree" && git diff --no-renames "$sha^" "$sha" -- "$path" |
    git apply --3way --whitespace=nowarn --index 2>/dev/null)
}

has_feature_commit() { # range
  local subjects
  subjects=$(git log --format=%s "$1")
  grep -qE '^feat(\(|:|!)' <<<"$subjects"
}

find_open_issue() { # exact title
  gh issue list --state open --label "$LABEL" --limit 100 --json number,title \
    --jq ".[] | select(.title == \"$1\") | .number" | head -1
}

upsert_issue() { # title body-file
  local title="$1" body_file="$2" number url
  number=$(find_open_issue "$title")
  if [ -n "$number" ]; then
    gh issue comment "$number" --body-file "$body_file" >/dev/null
    echo "$number"
    return 0
  fi
  url=$(gh issue create --title "$title" --label "$LABEL" --body-file "$body_file")
  echo "${url##*/}"
}

open_sync_pr() { # name from to bump blocked report
  local name="$1" from="$2" to="$3" bump="$4" blocked="$5" report="$6"
  local branch="sync/$name-${to:0:8}"
  local body
  body=$(mktemp)
  {
    printf '<!-- upstream-sync: %s %s %s %s blocked=%s -->\n\n' "$name" "$from" "$to" "$bump" "$blocked"
    cat "$report"
  } >"$body"
  if [ "$DRY_RUN" = true ]; then
    log "dry run: would open a draft PR from $branch"
    cp "$body" "$ARTIFACT_DIR/pr-$name.md" 2>/dev/null || true
    rm -f "$body"
    return 0
  fi
  # The sync branch is owned by this workflow: a rerun after a failed run must
  # be able to replace it.
  git push -q --force -u origin "$branch"
  gh pr create --draft --base main --head "$branch" \
    --title "chore(sync): $name ${from:0:8}..${to:0:8}" \
    --body-file "$body" --label "$LABEL" --label "sync:$bump" >/dev/null
  rm -f "$body"
}

reconcile_checks() { # name
  local number buckets body
  while read -r number; do
    [ -z "$number" ] && continue
    buckets=$(gh pr checks "$number" --json bucket --jq '[.[] | select(.bucket == "fail")] | length' 2>/dev/null || echo 0)
    if [ "${buckets:-0}" -gt 0 ]; then
      body=$(mktemp)
      printf '同步 PR #%s 的 Checks 失败。\n\n运行：https://github.com/%s/actions\n' "$number" "${GITHUB_REPOSITORY:-}" >"$body"
      upsert_issue "[sync] $1 同步 PR 检查失败" "$body" >/dev/null
      rm -f "$body"
    fi
  done < <(gh pr list --state open --label "$LABEL" --limit 100 --json number,body \
    --jq ".[] | select(.body | contains(\"<!-- upstream-sync: $1 \")) | .number")
}

sync_upstream() { # name
  local name="$1"
  local repo branch synced blocked_sha blocked_issue
  repo=$(state_value "$name" repo)
  branch=$(state_value "$name" branch)
  synced=$(state_value "$name" synced_sha)
  blocked_sha=$(state_value "$name" blocked_sha)
  blocked_issue=$(state_value "$name" blocked_issue)

  if [ -n "$blocked_sha" ] && [ "$blocked_issue" != "0" ] && [ "$blocked_issue" != "" ]; then
    if gh issue view "$blocked_issue" --json state --jq .state 2>/dev/null | grep -q OPEN; then
      log "$name: waiting on issue #$blocked_issue"
      return 0
    fi
    log "$name: issue #$blocked_issue closed, advancing to ${blocked_sha:0:8}"
    if [ "$DRY_RUN" != true ]; then
      set_state "$name" synced_sha "$blocked_sha"
      set_state "$name" blocked_sha ""
      set_state_number "$name" blocked_issue 0
      commit_state "chore(sync): $name 同步状态推进到 ${blocked_sha:0:8}"
    fi
    synced="$blocked_sha"
  fi

  local open_pr
  open_pr=$(gh pr list --state open --label "$LABEL" --limit 100 --json number,body \
    --jq "[.[] | select(.body | contains(\"<!-- upstream-sync: $name \"))] | .[0].number // empty")
  if [ -n "$open_pr" ]; then
    log "$name: draft PR #$open_pr still open"
    reconcile_checks "$name"
    return 0
  fi

  log "$name: fetching $repo@$branch"
  git fetch -q --no-tags "$(upstream_url "$repo")" "$branch"
  local to
  to=$(git rev-parse FETCH_HEAD)

  local previous_release latest_release
  previous_release=$(state_value "$name" last_release)
  latest_release=$(gh release view -R "$repo" --json tagName --jq .tagName 2>/dev/null || true)

  if [ "$to" = "$synced" ]; then
    log "$name: no new commits"
    if [ -n "$latest_release" ] && [ "$latest_release" != "$previous_release" ] && [ "$DRY_RUN" != true ]; then
      set_state "$name" last_release "$latest_release"
      commit_state "chore(sync): 记录 $name 上游版本 $latest_release"
    fi
    return 0
  fi

  if ! git merge-base --is-ancestor "$synced" "$to"; then
    log "$name: upstream history was rewritten; baseline needs review"
    local body
    body=$(mktemp)
    printf '上游 `%s` 的历史已重写，`%s` 不再是 `%s` 的祖先。请人工确认新基线后更新 `%s`。\n' \
      "$repo" "${synced:0:8}" "${to:0:8}" "$STATE_FILE" >"$body"
    if [ "$DRY_RUN" = true ]; then
      cp "$body" "$ARTIFACT_DIR/issue-$name-rewrite.md" 2>/dev/null || true
    else
      upsert_issue "[sync] $name 上游历史被重写" "$body" >/dev/null
    fi
    rm -f "$body"
    return 0
  fi

  local commit_count
  commit_count=$(git rev-list --count "$synced..$to")
  log "$name: $commit_count new commits ${synced:0:8}..${to:0:8}"

  local bump=patch
  if [ -n "$latest_release" ] && [ "$latest_release" != "$previous_release" ]; then
    bump=$(semver_bump "$previous_release" "$latest_release")
  fi
  if has_feature_commit "$synced..$to"; then
    bump=minor
  fi

  local branch_name="sync/$name-${to:0:8}"
  local worktree
  worktree="$(mktemp -d)/worktree"
  git worktree add -q -B "$branch_name" "$worktree" main

  local applied=() conflicted=() missing=()
  local sha path status
  while IFS= read -r sha; do
    [ -z "$sha" ] && continue
    while IFS=$'\t' read -r status path; do
      [ -z "${path:-}" ] && continue
      local mapped="$path"
      if protected_path "$mapped"; then
        case " ${conflicted[*]} " in
          *" $mapped "*) ;;
          *) conflicted+=("$mapped (受保护，需人工)") ;;
        esac
        continue
      fi
      if is_binary_diff "$sha" "$path"; then
        case " ${conflicted[*]} " in
          *" $mapped "*) ;;
          *) conflicted+=("$mapped (二进制)") ;;
        esac
        continue
      fi
      # Only files this repository already tracks are patched in place. A new
      # upstream file belongs to a codebase that does not exist here, so it is
      # reported for a manual port instead of being dropped in as dead code.
      if ! git cat-file -e "main:$mapped" 2>/dev/null; then
        case " ${missing[*]} " in
          *" $mapped "*) ;;
          *) missing+=("$mapped (新增文件，需人工)") ;;
        esac
        continue
      fi
      if apply_one "$worktree" "$sha" "$path"; then
        case " ${applied[*]} " in
          *" $mapped "*) ;;
          *) applied+=("$mapped") ;;
        esac
      else
        case " ${conflicted[*]} " in
          *" $mapped "*) ;;
          *) conflicted+=("$mapped") ;;
        esac
      fi
    done < <(git diff-tree --no-commit-id --name-status -r --no-renames "$sha" | awk -F'\t' '{print $1"\t"$2}')
  done < <(git log --reverse --format=%H "$synced..$to")

  mkdir -p "$worktree/$REPORT_DIR"
  local report="$worktree/$REPORT_DIR/$name-${synced:0:8}-${to:0:8}.md"
  {
    printf '# 上游同步报告：%s %s..%s\n\n' "$name" "${synced:0:8}" "${to:0:8}"
    printf -- '- 上游：`%s@%s`\n- 提交数：%s\n- 建议版本：%s\n\n' "$repo" "$branch" "$commit_count" "$bump"
    printf '## 提交\n\n```text\n'
    git log --format='%h %s' "$synced..$to"
    printf '```\n\n## 自动应用（%s）\n\n' "${#applied[@]}"
    printf '%s\n' "${applied[@]:-无}"
    printf '\n## 冲突需人工重写（%s）\n\n' "${#conflicted[@]}"
    printf '%s\n' "${conflicted[@]:-无}"
    printf '\n## 缺基线（%s）\n\n' "${#missing[@]}"
    printf '%s\n\n' "${missing[@]:-无}"
    printf '## 检查清单\n\n- [ ] 冲突文件已按本仓库架构重写\n- [ ] 新增第三方文件保留原许可证头并登记到 `NOTICE`\n- [ ] `go build ./...`、`go test ./...`、`npm run build` 通过\n'
  } >"$report"

  local blocked=0
  if [ "${#conflicted[@]}" -gt 0 ] || [ "${#missing[@]}" -gt 0 ]; then
    blocked=1
  fi

  if [ "${#applied[@]}" -gt 0 ]; then
    local build_status="未运行" build_output=""
    if [ "$DRY_RUN" != true ]; then
      # The sync PR is opened with GITHUB_TOKEN, so its pull_request event does
      # not start Checks. Compile here to keep the "broken patch" signal.
      if (cd "$worktree" && go build ./... >"$worktree/build.log" 2>&1); then
        build_status="通过"
      else
        build_status="失败"
        build_output=$(tail -40 "$worktree/build.log")
      fi
      {
        printf '\n## 构建验证\n\n- `go build ./...`：%s\n' "$build_status"
        if [ -n "$build_output" ]; then
          printf '\n```text\n%s\n```\n' "$build_output"
        fi
      } >>"$report"
      (
        cd "$worktree"
        rm -f build.log
        git add -A
        git commit -q -m "chore(sync): $name ${synced:0:8}..${to:0:8}"
      )
    fi
    open_sync_pr "$name" "$synced" "$to" "$bump" "$blocked" "$report"
    if [ "$build_status" = "失败" ]; then
      local build_body
      build_body=$(mktemp)
      {
        printf '自动移植后的代码无法通过 `go build ./...`。草稿 PR：%s\n\n```text\n%s\n```\n' \
          "sync/$name-${to:0:8}" "$build_output"
      } >"$build_body"
      if [ "$DRY_RUN" = true ]; then
        cp "$build_body" "$ARTIFACT_DIR/build-$name.md" 2>/dev/null || true
      else
        upsert_issue "[sync] $name 同步 PR 检查失败" "$build_body" >/dev/null
      fi
      rm -f "$build_body"
    fi
  else
    log "$name: nothing applied cleanly"
    blocked=1
  fi

  if [ "$blocked" = "1" ]; then
    local issue_body
    issue_body=$(mktemp)
    {
      printf '上游 `%s` 的区间 `%s..%s` 有 %s 个文件无法自动应用，需要按本仓库架构重写。\n\n' \
        "$repo" "${synced:0:8}" "${to:0:8}" "$(( ${#conflicted[@]} + ${#missing[@]} ))"
      cat "$report"
      printf '\n关闭本 Issue 视为该区间已人工处理：下一次同步会把 `synced_sha` 推进到 `%s`。\n' "$to"
    } >"$issue_body"
    if [ "$DRY_RUN" = true ]; then
      log "dry run: would open a conflict issue for $name"
      cp "$issue_body" "$ARTIFACT_DIR/issue-$name.md" 2>/dev/null || true
    else
      local issue_number
      issue_number=$(upsert_issue "[sync] $name 同步冲突" "$issue_body")
      set_state "$name" blocked_sha "$to"
      set_state_number "$name" blocked_issue "$issue_number"
      commit_state "chore(sync): 记录 $name 冲突区间 ${to:0:8}"
    fi
    rm -f "$issue_body"
  fi

  if [ "$DRY_RUN" = true ]; then
    cp "$report" "$ARTIFACT_DIR/" 2>/dev/null || true
  fi

  git worktree remove -f "$worktree" >/dev/null 2>&1 || true
  if [ "$DRY_RUN" != true ] && [ "${#applied[@]}" -eq 0 ]; then
    git branch -D "$branch_name" >/dev/null 2>&1 || true
  fi
}

main() {
  if [ "$DRY_RUN" = true ]; then
    mkdir -p "$ARTIFACT_DIR"
  else
    gh label create "$LABEL" --color FBCA04 --description "上游同步与兼容性通知" --force >/dev/null 2>&1 || true
    gh label create "sync:patch" --color 0E8A16 --description "同步合入后升 PATCH" --force >/dev/null 2>&1 || true
    gh label create "sync:minor" --color 1D76DB --description "同步合入后升 MINOR" --force >/dev/null 2>&1 || true
  fi

  local name
  for name in opencode2api resin; do
    if [ "$FILTER" != "all" ] && [ "$FILTER" != "$name" ]; then
      continue
    fi
    sync_upstream "$name"
    if [ "$DRY_RUN" != true ]; then
      set_state "$name" last_checked "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    fi
  done

  if [ "$DRY_RUN" != true ]; then
    commit_state "chore(sync): 记录检查时间"
  fi
  log "done"
}

main "$@"
