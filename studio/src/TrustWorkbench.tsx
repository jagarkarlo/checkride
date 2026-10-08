import { Download, KeyRound, LoaderCircle, ShieldAlert, ShieldCheck, Trash2, Upload, X } from "lucide-react";
import { strToU8, zipSync } from "fflate";
import { useEffect, useRef, useState } from "react";
import { request } from "./api";
import { announcePolicyChange, assertPublicKey, deleteKey, KEY_LIMIT, listKeys, saveKey, setKeyTrust } from "./keyStore";
import type { SavedKey } from "./keyStore";

interface SignatureCheck {
  apiVersion: "nostekon/signature-check/v1alpha1";
  signatureValid: true;
  keyId: string;
  evidenceSHA256: string;
  attestationVersion: string;
  trustSource: "selected-public-key";
}

export function TrustWorkbench({ evidence, attestation, importedKey, disabled, onAttach, onRemove, onServerVerify }: {
  evidence: string; attestation: string; importedKey?: { id: string }; disabled: boolean; onAttach: () => void; onRemove: () => void; onServerVerify?: () => void;
}) {
  const [keys, setKeys] = useState<SavedKey[]>([]);
  const [selected, setSelected] = useState("");
  const [label, setLabel] = useState("Operator key");
  const [error, setError] = useState("");
  const [proof, setProof] = useState<SignatureCheck | null>(null);
  const [busy, setBusy] = useState(false);
  const [pending, setPending] = useState<{ key: SavedKey; action: "trust" | "delete" } | null>(null);
  const [confirmed, setConfirmed] = useState(false);
  const dialog = useRef<HTMLDialogElement>(null);
  const keyInput = useRef<HTMLInputElement>(null);
  const generation = useRef(0);
  const chosen = keys.find(key => key.id === selected);

  async function refresh() { setKeys(await listKeys()); }
  useEffect(() => {
    let active = true;
    const reloadPolicy = () => {
      generation.current++; setProof(null);
      void listKeys().then(saved => { if (active) setKeys(saved); }).catch(reason => { if (active) setError(message(reason)); });
    };
    const channel = typeof BroadcastChannel === "undefined" ? null : new BroadcastChannel("nostekon-key-policy");
    if (channel) channel.onmessage = reloadPolicy;
    window.addEventListener("focus", reloadPolicy);
    reloadPolicy();
    return () => { active = false; generation.current++; channel?.close(); window.removeEventListener("focus", reloadPolicy); };
  }, []);
  useEffect(() => { generation.current++; setProof(null); setError(""); }, [evidence, attestation, selected, disabled]);
  useEffect(() => {
    if (!importedKey) return;
    let active = true;
    generation.current++; setProof(null); setError("");
    void listKeys().then(saved => {
      if (active) { setKeys(saved); setSelected(importedKey.id); }
    }).catch(reason => { if (active) setError(message(reason)); });
    return () => { active = false; };
  }, [importedKey]);

  async function importKey(file: File) {
    generation.current++;
    setBusy(true); setError(""); setProof(null);
    try {
      if (file.size > 16 * 1024) throw new Error("Public key exceeds the 16 KiB limit.");
      const pem = await file.text();
      assertPublicKey(pem);
      const response = await request("/api/v1/attestations/key", pem, undefined, { "Content-Type": "application/x-pem-file" });
      const result = await response.json();
      if (!response.ok || result.algorithm !== "Ed25519" || typeof result.keyId !== "string" || !/^[a-f0-9]{64}$/.test(result.keyId)) throw new Error(result.errors?.join("; ") || "Could not inspect this public key.");
      await saveKey(result.keyId, pem, label);
      announcePolicyChange();
      await refresh(); setSelected(result.keyId);
    } catch (reason) { setError(message(reason)); }
    finally { setBusy(false); }
  }

  async function changeTrust(key: SavedKey, trusted: boolean) {
    generation.current++; setProof(null); setBusy(true); setError("");
    try { await setKeyTrust(key.id, trusted); announcePolicyChange(); await refresh(); setPending(null); }
    catch (reason) { setError(message(reason)); }
    finally { setBusy(false); }
  }

  async function removeKey(key: SavedKey) {
    generation.current++; setProof(null); setBusy(true); setError("");
    try { await deleteKey(key.id); announcePolicyChange(); await refresh(); if (selected === key.id) setSelected(""); setPending(null); }
    catch (reason) { setError(message(reason)); }
    finally { setBusy(false); }
  }

  async function verifySignature() {
    if (!chosen?.trusted || disabled || !attestation || busy) return;
    const current = ++generation.current;
    setBusy(true); setError(""); setProof(null);
    try {
      const key = (await listKeys()).find(key => key.id === selected);
      if (!key?.trusted) throw new Error("Selected key is no longer trusted in this browser.");
      assertPublicKey(key.pem);
      const encode = (text: string) => btoa(String.fromCharCode(...new TextEncoder().encode(text)));
      const response = await request("/api/v1/attestations/verify", evidence, AbortSignal.timeout(10000), {
        "X-Nostekon-Attestation": encode(attestation), "X-Nostekon-Public-Key": encode(key.pem),
      });
      const result = await response.json();
      if (!response.ok) throw new Error(result.errors?.join("; ") || `Signature check failed (HTTP ${response.status}).`);
      if (result.apiVersion !== "nostekon/signature-check/v1alpha1" || result.signatureValid !== true || result.keyId !== key.id || result.trustSource !== "selected-public-key" || typeof result.evidenceSHA256 !== "string" || !/^[a-f0-9]{64}$/.test(result.evidenceSHA256) || !["nostekon/attestation/v1alpha1", "checkride/attestation/v1alpha1"].includes(result.attestationVersion)) throw new Error("Invalid signature-check response.");
      const latestKey = (await listKeys()).find(saved => saved.id === key.id);
      if (!latestKey?.trusted || latestKey.pem !== key.pem) throw new Error("Selected key changed. Verify the signature again.");
      if (current === generation.current) setProof(result);
    } catch (reason) { if (current === generation.current) setError(message(reason)); }
    finally { setBusy(false); }
  }

  async function exportOriginals() {
    if (!proof || !chosen?.trusted || disabled || busy) return;
    const current = generation.current;
    setBusy(true); setError("");
    try {
      const currentKey = (await listKeys()).find(key => key.id === chosen.id);
      if (current !== generation.current) return;
      if (!currentKey?.trusted || currentKey.pem !== chosen.pem) { setProof(null); throw new Error("Selected key changed. Verify the signature again."); }
      const entries = {
        "nostekon.run.json": strToU8(evidence), "nostekon.run.attestation.json": strToU8(attestation),
        "public-key.pem": strToU8(chosen.pem), "signature-check.json": strToU8(JSON.stringify(proof, null, 2) + "\n"),
      };
      const archive = zipSync(entries, { level: 0 });
      const link = document.createElement("a");
      link.href = URL.createObjectURL(new Blob([new Uint8Array(archive).buffer], { type: "application/zip" }));
      link.download = `nostekon-signed-${proof.evidenceSHA256.slice(0, 12)}.zip`; link.click();
      window.setTimeout(() => URL.revokeObjectURL(link.href), 1000);
    } catch (reason) { setError(message(reason)); }
    finally { setBusy(false); }
  }

  function downloadKey(key: SavedKey) {
    const link = document.createElement("a");
    link.href = URL.createObjectURL(new Blob([key.pem], { type: "application/x-pem-file" })); link.download = `${key.id.slice(0, 12)}.public.pem`; link.click();
    window.setTimeout(() => URL.revokeObjectURL(link.href), 1000);
  }

  return <section className="trust-workbench" aria-labelledby="trust-heading">
    <div className="section-toolbar"><h2 id="trust-heading"><ShieldCheck size={17} /> Evidence trust</h2><button className="tool" type="button" disabled={busy} onClick={() => { generation.current++; setProof(null); setPending(null); void refresh().catch(reason => setError(message(reason))); dialog.current?.showModal(); }}><KeyRound size={15} /> Public keys</button></div>
    <div className="trust-controls">
      <button className="tool" type="button" disabled={disabled || busy} onClick={onAttach}><Upload size={15} /> {attestation ? "Replace attestation" : "Attach attestation"}</button>
      {attestation && <button className="icon-button" type="button" disabled={busy} title="Remove attestation" aria-label="Remove attestation" onClick={onRemove}><X size={16} /></button>}
      <select aria-label="Verification public key" value={selected} disabled={busy} onChange={event => setSelected(event.target.value)}><option value="">Select a public key</option>{keys.map(key => <option key={key.id} value={key.id}>{key.label} · {key.trusted ? "trusted" : "not trusted"}</option>)}</select>
      <button className="primary" type="button" disabled={!chosen?.trusted || !attestation || disabled || busy} onClick={() => void verifySignature()}>{busy ? <LoaderCircle className="spin" size={15} /> : <ShieldCheck size={15} />} Verify signature</button>
      {onServerVerify && <button className="tool" type="button" disabled={!attestation || disabled || busy} onClick={onServerVerify}><ShieldCheck size={15} /> Check server trust</button>}
      <button className="icon-button" type="button" disabled={!proof || !chosen?.trusted || disabled || busy} title="Download signed originals" aria-label="Download signed originals" onClick={() => void exportOriginals()}><Download size={16} /></button>
    </div>
    {proof && chosen?.trusted && !disabled && <div className="banner ok trust-result" role="status" aria-label="Signature verification"><ShieldCheck size={18} /><span><strong>Signature valid</strong> · locally trusted key: {chosen.label}<small className="mono">{proof.keyId}</small><small>Byte binding to the selected key. Capture authenticity is not established.</small></span></div>}
    {error && <p className="banner bad" role="alert"><ShieldAlert size={16} />{error}</p>}
    <dialog ref={dialog} className="trust-dialog" aria-labelledby="keys-heading" onCancel={() => setPending(null)}>
      <div className="section-toolbar"><h2 id="keys-heading">Public keys <span>{keys.length} / {KEY_LIMIT}</span></h2><button className="icon-button" type="button" title="Close public keys" aria-label="Close public keys" onClick={() => { dialog.current?.close(); setPending(null); }}><X size={18} /></button></div>
      <div className="trust-import"><label>Label<input aria-label="Public key label" maxLength={80} value={label} disabled={busy} onChange={event => setLabel(event.target.value)} /></label><button className="tool" type="button" disabled={busy || !label.trim()} onClick={() => keyInput.current?.click()}><Upload size={15} /> Import public key</button></div>
      <input ref={keyInput} type="file" accept=".pem,application/x-pem-file" hidden data-testid="public-key-input" onChange={event => { const file = event.target.files?.[0]; event.target.value = ""; if (file) void importKey(file); }} />
      {error && <p className="banner bad" role="alert">{error}</p>}
      {pending && <section className="trust-decision" aria-label="Public key decision"><h3>{pending.action === "trust" ? "Trust this public key?" : "Delete this public key?"}</h3><p>{pending.key.label}</p><p className="mono">{pending.key.id}</p>{pending.action === "trust" ? <label><input type="checkbox" checked={confirmed} onChange={event => setConfirmed(event.target.checked)} /> I verified this fingerprint with the key owner through a trusted channel.</label> : <p>Original evidence remains saved. This key will no longer be available for local verification.</p>}<div className="library-actions"><button className="tool" type="button" disabled={busy} onClick={() => setPending(null)}>Cancel</button><button className="primary" type="button" disabled={busy || (pending.action === "trust" && !confirmed)} onClick={() => void (pending.action === "trust" ? changeTrust(pending.key, true) : removeKey(pending.key))}>{pending.action === "trust" ? "Confirm trust" : "Delete key"}</button></div></section>}
      {keys.length === 0 ? <p className="library-empty">No public keys yet</p> : <div className="table-scroll"><table className="runs-table trust-key-table"><thead><tr><th>Key</th><th>Local policy</th><th aria-label="Actions" /></tr></thead><tbody>{keys.map(key => <tr key={key.id}><th scope="row"><strong>{key.label}</strong><small className="mono">{key.id}</small></th><td>{key.trusted ? "Trusted locally" : "Not trusted"}</td><td><button className="icon-button" type="button" disabled={busy || !!pending} title={key.trusted ? "Revoke local trust" : "Trust public key"} aria-label={key.trusted ? "Revoke local trust" : "Trust public key"} onClick={() => { if (key.trusted) void changeTrust(key, false); else { setPending({ key, action: "trust" }); setConfirmed(false); } }}>{key.trusted ? <ShieldAlert size={16} /> : <ShieldCheck size={16} />}</button><button className="icon-button" type="button" disabled={busy} title="Download public key" aria-label="Download public key" onClick={() => downloadKey(key)}><Download size={16} /></button><button className="icon-button" type="button" disabled={busy || !!pending} title="Delete public key" aria-label="Delete public key" onClick={() => setPending({ key, action: "delete" })}><Trash2 size={16} /></button></td></tr>)}</tbody></table></div>}
    </dialog>
  </section>;
}

function message(reason: unknown) { return reason instanceof Error ? reason.message : "Could not complete the public-key operation."; }
