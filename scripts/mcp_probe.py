#!/usr/bin/env python3
"""MCP 协议级探针：直接对 POST /message 走 JSON-RPC，验证 initialize 握手 +
tools/list + tools/call 在 41 租户多租户场景是否成立。纯 stdlib，无第三方依赖。"""
import json
import os
import sys
import urllib.request
import urllib.error

BASE = os.environ.get("MCP_BASE", "http://localhost:18080")


def rpc(method, params=None, rid=1, notify=False):
    body = {"jsonrpc": "2.0", "method": method}
    if not notify:
        body["id"] = rid
    if params is not None:
        body["params"] = params
    req = urllib.request.Request(
        BASE + "/message",
        data=json.dumps(body).encode(),
        headers={"Content-Type": "application/json"},
    )
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            raw = resp.read().decode()
    except urllib.error.HTTPError as e:
        return {"__http_error__": e.code, "body": e.read().decode()[:300]}
    return json.loads(raw) if raw else None


def main():
    print("=== 1) initialize 握手 ===")
    r = rpc("initialize", {
        "protocolVersion": "2024-11-05",
        "capabilities": {},
        "clientInfo": {"name": "probe", "version": "1.0"},
    }, rid=1)
    print(json.dumps(r, ensure_ascii=False)[:600])

    print("\n=== 2) notifications/initialized ===")
    r = rpc("notifications/initialized", {}, notify=True)
    print("(notification, server 通常不返回)", r)

    print("\n=== 3) tools/list ===")
    r = rpc("tools/list", {}, rid=2)
    if r and "result" in r:
        names = [t["name"] for t in r["result"].get("tools", [])]
        print("tools:", names)
    else:
        print(json.dumps(r, ensure_ascii=False)[:600])

    print("\n=== 4) tools/call search_symbols (project=config, q=Watch) ===")
    r = rpc("tools/call", {
        "name": "search_symbols",
        "arguments": {"q": "Watch", "project": "config", "limit": 3},
    }, rid=3)
    print(json.dumps(r, ensure_ascii=False)[:900])

    # 若 search 返回了符号，进一步用 context 取真实代码上下文
    sym = None
    fqn = None
    try:
        if r and r.get("result", {}).get("content"):
            txt = r["result"]["content"][0].get("text", "")
            sj = json.loads(txt)
            if sj.get("results"):
                sym = sj["results"][0].get("symbol")
                fqn = sj["results"][0].get("fqn")
                print("\n[search 首条] symbol=%r fqn=%r" % (sym, fqn))
    except Exception as e:
        print("(parse symbol failed:", e, ")")

    if sym:
        print(f"\n=== 5) tools/call context (project=config, symbol={sym!r}) ===")
        r2 = rpc("tools/call", {
            "name": "context",
            "arguments": {"symbol": sym, "project": "config", "mode": "minimal"},
        }, rid=4)
        print(json.dumps(r2, ensure_ascii=False)[:600])

    if fqn:
        print(f"\n=== 6) tools/call context (project=config, fqn={fqn!r}) ===")
        r3 = rpc("tools/call", {
            "name": "context",
            "arguments": {"symbol": fqn, "project": "config", "mode": "minimal"},
        }, rid=5)
        print(json.dumps(r3, ensure_ascii=False)[:600])
        ok = bool(r3) and "result" in r3 and "error" not in r3
        print("\n[CHAIN search→context via fqn]:", "OK" if ok else "FAIL")


if __name__ == "__main__":
    main()
