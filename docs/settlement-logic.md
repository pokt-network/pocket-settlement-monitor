# Settlement Logic

This is the critical business logic document for pocket-settlement-monitor.

## Poktroll Tokenomics Events

All 6 event types emitted during the `EndBlocker` of the tokenomics module:

### 1. EventClaimSettled
Emitted when a claim is successfully settled (with or without proof).

| Field | Type | Description |
|-------|------|-------------|
| `SupplierOperatorAddress` | string | Supplier's operator bech32 address |
| `ApplicationAddress` | string | Application's bech32 address |
| `ServiceId` | string | Service identifier |
| `SessionEndBlockHeight` | int64 | Height at which the session ended |
| `ClaimedUpokt` | Coin | Amount of uPOKT claimed |
| `NumRelays` | uint64 | Relays in merkle tree (passed difficulty) |
| `NumClaimedComputeUnits` | uint64 | CUs from merkle tree (before expansion) |
| `NumEstimatedComputeUnits` | uint64 | CUs after difficulty multiplier (real workload) |
| `ProofRequirementInt` | int32 | 0=NOT_REQUIRED, 1=REQUIRED |
| `ClaimProofStatusInt` | int32 | 0=CLAIMED, 1=PROVEN, 2=SETTLED, 3=EXPIRED |
| `RewardDistribution` | map[string]string | Address → amount (e.g., "70upokt") |

### 2. EventClaimExpired
Emitted when a claim expires due to missing or invalid proof.

| Field | Type | Description |
|-------|------|-------------|
| `SupplierOperatorAddress` | string | Supplier's operator address |
| `ApplicationAddress` | string | Application's address |
| `ServiceId` | string | Service identifier |
| `SessionEndBlockHeight` | int64 | Session end height |
| `ClaimedUpokt` | Coin | uPOKT that was claimed (now lost) |
| `NumRelays` | uint64 | Relays in merkle tree |
| `NumClaimedComputeUnits` | uint64 | CUs from merkle tree |
| `NumEstimatedComputeUnits` | uint64 | Estimated CUs |
| `ExpirationReason` | enum | PROOF_MISSING, PROOF_INVALID |
| `ClaimProofStatusInt` | int32 | Proof status at expiration |

### 3. EventSupplierSlashed
Emitted when a supplier is slashed for proof failure.

| Field | Type | Description |
|-------|------|-------------|
| `SupplierOperatorAddress` | string | Slashed supplier's address |
| `ApplicationAddress` | string | Application's address |
| `ServiceId` | string | Service identifier |
| `SessionEndBlockHeight` | int64 | Session end height |
| `ProofMissingPenalty` | Coin | Penalty amount |
| `ClaimProofStatusInt` | int32 | Proof status |

### 4. EventClaimDiscarded
Emitted when a claim is discarded due to unexpected errors (prevents chain halts).

| Field | Type | Description |
|-------|------|-------------|
| `SupplierOperatorAddress` | string | Supplier's address |
| `ApplicationAddress` | string | Application's address |
| `ServiceId` | string | Service identifier |
| `SessionEndBlockHeight` | int64 | Session end height |
| `Error` | string | Error message |
| `ClaimProofStatusInt` | int32 | Proof status |

### 5. EventApplicationOverserviced
Emitted when an application's stake is insufficient to cover the full claim.

| Field | Type | Description |
|-------|------|-------------|
| `ApplicationAddr` | string | Application's address |
| `SupplierOperatorAddr` | string | Supplier's address |
| `ExpectedBurn` | Coin | What should have been burned |
| `EffectiveBurn` | Coin | What was actually burned |

**Critical**: `EffectiveBurn` (NOT `ClaimedUpokt`) is the actual amount paid/burned when overserviced.

### 6. EventApplicationReimbursementRequest
Emitted when an application requests DAO reimbursement.

| Field | Type | Description |
|-------|------|-------------|
| `ApplicationAddr` | string | Application's address |
| `SupplierOperatorAddr` | string | Supplier's operator address |
| `SupplierOwnerAddr` | string | Supplier's owner address |
| `ServiceId` | string | Service identifier |
| `SessionId` | string | Session identifier |
| `Amount` | Coin | Reimbursement amount |

## Estimated Relays (Difficulty Expansion)

This is a critical concept. The merkle tree does NOT contain all relays.

### How Difficulty Works

When relay mining difficulty is higher than base:
1. Only relays where `relayHash < targetDifficultyHash` are included in the merkle tree
2. Higher difficulty = fewer relays in tree, but each represents more work
3. The chain provides both pre-expansion and post-expansion CU counts

### Computing Estimated Relays

From `EventClaimSettled` or `EventClaimExpired`:

```
NumRelays                 = relays in merkle tree (passed difficulty filter)
NumClaimedComputeUnits    = CUs from the merkle tree (before expansion)
NumEstimatedComputeUnits  = CUs after difficulty multiplier (real workload)

DifficultyMultiplier = NumEstimatedComputeUnits / NumClaimedComputeUnits
EstimatedRelays      = NumRelays × DifficultyMultiplier
```

### Edge Cases

- `NumClaimedComputeUnits == 0`: Set multiplier to 1.0, estimated = NumRelays
- `NumClaimedComputeUnits == NumEstimatedComputeUnits`: Base difficulty, multiplier = 1.0
- `BaseRelayDifficultyHashBz` = all 0xFF bytes (easiest difficulty, multiplier always 1.0)

### Why This Matters

- `NumRelays` alone vastly understates real throughput when difficulty > base
- Reports and metrics MUST show both values
- PocketScan discovered this discrepancy — `NumRelays` only shows tree relays

## Overservice Correlation

### Same-Block Guarantee

Both `EventApplicationOverserviced` and `EventClaimSettled` are emitted in the same `EndBlocker` execution, which means they arrive in the **same block's events**.

### Correlation Key

`(SupplierOperatorAddress, ApplicationAddress)` — note field name differences:
- `EventClaimSettled`: `SupplierOperatorAddress`
- `EventApplicationOverserviced`: `SupplierOperatorAddr`

### Correlation Algorithm (In-Memory)

```
For each block flush (all events already in Collector):
  1. Build map: overserviceMap[supplier+app] = {ExpectedBurn, EffectiveBurn}
  2. For each settled claim:
     a. Look up key = (claim.SupplierOperatorAddress, claim.ApplicationAddress)
     b. If found in overserviceMap:
        - Mark claim as overserviced
        - Record EffectiveBurn as actual payment (not ClaimedUpokt)
        - OverserviceDiff = ExpectedBurn - EffectiveBurn
  3. Write all data to SQLite in single batch INSERT
```

No SQLite lookups needed — everything is correlated in memory from the same block.

## ABCI Event Decoding

### Event Format

Events arrive as `abci.Event` with `Type` and `[]EventAttribute`:
```
Event{
  Type: "pocket.tokenomics.EventClaimSettled",
  Attributes: [
    {Key: "supplier_operator_address", Value: "\"pokt1abc...\""},
    {Key: "num_relays", Value: "\"130\""},
    {Key: "mode", Value: "EndBlock"},
    ...
  ],
}
```

### Stripping Non-Proto Attributes

The `"mode"` attribute is injected by Cosmos SDK event manager. Its value (`EndBlock`) is a plain string, not JSON. This breaks `ParseTypedEvent` which expects JSON values.

**Solution**: `filterEventAttrs()` removes known non-proto attributes before parsing.

```go
var knownNonProtoAttrs = map[string]bool{
    "mode": true,
}
```

### ParseTypedEvent

`cosmos-sdk/types.ParseTypedEvent()` reconstructs a JSON object from ABCI attributes and deserializes into the registered proto type. This handles all type conversions (strings, numbers, enums, maps) correctly.

## Proof Status Mapping

`ClaimProofStatusInt` values:
| Value | Name | Description |
|-------|------|-------------|
| 0 | CLAIMED | Claim submitted, no proof yet |
| 1 | PROVEN | Proof verified valid |
| 2 | SETTLED | Settlement complete |
| 3 | EXPIRED | Proof window expired |

## Reward Distribution Format

`RewardDistribution` is a `map[string]string` where:
- Keys are bech32 addresses (supplier operator, owner, DAO, etc.)
- Values are coin amounts like `"70upokt"`

To extract supplier-specific reward: look up supplier's operator address in the map, parse `"70upokt"` → 70.

## Related Documentation

- [Architecture](architecture.md) — System design and data flow
- [Database Schema](database-schema.md) — How events are stored in SQLite
- [Metrics Reference](metrics-reference.md) — Prometheus metrics derived from these events
- [Gap Recovery](gap-recovery.md) — How missed events are backfilled
