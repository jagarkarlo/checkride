declare global {
  interface Window {
    Go: new () => { importObject: WebAssembly.Imports; run: (instance: WebAssembly.Instance) => Promise<void> };
    nostekonRequest?: (path: string, body: string, headers?: Record<string, string>) => { status: number; body: string };
    nostekonReady?: () => void;
  }
}

export const browserDemo = window.location.pathname.startsWith("/demo/");
let engine: Promise<void> | undefined;

function loadEngine(): Promise<void> {
  engine ??= new Promise<void>((resolve, reject) => {
    const script = document.createElement("script");
    script.src = `${import.meta.env.BASE_URL}wasm_exec.js`;
    script.onerror = () => reject(new Error("Could not load the browser validation engine."));
    script.onload = async () => {
      try {
        const go = new window.Go();
        const response = await fetch(`${import.meta.env.BASE_URL}nostekon-browser.wasm`);
        if (!response.ok) throw new Error("Could not download the browser validation engine.");
        const { instance } = await WebAssembly.instantiate(await response.arrayBuffer(), go.importObject);
        window.nostekonReady = resolve;
        void go.run(instance).catch(reject);
      } catch (error) {
        reject(error);
      }
    };
    document.head.appendChild(script);
  });
  return engine;
}

export async function request(path: string, body = "", signal?: AbortSignal, extraHeaders: Record<string, string> = {}): Promise<Response> {
  if (!browserDemo) {
    return fetch(path, path === "/healthz" ? { signal } : {
      method: "POST", headers: { "Content-Type": "application/json", ...extraHeaders }, body, signal,
    });
  }
  await loadEngine();
  if (signal?.aborted) throw signal.reason;
  const result = window.nostekonRequest?.(path, body, extraHeaders);
  if (!result) throw new Error("The browser validation engine is unavailable.");
  return new Response(result.status === 204 ? null : result.body, {
    status: result.status,
    headers: { "Content-Type": "application/json" },
  });
}