package verify

import (
	"os"
	"strings"
	"testing"
)

func loadRun(t *testing.T, name string) *Evidence {
	t.Helper()
	data, err := os.ReadFile("../../examples/runs/" + name)
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := ParseEvidence(data)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return evidence
}

func TestVerifiedRunReachesRequestedLevel(t *testing.T) {
	report := Build(loadRun(t, "mlflow-namespace-loss.run.json"))
	if report.Verdict != Verified || report.DeepestPassed == nil || *report.DeepestPassed != "V4" || report.FirstFailed != nil {
		t.Fatalf("verdict=%s deepest=%v first=%v findings=%+v", report.Verdict, report.DeepestPassed, report.FirstFailed, report.Findings)
	}
	if report.RTO == nil || report.RTO.Seconds != 703 || report.RTO.Met == nil || !*report.RTO.Met {
		t.Fatalf("rto = %+v", report.RTO)
	}
	if report.RTO.SlowestPhase != "restore-database-pitr" {
		t.Fatalf("slowest phase = %q", report.RTO.SlowestPhase)
	}
	rpo := report.RPO
	if rpo == nil || rpo.Lost != 75 || rpo.Holes != 0 || rpo.Seconds != 38 || rpo.Met == nil || !*rpo.Met {
		t.Fatalf("rpo = %+v", rpo)
	}
	if len(rpo.Timeline) != ledgerBuckets {
		t.Fatalf("timeline buckets = %d", len(rpo.Timeline))
	}
	total := 0
	for _, bucket := range rpo.Timeline {
		total += bucket.Recovered + bucket.Lost
	}
	if total != rpo.Acknowledged {
		t.Fatalf("timeline holds %d writes, want %d", total, rpo.Acknowledged)
	}
	ledgerChecks := report.Levels[V4].Checks
	if last := ledgerChecks[len(ledgerChecks)-1]; last.Source != "ledger" || !last.Passed {
		t.Fatalf("ledger check = %+v", last)
	}
}

func TestFailedRunReportsFirstFailureAndMissedRTO(t *testing.T) {
	report := Build(loadRun(t, "crud-cluster-loss.run.json"))
	if report.Verdict != Failed || report.FirstFailed == nil || *report.FirstFailed != "V3" {
		t.Fatalf("verdict=%s first=%v", report.Verdict, report.FirstFailed)
	}
	if report.DeepestPassed == nil || *report.DeepestPassed != "V2" {
		t.Fatalf("deepest = %v", report.DeepestPassed)
	}
	if !strings.Contains(report.Headline, "table row counts") {
		t.Fatalf("headline = %q", report.Headline)
	}
	if report.RTO == nil || report.RTO.Met == nil || *report.RTO.Met {
		t.Fatalf("expected missed RTO, got %+v", report.RTO)
	}
	if report.RTO.UncoveredSeconds != 166 {
		t.Fatalf("uncovered = %v, want 166", report.RTO.UncoveredSeconds)
	}
	if report.Findings[0].Severity != "error" {
		t.Fatalf("errors must sort first: %+v", report.Findings)
	}
	if report.Levels[V4].InScope {
		t.Fatal("V4 is beyond the requested V3")
	}
}

const minimalRun = `{
	"apiVersion": "checkride/v1alpha1", "kind": "DrillRun",
	"metadata": {"name": "demo"},
	"spec": {"upTo": "V2", "objectives": {"rpo": 10}},
	"status": {
		"failureAt": "2026-10-01T10:00:10Z",
		"checks": [
			{"level": "V0", "name": "backup", "passed": true},
			{"level": 2, "name": "pods", "passed": true}
		],
		"ledger": {
			"acks": [{"writeId": "a", "ackedAt": "2026-10-01T09:59:40Z"}, {"writeId": "b", "ackedAt": "2026-10-01T10:00:05Z"}],
			"present": ["a"]
		}
	}
}`

func TestGapInLevelsIsIncompleteAndRPOMissFails(t *testing.T) {
	evidence, err := ParseEvidence([]byte(minimalRun))
	if err != nil {
		t.Fatal(err)
	}
	report := Build(evidence)
	if report.DeepestPassed == nil || *report.DeepestPassed != "V0" {
		t.Fatalf("a missing V1 must stop the chain at V0, got %v", report.DeepestPassed)
	}
	if report.Verdict != Failed || report.RPO.Seconds != 30 || *report.RPO.Met {
		t.Fatalf("expected RPO miss: verdict=%s rpo=%+v", report.Verdict, report.RPO)
	}

	evidence, _ = ParseEvidence([]byte(strings.Replace(minimalRun, `"rpo": 10`, `"rpo": "1m"`, 1)))
	if report := Build(evidence); report.Verdict != Incomplete {
		t.Fatalf("verdict = %s, want incomplete; headline %q", report.Verdict, report.Headline)
	}
}

func TestParseEvidenceReportsProblemsWithPaths(t *testing.T) {
	body := `{"apiVersion": "v1", "kind": "DrillRun", "metadata": {"name": "Bad_Name"},
		"spec": {"upTo": "V9", "objectives": {"rto": "soon"}},
		"status": {
			"phases": [{"name": "restore", "startedAt": "2026-10-01T10:05:00Z", "endedAt": "2026-10-01T10:00:00Z"}],
			"checks": [{"level": "V1", "name": ""}],
			"ledger": {"acks": [{"writeId": "a", "ackedAt": "2026-10-01T10:00:00Z"}, {"writeId": "a", "ackedAt": "2026-10-01T10:00:01Z"}], "present": []}
		}}`
	_, err := ParseEvidence([]byte(body))
	problems, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("err = %v", err)
	}
	joined := strings.Join(problems.Problems, "\n")
	for _, want := range []string{
		"apiVersion:", "metadata.name:", "spec.upTo:", "spec.objectives.rto:", "status.failureAt: field required",
		"status.phases.0.endedAt:", "status.checks.0.name:", "status.checks.0.passed:", "status.ledger.acks.1.writeId: duplicate",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing problem %q in:\n%s", want, joined)
		}
	}
}

func TestParseEvidenceRejectsUnknownFields(t *testing.T) {
	if _, err := ParseEvidence([]byte(strings.Replace(minimalRun, `"kind"`, `"extra": 1, "kind"`, 1))); err == nil {
		t.Fatal("unknown fields must be rejected")
	}
}
