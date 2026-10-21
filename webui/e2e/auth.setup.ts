import fs from "node:fs";
import { test as setup } from "@playwright/test";

const password = process.env.E2E_PASSWORD ?? "e2e-only-Password-2026";

setup("authenticate", async ({ request }) => {
  fs.mkdirSync("e2e/.auth", { recursive: true });
  const response = await request.post("/api/auth/login", {
    data: { username: "admin", password },
  });
  if (!response.ok()) {
    throw new Error(`login failed: HTTP ${response.status()} ${await response.text()}`);
  }
  await request.storageState({ path: "e2e/.auth/state.json" });
});
