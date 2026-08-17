#!/usr/bin/env bash
# 验证 contrib/contextsdk 可独立发布（P2 前置）：仅标准库依赖、独立 module 可编译。
#
# 用法：
#   bash scripts/check-contextsdk-publish.sh
#
# 原理：把 contrib/contextsdk 复制到系统临时目录，go mod init 独立 module，
# go build 全包编译。若仅依赖标准库，无需任何外部依赖即可通过。
#
# 红线：发布前必须通过本脚本（没有验证就是没做）。

set -euo pipefail

SRC="$(cd "$(dirname "$0")/.." && pwd)/contrib/contextsdk"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

echo "==> 复制 contrib/contextsdk 到独立目录"
cp -R "$SRC"/. "$TMP"/

echo "==> go mod init github.com/idcu/codeschema-contextsdk"
(cd "$TMP" && go mod init github.com/idcu/codeschema-contextsdk >/dev/null 2>&1)

echo "==> go vet（静态检查）"
(cd "$TMP" && go vet ./...)

echo "==> go build ./...（独立编译，应无任何外部依赖）"
(cd "$TMP" && go build ./...)

echo "==> go test ./...（独立测试）"
(cd "$TMP" && go test ./...)

echo ""
echo "OK: contextsdk 独立发布验证通过（仅标准库，可发布 github.com/idcu/codeschema-contextsdk）"
