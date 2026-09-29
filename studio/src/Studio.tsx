import {
  AlertTriangle,
  CheckCircle2,
  Circle,
  CircleDot,
  Code2,
  Download,
  History,
  LoaderCircle,
  Play,
  Upload,
  XCircle,
} from "lucide-react";
import { useMemo, useRef, useState } from "react";
import { request } from "./api";
import { CodeEditor } from "./CodeEditor";
import type { CodeEditorHandle } from "./CodeEditor";
import { describePlan, fieldPathOf, inspectJSON, locateField, scenarioLabels, templates } from "./drill";

interface ValidationRun {
  id: number;
  valid: boolean;
  errors: string[];
  warnings: string[];
  status: number;
  checkedAt: Date;
  source: string;
  name: string;
}

const pretty = (value: unknown) => JSON.stringify(value, null, 2);
const strings = (value: unknown) =>
  Array.isArray(value) ? value.filter((item): item is string => typeof item === "string") : [];

export function Studio({ onReachability }: { onReachability: (online: boolean) => void }) {
  const [definition, setDefinition] = useState(() => pretty(templates[0].document));
  const [templateId, setTemplateId] = useState(templates[0].id);
  const [isValidating, setIsValidating] = useState(false);
  const [runs, setRuns] = useState<ValidationRun[]>([]);
  const [requestError, setRequestError] = useState("");
  const editorRef = useRef<CodeEditorHandle>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const runId = useRef(0);

  const inspection = useMemo(() => inspectJSON(definition), [definition]);
  const plan = useMemo(() => describePlan(inspection.ok ? inspection.value : null), [inspection]);
  const latest = runs[0] ?? null;
  const stale = latest !== null && latest.source !== definition;

  async function validate() {
    if (isValidating) return;
    setIsValidating(true);
    setRequestError("");
    const source = definition;
    try {
      const response = await request("/api/v1/drills/validate", source);
      const payload: unknown = await response.json();
      if (typeof payload !== "object" || payload === null || typeof (payload as { valid?: unknown }).valid !== "boolean") {
        throw new Error("The API returned an unexpected response.");
      }
      const body = payload as { valid: boolean; errors?: unknown; warnings?: unknown };
      onReachability(true);
      runId.current += 1;
      const run: ValidationRun = {
        id: runId.current,
        valid: response.status === 200 && body.valid,
        errors: strings(body.errors),
        warnings: strings(body.warnings),
        status: response.status,
        checkedAt: new Date(),
        source,
        name: plan.name || "untitled drill",
      };
      setRuns((previous) => [run, ...previous].slice(0, 6));
    } catch (error) {
      onReachability(false);
      setRequestError(error instanceof Error ? error.message : "Could not reach the Checkride API.");
    } finally {
      setIsValidating(false);
    }
  }

  function load(source: string, id = "") {
    setDefinition(source);
    setTemplateId(id);
    setRequestError("");
  }

  function jumpTo(message: string) {
    const path = fieldPathOf(message);
    if (!path) return;
    const offset = locateField(definition, path);
    if (offset >= 0) editorRef.current?.focusAt(offset);
  }

  function download() {
    const link = document.createElement("a");
    link.href = URL.createObjectURL(new Blob([definition], { type: "application/json" }));
    link.download = `${plan.name || "drill"}.json`;
    link.click();
    window.setTimeout(() => URL.revokeObjectURL(link.href), 1000);
  }

  return (
    <div className="layout">
        <aside className="rail" aria-label="Drill templates">
          <h2 className="rail-title">Start from a scenario</h2>
          <ul className="template-list">
            {templates.map((template) => (
              <li key={template.id}>
                <button
                  type="button"
                  className={`template ${templateId === template.id ? "active" : ""}`}
                  aria-pressed={templateId === template.id}
                  onClick={() => load(pretty(template.document), template.id)}
                >
                  <span className="template-label">{template.label}</span>
                  <span className="template-summary">{template.summary}</span>
                </button>
              </li>
            ))}
          </ul>

          <h2 className="rail-title history-title"><History size={13} /> Recent checks</h2>
          {runs.length === 0 ? (
            <p className="rail-empty">Validated drills appear here for this session.</p>
          ) : (
            <ul className="history-list">
              {runs.map((run) => (
                <li key={run.id}>
                  <button type="button" className="history-item" onClick={() => load(run.source)} title="Reopen this version">
                    {run.valid ? <CheckCircle2 size={14} className="ok" /> : <XCircle size={14} className="bad" />}
                    <span className="history-name">{run.name}</span>
                    <time>{run.checkedAt.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" })}</time>
                  </button>
                </li>
              ))}
            </ul>
          )}
        </aside>

        <section className="editor-pane" aria-label="Drill definition">
          <div className="pane-bar">
            <div className="pane-title">
              <span className="file-name">{plan.name || "drill"}.json</span>
              <span className={`syntax ${inspection.ok ? "ok" : "bad"}`}>
                {inspection.ok ? "Valid JSON" : `Syntax error · line ${inspection.line}`}
              </span>
            </div>
            <div className="toolbar">
              <input
                ref={fileInputRef}
                type="file"
                accept=".json,application/json"
                hidden
                onChange={(event) => {
                  const file = event.target.files?.[0];
                  event.target.value = "";
                  if (file) void file.text().then((source) => load(source));
                }}
              />
              <button className="tool" type="button" onClick={() => fileInputRef.current?.click()} title="Import a JSON drill">
                <Upload size={14} /> <span>Import</span>
              </button>
              <button className="tool" type="button" onClick={download} title="Download this drill">
                <Download size={14} /> <span>Export</span>
              </button>
              <button
                className="tool"
                type="button"
                disabled={!inspection.ok}
                onClick={() => inspection.ok && setDefinition(pretty(inspection.value))}
                title="Format JSON"
              >
                <Code2 size={14} /> <span>Format</span>
              </button>
            </div>
          </div>

          <CodeEditor
            ref={editorRef}
            value={definition}
            errorLine={inspection.ok ? null : inspection.line}
            onChange={setDefinition}
            onSubmit={() => void validate()}
          />

          {!inspection.ok && (
            <button className="syntax-banner" type="button" onClick={() => editorRef.current?.focusAt(inspection.offset)}>
              <XCircle size={14} />
              Line {inspection.line}, column {inspection.column}: {inspection.message}
            </button>
          )}

          <div className="pane-footer">
            <span className="hint"><kbd>Ctrl</kbd> + <kbd>Enter</kbd> to validate</span>
            <button className="primary" type="button" onClick={() => void validate()} disabled={isValidating}>
              {isValidating ? <LoaderCircle className="spin" size={15} /> : <Play size={14} fill="currentColor" />}
              {isValidating ? "Validating…" : "Validate drill"}
            </button>
          </div>
        </section>

        <aside className="inspector" aria-label="Drill plan and validation">
          <section className="card plan" aria-labelledby="plan-title">
            <header className="card-head">
              <h2 id="plan-title">Drill plan</h2>
              <span className="card-sub">Live from the editor</span>
            </header>
            <dl className="facts">
              <div><dt>Scenario</dt><dd>{scenarioLabels[plan.scenario] ?? (plan.scenario || "—")}</dd></div>
              <div><dt>Namespace</dt><dd className="mono">{plan.namespace || "—"}</dd></div>
              <div><dt>Database</dt><dd className="mono">{plan.database || "none"}</dd></div>
              <div><dt>Restore into</dt><dd>{plan.restoreInto === "namespace" ? "Source-cluster namespace" : "Separate cluster"}</dd></div>
              {plan.pointInTime && <div><dt>Point in time</dt><dd className="mono">{plan.pointInTime}</dd></div>}
              <div className="objective"><dt>RTO</dt><dd>{plan.rto || "unset"}</dd></div>
              <div className="objective"><dt>RPO</dt><dd>{plan.rpo || "unset"}</dd></div>
            </dl>

            <h3 className="ladder-title">Verification depth</h3>
            <ol className="ladder">
              {plan.levels.map((level) => (
                <li key={level.id} className={level.included ? "in" : "out"}>
                  <span className="ladder-id">{level.id}</span>
                  <span className="ladder-text">
                    <strong>{level.title}</strong>
                    <small>{level.evidence}</small>
                  </span>
                  {level.included ? <CircleDot size={14} /> : <Circle size={14} />}
                </li>
              ))}
            </ol>
            {plan.evidence.length > 0 && (
              <p className="evidence">Correctness evidence: {plan.evidence.join(" · ")}</p>
            )}
          </section>

          <section className={`card result ${latest ? (latest.valid ? "pass" : "fail") : ""}`} aria-live="polite" aria-labelledby="result-title">
            <header className="card-head">
              <h2 id="result-title">Validation</h2>
              {latest && <span className="card-sub">HTTP {latest.status} · {latest.checkedAt.toLocaleTimeString()}</span>}
            </header>

            {requestError ? (
              <div className="result-state">
                <XCircle size={18} className="bad" />
                <div>
                  <strong>Could not reach the API</strong>
                  <p>{requestError} Start it with <code>go run ./cmd/checkride-api</code>.</p>
                </div>
              </div>
            ) : !latest ? (
              <div className="result-state muted">
                <Play size={16} />
                <div>
                  <strong>Not validated yet</strong>
                  <p>Checks the contract only — nothing is sent to a cluster.</p>
                </div>
              </div>
            ) : (
              <>
                <div className="result-state">
                  {latest.valid ? <CheckCircle2 size={18} className="ok" /> : <XCircle size={18} className="bad" />}
                  <div>
                    <strong>{latest.valid ? "Ready to run" : `${latest.errors.length} ${latest.errors.length === 1 ? "problem" : "problems"} to fix`}</strong>
                    <p>{latest.valid ? "The drill specification satisfies the contract." : "Select a problem to jump to its field."}</p>
                  </div>
                </div>
                {stale && <p className="stale">Edited since this check. Validate again to refresh.</p>}
                {(latest.errors.length > 0 || latest.warnings.length > 0) && (
                  <ul className="findings">
                    {latest.errors.map((message, index) => (
                      <li key={`e${index}`}>
                        <button type="button" className="finding error" onClick={() => jumpTo(message)}>
                          <XCircle size={13} /> <span>{message}</span>
                        </button>
                      </li>
                    ))}
                    {latest.warnings.map((message, index) => (
                      <li key={`w${index}`}>
                        <button type="button" className="finding warning" onClick={() => jumpTo(message)}>
                          <AlertTriangle size={13} /> <span>{message}</span>
                        </button>
                      </li>
                    ))}
                  </ul>
                )}
              </>
            )}
          </section>
        </aside>
    </div>
  );
}
