import { unzipSync } from "fflate";
import { assertPublicKey } from "./keyStore";

export const MAX_SIGNED_ARCHIVE_BYTES = 17 * 1024 * 1024;
export interface SignedOriginals { evidence: string; attestation: string; publicKey: string }
const names = ["nostekon.run.json", "nostekon.run.attestation.json", "public-key.pem", "signature-check.json"];

export function readSignedArchive(data: Uint8Array): SignedOriginals {
  if (data.byteLength > MAX_SIGNED_ARCHIVE_BYTES) throw new Error("Signed archive exceeds the 17 MiB limit.");
  const seen = new Set<string>();
  const files = unzipSync(data, { filter: entry => {
    if (!names.includes(entry.name)) throw new Error(`Unexpected signed archive entry: ${entry.name}`);
    if (seen.has(entry.name)) throw new Error(`Duplicate signed archive entry: ${entry.name}`);
    seen.add(entry.name);
    const limit = entry.name === "nostekon.run.json" ? 16 * 1024 * 1024 : 16 * 1024;
    if (entry.compression !== 0) throw new Error("Only uncompressed Nostekon signed archives are supported.");
    if (entry.originalSize > limit || entry.size > limit || entry.originalSize !== entry.size) throw new Error(`${entry.name} exceeds its limit or has inconsistent sizes.`);
    return true;
  } });
  const decoder = new TextDecoder("utf-8", { fatal: true, ignoreBOM: true });
  const text = new Map<string, string>();
  for (const name of names) {
    if (!files[name]) throw new Error(`Missing signed archive entry: ${name}`);
    text.set(name, decoder.decode(files[name]));
  }
  const publicKey = text.get("public-key.pem")!;
  assertPublicKey(publicKey);
  for (const name of names.filter(name => name.endsWith(".json"))) {
    const value: unknown = JSON.parse(text.get(name)!);
    if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error(`${name} must contain a JSON object.`);
  }
  return { evidence: text.get("nostekon.run.json")!, attestation: text.get("nostekon.run.attestation.json")!, publicKey };
}