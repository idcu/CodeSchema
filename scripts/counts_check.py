#!/usr/bin/env python3
# 计数漂移断言（P3#10 的 CI 闭环）：比对 scripts/project_counts.py 实时输出
# 与 scripts/counts_baseline.json 基线，任一结构性计数（包数 / MCP 工具数 /
# HTTP 路由数）不一致即判定为「数字漂移」，退出码 1 使 CI 失败。
#
# 设计意图：文档口径不再手填，凡包数、工具数、路由数变化必须同步更新基线，
# 否则 CI 红灯——把「27/31/32/36 四处包数不一」这类漂移在提交时即拦截。
#
# 用法：python3 scripts/counts_check.py  （或 make counts-check）

import json
import os
import subprocess
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


def main():
    with open(os.path.join(ROOT, "scripts", "counts_baseline.json"), encoding="utf-8") as f:
        baseline = json.load(f)

    cur = json.loads(
        subprocess.run(
            [sys.executable, os.path.join(ROOT, "scripts", "project_counts.py"), "--json"],
            cwd=ROOT,
            capture_output=True,
            text=True,
        ).stdout
    )

    ok = True
    for key in baseline:
        b = baseline[key]
        c = cur.get(key)
        if c != b:
            ok = False
            print(f"DRIFT  {key}: baseline={b}  actual={c}")
        else:
            print(f"OK     {key}: {b}")

    if not ok:
        print("\n计数漂移断言失败：请核对代码变更并更新 scripts/counts_baseline.json")
        sys.exit(1)
    print("\n计数漂移断言通过：全部结构性计数与基线一致")
    sys.exit(0)


if __name__ == "__main__":
    main()
