# MVP Implementation Decisions

Date: 2026-07-19

## Decision

This document records the MVP implementation defaults that should remove ambiguity before building the first tracer bullet. These decisions keep SiYuanMemo's learning system independent from SiYuan `riff`, while still using the SiYuan kernel, storage conventions, and UI seams.

## Confirmed Decisions

1. **FSRS configuration is independent.** SiYuanMemo stores its own scheduler config under `workspace/data/storage/siyuanmemo/scheduler/`. It does not reuse or mutate SiYuan `riff` flashcard weights, retention, or maximum interval.
2. **FSRS defaults start simple.** The MVP uses `go-fsrs` default parameters for `fsrs-v1`. The first UI may display retention, weights, and maximum interval for diagnostics, but it should not expose full tuning controls until review history and validation exist.
3. **Final drill has no standalone MVP UI.** Reviews below Good may be recorded into session-level final drill state, but the first implementation does not need a separate final drill screen.
4. **Review outcomes persist; scoped session recovery stays local.** Every accepted Topic `Next` or Item grade is written to monthly `.smr` before queue advancement. The default due queue is rebuilt from authoritative state. Filtered, branch, temporary-practice, and other scoped queues may store one disposable recovery snapshot at `workspace/temp/siyuanmemo/active-session.json`; it is not synchronized and has no cross-device revision protocol.
5. **Item creation supports manual Q/A and one cloze.** MVP Item creation includes manual Q/A and single-blank cloze Items. Image occlusion and multi-cloze workflows are later features.
6. **Topic split starts from selection only.** MVP split/extract creates a child Topic from the selected HTML range. Auto-split by headings or paragraphs is reserved for later.
7. **Topic Done follows the SuperMemo-like rule.** If a Topic has child Elements, `Done` asks to dismiss the parent. If it has no child Elements, `Done` asks whether to delete it.
8. **Element note references start with SendToNote.** MVP does not need native Protyle `@` autocomplete. Element note anchors are created by explicit `SendToNote` actions.
9. **Deletion writes history and an immutable event.** Deleting an Element snapshots affected roots to local history and appends minimal deletion metadata to the monthly `.smr`; it does not create one tombstone file per Element.
10. **The first vertical slice is backend-first.** Build seeded Elements, Scheduler, FSRS candidate recording, and the learning loop before Browser or production UI. Real HTML import and richer Topic Reader actions can follow after the learning loop works.
11. **Queue names follow the existing plugin vocabulary.** Default Learn uses `IncrementalLearningQueue` / `incremental-learning`. Other reserved queue identifiers are `RetrievalPracticeQueue` / `retrieval-practice`, `FilterGroupQueue` / `filter-group`, `FinalDrillQueue` / `final-drill`, `NeuralRoamQueue` / `neural-roam`, and `LeechReviewQueue` / `leech`.
12. **Queues use ReviewTarget, not only Element.** MVP targets include `element.topic`, `element.item`, `element.concept`, and `note.block`; future targets may include audio/video without changing scheduler callers.
13. **Block-to-Topic is explicit and live.** One whole SiYuan block with no back-side answer creates a Block-backed Topic through `CreateTopicFromBlock`; its `.sme` payload stores `material.kind = siyuanBlock` and the stable block ID while Protyle continues to edit the live `.sy` material. Prompt/answer or cloze intent creates an Item snapshot through `CreateItemFromBlock` or `CreateClozeItemFromBlock`. MVP does not define a live selected-range Topic contract, and the source `.sy` block is never rewritten by Element creation.
14. **Element storage follows SiYuan's dual-tree model.** One root Element document owns one `.sme`; internal Elements are nested nodes, while root documents use ID-named directories plus `sort.json`. Promotion preserves ID and leaves no mount placeholder.
15. **Review history uses monthly `.smr`.** Immutable scheduling and lifecycle events are partitioned by month, not device ID, and merge by `eventId` set union.
16. **The unified Elements tree supports free mixed ordering.** At a root-document boundary, `sort.json` assigns one shared rank space to direct internal children and child root documents. The UI may drag either storage kind into any sibling position without creating mounts or changing Element identity.
17. **SiYuan assets are shared.** Topic and Item payloads reference `workspace/data/assets/`; asset indexing and cleanup scan both `.sy` and `.sme`.
18. **Indexes are disposable.** `workspace/temp/siyuanmemo/memo.db` is rebuilt from `.sme`, the complete retained `.smr` event set, sort, and scheduler configuration files at startup or after incompatible sync changes.
19. **Encrypted notebooks fail closed.** MVP block conversion, note integration, asset promotion, and indexing must reject operations that would persist decrypted encrypted-notebook content into the plaintext Element store.
20. **Element workflow state is orthogonal.** `.sme` stores `processingState = new | reading | processed`; monthly lifecycle events project `lifecycleState = pending | memorized | dismissed`; algorithm states remain versioned adapter-private data. Dismiss preserves schedule state, Remember restores it and surfaces overdue targets, and Forget resets current schedule state while retaining immutable history.
21. **Add new has one contextual binding.** A Topic created by `AddNewTopic` may store at most one `boundTo` relation to a Concept or Element. It is not structural and is not a backlink. Rebinding replaces it; additional associations are explicit Element links and appear through normal backlinks.
22. **Primary binding participates in default inheritance.** Effective defaults resolve as Element override, `boundTo` Concept context, nearest structural Concept, then collection defaults. Binding to another Element uses that Element's effective Concept but not its per-Element overrides. Missing or cyclic bindings fall back structurally; ordinary backlinks never affect inheritance.
23. **Concurrent review history keeps one adopted causal chain.** Every scheduling-changing event records `baseSchedulingEventId`. `.smr` merge retains all different event IDs, treats same-base events as concurrent siblings, and deterministically adopts the complete valid branch whose terminal is greatest by `(occurredAt, eventId)`. Only that branch drives queues and algorithm state; other branches remain immutable audit history with derived `memo.db` classifications. This rule is owned by one internal `SchedulingLedger`, not by UI callers, algorithm Adapters, device identity, or a pluggable conflict strategy.
24. **HTTP routes stay named and stable.** `/api/symemo/*` uses action-specific routes for the built-in frontend, plugins, automation, and diagnostics. Handlers only translate transport DTOs into Engine commands or queries; there is no generic public `executeCommand` route in the MVP.
25. **Learning Engine has five operation families.** One concrete Engine exposes `CreateElement`, `ChangeElement`, `RunLearningAction`, `SendToNote`, and `Query`. Named route actions are closed variants of these families rather than separate kernel Modules.
26. **Learning Session is separate from scheduling truth.** A concrete `LearningSession` owns Start, Act, Current, answer visibility, current target, queue advancement, and disposable scoped recovery. Opening another Element is only a preview and does not mutate the session.
27. **Scheduler has two operations.** `BuildQueue` constructs ReviewTarget plans and `Apply` commits a closed scheduling-action variant through `SchedulingLedger`. Scheduler does not own Element deletion, Browser queries, session UI state, or note integration.
28. **Topic material processing is internal.** HTML cleaning, selection extraction, stable nodes, and annotation remapping have no separate action Interface. Create workflows use `CreateElement`, persistence workflows use `ChangeElement`, and fine-grained TinyMCE edits remain local until Save Topic HTML.
29. **Only real variation gets Adapters.** The algorithm seam exposes Describe, Initialize, Predict, Review, and Migrate for `simple-v1`, `fsrs-v1`, and `topic-afactor-v1`, with declared target/action compatibility. SiYuan integration uses narrow `BlockReferenceReader`, `BlockSnapshotReader`, `NoteAnchorWriter`, `AssetStore`, and `HistorySnapshotWriter` Adapters. Concrete Ledger and query/index Modules do not receive hypothetical Go interfaces.
30. **Element types select different scheduling paths.** Items use `fsrs-v1` with `simple-v1` fallback/shadow and accept `GradeItem(0..5)`. HTML-backed and Block-backed Topics use ungraded `topic-afactor-v1` and accept `NextTopic`. Concepts use `none` until explicitly enrolled, then use the Topic path. No profile registry or active-policy pointer is required for these fixed MVP choices.
31. **Scheduler defaults follow SiYuan's configuration lifecycle.** Engine construction and queries compose versioned built-in defaults with valid persisted scheduler files and never write authority. A separate host-owned startup bootstrap persists only missing files before API availability when SiYuan is writable, skips read-only mode and byte-identical data, and never replaces an invalid existing file. Persisted configuration is required before the first scheduling-changing event; missing historical authority blocks new learning writes.
32. **SiYuan is the architectural skeleton.** Incremental-learning semantics are implemented through SiYuan's kernel ownership, workspace lifecycle, file/index split, sync/recovery, history/assets, and UI integration patterns. Projection failure is latched and rebuilt by the model-owned Runtime like a SiYuan full reindex; it is not a sixth Engine family or public maintenance route.
33. **Runtime drains Engine operations.** API handlers execute one Engine call through a bounded model Runtime lease and retain no naked Engine pointer. Close/rebuild rejects new work, waits for active operations, and only then closes the old Engine, matching SiYuan's model-level serialization responsibility.
34. **Scheduling writes remain locally serialized.** A schedule-changing `Scheduler.Apply` preserves the target base observed by the learner and serializes Adapter calculation, Ledger commit, and projection publication inside the existing Runtime/Ledger ownership. Learner think time holds no write lock, and no public lock or retry-token Interface is added.
35. **Feature 003 retains complete review history.** Monthly `.smr` payloads are retained indefinitely by default. Feature 003 adds no compaction, synchronized Checkpoint, evidence manifest, or continuation baseline.
36. **Future recovery machinery requires evidence.** Checkpoints may be specified only after measured full-replay cost, explicit `.smr` compaction, or a product requirement to continue scheduling after raw-history loss. Algorithm Arena may be specified only after its required algorithms and weighting behavior are verified. Neither future mechanism constrains Feature 003 storage or event fields. Removed design research is preserved in `0012-deferred-arena-and-checkpoint-research.md` as a non-authoritative archive.

## MVP Build Slice

The first tracer bullet should prove the core deep Module before polishing reader UI:

The sequence below is a capability map, not approval to implement all entries as one Spec Kit feature. The first feature spec must select a smaller end-to-end slice and leave the remaining entries for later specs.

```text
seed root .sme with Topic + Item
  -> dual-tree ElementStore + monthly SchedulingLedger
  -> Scheduler with simple-v1 + fsrs-v1 + topic-afactor-v1
  -> canonical QueueType + ReviewTarget session model
  -> rebuildable default queue + local scoped-session recovery
  -> BlockReferenceReader for live block -> Topic
  -> BlockSnapshotReader for block/range -> Item
  -> GetElementSubset(scope=due)
  -> ElementBrowser rows
  -> ElementTab Learn
  -> Topic Next / Item Show Answer + grade
  -> review event with algorithmCandidates
  -> memo.db rebuild from .sme + .smr
```

This slice deliberately avoids true clipping/import, automatic splitting, Protyle `@` autocomplete, runtime algorithm plugins, full final drill UI, and advanced FSRS tuning.

## First Spec Kit Feature Boundary

The first feature is narrowed to `001-item-learning-core`. It proves one existing root Q/A Item through the backend learning loop:

```text
prepared Item + introduction history
  -> due-only query
  -> default LearningSession Start
  -> Show Answer without scheduling
  -> Grade 0..5
  -> fsrs-v1 primary + simple-v1 shadow/fallback
  -> SchedulingLedger commit and adopted-chain projection
  -> memo.db removal and deterministic rebuild
```

The feature includes prepared concurrent-event fixtures so Ledger refresh, complete-branch adoption, duplicate handling, invalid causality, and input-order independence are fixed before later sync integration. It does not implement the SiYuan sync hook itself.

The feature excludes production UI, Topic behavior, Element creation/import, tree mutation, Browser, scoped-session recovery, lifecycle/postpone actions, block/note/assets integration, and advanced queue or algorithm behavior. Named backend routes and Engine tests are the tracer's outer test surface.

## Feature 003 Minimal Boundary

Feature 003 builds on the completed Item learning core and read-only Element tree. It adds one backend-only SuperMemo-aligned daily learning loop:

```text
Learning Day + Midnight Shift
  -> Outstanding stage over due Memorized Topics and Items
  -> optional confirmed Pending stage over the global Pending order
  -> optional confirmed Final Drill stage over Items admitted by grades below Good
  -> Topic Next through topic-afactor-v1
  -> Item Grade through existing fsrs-v1 + simple-v1 fallback/shadow
  -> monthly .smr commit
  -> memo.db rebuild from complete retained authority
```

The stage order, confirmation transitions, same-day exclusion, Pending introduction, and schedule-neutral dynamic Final Drill membership are in scope. Browser/subset learning, production UI, actual sync-hook integration, history compaction, Checkpoints, scheduler profile registries, Algorithm Arena, additional memory algorithms, R-Metric, and neural roam are separate later features. The first Feature 003 specification must not create files, fields, Modules, Interfaces, or tests solely for those deferred capabilities.

## Implementation Placement

The backend implementation should live in a new SiYuanMemo kernel package rather than inside `riff`:

```text
kernel/model/symemo/
  engine.go
  commands.go
  queries.go
  material.go
  store/
    element_store.go
    index_store.go
  session/
    session.go
    recovery_store.go
  scheduler/
    scheduler.go
    scheduling_ledger.go
    adapter.go
    queue_policy.go
    queue_type.go
    review_target.go
    topic_policy.go
    ledger/
      monthly_store.go
      causal_projection.go
    adapters/
      simple/simple.go
      fsrs/fsrs.go
  integration/
    block_reference_reader.go
    block_snapshot_reader.go
    note_anchor_writer.go
    asset_store.go
    history_snapshot_writer.go
```

The exact internal file split may change to avoid package cycles; this layout records ownership, not a requirement to create a shallow package for every file. The `scheduler/ledger/` package is implementation behind the internal `SchedulingLedger` Module, not another Interface exposed to API handlers. The API should route through `kernel/api/symemo.go`. Frontend code should call named action endpoints and must not know which scheduler Adapter wins, which causal branch is adopted, or how monthly events are merged.

Block-to-Element workflows use two distinct SiYuan-side Adapters. `BlockReferenceReader` resolves the stable live block identity for Block-backed Topics without copying content. `BlockSnapshotReader` returns sanitized block/range HTML, assets, formula source, and source-range metadata for explicit Item snapshots. The Learning Engine owns the selected command semantics and all resulting Element/event writes.

## Non-Goals

The MVP does not include advanced FSRS tuning UI, optimizer training, weighted Algorithm Arena consensus, runtime third-party adapter loading, image occlusion cards, automatic Topic splitting, automatic note-to-card generation, native Protyle `@Element` autocomplete, or full final drill screens.
