---
title: DrillRun evidence schema & report reference
description: Reference documentation for recorded DrillRun evidence documents and the computed verification report.
---

Nostekon strictly distinguishes between what you intend to test (`Drill`) and what was observed during an actual test run (`DrillRun`).

## `DrillRun` schema (`nostekon/v1alpha1`)

A `DrillRun` is an audit-ready JSON document capturing timing, execution phases, check outcomes, and write ledger state. The versioned JSON Schema is served by the API at `GET /api/v1/schemas/drillrun`. Evidence recorded before the Nostekon rename uses `checkride/v1alpha1` and is still accepted, so earlier captures and their detached signatures keep verifying.

### Document structure

```json
{
  "apiVersion": "nostekon/v1alpha1",
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

Passing a `DrillRun` document to `POST /api/v1/runs/report` or `nostekon-report` outputs an evaluated report object:

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

`nostekon-attest` signs the SHA-256 digest of the exact DrillRun file bytes and
writes a separate JSON sidecar. It does not add a claimed identity to the
DrillRun document, so existing schema consumers remain compatible.

```bash
install -d -m 700 "$HOME/.config/nostekon"
install -d -m 700 "$HOME/.config/nostekon/signing" "$HOME/.config/nostekon/trusted-keys"
go run ./cmd/nostekon-attest keygen --private "$HOME/.config/nostekon/signing/signing-key.pem" --public "$HOME/.config/nostekon/trusted-keys/operator.pem"
go run ./cmd/nostekon-attest sign --evidence run.json --key "$HOME/.config/nostekon/signing/signing-key.pem" --output run.attestation.json
go run ./cmd/nostekon-attest verify --evidence run.json --attestation run.attestation.json --trusted-key "$HOME/.config/nostekon/trusted-keys/operator.pem"
go run ./cmd/nostekon-report --attestation run.attestation.json --trusted-key "$HOME/.config/nostekon/trusted-keys/operator.pem" run.json
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
`X-Nostekon-Attestation` header against the public keys in
`NOSTEKON_TRUSTED_KEYS_DIR`. Put only `*.pem` PKIX Ed25519 public keys there;
the API loads at most 128 keys during startup and fails startup on an empty
configured directory or an invalid key. In Studio, attach the sidecar and use
**Check server trust** to send it to this API. A valid response includes
the verified key ID and evidence digest; an unknown key or changed evidence is
rejected. If a sidecar is sent while no trust store is configured, the API
returns `503`. Unsigned requests still return `provenance.status: unverified`.
This server policy is independent of the browser's public-key library. Importing
or trusting a key in Studio does not configure `NOSTEKON_TRUSTED_KEYS_DIR`.

For scripts that call the endpoint directly:

```bash
sidecar=$(base64 < run.attestation.json | tr -d '\n')
curl -sS http://localhost:8080/api/v1/runs/report \
  -H 'Content-Type: application/json' \
  -H "X-Nostekon-Attestation: $sidecar" \
  --data-binary @run.json
```

### Verify in Studio or the browser demo

Verified on 2026-10-08 against both the packaged API and browser WebAssembly.
Both use the same Go Ed25519 verifier as the CLI. No signing or private-key
import is available in Studio.

1. Import the **original** DrillRun JSON in **Evidence report**, then **Attach
   attestation**. Reformatting JSON changes the signed bytes.
2. Open **Public keys**, give the key a label and import the owner's PKIX
   Ed25519 `PUBLIC KEY` PEM. Imports start **Not trusted**. Private keys are
   rejected before any API request.
3. Compare the complete 64-character fingerprint with the owner through an
   independent trusted channel. This is SHA-256 of the raw 32-byte Ed25519 key,
   not a hash of the PEM or PKIX wrapper. Select **Trust public key**, confirm
   the fingerprint checkbox, then **Confirm trust**.
4. Close the dialog, select that key and **Verify signature**. A valid result
   binds the exact original bytes to the selected locally trusted key. Recovery
   verdicts and server provenance remain separate.
5. **Download signed originals** exports the original JSON, detached sidecar,
   public key and a computed signature-check receipt. The receipt and archive
   container are not independently signed. A bundled key is not a trust anchor.

Extract the ZIP into a new directory and independently verify it from the
repository root, using a public key you have already authenticated:

```bash
go run ./cmd/nostekon-attest verify \
  --evidence /path/to/extracted/nostekon.run.json \
  --attestation /path/to/extracted/nostekon.run.attestation.json \
  --trusted-key "$HOME/.config/nostekon/trusted-keys/operator.pem"
```

This is an original-file archive, not a host-job bundle for **Import bundle**.
Import its extracted evidence and sidecar separately. **Save run** retains
original evidence and the computed report, not the sidecar or local verification
receipt; reopen and check the signature again when needed.

The public-key library is local to this browser origin and capped at 20 keys;
there is no silent eviction. Re-importing a key preserves its trust or revoked
state. **Revoke local trust** prevents further checks and exports with that key;
open tabs are notified, and verification/export re-read policy. Deletion requires
confirmation and does not delete evidence. Neither operation revokes the key in
the server configuration or establishes when a signature was made. Clear site
data or a different origin means a different local policy. Keep authenticated
public keys and originals outside browser storage.

Changed evidence, a wrong key, malformed sidecars or unsupported versions fail
verification and disable signed export. Edits, selection changes and policy
changes clear local receipts. Signature validity is not independent capture
authenticity, operator identity, trusted signing time or successful recovery.
