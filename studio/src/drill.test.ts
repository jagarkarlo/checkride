import { describe, expect, it } from "vitest";
import { describePlan, fieldPathOf, inspectJSON, locateField, templates } from "./drill";

describe("inspectJSON", () => {
  it("accepts a well-formed document", () => {
    expect(inspectJSON('{"kind":"Drill"}')).toEqual({ ok: true, value: { kind: "Drill" } });
  });

  it("reports the line of a syntax error", () => {
    const result = inspectJSON('{\n  "kind": "Drill",\n  "spec": }\n');
    expect(result.ok).toBe(false);
    if (!result.ok) expect(result.line).toBe(3);
  });
});

describe("describePlan", () => {
  it("summarizes a V4 drill and marks the levels it covers", () => {
    const plan = describePlan(templates[0].document);
    expect(plan.name).toBe("shop-namespace-loss");
    expect(plan.restoreInto).toBe("separate-cluster");
    expect(plan.levels.map((level) => level.included)).toEqual([true, true, true, true, true]);
    expect(plan.evidence).toContain("Acknowledged-write ledger");
  });

  it("defaults to V4 and a separate cluster like the API", () => {
    const plan = describePlan({ spec: { scenario: "cluster-loss", verify: {} } });
    expect(plan.upTo).toBe(4);
    expect(plan.restoreInto).toBe("separate-cluster");
  });

  it("stops the ladder at the requested level", () => {
    const plan = describePlan({ spec: { verify: { upTo: "V2" } } });
    expect(plan.levels.filter((level) => level.included)).toHaveLength(3);
  });

  it("tolerates documents that are not objects", () => {
    expect(describePlan(42).name).toBe("");
  });
});

describe("error locations", () => {
  it("extracts the field path from an API message", () => {
    expect(fieldPathOf("spec.target.namespace: field required")).toEqual(["spec", "target", "namespace"]);
    expect(fieldPathOf("spec.verify.invariants.0: name, sql and expect are required")).toEqual([
      "spec", "verify", "invariants", "0",
    ]);
    expect(fieldPathOf("request body exceeds 1 MiB")).toBeNull();
  });

  it("finds the deepest existing key in the text", () => {
    const text = JSON.stringify({ spec: { target: { cnpgCluster: "db" }, objectives: { rto: "x" } } }, null, 2);
    expect(locateField(text, ["spec", "objectives", "rto"])).toBe(text.indexOf('"rto"'));
    expect(locateField(text, ["spec", "target", "namespace"])).toBe(text.indexOf('"target"'));
    expect(locateField(text, ["missing"])).toBe(-1);
  });
});

describe("templates", () => {
  it("covers distinct scenarios with unique names", () => {
    const names = templates.map((template) => template.document.metadata.name);
    expect(new Set(names).size).toBe(templates.length);
    expect(templates.length).toBeGreaterThanOrEqual(3);
  });
});
