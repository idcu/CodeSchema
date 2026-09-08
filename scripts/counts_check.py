#!/usr/bin/env python3
# 计数漂移断言（P3#10 的 CI 闭环）：比对 scripts/project_counts.py 实时输出
# 与 scripts/counts_baseline.json 基线，任一结构性计数（包数 / MCP 工具数 /
# HTTP 路由数）不一致即判定为「数字漂移」，退出码 1 使 CI 失败。
#
# 设计意图：文档口径不再手填，凡包数、工具数、路由数变化必须同步更新基线，
# 否则 CI 红灯——把「27/31/32/36 四处包数不一」这类漂移在提交时即拦截。
#
# 2026-09-03 扩容：① DOC_TARGETS 由 README + 架构与模块 扩至 AGENTS.md +
# 仓库结构.md，覆盖更多含硬数字的必读档；② 新增 dead-link 校验，扫描 docs/ 与
# 根 README/AGENTS 的相对 markdown 链接是否指向存在的文件，拦截死链（如曾出现
# 的 docs/dev/13-多租户设计文档.md 死链）。
#
# 用法：python3 scripts/counts_check.py  （或 make counts-check）

import json
import os
import re
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
    # AGENTS.md 是 AI 工具必读档，其 MCP/HTTP 计数错误影响最大；guard 此前漏检
    # 「HTTP 端点（14+）」漂移（现 23）。
    "AGENTS.md": lambda cur: [
        f"MCP 工具（{cur['mcp_tools']} 个）",
        f"HTTP 端点（{cur['http_routes']}）",
    ],
    # 仓库结构.md 的 internal 包数 / 计数基线同样易手写漂移（曾 32→应 33）。
    "docs/仓库结构.md": lambda cur: [
        f"共 {cur['internal_packages']} 个 internal Go 包",
        f"计数基线（{cur['internal_packages']} / {cur['total_packages']} / {cur['mcp_tools']} / {cur['http_routes']}）",
    ],
}


# 需排除的非相对链接目标前缀（http/https/mailto/锚点/绝对路径/自动生成）
_LINK_EXCLUDE = (
    "http://", "https://", "mailto:", "#", "/", "\\",
    "file:", "tel:", "ftp://", "www.",
)
# 明确跳过死链校验的伪链接目标（用于不存在的路径示意、占位等）
_LINK_IGNORE = {
    "docs/dev/",  # 历史归档目录，保留占位说明
    "docs/13-",   # 旧编号文档占位
    "git history",  # 非文件链接的指代
}


def _resolve_md_links(root_dir, rel):
    """扫描单个 .md 文件，返回其指向不存在的相对链接列表 [(target, resolved)]。"""
    path = os.path.join(root_dir, rel)
    try:
        with open(path, encoding="utf-8", errors="ignore") as f:
            text = f.read()
    except OSError:
        return []
    base_dir = os.path.dirname(path)
    broken = []
    for m in re.finditer(r"\[[^\]]*\]\(([^)]+)\)", text):
        target = m.group(1).strip()
        if not target:
            continue
        # 去掉内联 title 语法 [x](url "title") / url <#anchor>
        target = re.sub(r'\s+["\'].*$', "", target)
        if any(target.startswith(p) for p in _LINK_EXCLUDE):
            continue
        if any(target.startswith(p) for p in _LINK_IGNORE):
            continue
        # 去掉 URL fragment（锚点）后再判文件存在性
        no_anchor = target.split("#")[0]
        if not no_anchor:
            continue
        resolved = os.path.normpath(os.path.join(base_dir, no_anchor))
        if not os.path.exists(resolved):
            broken.append((target, resolved))
    return broken


def check_dead_links(cur):
    """校验 docs/ 及根 README/AGENTS 的相对 markdown 链接无死链。"""
    ok = True
    # 相对路径锚点：以 {ROOT} 为基准
    rel_targets = ["docs/", "AGENTS.md", "README.md"]
    for rel in rel_targets:
        base = os.path.join(ROOT, rel)
        if os.path.isdir(base):
            files = [os.path.join(dp, fn) for dp, _, fns in os.walk(base) for fn in fns if fn.endswith(".md")]
            for p in files:
                r = os.path.relpath(p, ROOT)
                for target, resolved in _resolve_md_links(ROOT, r):
                    print(f"LINK-DRIFT  {r}: 死链 [{target}] -> {os.path.relpath(resolved, ROOT)}（文件不存在）")
                    ok = False
        else:
            for target, resolved in _resolve_md_links(ROOT, rel):
                print(f"LINK-DRIFT  {rel}: 死链 [{target}] -> {os.path.relpath(resolved, ROOT)}（文件不存在）")
                ok = False
    if ok:
        print("LINK-OK   全部相对 markdown 链接可解析（无死链）")
    return ok


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
    # 死链守护：docs/ 与根 README/AGENTS 的相对链接可解析。
    ok = check_dead_links(cur) and ok

    if not ok:
        print("\n计数漂移断言失败：请核对代码变更并更新 scripts/counts_baseline.json（或同步文档声明/修复死链）")
        sys.exit(1)
    print("\n计数漂移断言通过：全部结构性计数、文档口径与链接均一致")
    sys.exit(0)


if __name__ == "__main__":
    main()