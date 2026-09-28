import {
  AlertTriangle,
  CheckCircle2,
  CircleDashed,
  ClipboardCopy,
  FileJson,
  FileText,
  Info,
  LoaderCircle,
  Play,
  Upload,
  XCircle,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { CodeEditor } from "./CodeEditor";
import type { CodeEditorHandle } from "./CodeEditor";
import { fieldPathOf, inspectJSON, locateField, scenarioLabels } from "./drill";
import { formatDuration, isReport, objectiveUsage, reportMarkdown, timelineBars } from "./report";
import type { LevelResult, Report, RPOResult, RTOResult } from "./report";

const sampleSources = import.meta.glob<string>("../../examples/runs/*.run.json", { query: "?raw", import: "default" });
const sampleInfo: Record<string, { label: string; summary: string }> = {
  "mlflow-namespace-loss": { label: "MLflow namespace loss", summary: "V4 with ledger · verified" },
  "crud-cluster-loss": { label: "CRUD cluster loss", summary: "V3 · row counts fail, RTO missed" },
};
const samples = Object.entries(sampleSources)
  .map(([path, load]) => {
    const id = path.split("/").pop()!.replace(".run.json", "");
    return { id, load, ...(sampleInfo[id] ?? { label: id, summary: "Example run" }) };
  })
  .sort((a, b) => a.label.localeCompare(b.label));

const verdictLabel = { verified: "Verified", failed: "Failed", incomplete: "Incomplete" } as const;

function download(name: string, text: string, type: string) {
  const link = document.createElement("a");
  link.href = URL.createObjectURL(new Blob([text], { type }));
  link.download = name;
  link.click();
  window.setTimeout(() => URL.revokeObjectURL(link.href), 1000);
}

export function ReportView({ onReachability }: { onReachability: (online: boolean) => void }) {
  const [source, setSource] = useState("");
  const [sampleId, setSampleId] = useState("");
  const [sourceError, setSourceError] = useState("");
  const [report, setReport] = useState<Report | null>(null);
  const [builtFrom, setBuiltFrom] = useState("");
  const [problems, setProblems] = useState<string[]>([]);
  const [requestError, setRequestError] = useState("");
  const [isBuilding, setIsBuilding] = useState(false);
  const [tab, setTab] = useState<"report" | "evidence">("report");
  const [copied, setCopied] = useState(false);
  const editorRef = useRef<CodeEditorHandle>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const inspection = useMemo(() => inspectJSON(source), [source]);

  const build = useCallback(
    async (text: string) => {
      setIsBuilding(true);
      setRequestError("");
      try {
        const response = await fetch("/api/v1/runs/report", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: text,
        });
        const payload: unknown = await response.json();
        onReachability(true);
        if (response.ok && isReport(payload)) {
          setReport(payload);
          setBuiltFrom(text);
          setProblems([]);
          setTab("report");
          return;
        }
        const errors = (payload as { errors?: unknown }).errors;
        setProblems(Array.isArray(errors) ? errors.filter((item): item is string => typeof item === "string") : [`HTTP ${response.status}`]);
        setTab("evidence");
      } catch (error) {
        onReachability(false);
        setRequestError(error instanceof Error ? error.message : "Could not reach the Checkride API.");
      } finally {
        setIsBuilding(false);
      }
    },
    [onReachability],
  );

  const loadSample = useCallback(
    async (id: string) => {
      const sample = samples.find((item) => item.id === id);
      if (!sample) return;
      const text = await sample.load();
      setSource(text);
      setSampleId(id);
      await build(text);
    },
    [build],
  );

  useEffect(() => {
    if (samples.length > 0) void loadSample(samples.find((item) => item.id === "mlflow-namespace-loss")?.id ?? samples[0].id);
  }, [loadSample]);

  function jumpTo(message: string) {
    const path = fieldPathOf(message);
    if (!path) return;
    setTab("evidence");
    const offset = locateField(source, path);
    if (offset >= 0) window.setTimeout(() => editorRef.current?.focusAt(offset), 0);
  }

  async function copyMarkdown() {
    if (!report) return;
    await navigator.clipboard.writeText(reportMarkdown(report));
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1600);
  }

  const stale = report !== null && builtFrom !== source;

  return (
    <div className="layout report-layout">
      <aside className="rail" aria-label="Drill evidence">
        <h2 className="rail-title">Example drill runs</h2>
        <ul className="template-list">
          {samples.map((sample) => (
            <li key={sample.id}>
              <button
                type="button"
                className={`template ${sampleId === sample.id ? "active" : ""}`}
                aria-pressed={sampleId === sample.id}
                onClick={() => void loadSample(sample.id)}
              >
                <span className="template-label">{sample.label}</span>
                <span className="template-summary">{sample.summary}</span>
              </button>
            </li>
          ))}
        </ul>
        {sampleId && (
          <div className="sample-caveat">
            <AlertTriangle size={14} />
            <p><strong>Illustrative evidence</strong>These traces are synthetic examples. They are not recorded from a real restore or production cluster.</p>
          </div>
        )}
        <input
          ref={fileInputRef}
          type="file"
          accept=".json,application/json"
          hidden
          onChange={(event) => {
            const file = event.target.files?.[0];
            event.target.value = "";
            if (file && file.size > 16 * 1024 * 1024) {
              setSourceError("DrillRun exceeds the API's 16 MiB request limit.");
              return;
            }
            if (file)
              void file.text().then((text) => {
                setSourceError("");
                setSource(text);
                setSampleId("");
                void build(text);
              });
          }}
        />
        <button className="rail-action" type="button" onClick={() => fileInputRef.current?.click()}>
          <Upload size={14} /> Import DrillRun JSON
        </button>
        <p className="rail-empty">
          A DrillRun records phases, level checks and the write ledger. The orchestrator will produce it; until then, record a
          manual drill in this format.
        </p>
      </aside>

      <section className="report-main" aria-label="Drill report">
        <div className="report-tabs" role="tablist" aria-label="Report or evidence">
              <button type="button" role="tab" aria-selected={tab === "report"} onClick={() => setTab("report")}>
            Report
          </button>
              <button type="button" role="tab" aria-selected={tab === "evidence"} onClick={() => setTab("evidence")}>
            Evidence JSON {problems.length > 0 && <span className="tab-badge">{problems.length}</span>}
          </button>
          <div className="report-actions">
            {stale && <span className="stale-inline">Evidence edited</span>}
            <button className="primary" type="button" disabled={isBuilding || !inspection.ok || source.length === 0} onClick={() => void build(source)}>
              {isBuilding ? <LoaderCircle className="spin" size={15} /> : <Play size={14} fill="currentColor" />}
              {isBuilding ? "Building…" : "Build report"}
            </button>
          </div>
        </div>

        {requestError && (
          <div className="banner bad">
            <XCircle size={15} /> {requestError} Start the API with <code>go run ./cmd/checkride-api</code>.
          </div>
        )}
        {sourceError && <div className="banner bad"><XCircle size={15} /> {sourceError}</div>}

        {tab === "evidence" ? (
          <div className="evidence-pane">
            {problems.length > 0 && (
              <ul className="findings evidence-problems">
                {problems.map((message, index) => (
                  <li key={index}>
                    <button type="button" className="finding error" onClick={() => jumpTo(message)}>
                      <XCircle size={13} /> <span>{message}</span>
                    </button>
                  </li>
                ))}
              </ul>
            )}
            <div className="editor-pane">
              <CodeEditor
                ref={editorRef}
                value={source}
                errorLine={inspection.ok ? null : inspection.line}
                onChange={setSource}
                onSubmit={() => void build(source)}
              />
            </div>
          </div>
        ) : report ? (
          <ReportBody report={report} onCopy={() => void copyMarkdown()} copied={copied} />
        ) : (
          <div className="empty-report">
            <CircleDashed size={22} />
            <strong>No report yet</strong>
            <p>Choose an example run or import a DrillRun document.</p>
          </div>
        )}
      </section>
    </div>
  );
}

function ReportBody({ report, onCopy, copied }: { report: Report; onCopy: () => void; copied: boolean }) {
  const initial = report.firstFailed ?? report.deepestPassed ?? "V0";
  const [selected, setSelected] = useState(initial);
  useEffect(() => setSelected(initial), [initial, report]);
  const level = report.levels.find((item) => item.id === selected) ?? report.levels[0];

  return (
    <div className="report-body">
      <section className={`verdict ${report.verdict}`} aria-live="polite">
        <div className="verdict-mark">
          {report.verdict === "verified" ? <CheckCircle2 size={26} /> : report.verdict === "failed" ? <XCircle size={26} /> : <CircleDashed size={26} />}
        </div>
        <div className="verdict-copy">
          <span className="eyebrow">
            {verdictLabel[report.verdict]} · {scenarioLabels[report.scenario ?? ""] ?? report.scenario ?? "drill"}
          </span>
          <h2>{report.headline}</h2>
          <p className="mono-meta">
            {report.name} · failure at {new Date(report.failureAt).toLocaleString()}
          </p>
        </div>
        <div className="verdict-actions">
          <button className="tool" type="button" title="Download the computed report as JSON" onClick={() => download(`${report.name}.report.json`, JSON.stringify(report, null, 2), "application/json")}>
            <FileJson size={14} /> <span>JSON</span>
          </button>
          <button className="tool" type="button" title="Download the evidence summary as Markdown" onClick={() => download(`${report.name}.report.md`, reportMarkdown(report), "text/markdown")}>
            <FileText size={14} /> <span>Markdown</span>
          </button>
          <button className="tool" type="button" title="Copy the evidence summary as Markdown" onClick={onCopy}>
            <ClipboardCopy size={14} /> <span>{copied ? "Copied" : "Copy"}</span>
          </button>
        </div>
      </section>

      <div className="metrics">
        <Metric label="Recovery time" value={formatDuration(report.rto?.seconds)} objective={report.rto?.objectiveSeconds ?? null} measured={report.rto?.seconds ?? null} met={report.rto?.met ?? null} />
        <Metric label="Data loss window" value={report.rpo ? formatDuration(report.rpo.seconds) : "—"} objective={report.rpo?.objectiveSeconds ?? null} measured={report.rpo?.seconds ?? null} met={report.rpo?.met ?? null} />
        <div className="metric">
          <span className="metric-label">Writes lost</span>
          <strong className="metric-value">{report.rpo ? report.rpo.lost.toLocaleString() : "—"}</strong>
          <span className="metric-sub">{report.rpo ? `of ${report.rpo.acknowledged.toLocaleString()} acknowledged · ${report.rpo.holes} holes` : "no write ledger"}</span>
        </div>
        <div className="metric">
          <span className="metric-label">Depth reached</span>
          <strong className="metric-value">{report.deepestPassed ?? "none"}</strong>
          <span className="metric-sub">requested {report.requestedLevel}</span>
        </div>
      </div>

      <section className="panel">
        <header className="panel-head">
          <h3>Verification depth</h3>
          <span className="card-sub">select a level for its checks</span>
        </header>
        <ol className="level-track">
          {report.levels.map((item) => (
            <li key={item.id}>
              <button
                type="button"
                className={`level-node ${item.status} ${item.inScope ? "" : "out-of-scope"} ${selected === item.id ? "selected" : ""}`}
                onClick={() => setSelected(item.id)}
                aria-pressed={selected === item.id}
              >
                <span className="level-id">{item.id}</span>
                <span className="level-state">{item.status === "not-checked" ? "not checked" : item.status}</span>
              </button>
            </li>
          ))}
        </ol>
        <LevelDetail level={level} />
      </section>

      {report.rto && <PhaseTimeline rto={report.rto} />}
      {report.rpo && <LedgerChart rpo={report.rpo} failureAt={report.failureAt} />}

      {report.findings.length > 0 && (
        <section className="panel">
          <header className="panel-head">
            <h3>Findings</h3>
            <span className="card-sub">{report.findings.length} total</span>
          </header>
          <ul className="findings">
            {report.findings.map((finding, index) => (
              <li key={index} className={`finding ${finding.severity}`}>
                {finding.severity === "error" ? <XCircle size={13} /> : finding.severity === "warning" ? <AlertTriangle size={13} /> : <Info size={13} />}
                <span>{finding.message}</span>
              </li>
            ))}
          </ul>
        </section>
      )}
    </div>
  );
}

function Metric({ label, value, objective, measured, met }: { label: string; value: string; objective: number | null; measured: number | null; met: boolean | null }) {
  const usage = measured === null ? null : objectiveUsage(measured, objective);
  return (
    <div className={`metric ${met === false ? "missed" : met ? "met" : ""}`}>
      <span className="metric-label">{label}</span>
      <strong className="metric-value">{value}</strong>
      <span className="metric-sub">{objective === null ? "no objective set" : `objective ${formatDuration(objective)} · ${met ? "met" : "missed"}`}</span>
      {usage !== null && (
        <span className="gauge" aria-hidden="true">
          <span style={{ width: `${(usage / 1.5) * 100}%` }} />
          <i style={{ left: `${100 / 1.5}%` }} />
        </span>
      )}
    </div>
  );
}

function LevelDetail({ level }: { level: LevelResult }) {
  return (
    <div className="level-detail">
      <p>
        <strong>{level.id}</strong> {level.question} <span className="card-sub">{level.evidence}</span>
        {!level.inScope && <span className="scope-note">beyond the requested depth</span>}
      </p>
      {level.checks.length === 0 ? (
        <p className="muted-line">No evidence was recorded for this level.</p>
      ) : (
        <ul className="check-list">
          {level.checks.map((check, index) => (
            <li key={index} className={check.passed ? "pass" : "fail"}>
              {check.passed ? <CheckCircle2 size={14} /> : <XCircle size={14} />}
              <span className="check-name">{check.name}</span>
              {check.detail && <span className="check-detail">{check.detail}</span>}
              {check.source === "ledger" && <span className="chip">computed</span>}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function PhaseTimeline({ rto }: { rto: RTOResult }) {
  const { bars, scaleSeconds, objectiveAt, failureAt, failureOffsetSeconds } = timelineBars(rto);
  const ticks = [0, 0.25, 0.5, 0.75, 1].map((share) => {
    const offset = share * scaleSeconds - failureOffsetSeconds;
    return { share, label: offset < 0 ? `−${formatDuration(-offset)}` : offset === 0 ? "failure" : `+${formatDuration(offset)}` };
  });
  return (
    <section className="panel">
      <header className="panel-head">
        <h3>Recovery timeline</h3>
        <span className="card-sub">{formatDuration(rto.seconds)} after failure{rto.uncoveredSeconds >= 1 ? ` · ${formatDuration(rto.uncoveredSeconds)} unaccounted` : ""}</span>
      </header>
      <div className="timeline">
        {failureOffsetSeconds > 0 && (
          <div className="failure-line" title="Failure injected" style={{ left: `calc(var(--label-w) + (100% - var(--label-w)) * ${failureAt / 100})` }} />
        )}
        {objectiveAt !== null && (
          <div
            className={`objective-line ${rto.met ? "" : "missed"}`}
            title={`RTO objective ${formatDuration(rto.objectiveSeconds)}`}
            style={{ left: `calc(var(--label-w) + (100% - var(--label-w)) * ${objectiveAt / 100})` }}
          />
        )}
        {bars.map((bar, index) => (
          <div className="timeline-row" key={`${bar.name}-${index}`}>
            <span className="timeline-label">{bar.name}</span>
            <span className="timeline-track">
              <span
                className={`timeline-bar ${bar.slowest ? "slowest" : ""}`}
                style={{ left: `${bar.left}%`, width: `${bar.width}%`, animationDelay: `${index * 70}ms` }}
                title={`${bar.name}: ${formatDuration(bar.durationSeconds)}`}
              >
                <em>{formatDuration(bar.durationSeconds)}</em>
              </span>
            </span>
          </div>
        ))}
        <div className="timeline-axis">
          {ticks.map((tick) => (
            <span key={tick.share} style={{ left: `calc(var(--label-w) + (100% - var(--label-w)) * ${tick.share})` }}>
              {tick.label}
            </span>
          ))}
        </div>
      </div>
      <div className="timeline-legend">
        {rto.objectiveSeconds !== null && (
          <span><i className={`legend-line objective ${rto.met ? "" : "missed"}`} /> RTO objective {formatDuration(rto.objectiveSeconds)}</span>
        )}
        {failureOffsetSeconds > 0 && <span><i className="legend-line failure" /> failure injected</span>}
        <span><i className="legend-swatch slowest" /> slowest phase</span>
      </div>
    </section>
  );
}

function LedgerChart({ rpo, failureAt }: { rpo: RPOResult; failureAt: string }) {
  const buckets = rpo.timeline;
  if (buckets.length === 0) return null;
  const start = buckets[0].offsetSeconds;
  const peak = Math.max(...buckets.map((bucket) => bucket.recovered + bucket.lost), 1);
  const width = 100 / buckets.length;
  const failure = Date.parse(failureAt);
  const markerAt = rpo.recoveryPoint ? ((Date.parse(rpo.recoveryPoint) - failure) / 1000 - start) / -start : null;
  return (
    <section className="panel">
      <header className="panel-head">
        <h3>Acknowledged-write ledger</h3>
        <span className="card-sub">
          {rpo.consistent ? "consistent point in time" : `${rpo.holes} holes · inconsistent`}
          {rpo.unexpected > 0 ? ` · ${rpo.unexpected} unexpected` : ""}
        </span>
      </header>
      <div className="ledger">
        <svg viewBox="0 0 100 40" preserveAspectRatio="none" role="img" aria-label={`${rpo.recovered} writes recovered and ${rpo.lost} lost before the failure`}>
          {buckets.map((bucket, index) => {
            const recoveredHeight = (bucket.recovered / peak) * 36;
            const lostHeight = (bucket.lost / peak) * 36;
            return (
              <g key={index}>
                <rect className="bar-recovered" x={index * width + width * 0.12} width={width * 0.76} y={40 - recoveredHeight} height={recoveredHeight} />
                <rect className="bar-lost" x={index * width + width * 0.12} width={width * 0.76} y={40 - recoveredHeight - lostHeight} height={lostHeight} />
              </g>
            );
          })}
        </svg>
        {markerAt !== null && markerAt >= 0 && markerAt <= 1 && (
          <span className="ledger-marker" title="Recovery point: last write of the fully recovered prefix" style={{ left: `${markerAt * 100}%` }} />
        )}
      </div>
      <div className="ledger-axis">
        <span>−{formatDuration(-start)}</span>
        <span className="legend">
          <i className="swatch recovered" /> recovered <i className="swatch lost" /> lost
          {markerAt !== null && <><i className="legend-line recovery" /> recovery point</>}
        </span>
        <span>failure</span>
      </div>
    </section>
  );
}
