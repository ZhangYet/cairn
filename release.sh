#!/usr/bin/env bash
set -euo pipefail

NEW_TAG="${1:-}"

if [ -z "$NEW_TAG" ]; then
  echo "❌ Usage: ./scripts/release.sh <new_tag>"
  exit 1
fi

# ===== 基础校验 =====
if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  echo "❌ Not a git repository"
  exit 1
fi

if git rev-parse "$NEW_TAG" >/dev/null 2>&1; then
  echo "❌ Tag already exists: $NEW_TAG"
  exit 1
fi

CURRENT_BRANCH=$(git branch --show-current)
echo "Current branch: $CURRENT_BRANCH"

# 可选：限制必须在 master 分支
if [ "$CURRENT_BRANCH" != "master" ]; then
  echo "⚠️ Warning: not on master branch"
fi

# ===== 获取上一个 tag =====
LAST_TAG=$(git describe --tags --abbrev=0 2>/dev/null || echo "")

if [ -z "$LAST_TAG" ]; then
  echo "⚠️ No previous tag found, using full history"
  RANGE=""
else
  RANGE="$LAST_TAG..HEAD"
fi

echo "Last tag: ${LAST_TAG:-<none>}"
echo "New tag: $NEW_TAG"

# ===== 获取 commit =====
COMMITS=$(git log $RANGE --pretty=format:"%s")

if [ -z "$COMMITS" ]; then
  echo "⚠️ No new commits"
fi

# ===== 分类 changelog =====
FEATURES=""
FIXES=""
OTHERS=""

while IFS= read -r line; do
  if [[ "$line" =~ ^feat(\(.+\))?: ]]; then
    FEATURES+="- ${line#*: }\n"
  elif [[ "$line" =~ ^fix(\(.+\))?: ]]; then
    FIXES+="- ${line#*: }\n"
  else
    OTHERS+="- $line\n"
  fi
done <<< "$COMMITS"

DATE=$(date +%Y-%m-%d)

CHANGELOG_CONTENT="## $NEW_TAG - $DATE\n"

if [ -n "$FEATURES" ]; then
  CHANGELOG_CONTENT+="\n### Features\n$FEATURES"
fi

if [ -n "$FIXES" ]; then
  CHANGELOG_CONTENT+="\n### Fixes\n$FIXES"
fi

if [ -n "$OTHERS" ]; then
  CHANGELOG_CONTENT+="\n### Others\n$OTHERS"
fi

CHANGELOG_CONTENT+="\n"

# ===== 写入 ChangeLog.md =====
if [ ! -f ChangeLog.md ]; then
  touch ChangeLog.md
fi

# 插入到文件顶部（而不是 append）
TMP_FILE=$(mktemp)
echo -e "$CHANGELOG_CONTENT" > "$TMP_FILE"
cat ChangeLog.md >> "$TMP_FILE"
mv "$TMP_FILE" ChangeLog.md

echo "✅ ChangeLog.md updated"

# ===== 提交 =====
git add ChangeLog.md
git commit -m "chore(release): $NEW_TAG"

# ===== 打 tag =====
git tag -a "$NEW_TAG" -m "Release $NEW_TAG"

# ===== 推送 =====
git push origin "$CURRENT_BRANCH"
git push origin "$NEW_TAG"

echo "🚀 Release $NEW_TAG completed"
