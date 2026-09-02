#!/usr/bin/env python3
# CodeSchema 项目计数审计（P3#10：计数类字段脚本化，防人工漂移）
#
# 此前文档出现过「包数量 27/31/32/36 四处不一致」「MCP 工具数 / HTTP 路由数
# 口径不明」等问题。本脚本从权威来源（go list / 源码正则）重新计算核心计数，
# 输出供文档口径核对与 CI 校验，避免手工数字长期漂移。
#
# 用法：
#   python3 scripts/project_counts.py            # 人类可读
#   python3 scripts/project_counts.py --json    # JSON（供 CI / 快照比对）
#
# 注意：源码文件含中文注释，部分环境下 grep/ripgrep 误判为二进制；
#       本脚本用 Python 直接读文件（errors="ignore"），不受此影响。

import subprocess
import re
import sys
import os
import json
import glob

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


def run(cmd):
    return subprocess.run(cmd, cwd=ROOT, capture_output=True, text=True)


def go_list(scope):
    r = run(["go", "list", scope])
    if r.returncode != 0:
        sys.stderr.write(f"go list {scope} failed: {r.stderr}\n")
        return []
    return [l for l in r.stdout.splitlines() if l.strip()]


def count_loc():
    """非 vendor / 非 down / 非 build / 非 .git 的 .go 文件总行数。"""
    total = 0
    for dp, _, fns in os.walk(ROOT):
        if any(seg in dp for seg in ("/vendor/", "/down/", "/build/", "/.git")):
            continue
        for fn in fns:
            if fn.endswith(".go"):
                p = os.path.join(dp, fn)
                try:
                    with open(p, encoding="utf-8", errors="ignore") as fh:
                        total += sum(1 for _ in fh)
                except OSError:
                    pass
    return total


def count_mcp_tools():
    """mcp.go 中 mcpTool{ Name: "..." } 的工具定义数量。"""
    p = os.path.join(ROOT, "internal", "server", "mcp.go")
    if not os.path.exists(p):
        return 0
    with open(p, encoding="utf-8", errors="ignore") as fh:
        txt = fh.read()
    # 仅匹配 mcpTool 定义里的 Name 字段（小写开头的工具名）
    return len(re.findall(r'\bName:\s+"([a-z][a-zA-Z0-9_]*)"', txt))


def count_http_routes():
    """internal/server 下所有非 mcp / 非测试的 .go 文件中注册的路径字面量（去重）数量。

    覆盖 http.go（核心 API 路由）与 viz.go（向量可视化 /viz、/viz/api/* 等）。
    排除 mcp*.go（JSON-RPC 传输端点如 /sse、/message，非 HTTP API 路由）与
    *_test.go（测试桩，会引用大量路由字符串造成污染）。

    此前仅扫 http.go，漏计 viz.go 的 6 个 /viz 路由；基线 http_routes 由 16 升为 22。
    """
    total = set()
    for p in glob.glob(os.path.join(ROOT, "internal", "server", "*.go")):
        base = os.path.basename(p)
        if base.startswith("mcp") or base.endswith("_test.go"):
            continue
        with open(p, encoding="utf-8", errors="ignore") as fh:
            txt = fh.read()
        total |= set(re.findall(r'"(/[a-zA-Z0-9_/:{}.]+)"', txt))
    return len(total)


def main():
    data = {
        "internal_packages": len(go_list("./internal/...")),
        "total_packages": len(go_list("./...")),
        "non_vendor_loc": count_loc(),
        "mcp_tools": count_mcp_tools(),
        "http_routes": count_http_routes(),
    }
    if "--json" in sys.argv:
        print(json.dumps(data, indent=2, ensure_ascii=False))
    else:
        print("CodeSchema 项目计数审计")
        print(f"  internal 包数  : {data['internal_packages']}")
        print(f"  全仓库包数    : {data['total_packages']}")
        print(f"  非 vendor LoC : {data['non_vendor_loc']}")
        print(f"  MCP 工具数    : {data['mcp_tools']}")
        print(f"  HTTP 路由数   : {data['http_routes']}")
    return data


if __name__ == "__main__":
    main()
