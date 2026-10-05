import "fake-indexeddb/auto";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { deleteDB, openDB } from "idb";
import { deleteRun, evidenceLabel, listRuns, RUN_LIMIT, saveRun } from "./runStore";
import type { Report } from "./report";

const report: Report = {
  name: "local-run", verdict: "verified", headline: "Verified to V3", requestedLevel: "V3",
  deepestPassed: "V3", firstFailed: null, failureAt: "2026-01-01T00:00:00Z",
  levels: [], findings: [], rto: null, rpo: null,
};

beforeEach(async () => {
  await deleteDB("checkride-runs");
  await deleteDB("nostekon-runs");
});

describe("saved run library", () => {
  it("uses only the new database on a fresh installation", async () => {
    await listRuns();
    expect((await indexedDB.databases()).map((db) => db.name)).toEqual(["nostekon-runs"]);
  });

  it("rolls back interrupted migration and safely retries it", async () => {
    const legacy = await openDB("checkride-runs", 1, {
      upgrade(db) { db.createObjectStore("runs", { keyPath: "id" }); },
    });
    const first = { id: "first", source: "first", report, sampleId: "", savedAt: 1 };
    const second = { ...first, id: "second", source: "second", savedAt: 2 };
    await legacy.put("runs", first);
    await legacy.put("runs", second);
    legacy.close();
    const originalPut = IDBObjectStore.prototype.put;
    const failure = vi.spyOn(IDBObjectStore.prototype, "put").mockImplementation(function (this: IDBObjectStore, value, key) {
      if (this.name === "runs" && value.id === "second") throw new DOMException("Storage full", "QuotaExceededError");
      return originalPut.call(this, value, key);
    });
    try { await expect(listRuns()).rejects.toThrow("Storage full"); }
    finally { failure.mockRestore(); }
    const current = await openDB("nostekon-runs", 1);
    try {
      expect(await current.count("runs")).toBe(0);
      expect(await current.get("migration", "legacy-imported")).toBeUndefined();
    } finally { current.close(); }
    expect(await listRuns()).toEqual([second, first]);
  });

  it("serializes concurrent imports and keeps an existing Nostekon record", async () => {
    const legacy = await openDB("checkride-runs", 1, {
      upgrade(db) { db.createObjectStore("runs", { keyPath: "id" }); },
    });
    const oldRun = { id: "same", source: "old", report, sampleId: "", savedAt: 1 };
    await legacy.put("runs", oldRun);
    legacy.close();
    const current = await openDB("nostekon-runs", 1, {
      upgrade(db) {
        db.createObjectStore("runs", { keyPath: "id" });
        db.createObjectStore("migration");
      },
    });
    const newRun = { ...oldRun, source: "current", savedAt: 2 };
    await current.put("runs", newRun);
    current.close();
    expect(await Promise.all([listRuns(), listRuns()])).toEqual([[newRun], [newRun]]);
  });

  it("migrates legacy runs once without changing evidence or resurrecting deleted runs", async () => {
    const legacy = await openDB("checkride-runs", 1, {
      upgrade(db) { db.createObjectStore("runs", { keyPath: "id" }); },
    });
    const saved = { id: "legacy", source: '{ "apiVersion": "checkride/v1alpha1" }\n', report, sampleId: "k3d-postgresql", savedAt: 123 };
    await legacy.put("runs", saved);
    legacy.close();
    expect(await listRuns()).toEqual([saved]);
    expect((await indexedDB.databases()).map((db) => db.name)).toContain("nostekon-runs");
    const current = await openDB("nostekon-runs", 1);
    try { expect(await current.get("runs", saved.id)).toEqual(saved); }
    finally { current.close(); }
    await deleteRun(saved.id);
    expect(await listRuns()).toEqual([]);
    const backup = await openDB("checkride-runs", 1);
    expect(await backup.get("runs", saved.id)).toEqual(saved);
    backup.close();
  });

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
    const db = await openDB("nostekon-runs", 1);
    await db.put("runs", { id: "bad", source: "{}", report: null });
    db.close();
    await expect(listRuns()).rejects.toThrow("unreadable");
  });

  it("does not call synthetic or imported data recorded lab evidence", () => {
    expect(evidenceLabel("k3d-postgresql")).toBe("Recorded lab");
    for (const id of ["k3d-ledger-zero-loss", "k3d-ledger-tail-loss", "k3d-ledger-budget-loss"]) {
      expect(evidenceLabel(id)).toBe("Recorded lab");
    }
    expect(evidenceLabel("k3d-unknown")).not.toBe("Recorded lab");
    expect(evidenceLabel("crud-cluster-loss")).toBe("Synthetic sample");
    expect(evidenceLabel("")).toBe("Imported evidence");
  });
});