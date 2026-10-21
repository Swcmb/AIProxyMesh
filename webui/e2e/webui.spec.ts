import { test, expect, type Page } from "@playwright/test";

const routes = [
  "/overview",
  "/monitor",
  "/quotas",
  "/models",
  "/nodes",
  "/subscriptions",
  "/rotation",
  "/upstreams",
  "/requests",
  "/diagnostics",
  "/api-keys",
  "/config",
  "/playground",
  "/access",
];

const navGroups = ["仪表盘", "资源管理", "可观测", "配置", "高级"];

function collectErrors(page: Page): string[] {
  const errors: string[] = [];
  page.on("console", (message) => {
    if (message.type() === "error") {
      errors.push(message.text());
    }
  });
  page.on("pageerror", (error) => {
    errors.push(error.message);
  });
  return errors;
}

async function assertNoOverflow(page: Page, route: string) {
  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth > window.innerWidth + 1,
  );
  expect(overflow, `${route} 出现横向溢出`).toBe(false);
}

test("导航分组与 14 条路由（深色）", async ({ page }) => {
  await page.addInitScript(() => {
    try {
      localStorage.setItem("mesh.theme", "dark");
    } catch {
      // about:blank 等无存储上下文
    }
  });
  const errors = collectErrors(page);
  await page.goto("/overview");
  await expect(page.locator("h1")).toBeVisible();
  await expect(page.locator(".sidebar")).toBeVisible();
  for (const group of navGroups) {
    await expect(page.getByText(group, { exact: true })).toBeVisible();
  }
  for (const route of routes) {
    await page.goto(route);
    await expect(page.locator("h1")).toBeVisible();
    await expect(page.locator(".sidebar"), `${route} 未进入应用壳层`).toBeVisible();
    expect((await page.locator("h1").innerText()).length, `${route} 标题为空`).toBeGreaterThan(0);
    await assertNoOverflow(page, route);
  }
  expect(errors, errors.join("\n")).toEqual([]);
});

test("浅色主题逐页渲染", async ({ page }) => {
  await page.addInitScript(() => {
    try {
      localStorage.setItem("mesh.theme", "light");
    } catch {
      // about:blank 等无存储上下文
    }
  });
  const errors = collectErrors(page);
  for (const route of routes) {
    await page.goto(route);
    await expect(page.locator("h1")).toBeVisible();
    await expect(page.locator(".sidebar"), `${route} 未进入应用壳层`).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.dataset.theme), `${route} 主题`).toBe(
      "light",
    );
    expect(
      await page.evaluate(() => getComputedStyle(document.documentElement).colorScheme),
      `${route} color-scheme`,
    ).toBe("light");
    expect(
      await page.evaluate(() => getComputedStyle(document.body).backgroundColor),
      `${route} 浅色背景`,
    ).toBe("rgb(250, 250, 250)");
    await assertNoOverflow(page, route);
  }
  expect(errors, errors.join("\n")).toEqual([]);
});

test("图表颜色取自 token 并随主题切换", async ({ page }) => {
  await page.addInitScript(() => {
    try {
      localStorage.setItem("mesh.theme", "dark");
    } catch {
      // about:blank 等无存储上下文
    }
  });
  await page.goto("/overview");
  const primaryCurve = page.locator(".chart-series-primary .recharts-curve").first();
  await expect(primaryCurve).toHaveCount(1);
  expect(await primaryCurve.evaluate((element) => getComputedStyle(element).stroke)).toBe(
    "rgb(91, 140, 255)",
  );
  const dangerCurve = page.locator(".chart-series-danger .recharts-curve").first();
  await expect(dangerCurve).toHaveCount(1);
  expect(await dangerCurve.evaluate((element) => getComputedStyle(element).stroke)).toBe(
    "rgb(242, 85, 90)",
  );
  await page.getByRole("button", { name: "切换到浅色主题" }).click();
  await page.waitForFunction(() => document.documentElement.dataset.theme === "light");
  expect(await primaryCurve.evaluate((element) => getComputedStyle(element).stroke)).toBe(
    "rgb(20, 112, 255)",
  );
  expect(await dangerCurve.evaluate((element) => getComputedStyle(element).stroke)).toBe(
    "rgb(201, 42, 42)",
  );

  await page.goto("/monitor");
  await expect(page.locator(".recharts-surface").first()).toBeVisible();
  await expect(page.locator(".chart-series-primary").first()).toBeAttached();
  await expect(page.locator(".chart-series-warning").first()).toBeAttached();
});

test("Key 创建、Esc 关闭弹窗与删除确认", async ({ page }) => {
  const name = `e2e-key-${Date.now()}`;
  const errors = collectErrors(page);
  await page.goto("/api-keys");
  await page.getByPlaceholder("例如：笔记本").fill(name);
  await page.getByRole("button", { name: "创建", exact: true }).click();
  await expect(page.getByText(name, { exact: true })).toBeVisible();

  const row = page.locator("tr", { hasText: name });
  await row.getByRole("button", { name: "改名" }).click();
  await expect(page.getByRole("dialog")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog")).toHaveCount(0);

  await row.getByRole("button", { name: "删除" }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toBeVisible();
  await dialog.getByRole("button", { name: "删除", exact: true }).click();
  await expect(page.getByText(name, { exact: true })).toHaveCount(0);
  expect(errors, errors.join("\n")).toEqual([]);
});
