---
title: DrillRun evidence schema & report reference
description: Reference documentation for recorded DrillRun evidence documents and the computed verification report.
---

Nostekon strictly distinguishes between what you intend to test (`Drill`) and what was observed during an actual test run (`DrillRun`).

## `DrillRun` schema (`checkride/v1alpha1`)

A `DrillRun` is an audit-ready JSON document capturing timing, execution phases, check outcomes, and write ledger state. The versioned JSON Schema is served by the API at `GET /api/v1/schemas/drillrun`.

### Document structure

```json
{
  "apiVersion": "checkride/v1alpha1",
  "kind": "DrillRun",
  "metadata": {
    "name": "mlflow-namespace-loss-20261001",
    "drill": "mlflow-namespace-loss"
  },
  "spec": {
    "scenario": "namespace-loss",
    "upTo": "V4",
    "v4Evidence": { "invariants": ["every-run-has-an-experiment"] },
    "objectives": { "rto": "15m", "rpo": "5m" }
  },
  "status": {
    "failureAt": "2026-10-01T10:00:00Z",
    "completedAt": "2026-10-01T10:11:43Z",
    "phases": [
      {
        "name": "provision-restore-cluster",
        "startedAt": "2026-10-01T10:00:42Z",
        "endedAt": "2026-10-01T10:03:18Z"
      }
    ],
    "checks": [
      {
        "level": "V0",
        "name": "base backup completed",
        "passed": true,
        "detail": "barman: backup 20261001T0900 COMPLETED"
      }
    ],
    "ledger": {
      "acks": [
        { "writeId": "run-00001", "ackedAt": "2026-10-01T09:50:00.500Z" }
      ],
      "present": ["run-00001"]
    }
  }
}
```

### Key constraints
- **`status.failureAt` & `status.completedAt`:** High-precision ISO 8601 UTC timestamps. RTO is strictly calculated from failure injection to completion time; uninstrumented gaps are flagged.
- **`status.phases`:** Up to 64 ordered recovery intervals.
- **`status.checks`:** Up to 256 individual assertions tagged with their respective verification level (`V0` to `V4`).
- **`status.ledger`:** Up to 200,000 acknowledged writes and restored database IDs.

---

## Computed report structure

Passing a `DrillRun` document to `POST /api/v1/runs/report` or `checkride-report` outputs an evaluated report object:

| Field | Type | Description |
| --- | --- | --- |
| `verdict` | string | Overall evaluation: `verified`, `failed`, or `incomplete`. |
| `headline` | string | Human-readable executive summary of the result. |
| `requestedLevel` | string | Target verification depth (`V0`–`V4`). |
| `deepestPassed` | string/null | Highest level reached where all lower levels also passed. |
| `firstFailed` | string/null | Lowest level that encountered a failing check. |
| `rto` | object/null | Recovery time breakdown, slowest phase, and objective status. |
| `rpo` | object/null | Mathematical data loss window, holes count, and timeline buckets. |
| `provenance` | object | Whether the exact evidence bytes have a valid signature from an explicitly trusted key. Unsigned reports are `unverified`. |
| `findings` | array | Prioritized list of `error`, `warning`, and `info` diagnostics. |

### Evaluation rules

1. **Contiguous depth:** A drill is only verified to level $N$ if every level from $V_0$ up to $N$ passed. A failed check at $V_2$ prevents claiming $V_3$ or $V_4$, even if data assertions succeeded.
2. **Strict V4 requirements:** Level V4 requires either an external write ledger or application invariants with matching checks.
3. **Ledger consistency:** The presence of a "hole" (an older acknowledged write missing while a newer write is restored) fails V4 automatically.
4. **Objective enforcement:** If an RTO or RPO objective is specified, exceeding the threshold fails the drill. If an objective is configured but no measurements are present, the verdict is `incomplete`.

## Detached Ed25519 attestation

`checkride-attest` signs the SHA-256 digest of the exact DrillRun file bytes and
writes a separate JSON sidecar. It does not add a claimed identity to the
DrillRun document, so existing schema consumers remain compatible.

```bash
install -d -m 700 "$HOME/.config/checkride"
install -d -m 700 "$HOME/.config/checkride/signing" "$HOME/.config/checkride/trusted-keys"
go run ./cmd/checkride-attest keygen --private "$HOME/.config/checkride/signing/signing-key.pem" --public "$HOME/.config/checkride/trusted-keys/operator.pem"
go run ./cmd/checkride-attest sign --evidence run.json --key "$HOME/.config/checkride/signing/signing-key.pem" --output run.attestation.json
go run ./cmd/checkride-attest verify --evidence run.json --attestation run.attestation.json --trusted-key "$HOME/.config/checkride/trusted-keys/operator.pem"
go run ./cmd/checkride-report --attestation run.attestation.json --trusted-key "$HOME/.config/checkride/trusted-keys/operator.pem" run.json
```

Verification must use a public key that the verifier already trusts through an
independent distribution channel. A key included beside the evidence is not a
trust anchor. The signature proves that the exact bytes were signed by the
holder of that key; it does not prove the runner's observations are truthful,
map the key to a real-world person, or provide a trusted signing timestamp.
Unix-like systems reject private keys accessible to group or other users;
Windows users must restrict the private key with filesystem ACLs. Keep private
keys in the signing directory, separate from the API's trusted-keys directory.
The report CLI marks reports `unverified` unless both `--attestation` and
`--trusted-key` are supplied; a valid signature adds the signer key ID and
evidence digest to the report. An invalid signature stops report generation.

The local report API optionally verifies the base64-encoded
`X-Checkride-Attestation` header against the public keys in
`CHECKRIDE_TRUSTED_KEYS_DIR`. Put only `*.pem` PKIX Ed25519 public keys there;
the API loads at most 128 keys during startup and fails startup on an empty
configured directory or an invalid key. The local Studio's **Attach
attestation** control sends the sidecar to this API. A valid response includes
the verified key ID and evidence digest; an unknown key or changed evidence is
rejected. If a sidecar is sent while no trust store is configured, the API
returns `503`. Unsigned requests still return `provenance.status: unverified`.
The static browser demo has no trusted-key configuration and therefore cannot
verify a signature; it explicitly reports that limitation instead.

For scripts that call the endpoint directly:

```bash
sidecar=$(base64 < run.attestation.json | tr -d '\n')
curl -sS http://localhost:8080/api/v1/runs/report \
  -H 'Content-Type: application/json' \
  -H "X-Checkride-Attestation: $sidecar" \
  --data-binary @run.json
```
