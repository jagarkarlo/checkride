import { unzipSync } from "fflate";

export const maxBundleBytes = 49 * 1024 * 1024;
const artifacts = ["suite.json", "zero-loss.drillrun.json", "tail-loss.drillrun.json", "budget-loss.drillrun.json"];
const decoder = new TextDecoder("utf-8", { fatal: true, ignoreBOM: true });

function record(value: unknown): Record<string, unknown> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) throw new Error("Invalid evidence bundle manifest.");
  return value as Record<string, unknown>;
}

export async function readEvidenceBundle(data: Uint8Array): Promise<Map<string, string>> {
  if (data.byteLength > maxBundleBytes) throw new Error("Evidence bundle exceeds the 49 MiB limit.");
  if (!globalThis.crypto?.subtle) throw new Error("Evidence bundle verification requires HTTPS or localhost.");
  const entries = new Set<string>();
  const files = unzipSync(data, { filter: file => {
    if (file.name !== "manifest.json" && !artifacts.includes(file.name)) throw new Error(`Unexpected bundle entry: ${file.name}`);
    if (entries.has(file.name)) throw new Error(`Duplicate bundle entry: ${file.name}`);
    entries.add(file.name);
    const limit = file.name === "manifest.json" || file.name === "suite.json" ? 64 * 1024 : 16 * 1024 * 1024;
    if (file.compression !== 0) throw new Error("Only uncompressed Nostekon evidence bundles are supported.");
    if (file.originalSize > limit || file.size > limit || file.originalSize !== file.size) throw new Error(`${file.name} exceeds its evidence limit or has inconsistent sizes.`);
    return true;
  } });
  if (!files["manifest.json"]) throw new Error("Missing evidence bundle manifest.json.");
  const manifest = record(JSON.parse(decoder.decode(files["manifest.json"])));
  const job = record(manifest.job);
  if (manifest.apiVersion !== "nostekon/evidence-bundle/v1alpha1" || manifest.kind !== "LabEvidenceBundle" || typeof job.id !== "string" || !/^[a-f0-9]{24}$/.test(job.id) || !["completed", "failed", "cancelled", "timed_out", "interrupted"].includes(String(job.status)) || typeof job.completedAt !== "string" || !Number.isFinite(Date.parse(job.completedAt)) || job.recoveryRequired === true || !Array.isArray(manifest.files) || manifest.files.length > 4 || !Array.isArray(manifest.missingArtifacts) || manifest.missingArtifacts.length > 4) throw new Error("Invalid or unresolved evidence bundle manifest.");
  const declared = new Set<string>();
  const sources = new Map<string, string>();
  for (const value of manifest.files) {
    const entry = record(value);
    if (typeof entry.name !== "string" || !artifacts.includes(entry.name) || declared.has(entry.name) || !Number.isSafeInteger(entry.size) || typeof entry.sha256 !== "string" || !/^[a-f0-9]{64}$/.test(entry.sha256)) throw new Error("Invalid or duplicate evidence checksum entry.");
    declared.add(entry.name);
    const bytes = files[entry.name];
    if (!bytes || bytes.length !== entry.size) throw new Error(`Missing evidence or size mismatch: ${entry.name}`);
    const digest = await crypto.subtle.digest("SHA-256", Uint8Array.from(bytes));
    const checksum = Array.from(new Uint8Array(digest), byte => byte.toString(16).padStart(2, "0")).join("");
    if (checksum !== entry.sha256) throw new Error(`Evidence checksum mismatch: ${entry.name}`);
    sources.set(entry.name, decoder.decode(bytes));
  }
  for (const name of manifest.missingArtifacts) {
    if (typeof name !== "string" || !artifacts.includes(name) || declared.has(name) || files[name] !== undefined) throw new Error("Invalid missing-artifact declaration.");
    declared.add(name);
  }
  if (declared.size !== artifacts.length || Object.keys(files).length !== sources.size + 1) throw new Error("Evidence bundle has undeclared or missing entries.");
  if (!sources.has("suite.json")) throw new Error("No suite.json in this partial evidence bundle.");
  return sources;
}