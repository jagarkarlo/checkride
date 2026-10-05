import { openDB } from "idb";
import type { DBSchema } from "idb";
import { isReport } from "./report";
import type { Report } from "./report";
import { isRecordedSample } from "./samples";

export interface SavedRun {
  id: string;
  source: string;
  report: Report;
  sampleId: string;
  savedAt: number;
}

interface RunDatabase extends DBSchema {
  runs: { key: string; value: SavedRun };
  migration: { key: string; value: boolean };
}

export const RUN_LIMIT = 50;
const SOURCE_LIMIT = 16 * 1024 * 1024;

async function database() {
  const db = await openDB<RunDatabase>("nostekon-runs", 1, {
    upgrade(db) {
      db.createObjectStore("runs", { keyPath: "id" });
      db.createObjectStore("migration");
    },
  });
  try {
    if (await db.get("migration", "legacy-imported")) return db;
    let missing = false;
    const legacy = await openDB<RunDatabase>("checkride-runs", 1, {
      upgrade(_db, _oldVersion, _newVersion, transaction) {
        missing = true;
        void transaction.done.catch(() => undefined);
        transaction.abort();
      },
    }).catch((error: unknown) => {
      if (missing) return null;
      throw error;
    });
    let runs: SavedRun[] = [];
    if (legacy) {
      try { runs = await legacy.getAll("runs"); }
      finally { legacy.close(); }
    }
    const transaction = db.transaction(["runs", "migration"], "readwrite");
    try {
      if (!await transaction.objectStore("migration").get("legacy-imported")) {
        for (const run of runs) {
          if (!await transaction.objectStore("runs").get(run.id)) {
            await transaction.objectStore("runs").put(run);
          }
        }
        await transaction.objectStore("migration").put(true, "legacy-imported");
      }
      await transaction.done;
    } catch (error) {
      try { transaction.abort(); } catch {}
      await transaction.done.catch(() => undefined);
      throw error;
    }
    return db;
  } catch (error) {
    db.close();
    throw error;
  }
}

export async function listRuns(): Promise<SavedRun[]> {
  const db = await database();
  try {
    const runs = await db.getAll("runs");
    if (runs.some((run) => typeof run.source !== "string" || typeof run.id !== "string" || !isReport(run.report))) {
      throw new Error("Saved run data is unreadable. Export available evidence before clearing site storage.");
    }
    return runs.sort((left, right) => right.savedAt - left.savedAt);
  } finally { db.close(); }
}

export async function saveRun(source: string, report: Report, sampleId = ""): Promise<SavedRun> {
  const bytes = new TextEncoder().encode(source);
  if (bytes.length > SOURCE_LIMIT) throw new Error("Evidence exceeds the 16 MiB limit.");
  const digest = await crypto.subtle.digest("SHA-256", bytes);
  const id = Array.from(new Uint8Array(digest), (byte) => byte.toString(16).padStart(2, "0")).join("");
  const run: SavedRun = { id, source, report, sampleId, savedAt: Date.now() };
  const db = await database();
  try {
    const transaction = db.transaction("runs", "readwrite");
    const existing = await transaction.store.get(id);
    if (!existing && await transaction.store.count() >= RUN_LIMIT) {
      await transaction.done;
      throw new Error(`The library holds ${RUN_LIMIT} runs. Delete a run before saving another.`);
    }
    await transaction.store.put(run);
    await transaction.done;
    return run;
  } finally { db.close(); }
}

export async function deleteRun(id: string): Promise<void> {
  const db = await database();
  try { await db.delete("runs", id); }
  finally { db.close(); }
}

export function evidenceLabel(sampleId: string): string {
  return isRecordedSample(sampleId) ? "Recorded lab" : sampleId ? "Synthetic sample" : "Imported evidence";
}