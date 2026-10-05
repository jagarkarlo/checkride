import type { Report, RPOResult } from "./report";

export type SuiteRPO = Pick<RPOResult, "acknowledged" | "recovered" | "lost" | "holes" | "unexpected" | "seconds"> & {
  objectiveSeconds: number;
  met: boolean;
};

export interface SuiteCase {
  name: "zero-loss" | "tail-loss" | "budget-loss";
  drillRun: string;
  expectedExitCode: number;
  observedExitCode: number | null;
  passed: boolean;
  rpo?: SuiteRPO;
  detail?: string;
  error?: string;
}

export interface LabSuite {
  apiVersion: "nostekon/lab-suite/v1alpha1";
  kind: "LabSuiteResult";
  status: "running" | "passed" | "failed" | "interrupted";
  passed: boolean;
  cases: SuiteCase[];
}

export interface ReviewedCase {
  recorded: SuiteCase;
  source?: string;
  report: Report | null;
  issues: string[];
}

export interface SuiteReview {
  suite: LabSuite;
  cases: ReviewedCase[];
  evidenceMatches: boolean;
}

export async function evaluateSuite(suite: LabSuite, sources: Map<string, string>, evaluate: (source: string) => Promise<Report>): Promise<SuiteReview> {
  const cases: ReviewedCase[] = [];
  for (const recorded of suite.cases) {
    const source = sources.get(recorded.drillRun);
    const reviewed: ReviewedCase = { recorded, source, report: null, issues: [] };
    cases.push(reviewed);
    if (source === undefined) {
      reviewed.issues.push(`Missing evidence: ${recorded.drillRun}`);
      continue;
    }
    if (new TextEncoder().encode(source).length > 16 * 1024 * 1024) {
      reviewed.issues.push("Evidence exceeds the 16 MiB limit.");
      continue;
    }
    try {
      const report = await evaluate(source);
      reviewed.report = report;
      const exitCode = report.verdict === "verified" ? 0 : report.verdict === "failed" ? 1 : 2;
      if (exitCode !== recorded.observedExitCode) reviewed.issues.push("Evaluated exit code differs from the suite summary.");
      if (report.requestedLevel !== "V4") reviewed.issues.push("Evidence does not request V4 verification.");
      if (!recorded.rpo || !report.rpo) {
        reviewed.issues.push("No complete RPO measurement to compare.");
      } else {
        for (const field of [...countFields, "objectiveSeconds", "met"] as const) {
          if (report.rpo[field] !== recorded.rpo[field]) reviewed.issues.push(`${field} differs from the suite summary.`);
        }
        if (Math.abs(report.rpo.seconds - recorded.rpo.seconds) > 0.000001) reviewed.issues.push("seconds differs from the suite summary.");
        if (!report.rpo.consistent) reviewed.issues.push("The restored ledger is inconsistent.");
      }
    } catch (error) {
      if (error instanceof DOMException && error.name === "AbortError") throw error;
      reviewed.issues.push(error instanceof Error ? error.message : "Could not evaluate evidence.");
    }
  }
  return { suite, cases, evidenceMatches: cases.length > 0 && cases.every((item) => item.issues.length === 0) };
}

const caseNames = ["zero-loss", "tail-loss", "budget-loss"] as const;
const countFields = ["acknowledged", "recovered", "lost", "holes", "unexpected"] as const;

function object(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

export function parseSuite(source: string): LabSuite {
  if (new TextEncoder().encode(source).length > 64 * 1024) throw new Error("Suite summary exceeds the 64 KiB limit.");
  const value: unknown = JSON.parse(source);
  if (!object(value) || value.apiVersion !== "nostekon/lab-suite/v1alpha1" || value.kind !== "LabSuiteResult") {
    throw new Error("Not a supported Nostekon LabSuiteResult.");
  }
  if (!(["running", "passed", "failed", "interrupted"] as unknown[]).includes(value.status) || typeof value.passed !== "boolean" || !Array.isArray(value.cases) || value.cases.length > 3) {
    throw new Error("Invalid lab suite status or cases.");
  }
  for (const [index, item] of value.cases.entries()) {
    if (!object(item) || item.name !== caseNames[index] || item.drillRun !== `${item.name}.drillrun.json` || typeof item.passed !== "boolean" || item.expectedExitCode !== (item.name === "tail-loss" ? 1 : 0) || ![null, 0, 1, 130].includes(item.observedExitCode as number | null)) {
      throw new Error(`Invalid lab suite case ${index + 1}.`);
    }
    for (const field of ["error", "detail"]) {
      if (item[field] !== undefined && (typeof item[field] !== "string" || (item[field] as string).length > 500)) throw new Error(`Invalid ${field} in ${item.name}.`);
    }
    if (item.rpo !== undefined) {
      const rpo = item.rpo;
      if (!object(rpo) || !countFields.every((field) => Number.isInteger(rpo[field]) && (rpo[field] as number) >= 0 && (rpo[field] as number) <= 100) || typeof rpo.seconds !== "number" || !Number.isFinite(rpo.seconds) || rpo.seconds < 0 || !Number.isInteger(rpo.objectiveSeconds) || (rpo.objectiveSeconds as number) < 0 || (rpo.objectiveSeconds as number) > 86400 || typeof rpo.met !== "boolean") {
        throw new Error(`Invalid RPO measurement in ${item.name}.`);
      }
    }
  }
  if (value.passed !== (value.status === "passed") || (value.passed && (value.cases.length !== 3 || value.cases.some((item) => !item.passed)))) {
    throw new Error("Suite status contradicts its case results.");
  }
  return value as unknown as LabSuite;
}