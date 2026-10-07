import { ArrowRight, CheckCircle2, Download, FileArchive, FlaskConical, LoaderCircle, RefreshCw, Save, ShieldAlert, Upload, XCircle } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { request } from "./api";
import { maxBundleBytes, readEvidenceBundle } from "./evidenceBundle";
import { evaluateSuite, parseSuite } from "./labSuite";
import type { SuiteReview } from "./labSuite";
import { formatDuration, isReport } from "./report";
import { saveRuns } from "./runStore";

const recordedFiles = import.meta.glob<string>("../../examples/suites/postgresql-policy/*.json", { query: "?raw", import: "default" });
const labels = { "zero-loss": "Zero loss", "tail-loss": "Strict tail loss", "budget-loss": "Budgeted tail loss" };

export function SuiteView({ active, selection, onReachability, onOpen }: {
  active: boolean;
  selection?: Map<string, string>;
  onReachability: (online: boolean) => void;
  onOpen: (source: string) => void;
}) {
  const [review, setReview] = useState<SuiteReview | null>(null);
  const [summary, setSummary] = useState("");
  const [recorded, setRecorded] = useState(false);
  const [busy, setBusy] = useState(false);
  const [saving, setSaving] = useState(false);
  const [savedCount, setSavedCount] = useState(0);
  const [error, setError] = useState("");
  const input = useRef<HTMLInputElement>(null);
  const bundleInput = useRef<HTMLInputElement>(null);
  const generation = useRef(0);
  const initialized = useRef(false);
  const controller = useRef<AbortController | null>(null);

  async function load(files: () => Promise<Map<string, string>>, isRecorded: boolean) {
    const current = ++generation.current;
    controller.current?.abort();
    const abort = new AbortController();
    controller.current = abort;
    setBusy(true);
    setError("");
    setSavedCount(0);
    setReview(null);
    setSummary("");
    setRecorded(isRecorded);
    try {
      const sources = await files();
      if (current !== generation.current) return;
      const text = sources.get("suite.json");
      if (text === undefined) throw new Error("Missing suite.json.");
      const suite = parseSuite(text);
      const result = await evaluateSuite(suite, sources, async (source) => {
        let response: Response;
        try {
          response = await request("/api/v1/runs/report", source, AbortSignal.any([abort.signal, AbortSignal.timeout(10000)]));
          if (current === generation.current) onReachability(true);
        } catch (reason) {
          if (current === generation.current && !abort.signal.aborted) onReachability(false);
          throw reason;
        }
        const payload: unknown = await response.json();
        if (!response.ok || !isReport(payload)) throw new Error(`Evidence evaluation failed (HTTP ${response.status}).`);
        return payload;
      });
      if (current !== generation.current) return;
      setReview(result);
      setSummary(text);
    } catch (reason) {
      if (current === generation.current) setError(reason instanceof Error ? reason.message : "Could not review the suite.");
    } finally {
      if (current === generation.current) setBusy(false);
    }
  }

  function loadRecorded() {
    return load(async () => new Map(await Promise.all(Object.entries(recordedFiles).map(async ([path, read]) => [path.split("/").pop()!, await read()] as const))), true);
  }

  useEffect(() => {
    if (active && !initialized.current) {
      initialized.current = true;
      void loadRecorded();
    }
  }, [active]);

  useEffect(() => {
    if (selection) { initialized.current = true; void load(async () => selection, false); }
  }, [selection]);

  useEffect(() => () => { generation.current++; controller.current?.abort(); }, []);

  const canSave = !!review?.cases.length && review.cases.every((item) => item.report && item.source !== undefined);

  async function saveCases() {
    if (!review || !canSave || busy || saving) return;
    const current = generation.current;
    setSaving(true);
    setSavedCount(0);
    setError("");
    try {
      const saved = await saveRuns(review.cases.map((item) => ({ source: item.source!, report: item.report! })));
      if (current === generation.current) setSavedCount(saved.length);
    } catch (reason) {
      if (current === generation.current) setError(reason instanceof Error ? reason.message : "Browser storage is unavailable.");
    } finally { setSaving(false); }
  }

  function downloadSummary() {
    const link = document.createElement("a");
    link.href = URL.createObjectURL(new Blob([summary], { type: "application/json" }));
    link.download = "suite.json";
    link.click();
    window.setTimeout(() => URL.revokeObjectURL(link.href), 1000);
  }

  return <main className="run-library suite-workspace">
    <header className="library-heading">
      <div><p className="workspace-label"><FlaskConical size={14} /> PostgreSQL lab</p><h1>Policy suite</h1></div>
      <div className="library-actions">
        <button className="tool" type="button" disabled={saving} onClick={() => void loadRecorded()}><RefreshCw size={15} /> Recorded suite</button>
        <button className="icon-button" type="button" title="Download original suite summary" aria-label="Download original suite summary" disabled={!summary || busy} onClick={downloadSummary}><Download size={16} /></button>
        <button className="tool" type="button" disabled={!canSave || busy || saving} onClick={() => void saveCases()}>{saving ? <LoaderCircle className="spin" size={15} /> : <Save size={15} />} {saving ? "Saving..." : "Save cases"}</button>
        <button className="primary" type="button" disabled={saving} onClick={() => input.current?.click()}><Upload size={15} /> Import suite</button>
        <button className="tool" type="button" disabled={saving} onClick={() => bundleInput.current?.click()}><FileArchive size={15} /> Import bundle</button>
      </div>
    </header>
    <input ref={input} type="file" accept=".json,application/json" multiple hidden disabled={saving} data-testid="suite-input" onChange={(event) => {
      const files = Array.from(event.target.files ?? []);
      event.target.value = "";
      if (!files.length) return;
      void load(async () => {
        if (files.length > 4) throw new Error("A suite import is limited to four JSON files.");
        if (new Set(files.map((file) => file.name)).size !== files.length) throw new Error("Duplicate filenames in suite import.");
        for (const file of files) {
          const limit = file.name === "suite.json" ? 64 * 1024 : 16 * 1024 * 1024;
          if (file.size > limit) throw new Error(`${file.name} exceeds the ${file.name === "suite.json" ? "64 KiB" : "16 MiB"} limit.`);
        }
        return new Map(await Promise.all(files.map(async (file) => [file.name, await file.text()] as const)));
      }, false);
    }} />
    <input ref={bundleInput} type="file" accept=".zip,application/zip" hidden disabled={saving} data-testid="suite-bundle-input" onChange={(event) => {
      const file = event.target.files?.[0];
      event.target.value = "";
      if (!file) return;
      void load(async () => {
        if (file.size > maxBundleBytes) throw new Error("Evidence bundle exceeds the 49 MiB limit.");
        return readEvidenceBundle(new Uint8Array(await file.arrayBuffer()));
      }, false);
    }} />
    {error && <p className="banner bad" role="alert"><XCircle size={16} /> {error}</p>}
    {savedCount > 0 && <p className="banner ok" role="status" aria-label="Suite save result"><CheckCircle2 size={16} /> {savedCount} {savedCount === 1 ? "case" : "cases"} saved to Runs.</p>}
    {busy && <p className="suite-loading" role="status"><LoaderCircle className="spin" size={18} /> Evaluating suite evidence...</p>}
    {review && <>
      <section className="library-stats suite-stats" aria-label="Suite review totals">
        <div><span>Runner result</span><strong className={review.suite.passed ? "ok" : "attention"}>{review.suite.status}</strong></div>
        <div><span>Cases captured</span><strong>{review.cases.length}<small> / 3</small></strong></div>
        <div><span>Reports evaluated</span><strong>{review.cases.filter((item) => item.report).length}</strong></div>
        <div><span>Matching summaries</span><strong>{review.cases.filter((item) => item.issues.length === 0).length}</strong></div>
      </section>
      <div className={`suite-agreement ${review.evidenceMatches ? "ok" : "attention"}`} role="status" aria-label="Suite evidence agreement">
        {review.evidenceMatches ? <CheckCircle2 size={18} /> : <ShieldAlert size={18} />}
        <strong>{review.evidenceMatches ? "Evidence matches the summary" : "Evidence needs attention"}</strong>
        <span>{recorded ? "Recorded local lab" : "Imported evidence"} · signatures unverified</span>
      </div>
      <section aria-labelledby="suite-cases-heading">
        <div className="section-toolbar"><h2 id="suite-cases-heading">Policy outcomes</h2><span className="workspace-label">RPO objectives differ by case</span></div>
        <div className="table-scroll"><table className="runs-table suite-table">
          <thead><tr><th>Case</th><th>Expected verdict</th><th>Evaluated verdict</th><th>Recovered</th><th>Lost</th><th>Loss window</th><th>RPO budget</th><th>Recovery time</th><th>Summary</th><th aria-label="Actions" /></tr></thead>
          <tbody>{review.cases.map((item) => <tr key={item.recorded.name}>
            <th scope="row"><strong>{labels[item.recorded.name]}</strong><small>{item.report?.name ?? item.recorded.drillRun}</small></th>
            <td data-label="Expected verdict"><span className={`verdict-tag ${item.recorded.expectedExitCode === 0 ? "verified" : "failed"}`}>{item.recorded.expectedExitCode === 0 ? "verified" : "failed"}</span></td>
            <td data-label="Evaluated verdict"><span className={`verdict-tag ${item.report?.verdict ?? "incomplete"}`}>{item.report?.verdict ?? "unavailable"}</span></td>
            <td data-label="Recovered" className="mono">{item.report?.rpo?.recovered ?? "Unmeasured"}</td>
            <td data-label="Lost" className="mono">{item.report?.rpo?.lost ?? "Unmeasured"}</td>
            <td data-label="Loss window" className="mono">{item.report?.rpo ? `${Number(item.report.rpo.seconds.toFixed(6))}s` : "Unmeasured"}</td>
            <td data-label="RPO budget" className="mono">{formatDuration(item.report?.rpo?.objectiveSeconds)}</td>
            <td data-label="Recovery time" className="mono">{formatDuration(item.report?.rto?.seconds)}</td>
            <td data-label="Summary" className={item.issues.length === 0 ? "ok" : "attention"}>{item.issues.length === 0 ? "Matches" : "Mismatch"}</td>
            <td className="suite-row-action"><button className="icon-button" type="button" title={`Open ${labels[item.recorded.name]} report`} aria-label={`Open ${labels[item.recorded.name]} report`} disabled={item.source === undefined} onClick={() => onOpen(item.source!)}><ArrowRight size={17} /></button></td>
          </tr>)}</tbody>
        </table></div>
      </section>
      {review.cases.some((item) => item.issues.length > 0) && <section className="suite-findings" aria-label="Suite findings">
        <h2>Review findings</h2>
        {review.cases.filter((item) => item.issues.length > 0).map((item) => <div key={item.recorded.name}><h3>{labels[item.recorded.name]}</h3><ul>{item.issues.map((issue) => <li key={issue}>{issue}</li>)}</ul>{item.recorded.detail && <p>{item.recorded.detail}</p>}</div>)}
      </section>}
    </>}
  </main>;
}