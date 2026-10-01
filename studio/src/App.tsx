import { Activity, FileCheck2, LayoutList, Moon, PencilRuler, RefreshCw, Sun } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { request } from "./api";
import { ReportView } from "./ReportView";
import { Studio } from "./Studio";
import { RunLibrary } from "./RunLibrary";

type View = "runs" | "design" | "report";
type APIState = "checking" | "online" | "offline";

const viewFromHash = (): View => {
  const hash = window.location.hash.toLowerCase().replace("%2f", "/");
  return hash === "#/report" ? "report" : hash === "#/design" ? "design" : "runs";
};
const browserDemo = window.location.pathname.startsWith("/demo/");

export function App() {
  const [view, setView] = useState<View>(viewFromHash);
  const [apiState, setAPIState] = useState<APIState>("checking");
  const [selection, setSelection] = useState<{ source: string; sampleId: string }>();
  const [theme, setTheme] = useState(() => {
    try { return localStorage.getItem("checkride-theme") ?? (matchMedia("(prefers-color-scheme: light)").matches ? "light" : "dark"); }
    catch { return "dark"; }
  });

  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    try { localStorage.setItem("checkride-theme", theme); } catch { /* storage unavailable */ }
  }, [theme]);

  const checkAPI = useCallback(async (signal?: AbortSignal) => {
    setAPIState("checking");
    try {
      const response = await request("/healthz", "", signal ?? AbortSignal.timeout(10000));
      setAPIState(response.status === 204 ? "online" : "offline");
    } catch {
      setAPIState("offline");
    }
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    const timeout = window.setTimeout(() => controller.abort(), 30000);
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
  const apiLabel = apiState === "checking" ? "Connecting" : apiState === "online" ? (browserDemo ? "Go engine · browser" : "API connected") : "Engine unavailable";

  return (
    <div className="studio">
      <header className="topbar">
        <a className="brand" href="#/runs">
          <span className="brand-mark" aria-hidden="true" />
          <span className="brand-name">Checkride</span>
          <span className="brand-product">Studio</span>
        </a>
        <nav className="views" aria-label="Studio views">
          <a href="#/runs" aria-current={view === "runs" ? "page" : undefined}><LayoutList size={15} /> Runs</a>
          <a href="#/design" aria-current={view === "design" ? "page" : undefined}>
            <PencilRuler size={15} /> Design
          </a>
          <a href="#/report" aria-current={view === "report" ? "page" : undefined}>
            <FileCheck2 size={15} /> Report
          </a>
        </nav>
        <a className="workbench-link" href={browserDemo ? "/" : "http://127.0.0.1:4321/"} title="Open the product site">
          <Activity size={14} /> <span>Product site</span>
        </a>
        <button className="studio-theme" type="button" aria-label={`Switch to ${theme === "dark" ? "light" : "dark"} mode`} title="Toggle color theme" onClick={() => setTheme(theme === "dark" ? "light" : "dark")}>
          {theme === "dark" ? <Sun size={17} /> : <Moon size={17} />}
        </button>
        <button className={`api-status ${apiState}`} type="button" onClick={() => void checkAPI()} title="Recheck the local API">
          <span className="status-dot" />
          {apiLabel}
          {apiState === "offline" && <RefreshCw size={13} />}
        </button>
      </header>
      {view === "runs" && <RunLibrary onOpen={(source, sampleId) => {
        setSelection({ source, sampleId });
        window.location.hash = "/report";
        setView("report");
      }} />}
      <div className="view" hidden={view !== "design"}>
        <Studio onReachability={onReachability} />
      </div>
      <div className="view" hidden={view !== "report"}>
        <ReportView onReachability={onReachability} selection={selection} />
      </div>
    </div>
  );
}
