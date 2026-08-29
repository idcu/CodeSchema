#!/bin/sh
# jdtls 包装脚本：Eclipse JDT Language Server 非单二进制，需以
# `java -jar org.eclipse.equinox.launcher_*.jar` 拉起。code-schema 的
# NewJDTLSAdapter 以命令名 "jdtls"（无参数）调用本脚本，由本脚本补全
# equinox 启动参数，并以 stdio 与 LSP 适配器通信。
#
# 约定：
#   - jdtls 解压于 /opt/jdtls（含 plugins/、config_linux/、features/）
#   - 每个实例需要独立 -data workspace 目录（用 mktemp 避免并发冲突）
set -e

JDTLS_HOME="${JDTLS_HOME:-/opt/jdtls}"
LAUNCHER=$(ls "$JDTLS_HOME"/plugins/org.eclipse.equinox.launcher_*.jar 2>/dev/null | head -1)
if [ -z "$LAUNCHER" ]; then
  echo "jdtls: launcher jar not found under $JDTLS_HOME/plugins" >&2
  exit 1
fi

WORKSPACE=$(mktemp -d "${TMPDIR:-/tmp}/jdtls-ws.XXXXXX")
trap 'rm -rf "$WORKSPACE"' EXIT

exec java \
  -Declipse.application=org.eclipse.jdt.ls.core.id1 \
  -Dosgi.bundles.defaultStartLevel=4 \
  -Declipse.product=org.eclipse.jdt.ls.core.product \
  -Dlog.level=ALL \
  -Xmx1G \
  --add-modules=ALL-SYSTEM \
  --add-opens java.base/java.util=ALL-UNNAMED \
  --add-opens java.base/java.lang=ALL-UNNAMED \
  -jar "$LAUNCHER" \
  -configuration "$JDTLS_HOME/config_linux" \
  -data "$WORKSPACE"
