import { describe, expect, it } from "vitest";
import { evaluateSuite, parseSuite } from "./labSuite";
import type { Report } from "./report";

const summary = {
  apiVersion: "nostekon/lab-suite/v1alpha1", kind: "LabSuiteResult", status: "passed", passed: true,
  cases: ["zero-loss", "tail-loss", "budget-loss"].map((name) => ({
    name, drillRun: `${name}.drillrun.json`, expectedExitCode: name === "tail-loss" ? 1 : 0,
    observedExitCode: name === "tail-loss" ? 1 : 0, passed: true,
  })),
};

describe("lab suite contract", () => {
  it("reads the runner summary without substituting it for evaluated evidence", () => {
    expect(parseSuite(JSON.stringify(summary))).toEqual(summary);
  });

  it.each(["failed", "running", "interrupted"])("accepts a partial %s suite", (status) => {
    const partial = { ...summary, status, passed: false, cases: summary.cases.slice(0, 1) };
    expect(parseSuite(JSON.stringify(partial))).toEqual(partial);
  });

  it.each([
    { ...summary, apiVersion: "unknown" },
    { ...summary, cases: [...summary.cases, summary.cases[0]] },
    { ...summary, cases: [summary.cases[1]] },
    { ...summary, status: "failed" },
    { ...summary, cases: [{ ...summary.cases[0], drillRun: "../outside.json" }] },
    { ...summary, cases: [{ ...summary.cases[0], observedExitCode: "0" }] },
    { ...summary, cases: [{ ...summary.cases[0], rpo: { lost: -1 } }] },
  ])("rejects malformed or contradictory summaries", (value) => {
    expect(() => parseSuite(JSON.stringify(value))).toThrow();
  });

  it("bounds summary size by UTF-8 bytes", () => {
    expect(() => parseSuite("\u00e9".repeat(33 * 1024))).toThrow("64 KiB");
  });
});

const measurement = { acknowledged: 10, recovered: 10, lost: 0, holes: 0, unexpected: 0, seconds: 0, objectiveSeconds: 0, met: true };
const report: Report = {
  name: "nostekon-test", verdict: "verified", headline: "Verified to V4", requestedLevel: "V4",
  deepestPassed: "V4", firstFailed: null, failureAt: "2026-10-05T12:00:00Z", levels: [], findings: [], rto: null,
  rpo: { ...measurement, consistent: true, recoveryPoint: null, firstLostAt: null, resolutionSeconds: null, timeline: [] },
};
const partialSuite = parseSuite(JSON.stringify({ ...summary, status: "failed", passed: false, cases: [{ ...summary.cases[0], rpo: measurement }] }));

describe("suite report agreement", () => {
  it("evaluates the exact source and compares each measurement", async () => {
    const source = '{ "original": true }\n';
    const result = await evaluateSuite(partialSuite, new Map([["zero-loss.drillrun.json", source]]), async (text) => {
      expect(text).toBe(source);
      return report;
    });
    expect(result.evidenceMatches).toBe(true);
    expect(result.cases[0].source).toBe(source);
    expect(result.cases[0].report).toBe(report);
    expect(result.cases[0].issues).toEqual([]);
  });

  it("does not accept a claimed success when measurements differ", async () => {
    const result = await evaluateSuite(partialSuite, new Map([["zero-loss.drillrun.json", "{}"]]), async () => ({ ...report, rpo: { ...report.rpo!, lost: 2 } }));
    expect(result.evidenceMatches).toBe(false);
    expect(result.cases[0].issues).toContain("lost differs from the suite summary.");
  });

  it("surfaces missing evidence without trusting the runner's result", async () => {
    const result = await evaluateSuite(partialSuite, new Map(), async () => { throw new Error("Must not evaluate"); });
    expect(result.evidenceMatches).toBe(false);
    expect(result.cases[0].report).toBeNull();
    expect(result.cases[0].issues).toContain("Missing evidence: zero-loss.drillrun.json");
  });

  it("keeps evaluator failures visible and never calls an empty suite a match", async () => {
    const result = await evaluateSuite(partialSuite, new Map([["zero-loss.drillrun.json", "{}"]]), async () => { throw new Error("Invalid evidence"); });
    expect(result.evidenceMatches).toBe(false);
    expect(result.cases[0].issues).toContain("Invalid evidence");
    const empty = parseSuite(JSON.stringify({ ...summary, status: "running", passed: false, cases: [] }));
    expect((await evaluateSuite(empty, new Map(), async () => report)).evidenceMatches).toBe(false);
  });
});