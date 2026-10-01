import { expect, test } from "@playwright/test";
import { readFile } from "node:fs/promises";

test("homepage directs users to the right recovery workflow", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Start with the proof you need." })).toBeVisible();
  await expect(page.getByRole("link", { name: /Open the browser demo/ })).toHaveAttribute("href", "/demo/index.html#/report");
  await expect(page.getByRole("link", { name: /Follow the CLI setup/ })).toHaveAttribute("href", "/docs/start/");
  await expect(page.getByRole("link", { name: /Read the lab walkthrough/ })).toHaveAttribute("href", "/docs/guides/k3d-isolated-restore/");
  await expect(page.getByText("The demo evaluates evidence; it does not connect to Kubernetes.")).toBeVisible();
  await expect(page.getByRole("heading", { name: "Verify your first restore drill." })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Beyond green backup checkboxes." })).toBeVisible();
  await expect(page.getByRole("link", { name: "GitHub repository" })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});

test("saved evidence survives reload, compares and deletes", async ({ page }) => {
  await page.goto("/demo/");
  await expect(page.getByRole("heading", { name: "Recovery runs" })).toBeVisible();
  await page.getByRole("button", { name: /Isolated PostgreSQL restore/ }).click();
  await expect(page.getByRole("heading", { name: "Verified to V3" })).toBeVisible();
  await page.getByRole("button", { name: "Save run", exact: true }).click();
  await expect(page.getByRole("button", { name: "Saved", exact: true })).toBeVisible();
  await page.getByRole("button", { name: /CRUD cluster loss/ }).click();
  await expect(page.getByRole("heading", { name: /Failed at V3/ })).toBeVisible();
  await page.getByRole("button", { name: "Save run", exact: true }).click();
  await expect(page.getByRole("button", { name: "Saved", exact: true })).toBeVisible();
  await page.getByRole("link", { name: "Runs", exact: true }).click();
  await page.reload();
  await expect(page.locator(".runs-table tbody tr")).toHaveCount(2);
  await page.getByRole("checkbox").nth(0).check();
  await page.getByRole("checkbox").nth(1).check();
  await page.getByRole("button", { name: "Compare (2/2)" }).click();
  await expect(page.getByRole("region", { name: "Run comparison" })).toBeVisible();
  await expect(page.getByText("Different scenarios or requested depths.", { exact: false })).toBeVisible();
  await page.getByRole("searchbox", { name: "Search runs" }).fill("no-such-run");
  await expect(page.getByRole("heading", { name: "No matching runs" })).toBeVisible();
  await page.getByRole("searchbox", { name: "Search runs" }).fill("");
  await page.getByRole("button", { name: /^Delete checkride-/ }).click();
  await page.getByRole("dialog").getByRole("button", { name: "Delete run" }).click();
  await expect(page.locator(".saved-runs tbody tr")).toHaveCount(1);
  await page.reload();
  await expect(page.locator(".saved-runs tbody tr")).toHaveCount(1);
});

test("imported evidence uses the browser engine and exports the original", async ({ page }) => {
  await page.goto("/demo/");
    const source = await readFile(new URL("../../examples/runs/k3d-postgresql.run.json", import.meta.url));
  await page.locator(".run-library input[type=file]").setInputFiles({ name: "lab.json", mimeType: "application/json", buffer: source });
  await expect(page.getByRole("heading", { name: "Verified to V3" })).toBeVisible();
  await expect(page.getByRole("status", { name: "Evidence provenance" })).toContainText("signature unverified");
  await expect(page.locator(".api-status")).toContainText("Go engine");
  const download = page.waitForEvent("download");
  await page.getByRole("button", { name: "Download original evidence" }).click();
  const downloaded = await download;
  expect(await readFile((await downloaded.path())!)).toEqual(source);
  await page.getByRole("button", { name: "Save run", exact: true }).click();
  await expect(page.getByRole("button", { name: "Saved", exact: true })).toBeVisible();
  await page.getByRole("link", { name: "Runs", exact: true }).click();
  await expect(page.getByText("Imported evidence", { exact: true })).toBeVisible();
});

test("invalid imported evidence cannot be saved as a previous green result", async ({ page }) => {
  await page.goto("/demo/");
  await page.getByRole("button", { name: /Isolated PostgreSQL restore/ }).click();
  await expect(page.getByRole("heading", { name: "Verified to V3" })).toBeVisible();
  await page.locator(".report-layout input[type=file]").setInputFiles({ name: "invalid.json", mimeType: "application/json", buffer: Buffer.from('{"kind":"Wrong"}') });
  await expect(page.getByRole("tab", { name: /Evidence JSON/ })).toHaveAttribute("aria-selected", "true");
  await expect(page.locator(".evidence-problems")).toBeVisible();
  await expect(page.getByRole("button", { name: "Save run", exact: true })).toBeDisabled();
});

test("docs, deep links, search and demo navigation work", async ({ page }) => {
  const response = await page.goto("/docs/");
  expect(response?.status()).toBe(200);
  await expect(page.getByRole("heading", { name: /^Checkride Documentation/ })).toBeVisible();
  await page.getByRole("link", { name: "Installation", exact: true }).first().click();
  await expect(page).toHaveURL(/\/docs\/start\//);
  const search = page.getByRole("textbox", { name: "Search", exact: true });
  await search.click();
  await search.pressSequentially("DrillRun", { delay: 40 });
  await expect(page.locator(".md-search-result__link").first()).toBeVisible();
  for (const path of ["/docs/guides/k3d-isolated-restore/", "/docs/reference/drillrun-evidence/", "/demo/"]) {
    expect((await page.request.get(path)).status()).toBe(200);
  }
});

for (const width of [390, 1440]) {
  test(`theme and layout remain usable at ${width}px`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height: 900 });
    await page.emulateMedia({ colorScheme: "light", reducedMotion: "reduce" });
    await page.goto("/demo/");
    await expect(page.getByRole("heading", { name: "Recovery runs" })).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await page.screenshot({ path: testInfo.outputPath("runs-light.png"), fullPage: true });
    await page.getByRole("button", { name: "Switch to dark mode" }).click();
    await page.reload();
    await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
    await page.getByRole("button", { name: /Isolated PostgreSQL restore/ }).click();
    await expect(page.getByRole("heading", { name: "Verified to V3" })).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await page.screenshot({ path: testInfo.outputPath("report-dark.png"), fullPage: true });
  });
}