#!/usr/bin/env bash
# 通用「contrib 资产独立发布验证」脚本：验证某个 contrib 子包可独立发布
# （仅标准库依赖、独立 module 可编译可测试）。
#
# 用法：
#   bash scripts/check-contrib-publish.sh <src_dir> <module_name>
#     src_dir      contrib 下的子包目录名（如 contextsdk / adapterx）
#     module_name  独立发布用的 module 路径（如 github.com/idcu/codeschema-contextsdk）
#
# 原理：把 contrib/<src_dir> 复制到系统临时目录，go mod init 独立 module，
# go build 全包编译。若仅依赖标准库，无需任何外部依赖即可通过。
#
# 红线：发布前必须通过本脚本（没有验证就是没做）。

set -euo pipefail

if [ "$#" -ne 2 ]; then
  echo "用法: $0 <src_dir> <module_name>" >&2
  exit 2
fi

SRC_DIR="$1"
MODULE_NAME="$2"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SRC="$ROOT/contrib/$SRC_DIR"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

if [ ! -d "$SRC" ]; then
  echo "错误: $SRC 不存在" >&2
  exit 2
fi

echo "==> 复制 contrib/$SRC_DIR 到独立目录"
cp -R "$SRC"/. "$TMP"/

echo "==> go mod init $MODULE_NAME"
(cd "$TMP" && go mod init "$MODULE_NAME" >/dev/null 2>&1)

echo "==> go vet（静态检查）"
(cd "$TMP" && go vet ./...)

echo "==> go build ./...（独立编译，应无任何外部依赖）"
(cd "$TMP" && go build ./...)

echo "==> go test ./...（独立测试）"
(cd "$TMP" && go test ./...)

echo ""
echo "OK: ${SRC_DIR} 独立发布验证通过（仅标准库，可发布 ${MODULE_NAME}）"
