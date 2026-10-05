package metrics

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jagarkarlo/nostekon/internal/verify"
)

// Sample is one Prometheus gauge value with its help text.
type Sample struct {
	Name  string
	Help  string
	Value float64
}

var levelOrdinal = map[string]float64{"V0": 0, "V1": 1, "V2": 2, "V3": 3, "V4": 4}

// FromReport turns a verify.Report into the gauges a dashboard can chart.
// Every sample is always present so a panel never has to guess why a
// series is missing; unmeasured values are reported as -1.
func FromReport(report verify.Report) []Sample {
	verified := 0.0
	if report.Verdict == verify.Verified {
		verified = 1
	}
	samples := []Sample{
		{"nostekon_drill_verified", "1 if the drill verdict is verified, 0 otherwise.", verified},
		{"nostekon_drill_requested_level", "Ordinal (V0=0..V4=4) of the requested verification depth.", level(report.RequestedLevel)},
		{"nostekon_drill_deepest_level", "Ordinal (V0=0..V4=4) of the deepest contiguous level passed, or -1 if none passed.", levelOrNegative(report.DeepestPassed)},
	}
	samples = append(samples, rtoSamples(report.RTO)...)
	samples = append(samples, rpoSamples(report.RPO)...)
	samples = append(samples, Sample{"nostekon_evidence_verified", "1 if the evidence has a verified detached signature, 0 otherwise.", provenanceVerified(report.Provenance)})
	return samples
}

func level(value string) float64 {
	if v, ok := levelOrdinal[value]; ok {
		return v
	}
	return -1
}

func levelOrNegative(value *string) float64 {
	if value == nil {
		return -1
	}
	return level(*value)
}

func provenanceVerified(provenance verify.Provenance) float64 {
	if provenance.Status == "verified" {
		return 1
	}
	return 0
}

func rtoSamples(rto *verify.RTOResult) []Sample {
	if rto == nil {
		return []Sample{
			{"nostekon_recovery_time_seconds", "Measured recovery time from failure to completion, or -1 if unmeasured.", -1},
			{"nostekon_recovery_time_met", "1 if recovery time met its objective, 0 if missed, -1 if unmeasured or no objective was set.", -1},
		}
	}
	met := -1.0
	if rto.Met != nil {
		if *rto.Met {
			met = 1
		} else {
			met = 0
		}
	}
	return []Sample{
		{"nostekon_recovery_time_seconds", "Measured recovery time from failure to completion, or -1 if unmeasured.", rto.Seconds},
		{"nostekon_recovery_time_met", "1 if recovery time met its objective, 0 if missed, -1 if unmeasured or no objective was set.", met},
	}
}

func rpoSamples(rpo *verify.RPOResult) []Sample {
	if rpo == nil {
		return []Sample{
			{"nostekon_data_loss_seconds", "Measured acknowledged-write data loss window, or -1 if unmeasured.", -1},
			{"nostekon_data_loss_met", "1 if data loss met its objective, 0 if missed, -1 if unmeasured or no objective was set.", -1},
			{"nostekon_acknowledged_writes_total", "Acknowledged writes recorded before the failure, or -1 if unmeasured.", -1},
			{"nostekon_acknowledged_writes_lost", "Acknowledged writes not found after recovery, or -1 if unmeasured.", -1},
		}
	}
	met := -1.0
	if rpo.Met != nil {
		if *rpo.Met {
			met = 1
		} else {
			met = 0
		}
	}
	return []Sample{
		{"nostekon_data_loss_seconds", "Measured acknowledged-write data loss window, or -1 if unmeasured.", rpo.Seconds},
		{"nostekon_data_loss_met", "1 if data loss met its objective, 0 if missed, -1 if unmeasured or no objective was set.", met},
		{"nostekon_acknowledged_writes_total", "Acknowledged writes recorded before the failure, or -1 if unmeasured.", float64(rpo.Acknowledged)},
		{"nostekon_acknowledged_writes_lost", "Acknowledged writes not found after recovery, or -1 if unmeasured.", float64(rpo.Lost)},
	}
}

// Format renders samples as Prometheus text exposition format (version 0.0.4).
func Format(samples []Sample) string {
	var buffer bytes.Buffer
	for _, sample := range samples {
		fmt.Fprintf(&buffer, "# HELP %s %s\n", sample.Name, sample.Help)
		fmt.Fprintf(&buffer, "# TYPE %s gauge\n", sample.Name)
		fmt.Fprintf(&buffer, "%s %s\n", sample.Name, formatValue(sample.Value))
	}
	return buffer.String()
}

func formatValue(value float64) string {
	text := strings.TrimRight(fmt.Sprintf("%.6f", value), "0")
	return strings.TrimSuffix(text, ".")
}

const maxPushResponseBytes = 4 << 10

// Push sends samples to a Prometheus Pushgateway, grouped under job and the
// given labels (commonly just "instance"). Label values must not contain a
// "/"; Pushgateway encodes the grouping key into the request path.
func Push(gatewayURL, job string, labels map[string]string, samples []Sample) error {
	if job == "" {
		return fmt.Errorf("pushgateway job name must not be empty")
	}
	path := "/metrics/job/" + job
	keys := make([]string, 0, len(labels))
	for key, value := range labels {
		if value == "" {
			return fmt.Errorf("pushgateway label %q must not be empty", key)
		}
		if strings.Contains(value, "/") {
			return fmt.Errorf("pushgateway label %q must not contain '/'", key)
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		path += "/" + key + "/" + labels[key]
	}
	request, err := http.NewRequest(http.MethodPut, strings.TrimRight(gatewayURL, "/")+path, strings.NewReader(Format(samples)))
	if err != nil {
		return fmt.Errorf("build pushgateway request: %w", err)
	}
	request.Header.Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	response, err := pushClient.Do(request)
	if err != nil {
		return fmt.Errorf("push to pushgateway: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, maxPushResponseBytes))
		return fmt.Errorf("pushgateway returned %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	return nil
}

// pushTimeout bounds how long Push waits for the Pushgateway to respond so a
// CI job cannot hang on an unreachable metrics endpoint.
const pushTimeout = 10 * time.Second

var pushClient = &http.Client{Timeout: pushTimeout}
