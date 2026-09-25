import {
  AlertTriangle,
  Check,
  CheckCircle2,
  Clock3,
  Code2,
  FileCheck2,
  LoaderCircle,
  Download,
  Play,
  RotateCcw,
  Server,
  Upload,
  XCircle,
} from "lucide-react";
import { useEffect, useRef, useState } from "react";
import type { FormEvent } from "react";

type APIState = "checking" | "online" | "offline";

interface APIValidationResult {
  valid: boolean;
  errors: string[];
  warnings: string[];
}

interface ValidationRun extends APIValidationResult {
  status: number;
  checkedAt: Date;
}

const exampleDrill = {
  apiVersion: "checkride/v1alpha1",
  kind: "Drill",
  metadata: { name: "shop-namespace-loss" },
  spec: {
    scenario: "namespace-loss",
    target: {
      namespace: "demo-shop",
      argocdApplication: "demo-shop",
      cnpgCluster: "shop-db",
    },
    restore: { into: "separate-cluster" },
    verify: {
      upTo: "V4",
      ledger: true,
      invariants: [
        {
          name: "every-order-has-a-customer",
          sql: "SELECT count(*) FROM orders o LEFT JOIN customers c ON c.id = o.customer_id WHERE c.id IS NULL",
          expect: 0,
        },
      ],
    },
    objectives: { rto: "15m", rpo: "5m" },
  },
};

const exampleJSON = JSON.stringify(exampleDrill, null, 2);

function normalizeResult(value: unknown): APIValidationResult {
  if (typeof value !== "object" || value === null) {
    throw new Error("The API returned an unexpected response.");
  }
  const result = value as Partial<APIValidationResult>;
  if (typeof result.valid !== "boolean") {
    throw new Error("The API response is missing its validation status.");
  }
  return {
    valid: result.valid,
    errors: Array.isArray(result.errors)
      ? result.errors.filter((item): item is string => typeof item === "string")
      : [],
    warnings: Array.isArray(result.warnings)
      ? result.warnings.filter((item): item is string => typeof item === "string")
      : [],
  };
}

function App() {
  const [definition, setDefinition] = useState(exampleJSON);
  const [apiState, setAPIState] = useState<APIState>("checking");
  const [isValidating, setIsValidating] = useState(false);
  const [result, setResult] = useState<ValidationRun | null>(null);
  const [editorError, setEditorError] = useState("");
  const [requestError, setRequestError] = useState("");
  const lineNumbersRef = useRef<HTMLDivElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);

  async function checkAPI() {
    setAPIState("checking");
    const controller = new AbortController();
    const timeout = window.setTimeout(() => controller.abort(), 3000);
    try {
      const response = await fetch("/healthz", { signal: controller.signal });
      const online = response.status === 204;
      setAPIState(online ? "online" : "offline");
      if (online) setRequestError("");
    } catch {
      setAPIState("offline");
    } finally {
      window.clearTimeout(timeout);
    }
  }

  useEffect(() => {
    const controller = new AbortController();
    const timeout = window.setTimeout(() => controller.abort(), 3000);
    fetch("/healthz", { signal: controller.signal })
      .then((response) => setAPIState(response.status === 204 ? "online" : "offline"))
      .catch(() => {
        if (!controller.signal.aborted) setAPIState("offline");
      })
      .finally(() => window.clearTimeout(timeout));
    return () => {
      window.clearTimeout(timeout);
      controller.abort();
    };
  }, []);

  async function validateDrill(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setIsValidating(true);
    setEditorError("");
    setRequestError("");

    try {
      const response = await fetch("/api/v1/drills/validate", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: definition,
      });
      const payload = normalizeResult(await response.json());
      setAPIState("online");
      setResult({
        ...payload,
        valid: response.status === 200 && payload.valid,
        status: response.status,
        checkedAt: new Date(),
      });
    } catch (error) {
      setAPIState("offline");
      setResult(null);
      setRequestError(
        error instanceof Error ? error.message : "Could not reach the Checkride API.",
      );
    } finally {
      setIsValidating(false);
    }
  }

  function formatDefinition() {
    try {
      setDefinition(JSON.stringify(JSON.parse(definition), null, 2));
      setEditorError("");
      setResult(null);
    } catch {
      setEditorError("This document is not valid JSON, so it cannot be formatted yet.");
    }
  }

  function restoreExample() {
    setDefinition(exampleJSON);
    setEditorError("");
    setRequestError("");
    setResult(null);
  }

  async function importDefinition(file: File | undefined) {
    if (!file) return;
    setDefinition(await file.text());
    setResult(null);
    setEditorError("");
    setRequestError("");
  }

  function downloadDefinition() {
    const link = document.createElement("a");
    link.href = URL.createObjectURL(new Blob([definition], { type: "application/json" }));
    link.download = "drill.json";
    link.click();
    window.setTimeout(() => URL.revokeObjectURL(link.href), 1000);
  }

  const lineCount = definition.split("\n").length;
  const byteCount = new TextEncoder().encode(definition).length;

  return (
    <div className="app-shell">
      <main className="main-area" id="workspace">
        <header className="topbar">
          <div className="masthead">
            <span className="brand-symbol" aria-hidden="true">C<span>/</span></span>
            <span className="masthead-title">CHECKRIDE <span> / STUDIO</span></span>
          </div>
          <div className="topbar-actions">
            <button className={`api-badge ${apiState}`} type="button" onClick={() => void checkAPI()} title="Check API connection" aria-live="polite">
              <span className={`connection-dot ${apiState}`} />
              <span>{apiState === "checking" ? "Checking local API" : apiState === "online" ? "Local API connected" : "Local API offline · retry"}</span>
            </button>
          </div>
        </header>

        <div className="workspace-content">
          <section className="page-heading">
            <div>
              <span className="eyebrow">SPECIFICATION / 01</span>
              <h1>Drill validation</h1>
              <p>Review the recovery plan before a drill. Nothing is submitted to a cluster.</p>
            </div>
          </section>

          <div className="workbench-grid">
            <form className="editor-panel" onSubmit={validateDrill}>
              <div className="panel-heading editor-heading">
                <div className="panel-title-group">
                  <span className="panel-icon editor-panel-icon"><FileCheck2 size={17} /></span>
                  <div>
                    <h2>Drill definition</h2>
                    <p>checkride/v1alpha1 · JSON</p>
                  </div>
                </div>
                <div className="editor-tools">
                  <input ref={fileInputRef} type="file" accept=".json,application/json" hidden onChange={(event) => { void importDefinition(event.target.files?.[0]); event.target.value = ""; }} />
                  <button className="icon-tool" type="button" onClick={() => fileInputRef.current?.click()} title="Import JSON file" aria-label="Import JSON file"><Upload size={15} /></button>
                  <button className="icon-tool" type="button" onClick={downloadDefinition} title="Download JSON file" aria-label="Download JSON file"><Download size={15} /></button>
                  <button className="text-tool" type="button" onClick={formatDefinition} title="Format JSON">
                    <Code2 size={15} />
                    <span>Format</span>
                  </button>
                  <button className="icon-tool" type="button" onClick={restoreExample} title="Restore example drill" aria-label="Restore example drill">
                    <RotateCcw size={15} />
                  </button>
                </div>
              </div>
              <div className="editor-toolbar">
                <span className="file-tab"><span className="file-dot" /> drill.json</span>
                <button className="load-sample" type="button" onClick={restoreExample}>Load sample</button>
              </div>
              <div className="code-input-wrap">
                <div className="line-numbers" aria-hidden="true" ref={lineNumbersRef}>
                  {Array.from({ length: lineCount }, (_, index) => <span key={index}>{index + 1}</span>)}
                </div>
                <textarea
                  className="code-input"
                  aria-label="Drill definition JSON"
                  autoCapitalize="off"
                  autoComplete="off"
                  autoCorrect="off"
                  spellCheck={false}
                  value={definition}
                  onScroll={(event) => {
                    if (lineNumbersRef.current) {
                      lineNumbersRef.current.scrollTop = event.currentTarget.scrollTop;
                    }
                  }}
                  onChange={(event) => {
                    setDefinition(event.target.value);
                    setEditorError("");
                    setResult(null);
                  }}
                />
              </div>
              {editorError && <div className="editor-message" role="alert"><XCircle size={15} />{editorError}</div>}
              <div className="editor-footer">
                <div className="editor-stats">
                  <span><span className="json-mark">{ "{}" }</span> JSON</span>
                  <span>{lineCount} lines</span>
                  <span>{byteCount.toLocaleString()} bytes</span>
                </div>
                <button className="validate-button" type="submit" disabled={isValidating}>
                  {isValidating ? <LoaderCircle className="spin" size={16} /> : <Play size={15} fill="currentColor" />}
                  <span>{isValidating ? "Checking" : "Validate drill"}</span>
                </button>
              </div>
            </form>

            <section className="result-panel" aria-labelledby="result-title" aria-live="polite">
              <div className="panel-heading result-heading">
                <div className="panel-title-group">
                  <span className="panel-icon result-panel-icon"><CheckCircle2 size={17} /></span>
                  <div>
                    <h2 id="result-title">Preflight result</h2>
                    <p>Specification checks</p>
                  </div>
                </div>
                {result && <span className={`result-tag ${result.valid ? "pass" : "fail"}`}>{result.valid ? "PASSED" : "NEEDS ATTENTION"}</span>}
              </div>

              {requestError ? (
                <div className="result-empty request-failed">
                  <span className="empty-icon error-icon"><Server size={20} /></span>
                  <h3>API request failed</h3>
                  <p>{requestError}</p>
                  <button className="retry-button" type="button" onClick={checkAPI}>
                    <RotateCcw size={14} /> Check connection
                  </button>
                </div>
              ) : result ? (
                <div className="result-body">
                  <div className={`result-summary ${result.valid ? "success" : "failure"}`}>
                    <span className="summary-symbol">{result.valid ? <CheckCircle2 size={22} /> : <XCircle size={22} />}</span>
                    <div>
                      <strong>{result.valid ? "Definition accepted" : "Definition needs changes"}</strong>
                      <span>{result.valid ? "The API accepted this drill specification." : `${result.errors.length} ${result.errors.length === 1 ? "issue" : "issues"} need attention.`}</span>
                    </div>
                  </div>

                  {result.errors.length > 0 && (
                    <div className="feedback-section">
                      <div className="feedback-heading error-heading"><XCircle size={15} /><h3>Errors <span>{result.errors.length}</span></h3></div>
                      <ul className="feedback-list errors-list">{result.errors.map((item, index) => <li key={`${index}-${item}`}>{item}</li>)}</ul>
                    </div>
                  )}

                  {result.warnings.length > 0 && (
                    <div className="feedback-section">
                      <div className="feedback-heading warning-heading"><AlertTriangle size={15} /><h3>Warnings <span>{result.warnings.length}</span></h3></div>
                      <ul className="feedback-list warnings-list">{result.warnings.map((item, index) => <li key={`${index}-${item}`}>{item}</li>)}</ul>
                    </div>
                  )}

                  {result.valid && result.warnings.length === 0 && (
                    <div className="passed-checks">
                      <div className="feedback-heading passed-heading"><Check size={15} /><h3>Contract checks</h3></div>
                      <ul>
                        <li><Check size={14} />API version and document kind</li>
                        <li><Check size={14} />Scenario and target fields</li>
                        <li><Check size={14} />Verification evidence requirements</li>
                        <li><Check size={14} />Recovery objectives</li>
                      </ul>
                    </div>
                  )}

                  <div className="result-meta">
                    <span><Clock3 size={13} />{result.checkedAt.toLocaleTimeString()}</span>
                    <span className="http-status">HTTP {result.status}</span>
                  </div>
                </div>
              ) : (
                <div className="result-empty">
                  <span className="empty-icon"><Code2 size={20} /></span>
                  <h3>No result yet</h3>
                  <p>Import a drill or edit the example, then run validation to see errors and warnings here.</p>
                </div>
              )}

              <div className="scope-note">
                <span className="scope-note-mark" />
                <span>Validation only. This does not create or run a drill.</span>
              </div>
            </section>
          </div>

          <footer className="workspace-footer">LOCAL WORKSPACE <span>SPECIFICATION CHECK ONLY · NO CLUSTER ACTION</span></footer>
        </div>
      </main>
    </div>
  );
}

export default App;
