#!/usr/bin/env python3
"""
为 4 个 opencode2api 实例生成 config.json。

用法：python3 generate_configs.py <根目录>
每个实例写 <根目录>/instance-N/config.json，代理指向同目录下的
proxies/proxies-X.txt（相对 config.json 解析）。

server_keys 与 WebUI 口令用 secrets 生成，不落在命令行历史里。
"""

import json
import os
import secrets
import string
import sys

GROUPS = ["a", "b", "c", "d"]
PER_GROUP = 40

# 容器内 opencode2api 用户的 uid/gid，宿主挂载目录需与其对齐
STATE_UID = 100
STATE_GID = 101


def gen_key(nbytes=24):
    return "sk-oc2api-" + secrets.token_hex(nbytes)


def gen_password(n=20):
    alphabet = string.ascii_letters + string.digits
    return "oc2-" + "".join(secrets.choice(alphabet) for _ in range(n))


def build(group):
    return {
        "listen": "0.0.0.0:8080",
        "server_keys": [gen_key()],
        "zen_keys": [],
        "go_keys": [],
        "anonymous": True,
        "proxies": [],
        "proxyfile": "proxies/proxies-%s.txt" % group,
        "upstream": {
            "zen": "https://opencode.ai/zen",
            "go": "https://opencode.ai/zen/go",
        },
        "prefer": "go",
        "retry": {
            "max_attempts": 3,
            "timeout_seconds": 120,
        },
        "models": {
            "refresh_seconds": 300,
            "protocols": {},
        },
        "performance": {
            "max_idle_conns": 512,
            "max_idle_conns_per_host": 64,
            "max_conns_per_host": 32,
            "idle_conn_timeout_seconds": 120,
            "connect_timeout_seconds": 5,
            "failure_cooldown_seconds": 30,
            "attempt_timeout_seconds": 30,
            "first_event_timeout_seconds": 30,
        },
        "logging": {
            "level": "info",
            "ring_size": 2000,
            "dump_request_bodies": False,
        },
        "webui": {
            "enabled": True,
            "listen": "0.0.0.0:8081",
            "username": "admin",
            "password": gen_password(),
            "session_ttl_minutes": 720,
        },
        "reasoning": {
            "effort": "",
            "effort_by_model": {},
        },
        "rotation": {
            "sota": {
                "alias": "sota",
                "order": [],
            },
            "sweet": {
                "alias": "sweet",
                "order": [],
            },
            "first_output_seconds": 30,
        },
    }


def main():
    root = sys.argv[1]
    creds = []
    for i, group in enumerate(GROUPS, start=1):
        d = os.path.join(root, "instance-%d" % i)
        os.makedirs(d, exist_ok=True)
        os.chown(d, STATE_UID, STATE_GID)
        os.chmod(d, 0o755)
        cfg = build(group)
        path = os.path.join(d, "config.json")
        with open(path, "w", encoding="utf-8") as fh:
            json.dump(cfg, fh, indent=2, ensure_ascii=False)
        os.chmod(path, 0o644)
        # 容器 entrypoint 以 root 起进程、chown 目录后 su-exec 降到 uid 100。
        # 目录会被 chown，目录内的文件不会，所以这里必须显式对齐属主，
        # 否则降权后读 config.json 会 permission denied 而反复重启。
        os.chown(path, STATE_UID, STATE_GID)
        creds.append({
            "instance": i,
            "group": group,
            "api_port": 8080 + (i - 1) * 2,
            "webui_port": 8081 + (i - 1) * 2,
            "server_key": cfg["server_keys"][0],
            "webui_user": cfg["webui"]["username"],
            "webui_password": cfg["webui"]["password"],
        })
        print("instance-%d -> %s (group %s, %d proxies)"
              % (i, path, group, PER_GROUP))

    creds_path = os.path.join(root, "credentials.json")
    with open(creds_path, "w", encoding="utf-8") as fh:
        json.dump(creds, fh, indent=2, ensure_ascii=False)
    os.chmod(creds_path, 0o600)
    print("凭据汇总 -> %s" % creds_path)


if __name__ == "__main__":
    main()