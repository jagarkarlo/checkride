import {
  Activity,
  AlertTriangle,
  Check,
  CheckCircle2,
  ChevronRight,
  CircleHelp,
  Clock3,
  Code2,
  FileCheck2,
  History,
  LoaderCircle,
  Play,
  RotateCcw,
  Server,
  ShieldCheck,
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
  metadata: { name: "mlflow-namespace-loss" },
  spec: {
    scenario: "namespace-loss",
    target: {
      namespace: "mlflow",
      argocdApplication: "mlflow",
      cnpgCluster: "mlflow-db",
    },
    restore: { into: "separate-cluster" },
    verify: {
      upTo: "V4",
      ledger: true,
      invariants: [
        {
          name: "every-run-has-an-experiment",
          sql: "SELECT count(*) FROM runs r LEFT JOIN experiments e ON e.experiment_id = r.experiment_id WHERE e.experiment_id IS NULL",
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

  const lineCount = definition.split("\n").length;
  const byteCount = new TextEncoder().encode(definition).length;

  return (
    <div className="app-shell">
      <aside className="sidebar">
        <a className="brand" href="#workspace" aria-label="Checkride Studio home">
          <span className="brand-icon"><ShieldCheck size={22} strokeWidth={2.1} /></span>
          <span className="brand-copy">
            <strong>CHECKRIDE</strong>
            <small>RECOVERY STUDIO</small>
          </span>
        </a>

        <div className="sidebar-nav-label">WORKSPACE</div>
        <nav className="primary-nav" aria-label="Primary navigation">
          <a className="nav-item" href="#overview" aria-disabled="true" onClick={(event) => event.preventDefault()}>
            <Activity size={17} />
            <span>Overview</span>
          </a>
          <a className="nav-item active" href="#workspace" aria-current="page">
            <FileCheck2 size={17} />
            <span>Validate drill</span>
            <span className="nav-current-mark" />
          </a>
          <a className="nav-item disabled" href="#history" aria-disabled="true" onClick={(event) => event.preventDefault()}>
            <History size={17} />
            <span>Run history</span>
            <span className="nav-soon">SOON</span>
          </a>
        </nav>

        <div className="sidebar-bottom">
          <div className="sidebar-lab-heading">LOCAL CONTROL PLANE</div>
          <div className="sidebar-api-status">
            <span className={`connection-dot ${apiState}`} />
            <span>{apiState === "checking" ? "Checking API" : apiState === "online" ? "API reachable" : "API unavailable"}</span>
            <span className="mono-port">:8080</span>
          </div>
          <div className="sidebar-build">CHECKRIDE · PRE-ALPHA</div>
        </div>
      </aside>

      <main className="main-area" id="workspace">
        <header className="topbar">
          <div className="breadcrumbs">
            <span>Studio</span>
            <ChevronRight size={14} />
            <strong>Drill validation</strong>
          </div>
          <div className="topbar-actions">
            <div className={`api-badge ${apiState}`} aria-live="polite">
              <span className={`connection-dot ${apiState}`} />
              <span>{apiState === "checking" ? "CONNECTING" : apiState === "online" ? "API ONLINE" : "API OFFLINE"}</span>
            </div>
            <span className="topbar-divider" />
            <a className="help-link" href="https://github.com/jagarkarlo/checkride/blob/main/README.md" target="_blank" rel="noreferrer">
              <CircleHelp size={16} />
              <span>Help</span>
            </a>
          </div>
        </header>

        <div className="workspace-content">
          <section className="page-heading">
            <div>
              <div className="eyebrow-row">
                <span className="eyebrow">DRILL BUILDER</span>
                <span className="eyebrow-separator" />
                <span className="schema-version">CHECKRIDE/V1ALPHA1</span>
              </div>
              <h1>Validate a recovery drill</h1>
              <p>Check the specification and recovery evidence before running a drill.</p>
            </div>
            <div className="preflight-count" aria-label="One preflight check available">
              <span className="preflight-number">01</span>
              <span>PRE-FLIGHT<br />CHECK</span>
            </div>
          </section>

          <div className="workbench-grid">
            <form className="editor-panel" onSubmit={validateDrill}>
              <div className="panel-heading editor-heading">
                <div className="panel-title-group">
                  <span className="panel-icon editor-panel-icon"><Code2 size={17} /></span>
                  <div>
                    <h2>Drill definition</h2>
                    <p>JSON document</p>
                  </div>
                </div>
                <div className="editor-tools">
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
                  {!isValidating && <span className="button-shortcut">↵</span>}
                </button>
              </div>
            </form>

            <section className="result-panel" aria-labelledby="result-title" aria-live="polite">
              <div className="panel-heading result-heading">
                <div className="panel-title-group">
                  <span className="panel-icon result-panel-icon"><Activity size={17} /></span>
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
                  <span className="empty-icon"><ShieldCheck size={21} /></span>
                  <h3>Awaiting preflight</h3>
                  <p>Validation checks the document contract and reports missing recovery evidence.</p>
                  <div className="empty-rule" />
                  <div className="result-capabilities">
                    <span><Check size={13} />Schema and field validation</span>
                    <span><Check size={13} />Verification-level requirements</span>
                    <span><Check size={13} />Recovery objective warnings</span>
                  </div>
                </div>
              )}

              <div className="scope-note">
                <span className="scope-note-mark" />
                <span>Validation only. This does not create or run a drill.</span>
              </div>
            </section>
          </div>

          <footer className="workspace-footer">
            <div className="footer-scope">
              <span className="footer-scope-label">THIS PASS COVERS</span>
              <span><Check size={13} />Spec contract</span>
              <span><Check size={13} />Evidence requirements</span>
              <span><Check size={13} />RTO / RPO format</span>
            </div>
            <span className="footer-note">No cluster credentials used</span>
          </footer>
        </div>
      </main>
    </div>
  );
}

export default App;
