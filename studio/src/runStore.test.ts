import "fake-indexeddb/auto";
import { beforeEach, describe, expect, it } from "vitest";
import { deleteDB, openDB } from "idb";
import { deleteRun, evidenceLabel, listRuns, RUN_LIMIT, saveRun } from "./runStore";
import type { Report } from "./report";

const report: Report = {
  name: "local-run", verdict: "verified", headline: "Verified to V3", requestedLevel: "V3",
  deepestPassed: "V3", firstFailed: null, failureAt: "2026-01-01T00:00:00Z",
  levels: [], findings: [], rto: null, rpo: null,
};

beforeEach(async () => { await deleteDB("checkride-runs"); });

describe("saved run library", () => {
  it("persists original evidence and provenance, deduplicates and deletes", async () => {
    const saved = await saveRun('{"run":1}', report, "k3d-postgresql");
    await saveRun('{"run":1}', report, "k3d-postgresql");
    expect(await listRuns()).toEqual([expect.objectContaining({ id: saved.id, source: '{"run":1}', sampleId: "k3d-postgresql" })]);
    await deleteRun(saved.id);
    expect(await listRuns()).toEqual([]);
  });

  it("does not silently evict evidence when the library is full", async () => {
    for (let index = 0; index < RUN_LIMIT; index++) await saveRun(String(index), report);
    await expect(saveRun("overflow", report)).rejects.toThrow("Delete a run");
    await saveRun("0", report);
    expect(await listRuns()).toHaveLength(RUN_LIMIT);
  });

  it("checks UTF-8 bytes rather than JavaScript character count", async () => {
    await expect(saveRun("\u00e9".repeat(9 * 1024 * 1024), report)).rejects.toThrow("16 MiB");
    expect(await listRuns()).toEqual([]);
  });

  it("surfaces damaged records instead of silently losing them", async () => {
    await listRuns();
    const db = await openDB("checkride-runs", 1);
    await db.put("runs", { id: "bad", source: "{}", report: null });
    db.close();
    await expect(listRuns()).rejects.toThrow("unreadable");
  });

  it("does not call synthetic or imported data recorded lab evidence", () => {
    expect(evidenceLabel("k3d-postgresql")).toBe("Recorded lab");
    expect(evidenceLabel("crud-cluster-loss")).toBe("Synthetic sample");
    expect(evidenceLabel("")).toBe("Imported evidence");
  });
});