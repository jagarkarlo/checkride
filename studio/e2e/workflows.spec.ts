import { expect, test } from "@playwright/test";
import { readFile } from "node:fs/promises";

async function suiteBundle() {
  return Promise.all(["suite.json", "zero-loss.drillrun.json", "tail-loss.drillrun.json", "budget-loss.drillrun.json"].map(async (name) => ({
    name, mimeType: "application/json", buffer: await readFile(new URL(`../../examples/suites/postgresql-policy/${name}`, import.meta.url)),
  })));
}

test("installed app keeps lab execution disabled by default", async ({ page }) => {
  test.skip(!process.env.NOSTEKON_APP_URL, "Requires the packaged app.");
  await page.goto("/#/lab");
  await expect(page.getByRole("link", { name: "Lab", exact: true })).toHaveCount(1);
  await expect(page.getByRole("heading", { name: "Lab jobs" })).toBeVisible();
  await expect(page.getByRole("status", { name: "Lab availability" })).toContainText("Execution disabled");
  await expect(page.getByRole("button", { name: "Run suite", exact: true })).toBeDisabled();
});

for (const width of [390, 1440]) {
  test(`installed app lab job progresses into independently reviewed evidence at ${width}px`, async ({ page }, testInfo) => {
    test.skip(!process.env.NOSTEKON_APP_URL, "Requires the packaged app.");
    const files = await suiteBundle();
    const summary = JSON.parse(files[0].buffer.toString());
    const identifier = "a".repeat(24);
    const errors: string[] = [];
    const starts: unknown[] = [];
    let started = false;
    let completed = false;
    const snapshot = () => ({
      id: identifier, status: completed ? "completed" : "running", options: { writes: 10, rpoSeconds: 60 },
      startedAt: "2026-10-07T10:00:00Z", ...(completed ? { completedAt: "2026-10-07T10:01:00Z", exitCode: 0 } : {}),
      log: completed ? "Suite finished.\n" : "Suite started.\n", logTruncated: false,
      artifacts: completed ? files.map(file => file.name) : ["suite.json"],
      summary: completed ? summary : { ...summary, status: "running", passed: false, cases: [] },
    });
    page.on("pageerror", error => errors.push(error.message));
    await page.route("**/api/v1/lab**", async route => {
      const request = route.request();
      const path = new URL(request.url()).pathname;
      expect(request.headers()["x-nostekon-lab"]).toBe("true");
      if (path.includes("/artifacts/")) {
        const file = files.find(file => path.endsWith(`/artifacts/${file.name}`));
        expect(file).toBeDefined();
        await route.fulfill({ status: 200, contentType: "application/json", body: file!.buffer });
      } else if (path === "/api/v1/lab/jobs" && request.method() === "POST") {
        starts.push(request.postDataJSON()); started = true;
        await route.fulfill({ status: 202, json: snapshot() });
      } else if (path === "/api/v1/lab/jobs") {
        await route.fulfill({ json: started ? [snapshot()] : [] });
      } else if (path.endsWith(identifier)) {
        await route.fulfill({ json: snapshot() });
      } else {
        await route.fulfill({ json: { enabled: true } });
      }
    });
    await page.setViewportSize({ width, height: 900 });
    await page.goto("/#/lab");
    await expect(page.getByRole("status", { name: "Lab availability" })).toContainText("Local execution enabled");
    await page.getByLabel("Writes per case").fill("99");
    await page.getByRole("button", { name: "Run suite", exact: true }).click();
    expect(starts).toEqual([]);
    await page.getByLabel("Writes per case").fill("10");
    await page.getByRole("button", { name: "Run suite", exact: true }).click();
    await expect(page.getByRole("status", { name: "Lab job status" })).toContainText("running");
    expect(starts).toEqual([{ writes: 10, rpoSeconds: 60 }]);
    await expect(page.getByRole("button", { name: "Run suite", exact: true })).toBeDisabled();
    completed = true;
    await expect(page.getByRole("status", { name: "Lab job status" })).toContainText("completed");
    await expect(page.getByRole("progressbar", { name: "Completed policy cases" })).toHaveAttribute("value", "3");
    const download = page.waitForEvent("download");
    await page.getByRole("button", { name: "suite.json", exact: true }).click();
    expect(await readFile((await (await download).path())!)).toEqual(files[0].buffer);
    if (width === 390) {
      const table = page.getByRole("region", { name: "Lab jobs table" });
      await table.focus();
      expect(await table.evaluate(element => { element.scrollLeft = element.scrollWidth; return element.scrollLeft > 0; })).toBe(true);
    }
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await page.screenshot({ path: testInfo.outputPath(`lab-completed-${width}.png`), fullPage: true });
    await page.getByRole("button", { name: "Review suite", exact: true }).click();
    await expect(page.getByRole("status", { name: "Suite evidence agreement" })).toContainText("Evidence matches the summary");
    await expect(page.getByRole("status", { name: "Suite evidence agreement" })).toContainText("Imported evidence");
    await page.getByRole("button", { name: "Save cases", exact: true }).click();
    await expect(page.getByRole("status", { name: "Suite save result" })).toContainText("3 cases saved to Runs");
    await page.getByRole("link", { name: "Runs", exact: true }).click();
    await page.reload();
    await expect(page.locator(".saved-runs tbody tr")).toHaveCount(3);
    expect(errors).toEqual([]);
  });
}

test("installed app lab cancellation retains an interrupted summary", async ({ page }) => {
  test.skip(!process.env.NOSTEKON_APP_URL, "Requires the packaged app.");
  const files = await suiteBundle();
  const summary = JSON.parse(files[0].buffer.toString());
  const identifier = "b".repeat(24);
  let cancelled = false;
  const snapshot = () => ({
    id: identifier, status: cancelled ? "cancelled" : "running", options: { writes: 10, rpoSeconds: 60 },
    startedAt: "2026-10-07T10:00:00Z", ...(cancelled ? { completedAt: "2026-10-07T10:01:00Z", exitCode: 130 } : {}),
    log: "", logTruncated: false, artifacts: ["suite.json"],
    summary: cancelled ? { ...summary, status: "interrupted", passed: false, cases: [] } : { invalid: "checkpoint" },
  });
  await page.route("**/api/v1/lab**", async route => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/cancel")) { cancelled = true; await route.fulfill({ status: 202, json: { status: "cancellation requested" } }); }
    else if (path === "/api/v1/lab/jobs") await route.fulfill({ json: [snapshot()] });
    else if (path.endsWith(identifier)) await route.fulfill({ json: snapshot() });
    else await route.fulfill({ json: { enabled: true } });
  });
  await page.goto("/#/lab");
  await expect(page.getByRole("status", { name: "Lab job status" })).toContainText("running");
  await expect(page.getByRole("alert")).toContainText("Could not read checkpoint");
  await page.getByRole("button", { name: "Cancel job", exact: true }).click();
  await expect(page.getByRole("status", { name: "Lab job status" })).toContainText("cancelled");
  await expect(page.getByRole("status", { name: "Lab job status" })).toContainText("exit 130");
  await expect(page.getByRole("button", { name: "Review suite", exact: true })).toBeEnabled();
});

for (const width of [390, 1440]) {
  test(`installed app evaluates and saves evidence through the API at ${width}px`, async ({ page }, testInfo) => {
    test.skip(!process.env.NOSTEKON_APP_URL, "Requires the packaged app; static demo checks run separately.");
    const errors: string[] = [];
    const files = await suiteBundle();
    const reportStatuses = new Map<string, number>();
    page.on("pageerror", (error) => errors.push(error.message));
    page.on("response", (response) => {
      if (new URL(response.url()).pathname === "/api/v1/runs/report") {
        reportStatuses.set(response.request().postDataJSON().metadata.name, response.status());
      }
    });
    await page.setViewportSize({ width, height: 900 });
    await page.goto("/#/suite");
    await expect(page.locator(".api-status")).toContainText("API connected");
    await expect(page.getByRole("status", { name: "Suite evidence agreement" })).toContainText("Evidence matches the summary");
    await expect(page.locator(".suite-table tbody tr > td:nth-child(2)")).toHaveText(["verified", "failed", "verified"]);
    expect(files.slice(1).map(file => reportStatuses.get(JSON.parse(file.buffer.toString()).metadata.name))).toEqual([200, 200, 200]);
    await page.getByRole("button", { name: "Save cases", exact: true }).click();
    await expect(page.getByRole("status", { name: "Suite save result" })).toContainText("3 cases saved to Runs");
    await page.getByRole("link", { name: "Runs", exact: true }).click();
    await page.reload();
    await expect(page.locator(".saved-runs tbody tr")).toHaveCount(3);
    await page.getByRole("button", { name: JSON.parse(files[2].buffer.toString()).metadata.name, exact: true }).click();
    await expect(page.getByRole("heading", { name: "Failed at V4" })).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await page.screenshot({ path: testInfo.outputPath(`installed-app-${width}.png`), fullPage: true });
    expect(errors).toEqual([]);
  });
}

for (const width of [390, 1440]) {
  test(`recorded suite reviews all policy outcomes at ${width}px`, async ({ page }, testInfo) => {
    const errors: string[] = [];
    page.on("pageerror", (error) => errors.push(error.message));
    await page.setViewportSize({ width, height: 900 });
    await page.goto("/demo/#/suite");
    await expect(page.getByRole("heading", { name: "Policy suite" })).toBeVisible();
    const agreement = page.getByRole("status", { name: "Suite evidence agreement" });
    await expect(agreement).toContainText("Evidence matches the summary");
    await expect(agreement).toContainText("Recorded local lab");
    await expect(agreement).toContainText("signatures unverified");
    const rows = page.locator(".suite-table tbody tr");
    await expect(rows).toHaveCount(3);
    for (const [index, verdict, lost, budget] of [[0, "verified", "0", "0s"], [1, "failed", "2", "0s"], [2, "verified", "2", "1m"]] as const) {
      await expect(rows.nth(index).locator("td").nth(1)).toHaveText(verdict);
      await expect(rows.nth(index).locator("td").nth(2)).toHaveText("10");
      await expect(rows.nth(index).locator("td").nth(3)).toHaveText(lost);
      await expect(rows.nth(index).locator("td").nth(5)).toHaveText(budget);
      await expect(rows.nth(index).locator("td").nth(7)).toHaveText("Matches");
    }
    await page.getByRole("button", { name: "Save cases", exact: true }).click();
    await expect(page.getByRole("status", { name: "Suite save result" })).toContainText("3 cases saved to Runs");
    for (const theme of ["dark", "light"]) {
      if (await page.locator("html").getAttribute("data-theme") !== theme) await page.locator(".studio-theme").click();
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
      if (width === 390) expect(await page.locator(".suite-workspace .table-scroll").evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
      await page.screenshot({ path: testInfo.outputPath(`suite-${width}-${theme}.png`), fullPage: true });
    }
    const download = page.waitForEvent("download");
    await page.getByRole("button", { name: "Download original suite summary" }).click();
    expect(await readFile((await (await download).path())!)).toEqual((await suiteBundle())[0].buffer);
    await page.getByRole("button", { name: "Open Strict tail loss report" }).click();
    await expect(page.getByRole("heading", { name: "Failed at V4" })).toBeVisible();
    await page.getByRole("link", { name: "Suite", exact: true }).click();
    await expect(agreement).toContainText("Evidence matches the summary");
    expect(errors).toEqual([]);
  });
}

test("suite imports expose changed claims and preserve original evidence", async ({ page }) => {
  await page.goto("/demo/#/suite");
  await expect(page.getByRole("status", { name: "Suite evidence agreement" })).toContainText("Evidence matches");
  const files = await suiteBundle();
  const summary = JSON.parse(files[0].buffer.toString());
  summary.cases[2].rpo.lost = 0;
  files[0].buffer = Buffer.from(JSON.stringify(summary));
  await page.locator('[data-testid="suite-input"]').setInputFiles(files);
  const agreement = page.getByRole("status", { name: "Suite evidence agreement" });
  await expect(agreement).toContainText("Evidence needs attention");
  await expect(agreement).toContainText("Imported evidence");
  await expect(page.getByRole("region", { name: "Suite findings" })).toContainText("lost differs from the suite summary.");
  await expect(page.locator(".suite-table tbody tr").nth(2)).toContainText("Mismatch");
  const summaryDownload = page.waitForEvent("download");
  await page.getByRole("button", { name: "Download original suite summary" }).click();
  expect(await readFile((await (await summaryDownload).path())!)).toEqual(files[0].buffer);
  await page.getByRole("button", { name: "Open Budgeted tail loss report" }).click();
  await expect(page.getByRole("heading", { name: "Verified to V4" })).toBeVisible();
  const evidenceDownload = page.waitForEvent("download");
  await page.getByRole("button", { name: "Download original evidence" }).click();
  expect(await readFile((await (await evidenceDownload).path())!)).toEqual(files[3].buffer);
});

test("suite cases save together, survive reload and deduplicate without adding provenance", async ({ page }) => {
  await page.goto("/demo/#/suite");
  await expect(page.getByRole("status", { name: "Suite evidence agreement" })).toContainText("Evidence matches");
  const files = await suiteBundle();
  await page.locator('[data-testid="suite-input"]').setInputFiles(files);
  await expect(page.getByRole("status", { name: "Suite evidence agreement" })).toContainText("Imported evidence");
  await page.getByRole("button", { name: "Save cases", exact: true }).click();
  await expect(page.getByRole("status", { name: "Suite save result" })).toContainText("3 cases saved to Runs");
  await page.getByRole("button", { name: "Save cases", exact: true }).click();
  await expect(page.getByRole("status", { name: "Suite save result" })).toContainText("3 cases saved to Runs");
  await page.getByRole("link", { name: "Runs", exact: true }).click();
  await page.reload();
  const rows = page.locator(".saved-runs tbody tr");
  await expect(rows).toHaveCount(3);
  await expect(rows.locator(".evidence-kind")).toHaveText(["Imported evidence", "Imported evidence", "Imported evidence"]);
  for (const [index, verdict] of [[1, "verified"], [2, "failed"], [3, "verified"]] as const) {
    const name = JSON.parse(files[index].buffer.toString()).metadata.name;
    await expect(rows.filter({ has: page.getByRole("button", { name, exact: true }) }).locator(".verdict-tag")).toHaveText(verdict);
  }
  await page.getByRole("button", { name: JSON.parse(files[2].buffer.toString()).metadata.name, exact: true }).click();
  await expect(page.getByRole("heading", { name: "Failed at V4" })).toBeVisible();
  const evidenceDownload = page.waitForEvent("download");
  await page.getByRole("button", { name: "Download original evidence" }).click();
  expect(await readFile((await (await evidenceDownload).path())!)).toEqual(files[2].buffer);
  await page.getByRole("link", { name: "Suite", exact: true }).click();
  await page.getByRole("button", { name: "Recorded suite", exact: true }).click();
  await expect(page.getByRole("status", { name: "Suite evidence agreement" })).toContainText("Recorded local lab");
  await expect(page.getByRole("status", { name: "Suite save result" })).toHaveCount(0);
});

test("suite storage failures leave no partial cases or stale save success and can be retried", async ({ page }) => {
  await page.goto("/demo/#/suite");
  await expect(page.getByRole("status", { name: "Suite evidence agreement" })).toContainText("Evidence matches");
  await page.evaluate(() => {
    const originalPut = IDBObjectStore.prototype.put;
    let writes = 0;
    IDBObjectStore.prototype.put = function (value, key) {
      if (this.name === "runs" && ++writes === 2) {
        IDBObjectStore.prototype.put = originalPut;
        throw new DOMException("Storage full", "QuotaExceededError");
      }
      return originalPut.call(this, value, key);
    };
  });
  await page.getByRole("button", { name: "Save cases", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("Storage full");
  await expect(page.getByRole("status", { name: "Suite save result" })).toHaveCount(0);
  await page.getByRole("link", { name: "Runs", exact: true }).click();
  await expect(page.getByRole("heading", { name: "No saved runs yet" })).toBeVisible();
  await page.getByRole("link", { name: "Suite", exact: true }).click();
  await page.getByRole("button", { name: "Save cases", exact: true }).click();
  await expect(page.getByRole("status", { name: "Suite save result" })).toContainText("3 cases saved to Runs");
  await expect(page.getByRole("alert")).toHaveCount(0);
  await page.getByRole("link", { name: "Runs", exact: true }).click();
  await expect(page.locator(".saved-runs tbody tr")).toHaveCount(3);
});

test("missing and invalid suite files cannot leave a previous matching review visible", async ({ page }) => {
  await page.goto("/demo/#/suite");
  await expect(page.getByRole("status", { name: "Suite evidence agreement" })).toContainText("Evidence matches");
  await page.locator('[data-testid="suite-input"]').setInputFiles((await suiteBundle()).slice(0, 1));
  await expect(page.getByRole("status", { name: "Suite evidence agreement" })).toContainText("Evidence needs attention");
  await expect(page.getByRole("region", { name: "Suite findings" })).toContainText("Missing evidence: zero-loss.drillrun.json");
  await expect(page.getByRole("button", { name: "Save cases", exact: true })).toBeDisabled();
  await expect(page.locator(".api-status")).toContainText("Go engine");
  await page.locator('[data-testid="suite-input"]').setInputFiles({ name: "suite.json", mimeType: "application/json", buffer: Buffer.from('{"kind":"Wrong"}') });
  await expect(page.getByRole("alert")).toContainText("Not a supported Nostekon LabSuiteResult");
  await expect(page.locator(".suite-table")).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Download original suite summary" })).toBeDisabled();
  await expect(page.getByRole("button", { name: "Save cases", exact: true })).toBeDisabled();
  await page.getByRole("button", { name: "Recorded suite", exact: true }).click();
  await expect(page.getByRole("status", { name: "Suite evidence agreement" })).toContainText("Evidence matches");
});

test("theme preference persists across product, docs and demo pages", async ({ page }) => {
  await page.emulateMedia({ colorScheme: "dark" });
  await page.goto("/");
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await expect(page.locator("[data-theme-toggle]")).toHaveText("☀️");
  await expect(page.locator("[data-theme-toggle]")).toHaveAttribute("title", "Switch to light mode");
  await page.locator("[data-theme-toggle]").click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await expect(page.locator("[data-theme-toggle]")).toHaveText("🌙");
  await expect(page.locator("[data-theme-toggle]")).toHaveAttribute("title", "Switch to dark mode");
  await expect.poll(() => page.evaluate(() => localStorage.getItem("nostekon-theme"))).toBe("light");

  await page.getByRole("link", { name: "Product", exact: true }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await expect(page.locator("[data-theme-toggle]")).toHaveText("🌙");
  await page.getByRole("link", { name: "Docs", exact: true }).click();
  await expect(page.locator("body")).toHaveAttribute("data-md-color-scheme", "default");

  await page.locator('label[for="__palette_1"]').click();
  await expect(page.locator("body")).toHaveAttribute("data-md-color-scheme", "slate");
  await expect.poll(() => page.evaluate(() => localStorage.getItem("nostekon-theme"))).toBe("dark");

  await page.goto("/demo/");
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await expect(page.getByRole("heading", { name: "Recovery runs" })).toBeVisible();
  await page.goto("/product/");
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await expect(page.locator("[data-theme-toggle]")).toHaveText("☀️");
});

test("browser demo refuses to claim it verified a detached attestation", async ({ page }) => {
  await page.goto("/demo/#/report");
  await page.getByRole("button", { name: /Isolated PostgreSQL restore/ }).click();
  await page.getByRole("button", { name: "Attach attestation" }).click();
  await page.locator('input[data-testid="attestation-input"]').setInputFiles({
    name: "run.attestation.json",
    mimeType: "application/json",
    buffer: Buffer.from('{"apiVersion":"nostekon/attestation/v1alpha1"}'),
  });
  await expect(page.locator(".attestation-boundary")).toContainText("browser demo cannot verify");
});

for (const width of [390, 1440]) {
  test(`original brand assets render across themes at ${width}px`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height: 900 });
    await page.goto("/");
    for (const theme of ["light", "dark"]) {
      await page.evaluate((value) => localStorage.setItem("nostekon-theme", value), theme);
      for (const path of ["/roadmap/", "/demo/", "/docs/"]) {
        await page.goto(path);
        const mark = page.locator(path === "/demo/" ? ".brand-mark" : path === "/docs/" ? ".md-header .md-logo img" : ".wordmark-mark");
        await expect(mark).toHaveAttribute("src", /nostekon-mark|^data:image\/svg\+xml,/);
        await expect.poll(() => mark.evaluate((element) => element instanceof HTMLImageElement && element.complete && element.naturalWidth > 0)).toBe(true);
        const favicon = page.locator('link[rel="icon"]');
        await expect(favicon).toHaveAttribute("href", /nostekon-mark|^data:image\/svg\+xml,/);
        const asset = await page.evaluate(async (source) => {
          const response = await fetch(source);
          return { status: response.status, text: await response.text() };
        }, new URL((await mark.getAttribute("src"))!, page.url()).href);
        expect(asset.status).toBe(200);
        expect(asset.text).toContain("Nostekon stepping-stones mark");
        if (path !== "/docs/") {
          await expect(mark).toBeVisible();
          const toggle = page.locator(path === "/demo/" ? ".studio-theme" : "[data-theme-toggle]");
          await expect(toggle).toHaveText(theme === "dark" ? "☀️" : "🌙");
        }
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
        await page.evaluate(() => window.scrollTo(0, 0));
        await page.screenshot({ path: testInfo.outputPath(`${path.replaceAll("/", "")}-${theme}.png`), fullPage: true });
      }
    }
  });
}

test("homepage explains the product and its commands", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByRole("heading", { level: 1, name: "Find out whether a restore really worked" })).toBeVisible();
  await expect(page.locator(".hero").getByRole("link", { name: "Open the demo" })).toHaveAttribute("href", "/demo/index.html#/report");
  await expect(page.getByText("The demo evaluates evidence; it does not connect to Kubernetes.")).toBeVisible();
  await expect(page.getByRole("heading", { name: "What a report tells you" })).toBeVisible();
  await expect(page.locator(".step")).toHaveCount(5);
  await expect(page.locator(".step-state", { hasText: "Lab only" })).toHaveCount(1);
  await expect(page.getByRole("heading", { name: "Not built yet" })).toBeVisible();

  await expect(page.locator("#quickstart-install")).toContainText("git clone https://github.com/jagarkarlo/nostekon.git");
  await expect(page.locator("#quickstart-install")).toContainText("examples/drills/mlflow-namespace-loss.yaml");
  await expect(page.locator("#quickstart")).toContainText("not on PyPI yet");
  for (const label of ["Copy CLI setup commands", "Copy lab commands", "Copy key setup commands", "Copy report commands"]) {
    await expect(page.getByRole("button", { name: label })).toBeVisible();
  }
  await page.context().grantPermissions(["clipboard-read", "clipboard-write"]);
  const copyButton = page.locator('button[data-copy-target="quickstart-install"]');
  await copyButton.click();
  await expect(copyButton).toHaveText("Copied");
  expect(await page.evaluate(() => navigator.clipboard.readText())).toContain("git clone https://github.com/jagarkarlo/nostekon.git");

  await expect(page.getByRole("link", { name: "GitHub repository" })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});

test("site stays readable, self-hosted and free of decoration", async ({ page }) => {
  const external: string[] = [];
  page.on("request", (request) => {
    const url = new URL(request.url());
    if (url.hostname !== "127.0.0.1" && url.hostname !== "localhost") external.push(url.hostname);
  });
  for (const path of ["/", "/product/", "/evidence/", "/roadmap/"]) {
    await page.goto(path);
    await expect(page.locator("h1")).toHaveCount(1);
    const smallText = await page.evaluate(() => {
      const found: string[] = [];
      for (const element of document.querySelectorAll("main *, header *, footer *")) {
        const own = [...element.childNodes].some((node) => node.nodeType === Node.TEXT_NODE && node.textContent?.trim());
        if (own && parseFloat(getComputedStyle(element).fontSize) < 13) found.push(`${element.tagName}.${element.className}`);
      }
      return found;
    });
    expect(smallText, `${path} has text under 13px`).toEqual([]);
    const effects = await page.evaluate(() =>
      [...document.querySelectorAll("main *")].filter((element) => {
        const style = getComputedStyle(element);
        return style.textShadow !== "none" || /rgba?\([^)]*\)\s+0px 0px \d+px/.test(style.boxShadow) || style.textTransform === "uppercase";
      }).map((element) => `${element.tagName}.${element.className}`),
    );
    expect(effects, `${path} has glow or uppercase labels`).toEqual([]);
  }
  expect(external).toEqual([]);
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

test("run comparison exposes different recovery policies", async ({ page }, testInfo) => {
  await page.setViewportSize({ width: 390, height: 900 });
  await page.goto("/demo/");
  for (const label of ["PostgreSQL RPO exceeded", "PostgreSQL loss within budget"]) {
    await page.getByRole("button", { name: new RegExp(label) }).click();
    await page.getByRole("button", { name: "Save run", exact: true }).click();
    await expect(page.getByRole("button", { name: "Saved", exact: true })).toBeVisible();
  }
  await page.getByRole("link", { name: "Runs", exact: true }).click();
  await page.getByRole("checkbox").nth(0).check();
  await page.getByRole("checkbox").nth(1).check();
  await page.getByRole("button", { name: "Compare (2/2)" }).click();
  const comparison = page.getByRole("region", { name: "Run comparison" });
  await expect(comparison).toContainText("Different recovery objectives. Verdicts use different policies.");
  const objective = comparison.getByRole("row").filter({ hasText: "RPO objective" });
  await expect(objective).toContainText("0s");
  await expect(objective).toContainText("1m");
  await expect(comparison.getByRole("row").filter({ hasText: "Acknowledged writes lost" }).getByRole("cell")).toHaveText(["2", "2"]);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.screenshot({ path: testInfo.outputPath("policy-comparison-mobile.png"), fullPage: true });
});

test("imported evidence uses the browser engine and exports the original", async ({ page }) => {
  await page.goto("/demo/");
    const source = await readFile(new URL("../../examples/runs/k3d-postgresql.run.json", import.meta.url));
  await page.getByRole("main").locator('input[type="file"]').setInputFiles({ name: "lab.json", mimeType: "application/json", buffer: source });
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
  await page.locator('[data-testid="evidence-input"]').setInputFiles({ name: "invalid.json", mimeType: "application/json", buffer: Buffer.from('{"kind":"Wrong"}') });
  await expect(page.getByRole("tab", { name: /Evidence JSON/ })).toHaveAttribute("aria-selected", "true");
  await expect(page.locator(".evidence-problems")).toBeVisible();
  await expect(page.getByRole("button", { name: "Save run", exact: true })).toBeDisabled();
});

test("docs, deep links, search and demo navigation work", async ({ page }) => {
  const response = await page.goto("/docs/");
  expect(response?.status()).toBe(200);
  await expect(page.getByRole("heading", { name: /^Nostekon Documentation/ })).toBeVisible();
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
  for (const sample of [
    { label: "PostgreSQL zero loss", heading: "Verified to V4", lost: "0", acknowledged: 10, objective: "objective 0s · met", verdict: "verified" },
    { label: "PostgreSQL RPO exceeded", heading: /^Failed at V4/, lost: "2", acknowledged: 12, objective: "objective 0s · missed", verdict: "failed" },
    { label: "PostgreSQL loss within budget", heading: "Verified to V4", lost: "2", acknowledged: 12, objective: "objective 1m · met", verdict: "verified" },
  ]) {
    test(`recorded ledger ${sample.label} at ${width}px`, async ({ page }, testInfo) => {
      await page.setViewportSize({ width, height: 900 });
      await page.goto("/demo/");
      await expect(page.locator(".sample-section")).toContainText("4 recorded lab · 2 synthetic");
      await page.getByRole("button", { name: new RegExp(sample.label) }).click();
      await expect(page.getByRole("heading", { name: sample.heading })).toBeVisible();
      await expect(page.locator(".sample-caveat")).toContainText("Recorded local lab run");
      await expect(page.locator(".sample-caveat")).not.toContainText("synthetic");
      await expect(page.locator(".metric").filter({ hasText: "Writes lost" }).locator(".metric-value")).toHaveText(sample.lost);
      await expect(page.locator(".metric").filter({ hasText: "Writes lost" })).toContainText(`of ${sample.acknowledged} acknowledged`);
      await expect(page.locator(".metric").filter({ hasText: "Data loss window" })).toContainText(sample.objective);
      await expect(page.getByRole("img", { name: `10 writes recovered and ${sample.lost} lost before the failure` })).toBeVisible();
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
      await page.evaluate(() => window.scrollTo(0, 0));
      await page.screenshot({ path: testInfo.outputPath("recorded-ledger.png"), fullPage: true });
      await page.getByRole("button", { name: "Save run", exact: true }).click();
      await expect(page.getByRole("button", { name: "Saved", exact: true })).toBeVisible();
      await page.getByRole("link", { name: "Runs", exact: true }).click();
      await page.reload();
      const saved = page.locator(".runs-table tbody tr");
      await expect(saved).toHaveCount(1);
      await expect(saved).toContainText("Recorded lab");
      await expect(saved.locator(".verdict-tag")).toHaveText(sample.verdict);
    });
  }

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