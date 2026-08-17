#!/usr/bin/env bash
# 验证 contrib/adapterx 可独立发布（A 级生态资产，P2 前置）。
# 内部复用通用验证脚本 scripts/check-contrib-publish.sh。
set -euo pipefail
exec "$(cd "$(dirname "$0")" && pwd)/check-contrib-publish.sh" adapterx github.com/idcu/codeschema-adapterx
