import { Activity, Moon, RefreshCw, Sun } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { request } from "./api";
import { ReportView } from "./ReportView";
import { Studio } from "./Studio";

type View = "design" | "report";
type APIState = "checking" | "online" | "offline";

const viewFromHash = (): View => (window.location.hash === "#/report" ? "report" : "design");
const browserDemo = window.location.pathname.startsWith("/demo/");

export function App() {
  const [view, setView] = useState<View>(viewFromHash);
  const [apiState, setAPIState] = useState<APIState>("checking");
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
  const apiLabel = apiState === "checking" ? "Connecting" : apiState === "online" ? (browserDemo ? "Go engine · browser" : "API connected") : "Engine unavailable";

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
      <div className="view" hidden={view !== "design"}>
        <Studio onReachability={onReachability} />
      </div>
      <div className="view" hidden={view !== "report"}>
        <ReportView onReachability={onReachability} />
      </div>
    </div>
  );
}
