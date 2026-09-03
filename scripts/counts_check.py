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


def fmt_loc_k(n):
    return f"{n / 1000:.1f}k"


# 文档口径守护目标：每文档的关键数字声明，须与实际计数一致（防文档领先/落后于代码）。
# key 里含换行与空格，匹配时对空格不敏感（兼容加粗/间距格式）。
DOC_TARGETS = {
    "docs/README.md": lambda cur: [
        f"internal 包 **{cur['internal_packages']}**",
        f"全仓库 **{cur['total_packages']}**",
        f"MCP 工具 **{cur['mcp_tools']}**",
        f"HTTP 路由 **{cur['http_routes']}**",
        f"非 vendor LoC ≈ {fmt_loc_k(cur['non_vendor_loc'])}",
    ],
    "docs/01-开发者/架构与模块.md": lambda cur: [
        f"internal 包数：**{cur['internal_packages']}**",
        f"全仓库包数：**{cur['total_packages']}**",
        f"MCP 工具：**{cur['mcp_tools']}**",
        f"HTTP 路由：**{cur['http_routes']}**",
        f"http={cur['http_routes']}",
        f"非 vendor LoC ≈ {fmt_loc_k(cur['non_vendor_loc'])}",
    ],
}


def check_docs(cur):
    """校验文档关键数字与实际计数一致，任一缺失判定文档口径漂移。"""

    all_ok = True
    for rel, make_tokens in DOC_TARGETS.items():
        path = os.path.join(ROOT, rel)
        try:
            with open(path, encoding="utf-8") as f:
                flat = f.read().replace(" ", "").replace("\n", "")
        except OSError as e:
            print(f"DOC-DRIFT  {rel}: 无法读取 ({e})")
            all_ok = False
            continue
        for tok in make_tokens(cur):
            if tok.replace(" ", "") in flat:
                print(f"DOC-OK     {rel}: {tok}")
            else:
                print(f"DOC-DRIFT  {rel}: 缺少声明「{tok}」（文档口径落后于代码）")
                all_ok = False
    return all_ok


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

    # 文档口径守护：README 事实基线行关键数字与实际计数必须一致。
    ok = check_docs(cur) and ok

    if not ok:
        print("\n计数漂移断言失败：请核对代码变更并更新 scripts/counts_baseline.json（或同步文档声明）")
        sys.exit(1)
    print("\n计数漂移断言通过：全部结构性计数与文档口径一致")
    sys.exit(0)


if __name__ == "__main__":
    main()
