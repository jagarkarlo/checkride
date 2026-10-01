import { defineConfig } from "@playwright/test";

export default defineConfig({
  testDir: "./e2e",
  timeout: 30000,
  fullyParallel: true,
    use: { baseURL: "http://127.0.0.1:4326", viewport: { width: 1440, height: 1000 }, trace: "retain-on-failure" },
  webServer: {
      command: "python3 -m http.server 4326 --bind 127.0.0.1 --directory ../site/dist",
      url: "http://127.0.0.1:4326/demo/",
    reuseExistingServer: false,
  },
});