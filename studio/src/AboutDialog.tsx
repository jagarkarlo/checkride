import { RefreshCw, X } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { browserDemo, request } from "./api";

interface AppInfo {
  build: { version: string; revision: string; goVersion: string; platform: string; modified: boolean };
  capabilities: { studio: boolean; labExecution: boolean; signatureVerification: boolean };
}

export function AboutDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const dialog = useRef<HTMLDialogElement>(null);
  const [info, setInfo] = useState<AppInfo>();
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    if (open) dialog.current?.showModal(); else dialog.current?.close();
  }, [open]);
  useEffect(() => {
    if (!open) return;
    let active = true;
    const controller = new AbortController();
    const timeout = window.setTimeout(() => controller.abort(), 10000);
    setInfo(undefined); setError(""); setLoading(true);
    void request("/api/v1/info", "", controller.signal).then(async response => {
      if (!response.ok) throw new Error(`App information unavailable (HTTP ${response.status}).`);
      const value: AppInfo = await response.json();
      if (!value?.build || !value?.capabilities || ![value.build.version, value.build.revision, value.build.goVersion, value.build.platform].every(text => typeof text === "string" && text.length <= 128) || ![value.build.modified, value.capabilities.studio, value.capabilities.labExecution, value.capabilities.signatureVerification].every(flag => typeof flag === "boolean")) throw new Error("App information response is invalid.");
      if (active) setInfo(value);
    }).catch(reason => { if (active) setError(reason instanceof Error ? reason.message : "App information is unavailable."); })
      .finally(() => { if (active) setLoading(false); window.clearTimeout(timeout); });
    return () => { active = false; controller.abort(); window.clearTimeout(timeout); };
  }, [open, attempt]);
  return <dialog ref={dialog} className="delete-dialog about-dialog" aria-labelledby="about-title" onCancel={onClose}>
    <div className="section-toolbar"><h2 id="about-title">About Nostekon</h2><button type="button" className="icon-button" aria-label="Close app information" title="Close app information" onClick={onClose}><X size={18} /></button></div>
    {loading && <p role="status">Loading app information...</p>}
    {error && <p className="banner bad" role="alert">{error}</p>}
    {info && <dl className="app-info">
      <div><dt>Version</dt><dd>{info.build.version}{info.build.modified ? " (modified source)" : ""}</dd></div>
      <div><dt>Revision</dt><dd className="mono">{info.build.revision}</dd></div>
      <div><dt>Engine</dt><dd>{browserDemo ? "Go in this browser" : "Local Go API"}</dd></div>
      <div><dt>Runtime</dt><dd>{info.build.goVersion} · {info.build.platform}</dd></div>
      <div><dt>Lab execution</dt><dd>{info.capabilities.labExecution ? "Enabled" : "Disabled"}</dd></div>
      <div><dt>Signature verification</dt><dd>{info.capabilities.signatureVerification ? "Available" : "Unavailable"}</dd></div>
      <div><dt>Browser storage origin</dt><dd className="mono">{location.origin}</dd></div>
    </dl>}
    <div className="library-actions"><button type="button" className="tool" disabled={loading} onClick={() => setAttempt(attempt + 1)}><RefreshCw size={15} /> Refresh app information</button></div>
  </dialog>;
}