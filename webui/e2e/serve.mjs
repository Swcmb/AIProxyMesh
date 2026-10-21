// 为 Playwright 准备一次性配置并启动 mesh；进程退出时清理临时数据库。
import { spawn } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(here, "../..");
const webPort = process.env.E2E_WEB_PORT ?? "18081";
const apiPort = process.env.E2E_API_PORT ?? "18080";
const password = process.env.E2E_PASSWORD ?? "e2e-only-Password-2026";
const binary = path.join(root, process.platform === "win32" ? "mesh.exe" : "mesh");

if (!fs.existsSync(binary)) {
  console.error(`[e2e] missing ${binary}; run: go build -o ${path.basename(binary)} ./cmd/mesh`);
  process.exit(1);
}

const tempDir = fs.mkdtempSync(path.join(os.tmpdir(), "mesh-e2e-"));
const example = JSON.parse(fs.readFileSync(path.join(root, "config.example.json"), "utf8"));
const config = {
  ...example,
  listen: `127.0.0.1:${apiPort}`,
  webui: { ...example.webui, username: "admin", password, listen: `127.0.0.1:${webPort}` },
  egress: {
    ...(example.egress ?? {}),
    enabled: true,
    import_legacy_proxies: true,
    state_path: path.join(tempDir, "egress.db"),
  },
  logging: {
    ...example.logging,
    requests: {
      ...(example.logging?.requests ?? {}),
      enabled: true,
      state_path: path.join(tempDir, "requests.db"),
    },
  },
};
const configPath = path.join(tempDir, "config.json");
fs.writeFileSync(configPath, JSON.stringify(config, null, 2));

const child = spawn(binary, ["-config", configPath], { cwd: root, stdio: "inherit" });

let cleaned = false;
function cleanup() {
  if (cleaned) {
    return;
  }
  cleaned = true;
  try {
    child.kill();
  } catch {
    // already gone
  }
  try {
    fs.rmSync(tempDir, { recursive: true, force: true });
  } catch {
    // Windows may still hold the database briefly; the OS temp cleaner handles it.
  }
}

for (const signal of ["SIGINT", "SIGTERM", "SIGHUP"]) {
  process.on(signal, () => {
    cleanup();
    process.exit(0);
  });
}
process.on("exit", cleanup);
child.on("exit", (code) => {
  cleanup();
  process.exit(code ?? 0);
});
