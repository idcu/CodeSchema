#!/usr/bin/env python3
"""Generate CodeSchema multi-tenant config for the idcu-go monorepo.

idcu-go is a single-tree Go monorepo (go.work) with ~41 top-level Go modules
plus nested COPIES under devbox/workspace/modules/* (which also carry go.mod).
We only treat depth-1 modules as tenants; the scanner's recursive walk already
covers each module's internal sub-packages, so nested go.mod dirs must NOT
become separate tenants (that would double-index devbox copies).

Outputs:
  build/idcu-go-mt.local.yaml   root=/abs/idcu-go/<m>, dsn=<abs>/build/idcu-mt/<m>
  build/idcu-go-mt.docker.yaml  root=/repo/<m>,            dsn=/app/data/<m>
"""
import os
import sys

IDCU = "/Volumes/Data/idcu-go"
LOCAL_DSN = "/Volumes/Data/code-schema/build/idcu-mt"
CODESCHEMA_ROOT = "/Volumes/Data/code-schema"


def list_modules():
    mods = []
    for name in sorted(os.listdir(IDCU)):
        p = os.path.join(IDCU, name)
        if os.path.isdir(p) and os.path.isfile(os.path.join(p, "go.mod")):
            mods.append(name)
    return mods


def tenant_block(mid, root_base, dsn_base, indent="  "):
    root = f"{root_base}/{mid}"
    dsn = f"{dsn_base}/{mid}"
    return (
        f"{indent}- id: {mid}\n"
        f"{indent}  name: \"{mid}\"\n"
        f"{indent}  root: {root}\n"
        f"{indent}  storage:\n"
        f"{indent}    driver: sqlite\n"
        f"{indent}    dsn: {dsn}\n"
        f"{indent}  auto_scan: true\n"
        f"{indent}  watch: false\n"
    )


def gen(root_base, dsn_base, http_port, mcp_port):
    mods = list_modules()
    tenants = "".join(tenant_block(m, root_base, dsn_base) for m in mods)
    return (
        "server:\n"
        f"  http_addr: \":{http_port}\"\n"
        f"  mcp_addr: \":{mcp_port}\"\n"
        "project:\n"
        "  name: \"idcu-go\"\n"
        f"  root: {root_base}\n"
        "tenants:\n"
        f"{tenants}"
    )


def main():
    local = gen(IDCU, LOCAL_DSN, 18081, 18080)
    docker = gen("/repo", "/app/data", 8081, 8080)
    with open(os.path.join(CODESCHEMA_ROOT, "build", "idcu-go-mt.local.yaml"), "w") as f:
        f.write(local)
    with open(os.path.join(CODESCHEMA_ROOT, "build", "idcu-go-mt.docker.yaml"), "w") as f:
        f.write(docker)
    print(f"generated {len(list_modules())} tenant configs (local + docker)")


if __name__ == "__main__":
    main()
