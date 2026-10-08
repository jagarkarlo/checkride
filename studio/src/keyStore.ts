import { openDB } from "idb";
import type { DBSchema } from "idb";

export interface SavedKey { id: string; pem: string; label: string; trusted: boolean }
interface KeyDatabase extends DBSchema { keys: { key: string; value: SavedKey } }
export const KEY_LIMIT = 20;

export function announcePolicyChange() {
  if (typeof BroadcastChannel === "undefined") return;
  const channel = new BroadcastChannel("nostekon-key-policy");
  channel.postMessage("changed"); channel.close();
}

export function assertPublicKey(pem: string): void {
  if (typeof pem !== "string" || new TextEncoder().encode(pem).length > 16 * 1024 || !/^\s*-----BEGIN PUBLIC KEY-----\s+[A-Za-z0-9+/=\r\n]+\s+-----END PUBLIC KEY-----\s*$/.test(pem)) throw new Error("Select one public PKIX PEM key, at most 16 KiB. Private keys are not accepted.");
}

function validate(key: SavedKey) {
  if (!key || !/^[a-f0-9]{64}$/.test(key.id) || typeof key.label !== "string" || !key.label.trim() || key.label.length > 80 || typeof key.trusted !== "boolean") throw new Error("Public-key record is unreadable.");
  assertPublicKey(key.pem);
}

function database() {
  return openDB<KeyDatabase>("nostekon-trusted-keys", 1, { upgrade(db) { db.createObjectStore("keys", { keyPath: "id" }); } });
}

export async function listKeys(): Promise<SavedKey[]> {
  const db = await database();
  try {
    const keys = await db.getAll("keys");
    keys.forEach(validate);
    return keys.sort((left, right) => left.label.localeCompare(right.label));
  } finally { db.close(); }
}

export async function saveKey(id: string, pem: string, label: string): Promise<SavedKey> {
  const key = { id, pem, label: label.trim(), trusted: false };
  validate(key);
  const db = await database();
  try {
    const transaction = db.transaction("keys", "readwrite");
    try {
      const existing = await transaction.store.get(id);
      if (existing) { validate(existing); key.trusted = existing.trusted; }
      else if (await transaction.store.count() >= KEY_LIMIT) throw new Error(`The library holds ${KEY_LIMIT} public keys. Delete a key before importing another.`);
      await transaction.store.put(key);
      await transaction.done;
      return key;
    } catch (reason) {
      try { transaction.abort(); } catch {}
      await transaction.done.catch(() => undefined);
      throw reason;
    }
  } finally { db.close(); }
}

export async function setKeyTrust(id: string, trusted: boolean): Promise<void> {
  const db = await database();
  try {
    const transaction = db.transaction("keys", "readwrite");
    try {
      const key = await transaction.store.get(id);
      if (!key) throw new Error("Public key not found.");
      validate(key);
      if (typeof trusted !== "boolean") throw new Error("Invalid trust decision.");
      await transaction.store.put({ ...key, trusted });
      await transaction.done;
    } catch (reason) {
      try { transaction.abort(); } catch {}
      await transaction.done.catch(() => undefined);
      throw reason;
    }
  } finally { db.close(); }
}

export async function deleteKey(id: string): Promise<void> {
  const db = await database();
  try { await db.delete("keys", id); }
  finally { db.close(); }
}