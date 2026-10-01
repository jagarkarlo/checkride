const sources = import.meta.glob<string>("../../examples/runs/*.run.json", { query: "?raw", import: "default" });
const info: Record<string, { label: string; summary: string }> = {
  "k3d-postgresql": { label: "Isolated PostgreSQL restore", summary: "Recorded lab / two clusters / V3" },
  "mlflow-namespace-loss": { label: "MLflow namespace loss", summary: "Synthetic / V4 with write ledger" },
  "crud-cluster-loss": { label: "CRUD cluster loss", summary: "Synthetic / failed row counts and RTO" },
};

export const samples = Object.entries(sources).map(([path, load]) => {
  const id = path.split("/").pop()!.replace(".run.json", "");
  return { id, load, ...(info[id] ?? { label: id, summary: "Example evidence" }) };
}).sort((left, right) => left.id === "k3d-postgresql" ? -1 : right.id === "k3d-postgresql" ? 1 : left.label.localeCompare(right.label));