import { Activity, RefreshCw } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { ReportView } from "./ReportView";
import { Studio } from "./Studio";

type View = "design" | "report";
type APIState = "checking" | "online" | "offline";

const viewFromHash = (): View => (window.location.hash === "#/report" ? "report" : "design");

export function App() {
  const [view, setView] = useState<View>(viewFromHash);
  const [apiState, setAPIState] = useState<APIState>("checking");

  const checkAPI = useCallback(async (signal?: AbortSignal) => {
    setAPIState("checking");
    try {
      const response = await fetch("/healthz", { signal: signal ?? AbortSignal.timeout(3000) });
      setAPIState(response.status === 204 ? "online" : "offline");
    } catch {
      if (!signal?.aborted) setAPIState("offline");
    }
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    const timeout = window.setTimeout(() => controller.abort(), 3000);
    void checkAPI(controller.signal).finally(() => window.clearTimeout(timeout));
    const onHash = () => setView(viewFromHash());
    window.addEventListener("hashchange", onHash);
    return () => {
      window.clearTimeout(timeout);
      controller.abort();
      window.removeEventListener("hashchange", onHash);
    };
  }, [checkAPI]);

  const onReachability = useCallback((online: boolean) => setAPIState(online ? "online" : "offline"), []);
  const apiLabel = apiState === "checking" ? "Connecting" : apiState === "online" ? "API connected" : "API offline";

  return (
    <div className="studio">
      <header className="topbar">
        <a className="brand" href="#/design">
          <span className="brand-mark" aria-hidden="true" />
          <span className="brand-name">Checkride</span>
          <span className="brand-product">Studio</span>
        </a>
        <nav className="views" aria-label="Studio views">
          <a href="#/design" aria-current={view === "design" ? "page" : undefined}>
            <span className="view-index">01</span> Design drill
          </a>
          <a href="#/report" aria-current={view === "report" ? "page" : undefined}>
            <span className="view-index">02</span> Evidence report
          </a>
        </nav>
        <a className="workbench-link" href="http://127.0.0.1:4321/" target="_blank" rel="noreferrer" title="Open the product site in a new tab">
          <Activity size={14} /> <span>Product site</span>
        </a>
        <button className={`api-status ${apiState}`} type="button" onClick={() => void checkAPI()} title="Recheck the local API">
          <span className="status-dot" />
          {apiLabel}
          {apiState === "offline" && <RefreshCw size={13} />}
        </button>
      </header>
      <div className="view" hidden={view !== "design"}>
        <Studio onReachability={onReachability} />
      </div>
      <div className="view" hidden={view !== "report"}>
        <ReportView onReachability={onReachability} />
      </div>
    </div>
  );
}
