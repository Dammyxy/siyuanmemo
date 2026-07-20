# Deep Module Interface Design

Date: 2026-07-19

## Decision And Precedence

SiYuanMemo keeps stable, named `/api/symemo/*` HTTP routes for the built-in frontend, plugins, automation, and diagnostics. Those routes are transport Adapter methods; their count does not define the Learning Engine's internal Interface.

Inside the kernel, one concrete Learning Engine Module owns user-intent workflows through five command families. The Scheduler, Learning Session, Scheduling Ledger, Topic material processing, and query index remain implementation behind that Interface. This document supersedes the method-per-action Interface inventories in documents `0002` through `0005`; those action names remain useful as HTTP route names and command variants, not as separate internal Modules.

## Public Transport Surface

Public HTTP routes remain named and typed in the SiYuan style:

```text
/api/symemo/extractTopic
/api/symemo/saveTopicHTML
/api/symemo/startLearning
/api/symemo/gradeItem
/api/symemo/getElementTree
/api/symemo/getElementBacklinks
```

Each handler validates its transport payload, constructs one closed kernel command or query variant, invokes the Learning Engine, and translates the result or typed error back to JSON. Handlers must not coordinate storage, scheduling, indexing, session advancement, or note insertion themselves. SiYuanMemo does not expose one generic public `/executeCommand` route in the MVP.

The TypeScript client mirrors these routes with named functions such as `extractTopic()`, `gradeItem()`, and `getElementTree()`. It is a type-safe transport Adapter, not a second domain Module. It may normalize HTTP errors and DTOs, but it must not reconstruct a multi-step workflow in the frontend.

## Learning Engine Interface

The kernel uses one concrete `Engine` with five operation families:

```text
Engine
  CreateElement(ctx, command) -> CreateElementResult
  ChangeElement(ctx, command) -> ChangeElementResult
  RunLearningAction(ctx, action) -> LearningResult
  SendToNote(ctx, command) -> SendToNoteResult
  Query(ctx, query) -> QueryResult
```

This is the Module Interface in the architectural sense. The MVP has one Engine implementation, so it does not require a Go `interface` solely for mocking. Tests construct the concrete Engine with temporary storage and focused SiYuan integration Adapters.

Engine construction is read-only with respect to authoritative learning files. It composes effective scheduler configuration from versioned built-in defaults and valid persisted files, opens only disposable projection state, and never creates the scheduler directory. A host-owned startup bootstrap is a SiYuan lifecycle operation outside the five user-intent families: before API availability and only in writable mode, it may persist missing scheduler files using the same default producer. Learning actions require that bootstrap to have established persisted authority; queries never trigger it.

The same SiYuan host lifecycle owns projection bootstrap and full rebuild. API/model callers execute one Engine operation through a bounded `symemoRuntime` lease and never retain a naked Engine pointer. Runtime latches construction/publication failure, rejects new operations while draining, waits for active calls before close/rebuild, recreates the Engine internally, and publishes only a complete replacement. Workspace restart clears the in-memory lifecycle. This is not a sixth Engine family, public route, or frontend control; Engine projection refresh remains package-internal.

Commands and queries are closed, versioned variants with typed payloads. They must not be unvalidated `map[string]any` bags. Family result types may contain a tagged variant, but invalid command/result combinations must be rejected inside the Engine rather than left to callers.

### CreateElement

`CreateElement` owns workflows that produce a new Element. Initial variants include:

- `AddNewTopic`
- `ImportTopic`
- `ExtractTopic`
- `SplitTopic`
- `CreateItem`
- `CreateClozeItem`
- `CreateTopicFromBlock`
- `CreateItemFromBlock`
- `CreateClozeItemFromBlock`

The family owns source parsing, HTML normalization, stable IDs, asset import, source provenance, tree insertion, inherited-default resolution, scheduler introduction, monthly event commit, and index projection. A caller never creates an Element record and then separately asks the Scheduler to initialize it.

`CreateTopicFromBlock` is an explicit Block-backed Topic workflow. It validates a stable SiYuan block ID and a non-encrypted current source before writing, stores canonical `material.kind = siyuanBlock` with that identity plus optional source-notebook provenance in `.sme`, initializes the Topic schedule, and opens the resulting target through the Protyle surface. The notebook value is not part of source identity, so an ordinary cross-notebook move remains resolvable. The workflow does not copy the block into HTML. Protyle transactions edit the referenced native `.sy` block directly, while Topic scheduling events remain in `.smr`.

`CreateItemFromBlock` and its cloze variant are snapshot workflows. They read the source block through `BlockSnapshotReader`, create versioned prompt/answer or cloze payloads in `.sme`, and open the Item HTML Editor. The source block remains unchanged and is not automatically synchronized after the snapshot is created.

Future `CreateConcept`, audio, video, and other Element variants may join this family without adding storage or scheduling methods to the frontend.

### ChangeElement

`ChangeElement` owns mutations to an existing Element and its structural placement. Initial variants include:

- `SaveTopicHTML`
- `UpdateReadingPosition`
- `SetReadPoint`
- `ClearReadPoint`
- `SetAnnotation`
- `RenameElement`
- `MoveElement`
- `SetBinding`
- `DeleteElement`

The family owns validation, complete mutation planning, local-history requests, destination-first whole-root `.sme` writes, mixed sibling ordering, annotation remapping, asset/reference reindexing, deletion events, and partial-write recovery facts. Scheduler lifecycle actions do not belong here even when they affect how an Element appears in the UI.

For every destructive multi-file command, the internal Element storage implementation serializes and validates all intended bytes before calling `HistorySnapshotWriter` once for every existing pre-image. Snapshot failure returns before any authority write. Cross-root move, promotion, and demotion then write destination/additive authority first, source/destructive authority second, affected `sort.json` last, and projection state only after complete authority success. Deletion writes source removal/replacement, its immutable deletion event, sort cleanup, then projection. These workflows remain one `ChangeElement` command rather than separate history, write, and rebuild calls from the frontend.

If a later authority write fails after an earlier one completed, the command returns `element-write-partial`. The Runtime gates subsequent calls from the stale Engine, waits for the triggering lease to release, and performs one internal rebuild from actual authority. It never auto-restores history or presents the command as successful. Startup likewise validates current files and leaves repair explicit.

Fine-grained TinyMCE operations such as deleting before the cursor, inserting a web link, or changing inline formatting remain editor-local operations. The kernel receives the resulting material through `SaveTopicHTML`, validates and normalizes it, and persists one coherent change.

Editing a Block-backed Topic remains a native Protyle transaction against the referenced `.sy` block. It does not pass through `SaveTopicHTML` and does not rewrite the Topic `.sme` payload with a second copy of the block body. Changes to the block do not silently create scheduling events.

### RunLearningAction

`RunLearningAction` owns both learning-session intents and direct scheduling intents. Session variants include:

- `Start`
- `Resume`
- `DiscardRecovery`
- `ReturnToQueue`
- `ShowAnswer`
- `NextTopic`
- `GradeItem`
- `Stop`

Direct scheduling variants include:

- `Postpone`
- `Reschedule`
- `SetPriorityPosition`
- `Remember`
- `Forget`
- `Dismiss`
- `MarkDone`

The Engine delegates session state to `LearningSession` and scheduling transitions to Scheduler `Apply`. It returns the resulting current target, visible session phase, adopted schedule projection, and any required confirmation state. The frontend never calls Scheduler or `SchedulingLedger` directly.

Scheduler configuration and algorithm-management commands may later be added as explicit administrative variants of this family or a separate settings Module when a real settings workflow exists. They are not separate MVP methods merely because older inventories listed them.

### SendToNote

`SendToNote` remains a distinct operation because it crosses from the Element domain into SiYuan's block transaction model. It owns target resolution, Daily Note anchor reuse, list/list-item structure, stable Element attributes, insertion focus, encrypted-notebook rejection, and the result returned to the frontend.

The Engine performs this workflow through `NoteAnchorWriter`; it never writes `.sy` files directly.

### Query

`Query` is the only Learning Engine read family. Initial closed variants include:

- `GetElement`
- `GetElementTree`
- `GetElementSubset`
- `GetElementBacklinks`
- `GetCurrentLearningSession`
- `GetAlgorithmArenaReport`
- `PreviewAnnotationImpact`

Named HTTP query routes remain available. Internally they map to this family so no caller depends on `memo.db` tables, root-file paths, or Scheduler implementation state. `Query` may combine authoritative files, the rebuildable index, and current local session state, but it must not mutate them.

A valid unknown future Element type is a successful read result, not a query error. `GetElementTree` and `GetElement` return its common envelope, `supportStatus = unsupportedReadOnly`, and opaque payload metadata. Unsupported rendering or mutation is rejected only when a caller requests that later capability; the query layer must not hide the Element or classify it as missing.

## LearningSession Module

`LearningSession` is a concrete internal Module separate from Scheduler:

```text
LearningSession
  Start(request) -> SessionResult
  Act(action) -> SessionResult
  Current() -> SessionResult
```

It owns the current ReviewTarget, answer visibility, scoped-queue resume prompt, preview-return behavior, queue advancement, stop state, and disposable `active-session.json` recovery. `Start` either builds the default queue or reports a recoverable scoped session. `Act` handles Resume, Discard, Show Answer, Topic Next, Item Grade, Return to Queue, and Stop.

Opening an unrelated Element is a frontend preview and does not mutate the session. The frontend compares the opened Element with `Current()` to decide whether the primary control shows `Learn`, `Next`, or `Show Answer`. Only accepted Topic Next and Item Grade actions invoke Scheduler `Apply` and advance after the `.smr` commit succeeds.

## Scheduler Module

Scheduler is a concrete internal deep Module with two operations:

```text
Scheduler
  BuildQueue(request) -> QueuePlan
  Apply(action) -> ScheduleResult
```

`BuildQueue` owns due eligibility, queue type, scope, pending introduction, same-day exclusion, and ordering. It returns ReviewTargets and renderer metadata without creating a synchronized cursor.

`Apply` accepts a closed `ScheduleAction` variant. Initial variants are Introduce, `NextTopic`, `GradeItem`, Postpone, Reschedule, Set Priority Position, Remember, Forget, and Dismiss. It resolves the target's schedule profile, runs only compatible policy and Adapter paths, asks the applicable arena for one candidate where an arena exists, and commits through `SchedulingLedger`.

Profile selection is centralized here rather than spread through callers. Items default to `fsrs-v1` with `simple-v1` fallback/shadow and accept only graded Item review. HTML-backed and Block-backed Topics use `topic-afactor-v1` and accept only ungraded Topic `Next`. Concepts have profile `none` unless explicitly enrolled, after which they use the Topic profile. Future ReviewTarget kinds can add a profile and compatible action without changing the Engine Interface.

Scheduler does not expose creation-specific methods, session navigation, Element deletion, Browser queries, note integration, raw index mutation, or algorithm-specific routes.

## SchedulingLedger Module

`SchedulingLedger` remains a concrete internal Module with one implementation and three operations:

```text
SchedulingLedger
  Snapshot(elementID) -> SchedulingSnapshot
  Commit(transition) -> SchedulingProjection
  Refresh(changeSet) -> RefreshResult
```

`Snapshot` returns the adopted terminal and current projection. `Commit` persists a transition with the base observed by the user action, re-evaluates the complete branch graph, and returns the latest adopted projection. A grade uses the base observed when the target was shown; a sync change before commit therefore creates a legal concurrent branch rather than silently rebasing the user's review.

`Refresh` handles both full startup rebuild and sync-driven changed-month reload. The module hides atomic monthly file replacement, de-duplication, causal validation, adopted-chain selection, derived classifications, and `memo.db` scheduling projection.

No Go `interface`, in-memory fake Adapter, public conflict strategy, or device-specific implementation is introduced for the MVP. Ledger tests use real temporary files and a temporary index.

## Algorithm Adapter Interface

Scheduling algorithms have a real internal seam because the first implementation has `simple-v1`, `fsrs-v1`, and `topic-afactor-v1` Adapters:

```text
AlgorithmAdapter
  Describe() -> AlgorithmDescriptor
  Initialize(input) -> VersionedAlgorithmState
  Predict(input) -> Prediction
  Review(input) -> ScheduleCandidate
  Migrate(state) -> VersionedAlgorithmState
```

`Describe` reports identity, version, supported target kinds, supported action kind, rating requirements, and state schema. `Initialize`, `Predict`, and `Review` are deterministic for the same normalized input, state, parameters, and recorded seed. `Migrate` upgrades only the Adapter's versioned state. The common `Review` operation receives a closed normalized action payload: Item Adapters accept `GradeItemInput`, while `topic-afactor-v1` accepts `TopicNextInput`; incompatible action/profile pairs fail before Adapter execution.

`topic-afactor-v1` returns a Topic schedule candidate from the previous Topic interval, effective A-Factor, and Topic min/max/skip policy. It never receives a raw Item grade and never enters the Item memory arena. Closed review variants preserve this distinction in `.smr`: the MVP envelope may retain `type = reviewElement`, but `reviewKind = gradeItem` identifies graded Item recall and `reviewKind = nextTopic` identifies passive Topic review.

Manual rescheduling belongs to Scheduler policy, not to the algorithm Adapter. Adapter state is already a stable versioned DTO, so the Interface does not expose `serializeState`. Adapters do not write files, build queues, manage lifecycle, select conflict branches, or own arena decisions.

## Topic Material Processing

There is no separate public or kernel-level Topic Material action Interface. Topic material processing is internal implementation used by `CreateElement` and `ChangeElement`. It provides focused pure operations for:

- sanitizing and normalizing HTML;
- assigning and preserving stable node IDs;
- resolving a selected range and extracting a fragment;
- applying material edits;
- validating asset and formula references;
- remapping or invalidating annotations.

It does not create Elements, schedule targets, write `.smr`, update `memo.db`, or send notes. Those workflows remain in the Learning Engine family that owns their complete transaction.

## SiYuan Integration Adapters

The Engine reaches existing SiYuan behavior through narrow capability Adapters:

- `BlockReferenceReader`: receives only node IDs already validated with SiYuan's syntax rule and exposes two operations matching current SiYuan behavior. `LookupMany(blockIDs)` maps to `treenode.GetBlockTrees`, resolves one tree request in a batch, and returns misses as `unresolved` without per-reference filesystem recovery. `Load(blockID)` maps to `model.LoadTreeByBlockIDWithReindex` for one Element being opened or reviewed; it may repair only SiYuan's disposable index, returns stable conclusive absence as `unavailable`, and preserves indexing, synchronization, closed-notebook, rate-limit, or temporary integration uncertainty as `unresolved`. Both return any current derived notebook location, never treat a changed ordinary notebook as source loss, and never write `.sy`, `.sme`, or `.smr`. Live results override any `memo.db` cache. Invalid IDs never enter the Adapter; resolved encrypted sources return `encrypted-source-unsupported` outside the tri-state. Existing Elements with either material diagnostic remain queryable but cannot execute material-dependent capabilities or enter a learning queue.
- `BlockSnapshotReader`: reads sanitized block/range snapshots, source IDs, assets, and formula source for Item snapshot commands.
- `NoteAnchorWriter`: performs native block transactions for `SendToNote` and returns the created or reused anchor and focus target.
- `AssetStore`: imports, resolves, and validates references in SiYuan's shared asset store.
- `HistorySnapshotWriter`: accepts one operation category and the complete set of existing authority-relative paths for a logical destructive command. The production Adapter creates one `workspace/history/<timestamp>-update|delete|replace/storage/siyuanmemo/...` entry through SiYuan's history/file-lock conventions, so native retention and workspace-history clearing apply. It does not feed `.sme` into the current `indexHistoryDir`, whose disposable schema recognizes only `.sy`, assets, and attribute views; a later visible Element-history browser requires a dedicated history type. The Adapter either reports a complete snapshot or fails before Element storage writes. It does not choose transaction members, write order, rollback policy, or repair behavior.

Production Adapters call SiYuan kernel behavior. Tests use in-memory Adapters, making these real seams. SiYuanMemo must not create a broad `SiYuanBridge` whose Interface mirrors unrelated `kernel/model` functions.

The frontend `ContentSurfaceHost` keeps the SiYuanMemo workspace Shell stable and selects a real surface Adapter by target source and workflow: HTML Topic Reader/Editor, Protyle block surface, Item HTML Editor, Item review renderer, or future media surfaces. These surfaces own presentation and editor-local operations; Engine commands own Element persistence, scheduling, and cross-module workflows.

## Query And Index Implementation

The rebuildable Element query/index implementation remains concrete and internal. It can query `memo.db`, load authoritative root data when needed, and combine current session state through the Engine. There are no separate public `ElementQueryService`, `BacklinkService`, or `BrowserService` Interfaces.

This internal implementation owns all-or-nothing projection publication. CGO uses one SQLite replacement transaction. Non-CGO constructs and atomically persists a separate complete next value before swapping the in-memory snapshot. If an accepted scheduling event triggers publication failure, that command first returns `projection-refresh-failed` with accepted-event recovery facts; the Engine then gates every subsequent query and learning action with `projection-rebuild-failed` until a complete retry succeeds. Callers cannot request an old, partial, or stale projection. This keeps recovery policy behind the existing Engine Interface instead of creating a projection-state Interface or frontend freshness workflow.

The deletion test for this Module shape is intentional: deleting the Learning Engine would force storage transactions, scheduling, indexing, session behavior, and SiYuan note integration back into every HTTP handler. Deleting Scheduler or `SchedulingLedger` would force algorithm and concurrency rules into Element workflows. In contrast, deleting a hypothetical per-action pass-through Module should remove no real complexity, so those Modules are not created.

## Ownership Matrix

| Concern | Owner |
| --- | --- |
| Named HTTP routes and JSON translation | `kernel/api/symemo.go` transport Adapter |
| User-intent transaction orchestration | Learning Engine |
| Active target, answer phase, cursor, local resume | `LearningSession` |
| Queue policy and schedule transitions | Scheduler |
| Monthly events, causal branches, adopted projection | `SchedulingLedger` |
| Memory interval proposals | `AlgorithmAdapter` implementations |
| HTML cleaning and annotation remapping | internal Topic material implementation |
| Root `.sme`, event, sort write plan and ordering | internal Element storage implementation |
| Native-layout pre-image snapshot placement | `HistorySnapshotWriter` |
| Missing scheduler-config bootstrap and SiYuan read-only guard | host runtime lifecycle plus internal scheduler-config implementation |
| Projection bootstrap, failure latch, and full rebuild | host `symemoRuntime` following SiYuan model-level reindex lifecycle |
| Engine operation leases and in-flight drain | host `symemoRuntime`; transport calls a bounded model facade |
| Block-backed Topic source resolution | `BlockReferenceReader` |
| Block snapshot reads | `BlockSnapshotReader` |
| Native note insertion | `NoteAnchorWriter` |
| Shared assets | `AssetStore` |
| Tree, subset, backlinks, and schedule lookup | internal query/index implementation |
| Named TypeScript calls and DTO decoding | frontend client Adapter |

## Testing Surface

Behavioral tests should primarily exercise the five Learning Engine families with temporary Element storage, monthly events, and `memo.db`. These tests assert completed workflows and returned results, not calls between internal policies.

Focused tests additionally cover:

- Scheduler `BuildQueue` and `Apply` with deterministic time and algorithm Adapters;
- `LearningSession` transitions and disposable recovery;
- real temporary-file `SchedulingLedger` concurrency and rebuild behavior;
- the shared `AlgorithmAdapter` contract against both initial Adapters;
- SiYuan integration Adapters with focused integration tests and in-memory Engine tests;
- HTTP route payload/result translation without duplicating Engine workflow assertions.

Tests must not require exporting internal policies or introducing an Interface with only one implementation.

## Invariants

1. Named public HTTP routes do not imply method-per-route internal Modules.
2. The frontend and API handlers never coordinate partial domain workflows.
3. Learning Engine commands express user intent, not storage CRUD.
4. Scheduler has only queue construction and schedule application responsibilities.
5. Session state and synchronized scheduling truth remain separate.
6. Topic material processing is internal and scheduler-neutral.
7. Only real variation seams receive Adapters.
8. Query callers never depend on `memo.db` schema.
9. Internal Modules remain concrete until a second implementation creates real variation.
10. Destructive multi-file commands snapshot all existing pre-images before writing, prefer detectable duplication over loss through destination-first order, and never serve a stale projection after partial authority failure.
11. Native history enables explicit repair; it is not an automatic rollback log, synchronized authority, or a second transaction system.
