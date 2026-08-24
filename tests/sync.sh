#!/usr/bin/env bash
# 自包含测试模块的源码同步脚本。
# 用法：cd tests && ./sync.sh
# 作用：把仓库根目录的非测试源码、依赖清单、embed 资源复制到本目录，
#       保证 tests/ 是一份可与根目录代码一致编译的完整副本（防止漂移）。
#       测试文件（*_test.go）由本目录自行维护，不会被覆盖。
set -euo pipefail

SELF="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$SELF/.." && pwd)"
cd "$SELF"

for f in "$ROOT"/*.go; do
  case "$(basename "$f")" in
    *_test.go) continue ;;
  esac
  cp "$f" .
done

cp "$ROOT"/go.mod "$ROOT"/go.sum .
cp "$ROOT"/dashboard.html .

mkdir -p assets
cp -r "$ROOT"/assets/* assets/
mkdir -p tools
cp -r "$ROOT"/tools/* tools/ 2>/dev/null || true

echo "[sync] 已同步 $(ls *.go | grep -vc _test) 个源码文件 + go.mod/go.sum + dashboard.html + assets/ + tools/"
