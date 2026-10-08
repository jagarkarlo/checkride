import "fake-indexeddb/auto";
import { deleteDB, openDB } from "idb";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { assertPublicKey, deleteKey, KEY_LIMIT, listKeys, saveKey, setKeyTrust } from "./keyStore";

const pem = "-----BEGIN PUBLIC KEY-----\nMCowBQYDK2VwAyEAaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa=\n-----END PUBLIC KEY-----\n";
beforeEach(async () => { await deleteDB("nostekon-trusted-keys"); });

describe("local public-key policy", () => {
  it("imports untrusted, deduplicates, retains revocation and deletes independently", async () => {
    const id = "a".repeat(64);
    await saveKey(id, pem, "Operator");
    expect((await listKeys())[0]).toMatchObject({ id, label: "Operator", trusted: false });
    await setKeyTrust(id, true);
    await saveKey(id, pem, "Renamed operator");
    expect(await listKeys()).toHaveLength(1);
    expect((await listKeys())[0]).toMatchObject({ label: "Renamed operator", trusted: true });
    await setKeyTrust(id, false);
    await saveKey(id, pem, "Operator");
    expect((await listKeys())[0].trusted).toBe(false);
    await deleteKey(id);
    expect(await listKeys()).toEqual([]);
  });

  it("rejects private keys, multiple blocks, trailing material and byte oversize before storage", async () => {
    for (const invalid of [pem.replaceAll("PUBLIC KEY", "PRIVATE KEY"), pem + pem, pem + "junk", "\u00e9".repeat(9 * 1024)]) {
      expect(() => assertPublicKey(invalid)).toThrow();
      await expect(saveKey("a".repeat(64), invalid, "Operator")).rejects.toThrow();
    }
    expect(await listKeys()).toEqual([]);
  });

  it("keeps a bounded library without eviction and allows existing-key updates at capacity", async () => {
    for (let index = 0; index < KEY_LIMIT; index++) await saveKey(index.toString(16).padStart(64, "0"), pem, `Operator ${index}`);
    await expect(saveKey("f".repeat(64), pem, "Another")).rejects.toThrow("Delete a key");
    await saveKey("0".repeat(64), pem, "Updated");
    expect(await listKeys()).toHaveLength(KEY_LIMIT);
    await expect(setKeyTrust("f".repeat(64), true)).rejects.toThrow("not found");
  });

  it("serializes concurrent final-slot imports without evicting a trusted key", async () => {
    for (let index = 0; index < KEY_LIMIT - 1; index++) await saveKey(index.toString(16).padStart(64, "0"), pem, `Operator ${index}`);
    await setKeyTrust("0".repeat(64), true);
    const results = await Promise.allSettled([saveKey("a".repeat(64), pem, "First"), saveKey("b".repeat(64), pem, "Second")]);
    expect(results.filter(result => result.status === "fulfilled")).toHaveLength(1);
    expect(results.find(result => result.status === "rejected")?.reason.message).toContain("Delete a key");
    expect(await listKeys()).toHaveLength(KEY_LIMIT);
    expect((await listKeys()).find(key => key.id === "0".repeat(64))?.trusted).toBe(true);
  });

  it("rolls back a failed trust write and permits a retry without modifying other databases", async () => {
    const id = "a".repeat(64);
    await saveKey(id, pem, "Operator");
    const evidence = await openDB("nostekon-runs", 2, { upgrade(db) { db.createObjectStore("runs"); } });
    await evidence.put("runs", "original evidence", "original");
    evidence.close();
    const originalPut = IDBObjectStore.prototype.put;
    const failure = vi.spyOn(IDBObjectStore.prototype, "put").mockImplementation(function (this: IDBObjectStore, value, key) {
      if (this.name === "keys") throw new DOMException("Storage full", "QuotaExceededError");
      return originalPut.call(this, value, key);
    });
    try { await expect(setKeyTrust(id, true)).rejects.toThrow("Storage full"); }
    finally { failure.mockRestore(); }
    expect((await listKeys())[0].trusted).toBe(false);
    await setKeyTrust(id, true);
    expect((await listKeys())[0].trusted).toBe(true);
    await deleteKey(id);
    const existing = await openDB("nostekon-runs", 2);
    try { expect(await existing.get("runs", "original")).toBe("original evidence"); }
    finally { existing.close(); await deleteDB("nostekon-runs"); }
  });

  it("surfaces a malformed record without silently pruning it", async () => {
    await listKeys();
    const db = await openDB("nostekon-trusted-keys", 1);
    try { await db.put("keys", { id: "bad", pem, label: "Bad", trusted: true }); }
    finally { db.close(); }
    await expect(listKeys()).rejects.toThrow();
    const retained = await openDB("nostekon-trusted-keys", 1);
    try { expect(await retained.count("keys")).toBe(1); }
    finally { retained.close(); }
  });
});