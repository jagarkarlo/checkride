export interface DrillDocument {
  apiVersion: "checkride/v1alpha1";
  kind: "Drill";
  metadata: { name: string };
  spec: Record<string, unknown>;
}

export interface Template {
  id: string;
  label: string;
  summary: string;
  document: DrillDocument;
}

export const templates: Template[] = [
  {
    id: "namespace-loss",
    label: "Namespace loss",
    summary: "Namespace deleted; prove orders survive with V4 evidence.",
    document: {
      apiVersion: "checkride/v1alpha1",
      kind: "Drill",
      metadata: { name: "shop-namespace-loss" },
      spec: {
        scenario: "namespace-loss",
        target: { namespace: "demo-shop", argocdApplication: "demo-shop", cnpgCluster: "shop-db" },
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
    },
  },
  {
    id: "cluster-loss",
    label: "Cluster loss",
    summary: "Whole source cluster gone; rebuild elsewhere and check data shape.",
    document: {
      apiVersion: "checkride/v1alpha1",
      kind: "Drill",
      metadata: { name: "billing-cluster-loss" },
      spec: {
        scenario: "cluster-loss",
        target: { namespace: "billing", argocdApplication: "billing", cnpgCluster: "billing-db" },
        restore: { into: "separate-cluster" },
        verify: { upTo: "V3" },
        objectives: { rto: "30m", rpo: "5m" },
      },
    },
  },
  {
    id: "bad-migration",
    label: "Bad migration",
    summary: "Schema migration corrupted data; recover to a point in time.",
    document: {
      apiVersion: "checkride/v1alpha1",
      kind: "Drill",
      metadata: { name: "catalog-bad-migration" },
      spec: {
        scenario: "bad-migration",
        target: { namespace: "catalog", cnpgCluster: "catalog-db" },
        restore: { into: "separate-cluster", pointInTime: "2026-10-01T11:59:00+02:00" },
        verify: { upTo: "V4", ledger: true },
        objectives: { rto: "20m", rpo: "1m" },
      },
    },
  },
  {
    id: "lost-secret",
    label: "Lost secret",
    summary: "Credentials Secret deleted; check the workload comes back healthy.",
    document: {
      apiVersion: "checkride/v1alpha1",
      kind: "Drill",
      metadata: { name: "gateway-lost-secret" },
      spec: {
        scenario: "lost-secret",
        target: { namespace: "gateway", argocdApplication: "gateway" },
        restore: { into: "namespace" },
        verify: { upTo: "V2" },
        objectives: { rto: "10m", rpo: "0s" },
      },
    },
  },
];

export const scenarioLabels: Record<string, string> = {
  "namespace-loss": "Namespace loss",
  "cluster-loss": "Cluster loss",
  "bad-migration": "Bad migration",
  ransomware: "Ransomware",
  "lost-secret": "Lost secret",
  "storage-class-mismatch": "Storage class mismatch",
};

const levelDefinitions = [
  { id: "V0", title: "Backup reported success", evidence: "Backup tool status" },
  { id: "V1", title: "Restore reported success", evidence: "Restore job status" },
  { id: "V2", title: "Workload is healthy", evidence: "Ready pods, HTTP and TCP checks" },
  { id: "V3", title: "Data is structurally intact", evidence: "Tables, row counts, checksums" },
  { id: "V4", title: "Data is correct", evidence: "Invariants and acknowledged writes" },
];

export interface PlanLevel {
  id: string;
  title: string;
  evidence: string;
  included: boolean;
}

export interface DrillPlan {
  name: string;
  scenario: string;
  namespace: string;
  application: string;
  database: string;
  restoreInto: string;
  pointInTime: string;
  upTo: number | null;
  levels: PlanLevel[];
  evidence: string[];
  rto: string;
  rpo: string;
}

function record(value: unknown): Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}

function text(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function parseLevel(value: unknown): number | null {
  if (value === undefined) return 4;
  if (typeof value === "number" && Number.isInteger(value) && value >= 0 && value <= 4) return value;
  if (typeof value === "string") {
    const match = /^V?([0-4])$/i.exec(value.trim());
    if (match) return Number(match[1]);
  }
  return null;
}

export function describePlan(document: unknown): DrillPlan {
  const root = record(document);
  const spec = record(root.spec);
  const target = record(spec.target);
  const restore = record(spec.restore);
  const verify = record(spec.verify);
  const objectives = record(spec.objectives);
  const upTo = parseLevel(verify.upTo);

  const evidence: string[] = [];
  if (verify.ledger === true) evidence.push("Acknowledged-write ledger");
  if (Array.isArray(verify.invariants) && verify.invariants.length > 0) {
    const count = verify.invariants.length;
    evidence.push(`${count} SQL ${count === 1 ? "invariant" : "invariants"}`);
  }

  return {
    name: text(record(root.metadata).name),
    scenario: text(spec.scenario),
    namespace: text(target.namespace),
    application: text(target.argocdApplication),
    database: text(target.cnpgCluster),
    restoreInto: text(restore.into) || "separate-cluster",
    pointInTime: text(restore.pointInTime),
    upTo,
    levels: levelDefinitions.map((level, index) => ({ ...level, included: upTo !== null && index <= upTo })),
    evidence,
    rto: text(objectives.rto),
    rpo: text(objectives.rpo),
  };
}

export type JSONInspection =
  | { ok: true; value: unknown }
  | { ok: false; message: string; offset: number; line: number; column: number };

// Finds the first syntax error offset; JSON.parse messages differ across browsers.
function syntaxErrorOffset(source: string): number {
  let index = 0;
  const skip = () => {
    while (index < source.length && " \t\n\r".includes(source[index])) index++;
  };
  const fail = (): never => {
    throw index;
  };
  const literal = (word: string) => {
    if (source.startsWith(word, index)) index += word.length;
    else fail();
  };
  const string = () => {
    index++;
    while (index < source.length) {
      const char = source[index];
      if (char === '"') {
        index++;
        return;
      }
      if (char === "\\") index++;
      else if (char < " ") fail();
      index++;
    }
    fail();
  };
  const number = () => {
    const match = /^-?(0|[1-9]\d*)(\.\d+)?([eE][+-]?\d+)?/.exec(source.slice(index));
    if (!match) fail();
    index += match![0].length;
  };
  const value = (): void => {
    skip();
    const char = source[index];
    if (char === "{") {
      index++;
      skip();
      if (source[index] === "}") {
        index++;
        return;
      }
      for (;;) {
        skip();
        if (source[index] !== '"') fail();
        string();
        skip();
        if (source[index] !== ":") fail();
        index++;
        value();
        skip();
        if (source[index] === ",") index++;
        else if (source[index] === "}") {
          index++;
          return;
        } else fail();
      }
    }
    if (char === "[") {
      index++;
      skip();
      if (source[index] === "]") {
        index++;
        return;
      }
      for (;;) {
        value();
        skip();
        if (source[index] === ",") index++;
        else if (source[index] === "]") {
          index++;
          return;
        } else fail();
      }
    }
    if (char === '"') return string();
    if (char === "t") return literal("true");
    if (char === "f") return literal("false");
    if (char === "n") return literal("null");
    return number();
  };
  try {
    value();
    skip();
    if (index < source.length) fail();
  } catch (offset) {
    return typeof offset === "number" ? offset : 0;
  }
  return source.length;
}

export function inspectJSON(source: string): JSONInspection {
  try {
    return { ok: true, value: JSON.parse(source) };
  } catch (error) {
    const offset = Math.min(syntaxErrorOffset(source), source.length);
    const before = source.slice(0, offset).split("\n");
    return {
      ok: false,
      message: error instanceof Error ? error.message : "Invalid JSON",
      offset,
      line: before.length,
      column: before[before.length - 1].length + 1,
    };
  }
}

export function fieldPathOf(message: string): string[] | null {
  const match = /^([A-Za-z]\w*(?:\.\w+)*):\s/.exec(message);
  return match ? match[1].split(".") : null;
}

export function locateField(source: string, path: string[]): number {
  let from = 0;
  let found = -1;
  for (const segment of path) {
    if (/^\d+$/.test(segment)) continue;
    const pattern = new RegExp(`"${segment}"\\s*:`, "g");
    pattern.lastIndex = from;
    const match = pattern.exec(source);
    if (!match) break;
    found = match.index;
    from = match.index + match[0].length;
  }
  return found;
}

export type TokenKind = "key" | "string" | "number" | "literal" | "punct" | "plain";

export function highlight(source: string): { kind: TokenKind; text: string }[] {
  const tokens: { kind: TokenKind; text: string }[] = [];
  const pattern = /("(?:\\.|[^"\\\n])*"?)(\s*:)?|(-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)|\b(true|false|null)\b|([{}[\],])/g;
  let last = 0;
  for (let match = pattern.exec(source); match; match = pattern.exec(source)) {
    if (match.index > last) tokens.push({ kind: "plain", text: source.slice(last, match.index) });
    if (match[1] !== undefined) {
      tokens.push({ kind: match[2] ? "key" : "string", text: match[1] });
      if (match[2]) tokens.push({ kind: "punct", text: match[2] });
    } else if (match[3] !== undefined) tokens.push({ kind: "number", text: match[3] });
    else if (match[4] !== undefined) tokens.push({ kind: "literal", text: match[4] });
    else tokens.push({ kind: "punct", text: match[5] });
    last = pattern.lastIndex;
  }
  if (last < source.length) tokens.push({ kind: "plain", text: source.slice(last) });
  return tokens;
}
