# Deferred Algorithm Arena And Checkpoint Research

Date: 2026-07-22

## Status And Precedence

This document preserves design research removed while narrowing Feature 003. It is not an implementation specification, confirmed baseline, ADR, storage authority, or permission to scaffold future capability.

The current Feature 003 boundary remains authoritative in `0007-mvp-implementation-decisions.md` and `0009-confirmed-design-baseline.md`. Current storage and Module Interfaces remain authoritative in `0008-element-storage-sync-recovery-design.md` and `0010-deep-module-interface-design.md`. When this document conflicts with any of them, this document loses.

None of the names, file paths, event fields, policies, algorithms, formulas, failure behavior, or publication sequences below are fixed. A later feature must research SuperMemo behavior again, resolve the open questions, and replace this document with a specification and decision record before implementation.

## Why This Research Is Deferred

Algorithm Arena is deferred until all required participant algorithms and SuperMemo's weighting behavior are verified. In particular, the participant set, initial weights, adaptive update rule, prediction timing, weighted-interval conversion and rounding, discrepancy behavior, R-Metric calculation, anti-cramming behavior, and internal failure semantics remain incomplete.

Current Schedule Checkpoints are deferred until at least one concrete need exists:

1. Measured full replay of complete `.smr` history is too slow.
2. `.smr` history compaction is introduced.
3. The product explicitly requires scheduling to continue after raw-history loss.

Feature 003 instead retains complete `.smr` history, rebuilds `memo.db` from that authority, and fails affected scheduling closed when required raw history is unavailable.

## Preserved Algorithm Arena Model

The discarded design explored an Item-only Algorithm Arena that compares compatible memory algorithms over the same adopted Item Repetition history and produces one canonical interval. It did not make Topics compete with Item algorithms. Topic `Next` remained an ungraded `topic-afactor-v1` transition.

The explored SuperMemo-aligned participant set was SM-2, SM-15, SM-19, SM-20, and FSRS, with SM-19 also supplying a single-algorithm R-Metric baseline. This set and every associated formula remain hypotheses requiring primary-source verification.

The following concepts were explored:

- a versioned `ScheduleProfile` identifying compatible target/action kinds, ordered Adapter participants, initial parameters, and a decision policy;
- a fixed bootstrap path in which FSRS is primary and Simple is fallback/shadow, without calling that path Algorithm Arena;
- an all-required-participants weighted Arena policy that produces no partial weighted result;
- an explicitly non-Arena emergency path that reuses a valid FSRS candidate and otherwise a valid Simple candidate;
- one canonical user-facing schedule even when multiple candidate intervals are recorded;
- manual rescheduling outside algorithm combination, with explicit override provenance;
- collection-level weights and metrics derived from adopted Item evidence rather than stored as editable optimizer settings.

The previous design called the internal candidate-selection implementation `CandidateDecisionCore`. That name and Module are not retained by current architecture. If a later Arena feature needs such depth, it must prove that the complexity cannot remain private Scheduler implementation.

## Projection Lineages

The discarded design isolated Arena derived state by an immutable key:

```text
{profileId, profileVersion, policyId, policyVersion}
```

Each Arena Projection Lineage would begin from its own declared initial state and fold every adopted Item evidence record compatible with that lineage in deterministic `(occurredAt, eventId)` order. A newer lineage would not inherit learned weights from an older lineage, and historical events would not be rewritten when the active lineage changed.

The explored activation protocol was prebuild then switch:

1. Persist prospective immutable Profile and policy definitions without selecting them.
2. Capture a local canonical authority digest and build the prospective lineage outside the Scheduler write lease.
3. Enter the existing collection Scheduler write lease.
4. Incorporate authority changes since the captured digest or perform a complete refold.
5. Validate and publish the prospective derived state.
6. Atomically replace a minimal synchronized `active-lineage.json` pointer.

The pointer replacement was only a local process commit point, never a distributed transaction, cross-device CAS, DejaVu repository revision, or global ordering rule. DejaVu would still select whole conflicting files and preserve losing versions in native sync history.

This activation design is not current authority. A later feature must determine whether an active pointer, immutable definition registry, or retained historical executable policy is needed at all.

## Prediction And Decision Evidence

The discarded design considered capturing predictions before answer reveal so Arena scoring could not see the learner's outcome. A local `PredictionSnapshot` would have bound:

- Element ID and observed `baseSchedulingEventId`;
- Profile, decision-policy, and Adapter versions;
- prediction time;
- each available participant recall prediction.

On grading, the snapshot and observed result would have been written atomically into the accepted `.smr` event beside participant candidates and final states. Abandoning an answer reveal would not create an event. A missing or stale snapshot would have made that Repetition ineligible for some Arena metrics without discarding an otherwise valid grade.

The explored weighted event envelope also included `arenaDecisionContext`: exact normalized participant weights, every accumulator capable of affecting combination, and an evidence watermark:

```json
{
  "foldVersion": 1,
  "eventCount": 0,
  "lastEventKey": null,
  "digest": "sha256:..."
}
```

The digest would identify the complete canonical decision-time evidence set rather than a chronological prefix. Historical decision context would be audit evidence only and would not seed a rebuild of current Arena state.

Feature 003 deliberately has no `PrepareItemPrediction` action, `PredictionSnapshot`, Arena event envelope, or evidence watermark. A later Arena specification must reconsider whether pre-answer capture is necessary and how it interacts with session recovery and synchronization.

## Observed And Replayed Evidence

The discarded design distinguished two evidence origins for a target lineage:

- `observed`: a same-lineage event with the complete predictions actually captured before its outcome;
- `replayed`: predictions reconstructed for a foreign-lineage or pre-Arena event under the target lineage's own configuration.

The proposed prequential replay processed one Element's adopted causal chain in order. For each historical Repetition, every required prediction was produced from prior state before the current outcome was supplied to Adapter `Review`. Replayed candidate schedules were simulation output only and never replaced the event's historical schedule, due date, lifecycle, or canonical decision.

A recorded same-lineage prediction failure could not be repaired by replay. It represented what was actually unavailable at decision time. Foreign stored prediction subsets could not be mixed with newly derived participants; replay had to produce one complete target-lineage pack.

## Replay Quarantine

The discarded design stopped replay after trustworthy continuation state was lost for one `{lineage, elementId}` pair:

- failure before a complete prediction pack excluded the current event and started quarantine;
- failure while applying the outcome could retain the complete current prediction/outcome for scoring, but discarded partial next state and started quarantine afterward;
- later foreign-lineage or pre-Arena events stayed excluded while quarantined;
- replay could not skip the failed transition, continue from stale state, or call `Initialize` as an implicit repair.

Replay could resume only after a same-lineage event supplied complete observed predictions and valid final state for every required participant, or after a lineage-declared reset followed by valid formal initialization. Events inside the gap remained excluded.

Replay Quarantine was intended as deterministic derived state, not an event, user choice, or separate public Module. Its necessity and exact recovery rules remain unverified.

## Preserved Checkpoint Model

The discarded Checkpoint design separated immutable raw history from a synchronized reducer continuation baseline. A Checkpoint was not intended to be another event log, a `memo.db` export, a queue cursor, or caller-editable configuration.

It explored two Checkpoint kinds:

- one flat Root Checkpoint per current root `.sme`, named by stable root Element ID and containing current per-Element scheduling, Adapter, and schedule-neutral overlay reducer state;
- one collection Arena Checkpoint per retained Arena Projection Lineage, containing only lineage-specific weight, metric, normalizer, R-Metric, replay-continuation, and quarantine state.

The explored storage layout was:

```text
workspace/data/storage/siyuanmemo/scheduler/
  checkpoints/
    roots/
      <root-element-id>.json
    arena/
      <profile-id>/<profile-version>/<policy-id>/<policy-version>.json
```

Internal Elements shared their owning root's Root Checkpoint. Moving a root document would not rename its flat ID-addressed Checkpoint. Promotion and demotion would have transferred reducer state between owning roots.

No such files exist in the current Feature 003 design.

## Exact Evidence Coverage

The discarded design required exact event membership rather than a timestamp cutoff, device vector, repository revision, maximum event time, or whole-month digest. Late offline events do not necessarily form a chronological prefix, and one monthly file may contain unrelated roots.

An explored Root Checkpoint shape was:

```json
{
  "spec": 1,
  "rootElementId": "20260722010101-abcdefg",
  "coverage": {
    "spec": 1,
    "foldVersion": 1,
    "eventCount": 1,
    "events": [
      {
        "partition": "2026-07",
        "eventId": "20260722090000-hijklmn",
        "eventDigest": "sha256:0123456789abcdef"
      }
    ],
    "digest": "sha256:fedcba9876543210"
  },
  "elements": [
    {
      "elementId": "20260722010102-opqrstu",
      "adoptedHeadEventId": "20260722090000-hijklmn",
      "state": {},
      "adapterStates": [],
      "causalAnchors": [],
      "overlayFrontiers": {}
    }
  ]
}
```

Coverage entries would have been sorted and based on a versioned canonical semantic event form so JSON whitespace and object-key order could not change identity. Same-ID/same-payload events could collapse, while same-ID/different-payload corruption remained detectable.

The explored `causalAnchors` retained every base-addressable scheduling transition needed for a late child to extend a formerly superseded branch after covered raw payloads were gone. `overlayFrontiers` retained complete reducer continuation for schedule-neutral Pending, Final Drill, and priority operations. Full grades, candidates, predictions, and audit payloads remained raw `.smr` evidence.

These structures are research notes, not approved schemas. A future specification must prove which fields are actually necessary from measured reducer behavior.

## Publication And Failure Research

The discarded publication sequence was:

```text
monthly .smr
  -> affected Root Checkpoint
  -> active Arena Checkpoint when applicable
  -> memo.db
```

The `.smr` replacement remained the acceptance point, and no Checkpoint could precede its event. The design explored an `accepted-pending-recovery` result when a later baseline or projection publication failed, with LearningSession preventing a duplicate grade while Runtime recovered derived state.

Current Feature 003 does not use that error. It publishes `.smr` before `memo.db` and reports `projection-refresh-failed` with `reviewAccepted = true` and the accepted event ID when projection publication fails. A later Checkpoint feature must explicitly decide whether another error contract is justified.

For cross-root moves, the discarded ordering inserted destination content and destination Checkpoint state before removing source content and source Checkpoint state, then wrote `sort.json` and `memo.db`. This preferred a detectable duplicate over loss. Current Feature 003 has no Checkpoint writes; its authority order remains destination `.sme`, source `.sme`, `sort.json`, then projection.

## Sync Conflict Research

The discarded design never field-merged Checkpoints. It proposed validating the complete file selected by DejaVu and inspecting the losing native-history version:

- when complete raw events were available, rebuild the Checkpoint from canonical event union;
- when covered raw payloads were unavailable, one valid selected Checkpoint might anchor continuation with later events folded on top;
- two incomparable or conflicting baselines could not be spliced into an unproven reducer state;
- a losing Checkpoint stayed in native history and never became a visible Element conflict root.

Profile and policy definitions were explored as immutable identity/version-addressed files. Different bytes at the same identity/version path were treated as authority corruption rather than field-merged configuration. `active-lineage.json` conflicts were explored as whole-file pointer conflicts resolved by DejaVu selection followed by complete local validation and rebuild.

None of these rules apply to current storage because the files do not exist. A future sync specification must re-evaluate them against the implemented SiYuan synchronization lifecycle.

## Compaction And Raw-History Loss

The discarded design retained `.smr` indefinitely even after adding Checkpoints. Compaction was not implicit and could not reuse SiYuan workspace-history retention settings. Any later compaction feature would have needed to:

1. define which audit and rebuild promises survive;
2. catch up every reducer or Arena lineage promised to remain recoverable;
3. prove exact Coverage before removing payloads;
4. define how unsupported historical algorithms remain executable;
5. expose missing audit history separately from current schedule availability;
6. fail closed when no valid continuation proof exists.

The current simpler rule is stronger: no automatic compaction and no scheduling continuation after unrecoverable raw-history loss.

## Questions For Future Specification

Before reviving Algorithm Arena:

1. Which algorithms and versions actually participate in SuperMemo 20?
2. When is each prediction made, and which information may it observe?
3. How are weights initialized and updated?
4. How are candidate intervals normalized, combined, rounded, and constrained?
5. What exactly is R-Metric and which baseline does SuperMemo use?
6. What happens when one participant fails?
7. Does historical replay match SuperMemo behavior, or is it a SiYuanMemo extension?
8. Is an activation pointer necessary, or can one fixed configured algorithm set suffice?

Before reviving Checkpoints:

1. What measured replay cost is unacceptable?
2. Is the goal startup speed, compaction, raw-loss continuation, or more than one of these?
3. Which reducers require continuation state, and which can still replay cheaply?
4. Can a smaller per-month index solve the measured problem without synchronized baselines?
5. What exact proof is required before raw events may be removed?
6. How should missing raw audit history be reported independently from current scheduling?
7. Does synchronization need Checkpoint authority, or should Checkpoints remain disposable local acceleration?

Until these questions are answered in a new specification, the simplest complete system remains full `.smr` retention plus deterministic `memo.db` rebuild.
