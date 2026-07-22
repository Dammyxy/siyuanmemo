# Spaced Repetition Scheduler Core

Date: 2026-07-19

## Decision

SiYuanMemo will implement its spaced repetition core as an independent Scheduler Engine, not as a wrapper around SiYuan `riff` and not as one interval formula. The Scheduler Engine owns ReviewTarget queues, Topic reading queues, Item memory reviews, priority position, postponing, review history, and versioned algorithm Adapters.

The MVP will ship with `fsrs-v1`, `simple-v1`, and `topic-afactor-v1`. `fsrs-v1` drives Item scheduling, while `simple-v1` remains the deterministic Item fallback and shadow comparator. `topic-afactor-v1` independently drives passive Topic scheduling. "Pluggable" means these real algorithm implementations share one Adapter Interface; callers still use named HTTP actions that map to Learning Engine `RunLearningAction` variants, not algorithm-specific routes.

## Design Summary

The learning system is a layered scheduler, not a single algorithm. The important layers are:

- **ReviewTarget model**: queue/session targets such as `element.topic`, `element.item`, `note.block`, and future audio/video targets.
- **Element model**: Topic, Item, Concept, parent-child tree, links, priority position, status, and scheduling state.
- **Topic queue**: reading, extraction, splitting, revisiting, postponing, and dismissal for material units.
- **Item memory model**: recall review state using difficulty, stability, retrievability, interval, repetitions, lapses, and review outcome.
- **Priority model**: user priority position plus propagated priority position from Element relationships such as siblings, descendants, Concepts, and explicit links.
- **Postpone model**: overload management that adjusts due dates based on type, priority position, interval, repetition/lapse state, thresholds, and optional randomization.
- **Algorithm adapter model**: each compatible algorithm receives the same profile-specific review input and returns a candidate schedule without mutating Element files directly.

This means SiYuanMemo's Scheduler Engine separates queue selection, algorithm evaluation, schedule adoption, and state persistence. A Topic can be due for reading without being an Item, an Item can be reviewed for memory without becoming a SiYuan block, and a note block can appear in a NeuralRoam-style session without being converted into an Element unless the user explicitly creates one.

Opening or browsing an Element is not a scheduler event. A Topic `Next` inside an active learning session is a Topic repetition and updates schedule state; ordinary reading/navigation only updates UI state such as scroll position or read point.

## Algorithm Requirements

The first serious memory adapter should use DSR-compatible state because it gives us a stable vocabulary for long-term scheduler evolution:

- difficulty: how hard the material is for the user;
- stability: the interval associated with a target recall probability;
- retrievability: predicted recall probability at review time;
- forgetting index: the target forgetting risk used to translate stability into an interval.

Button ratings are UI labels, not the scheduler's permanent interface. The scheduler stores the raw SuperMemo-style grade as an integer in `0..5`, the user-facing rating label, and the mapping version used by the UI. A UI may mimic SuperMemo's compact grading surface with five visible buttons, but the Engine must still preserve all six numeric grades and must provide an explicit way to record grade `0` through keyboard input, an overflow command, or a dedicated button.

Passing recall is binary and derived from the raw grade: `passed = grade >= 3`; `failed = grade < 3`. This binary pass/fail value is used for recall metrics and lapse detection, but it does not replace the raw grade. Final drill eligibility is a separate concept: in SuperMemo-like mode, a review below Good (`grade < 4`) can enter the session's final-drill queue even when `grade == 3` is still a passing recall.

Priority position is a first-class scheduling input. It follows SuperMemo semantics: `0%` is highest priority and larger values are lower priority. The first policy can sort by due time and priority position, but the model must leave room for propagated priority position from parent/child relationships, sibling proximity, Concept assignment, and explicit Element links.

Postpone is part of the core scheduler. It should not be implemented as a fixed "add N days" utility only. MVP may offer fixed postpone, while the internal Postpone policy must remain capable of algorithm-aware previews and batch calculations requested through Engine commands or queries later. This does not require another Scheduler operation beyond `BuildQueue` and `Apply`. Postpone changes the adopted schedule, but it does not directly train memory Adapters unless a later Adapter explicitly models postpone behavior from event history.

The scheduler must distinguish global due learning from subset review. `Learn subset` processes due/outstanding memorized Elements in the current subset order, then introduces pending Elements from that subset according to pending-queue order. `Review all` can execute mid-interval repetitions for non-dismissed Elements that are not due. `Review topics` is a topic-only variant intended for passive reading review without forcing Item recall.

The MVP has one active learning session per running workspace. Starting `Learn` while that session is active returns to its next target instead of creating a competing queue. Each accepted Topic `Next` or Item grade is appended to the monthly `.smr` stream before the UI advances, so completed review work survives a crash independently of the queue surface.

The default due `IncrementalLearningQueue` is rebuilt from authoritative Element and event state rather than persisting an exact cursor. Filtered branches, temporary practice, and other scoped queues may write one lightweight recovery snapshot to `workspace/temp/siyuanmemo/active-session.json`. On restart, the user may continue or discard that local snapshot; recovery removes targets already completed for that queue or learning day, deleted, dismissed, or no longer eligible. The snapshot is disposable, does not participate in sync, and has no cross-device revision or merge protocol.

The `IncrementalLearningQueue` pipeline is ordered as: outstanding due ReviewTargets first, then pending Element targets up to configurable introduction limits, then optional final drill. Pending introduction limits are user settings, not hard-coded constants. MVP defaults should be conservative, such as `dailyNewElementLimit = 20` and `sessionNewElementLimit = 10`, with collection, Concept, and later subset overrides. Final drill is recorded as session-level state in the MVP, but it does not need a standalone UI.

The Scheduler should skip targets already reviewed on the same local learning day when building normal queues, subset queues, and add-to-outstanding queues. Explicit commands such as `ExecuteRepetition` or a future "review again today" override may bypass this guard, but the event must record that the repetition was explicit.

## Canonical Queue Types

SiYuanMemo uses the following confirmed queue English names and persisted identifiers:

| Queue module | QueueType enum | Persisted ID | SiYuanMemo role |
| --- | --- | --- | --- |
| `IncrementalLearningQueue` | `IncrementalLearning` | `incremental-learning` | Default `Learn` queue: due review, learning/relearning, new/pending introduction, Topic rotation/manual entries. |
| `RetrievalPracticeQueue` | `RetrievalPractice` | `retrieval-practice` | Pure retrieval/due review queue, default excluding new/pending introduction. |
| `FilterGroupQueue` | `FilterGroup` | `filter-group` | Persistent filtered queue created from Browser filters or saved subsets. |
| `FinalDrillQueue` | `FinalDrill` | `final-drill` | Session/practice queue for below-Good or explicit drill entries; does not own SRS scheduling decisions. |
| `NeuralRoamQueue` | `NeuralRoam` | `neural-roam` | Future neural roam/session path over Elements, every note block that explicitly references an Element, and later media targets. |
| `LeechReviewQueue` | `Leech` | `leech` | Lapse/manual leech review queue. |

`SubsetReviewQueue` and `TemporaryDrillQueue` remain reserved internal helper queues. They can back temporary branch learning or deliberate practice, but they are not persisted user-facing queue types in the first design. Do not introduce new names such as "DueQueue" when one of the canonical queues expresses the behavior.

The active learning session stores the queue type string, queue module name, target IDs, current target, and session policy. Default toolbar `Learn` starts or resumes `IncrementalLearningQueue`; a pure flashcard-only command may start `RetrievalPracticeQueue`; Browser saved filters use `FilterGroupQueue`; future neural review uses `NeuralRoamQueue`.

## ReviewTarget

Queues operate on `ReviewTarget`, not directly on one Element shape:

```text
ReviewTarget
  kind: element.topic | element.item | element.concept | note.block | media.audio | media.video | ...
  id: stable target id
  sourceId: optional source anchor
  renderer: topic-reader | item-review | protyle-block | media-review | ...
  schedulerPolicy: topic-next | item-grade | preview-only | drill-only | session-only
```

Element-backed targets persist scheduling actions as monthly `.smr` events and materialize current state in `memo.db`. Note-block targets do not mutate `.sy` scheduling fields; if a note block has been explicitly converted into a Topic or Item, the created Element owns the schedule and keeps the note block only as source provenance.

## Algorithm Adapter Interface

Scheduling algorithms are internal adapters. They do not own queues, Element lifecycle, note integration, UI state, pending introduction, or file writes. They only propose memory-state and interval updates from a normalized review input.

The Adapter Interface is:

```text
AlgorithmAdapter
  Describe() -> identity, version, capabilities, and state schema
  Initialize(input) -> versioned adapter state
  Predict(input) -> prediction before the profile-specific review action
  Review(input) -> candidate schedule after the profile-specific review action
  Migrate(state) -> upgraded versioned adapter state
```

Normalized Item review input is one closed variant of the common input union:

```json
{
  "target": {
    "kind": "element.item",
    "id": "20260719010101-abcdefg"
  },
  "elementId": "20260719010101-abcdefg",
  "elementType": "item",
  "reviewKind": "gradeItem",
  "reviewAt": "2026-07-19T04:30:00+08:00",
  "localLearningDate": "2026-07-19",
  "scheduledDueAt": "2026-07-19T04:30:00+08:00",
  "lastReviewAt": "2026-07-16T04:30:00+08:00",
  "elapsedDays": 3,
  "scheduledIntervalDays": 3,
  "rawGrade": 4,
  "passed": true,
  "ratingMapping": "supermemo-grade-v1",
  "adapterRating": {
    "scale": "fsrs-4",
    "value": "good"
  },
  "forgettingIndex": 0.1,
  "historySummary": {},
  "adapterState": {}
}
```

Candidate output:

```json
{
  "algorithm": "fsrs-v1",
  "predictedRecallBeforeGrade": 0.91,
  "confidence": 0.8,
  "nextIntervalDays": 8,
  "nextDueAt": "2026-07-27T04:30:00+08:00",
  "statePatch": {
    "difficulty": 4.7,
    "stability": 8.2,
    "retrievability": 0.91
  },
  "explain": {
    "reason": "graded good after scheduled review"
  }
}
```

Adapters are deterministic for the same input, adapter state, parameters, and random seed. If an adapter uses dispersion or sampling, the seed is provided by the Engine and recorded in the event. Manual rescheduling remains Scheduler policy, and versioned state DTOs remove the need for a separate `serializeState` method.

The first implementation uses compiled Go adapters behind this interface. Runtime-loaded arbitrary plugins are not an MVP requirement because they complicate desktop portability and safety. A later external adapter seam may use a subprocess, WASM, or RPC adapter with the same DTOs.

## Item Candidate Decision

Scheduler runs FSRS and Simple against the same adopted Item state and normalized grade. It records both candidates and adopts one canonical schedule, so the user-facing queue never forks into competing due dates. Concurrent superseded or invalid branches remain audit history and are not fed into Adapter state.

The adopted schedule is still one canonical materialized `dueAt`, `intervalDays`, `repetition`, and lifecycle state for the Element. Events store valid individual predictions/candidates and the canonical result for learning and audit, but the system never forks the user-facing queue into competing due dates.

FSRS drives the normal result. `simple-v1` supplies deterministic fallback when FSRS cannot produce a valid candidate and remains a non-authoritative shadow comparator otherwise. If neither candidate is valid, the whole action fails before writing an event. Manual rescheduling remains ordinary Scheduler logic outside the Adapters.

Algorithm Arena is deferred to a separate feature. It must not add prediction snapshots, lineages, registries, activation pointers, replay machinery, reports, or weighted decision fields to Feature 003. Arena design starts only after the required algorithms and SuperMemo weighting behavior have been verified.

`fsrs-v1` uses SiYuanMemo-owned config under `scheduler/fsrs-v1.json`. It starts with `go-fsrs` default parameters and does not read or mutate SiYuan `riff` flashcard settings.

Configuration loading follows SiYuan's default/load/save lifecycle. Engine construction and queries compose versioned built-in defaults with valid persisted files without writing. A host-owned startup bootstrap persists only missing scheduler files before API availability when the workspace is writable, while SiYuan read-only mode skips bootstrap. Existing invalid files are never automatically replaced, and no scheduling-changing event may be committed before effective configuration is persisted. If review history exists but required configuration is missing or invalid, queries expose diagnostics where safe and all new learning writes remain blocked until recovery.

FSRS maps SiYuanMemo's raw grades into its own adapter rating scale instead of changing the product-wide grade model:

```text
raw grades 0, 1, 2 -> again
raw grade 3        -> hard
raw grade 4        -> good
raw grade 5        -> easy
```

The raw grade, mapped adapter rating, and mapping version are all stored in Item review events. Other Item adapters may use different internal scales, but they must accept the same normalized Item input variant and return the same candidate output shape. Topic Adapters receive `TopicNextInput` instead.

## Topic Interval Policy

Topics are passive reading Elements, so Topic `Next` does not ask for an Item-style recall grade. It records a Topic repetition only when the Element is currently being processed by an active learning session. Any `Next` used for ordinary browsing, page navigation, search result stepping, or read-point movement must not call scheduler review actions and must not update `dueAt`, `intervalDays`, `repetition`, or review history.

The MVP Topic interval policy should use an editable `topicAFactor`, resolved through the standard Element override, `boundTo` Concept context, structural Concept, and collection-default precedence. Recommended defaults:

- collection default `topicAFactor = 2.5`;
- normal editable range starts around `1.3..5.0`;
- advanced users may set values down to `1.01`, but the UI should warn that low values make material recur very frequently;
- `0` may later mean "use the collection's lowest allowed value", but the MVP should avoid implicit magic values.

This default avoids the common failure mode where Topic intervals collapse into `0/1/2/3` day loops. A simple Topic sequence can be:

```text
first active Topic Next: intervalDays = 1
later active Topic Next: intervalDays = max(1, ceil(intervalDays * topicAFactor))
dueAt = current local learning date + intervalDays
```

With `topicAFactor = 2.5`, a Topic moves roughly `1 -> 3 -> 8 -> 20 -> 50` days. With `topicAFactor = 1.5`, it moves roughly `1 -> 2 -> 3 -> 5 -> 8` days, which is useful only for intentionally high-contact reading material.

## Scheduler Engine Interface

The authoritative internal Interface is defined in `0010-deep-module-interface-design.md`. Scheduler is a concrete internal Module with two operations:

- `BuildQueue(request)`: resolve due eligibility, queue scope, pending introduction, same-day exclusion, and ordering.
- `Apply(action)`: handle a closed scheduling-action variant and commit schedule-changing results through `SchedulingLedger`.

Introduction, Topic Next, Item Grade, Postpone, Reschedule, Priority, Remember, Forget, and Dismiss are `Apply` variants. Learning-session navigation and answer visibility belong to `LearningSession`; Element creation/deletion, Browser queries, note integration, raw index mutation, and algorithm-specific routes are not Scheduler responsibilities.

## Internal Policies

The Scheduler Engine is one deep Module. Its implementation keeps these rules private; the list does not require one type, file, or Interface per rule:

- `QueuePolicy`: selects due ReviewTargets and orders them by due time, priority position, status, type, and future ranking signals.
- `TopicPolicy`: schedules imported and extracted Topics for progressive reading actions.
- Item candidate selection: normalizes review input, calls the fixed FSRS and Simple Adapters, validates candidates, and applies primary/fallback/shadow behavior.
- `PriorityModel`: computes direct and propagated priority position.
- `PostponePolicy`: computes single-item and batch postpone changes.
- `SchedulingLedger`: commits immutable scheduler transitions to monthly `.smr`, resolves causal concurrency, and materializes exactly one adopted scheduling chain per Element.

The public interface should remain stable even when these internal policies change.

## Materialized Element Scheduling State

Current scheduling state is not duplicated inside every `.sme` node. It is materialized in `memo.db` from complete `.smr` history together with Element existence, type, relations, resolved Concept defaults, and scheduler configuration. Concurrent superseded, invalid, and duplicate events do not drive this projection. Default resolution follows explicit Element override, primary `boundTo` Concept context, nearest structural Concept, then collection defaults; explicit links never affect scheduling inheritance.

The materialized row must retain enough state for simple scheduling and later DSR-compatible adapters:

```json
{
  "elementId": "20260719010101-abcdefg",
  "schemaVersion": 1,
  "processingState": "new",
  "lifecycleState": "memorized",
  "scheduleProfile": { "id": "item-memory-v1", "version": 1 },
  "decisionPolicy": { "id": "primary-fallback-shadow-v1", "version": 1 },
  "dueAt": "2026-07-19T04:30:00+08:00",
  "priorityPosition": 5000,
  "intervalDays": 0,
  "repetition": 0,
  "lapses": 0,
  "difficulty": null,
  "stability": null,
  "retrievability": null,
  "forgettingIndex": 0.1,
  "topicAFactor": 2.5,
  "ordinal": null,
  "lastReviewAt": null,
  "lastGrade": null,
  "lastRating": null,
  "lastPassed": null,
  "lastReviewKind": null,
  "postponeCount": 0,
  "algorithmStates": {}
}
```

The canonical lifecycle field is `lifecycleState`, with values `pending`, `memorized`, and `dismissed`: pending waits for introduction, memorized participates in learning, and dismissed remains browsable while excluded from learning. The separate `processingState` field uses `new`, `reading`, and `processed` and is projected from `.sme`; scheduling algorithms must not read it. Remaining event-envelope field names are finalized in the first scheduler feature spec.

Queue eligibility is derived from target kind, lifecycle, queue policy, and available adapter support. It is not another authoritative persisted boolean such as `schedulable` that can drift out of agreement with those inputs.

`RememberElement`, `ForgetElement`, and `DismissElement` append lifecycle events whose adopted-chain projection changes the materialized state. Dismiss preserves the current adopted schedule and every adapter state while excluding the target from queues. Remembering a dismissed target restores participation and makes it due immediately when its preserved due time is past. Forget clears current adopted and adapter state, returns the Element to pending, and causes the next introduction to initialize a fresh adapter state; immutable historical events are retained.

Schedule profile is determined by target kind and explicit enrollment. Items use `fsrs-v1` as primary with `simple-v1` fallback/shadow. HTML-backed and Block-backed Topics use `topic-afactor-v1`. Concepts use `none` until explicitly enrolled, then use the Topic profile. Future target kinds declare additional profiles without changing callers.

`topic-afactor-v1` uses `topicAFactor` plus Topic-specific minimum, maximum, and skip-interval policy. Imported and extracted Topics resolve A-Factor `2.5` from collection defaults unless an Element override, primary binding context, or structural Concept overrides it. Its normal transition is `nextInterval = previousInterval * effectiveAFactor`. Items use memory-model fields such as difficulty, stability, retrievability, forgetting index, repetitions, and lapses. Adapter-specific state is included in review event `after` state so the index can be rebuilt without an adapter-private database.

The latest graded review event stores raw grade `0..5`; materialized `lastGrade` and `lastPassed` are projections. Topic `Next` events leave grade null, record `reviewKind = topicNext`, and update Topic interval state only when executed in an active learning session.

## Algorithm Adapter Path

`simple-v1` is the deterministic Item fallback and shadow adapter. It exists to keep manual Q/A and cloze Item reviews operable when the primary Item adapter cannot produce a valid candidate. Priority position, pending introduction, fixed postpone, and lifecycle transitions remain Scheduler policies rather than behavior owned by `simple-v1`.

Item review history records the final shape regardless of which compatible Item Adapter wins: timestamp, Element ID, type, observed `baseSchedulingEventId`, user-facing rating, raw internal grade `0..5`, derived pass/fail, lapse flag, final-drill flag, mapping version, both Adapter candidates, selected result, complete before/after scheduling state, `reviewKind = gradeItem`, session ID, and command source. That keeps projection rebuild and scheduler tests deterministic.

The first serious memory adapter is `fsrs-v1` for Items. It plugs into the adapter interface, maps raw grades into its internal rating scale, keeps its private state under `algorithmStates.fsrs-v1`, and returns candidate `difficulty`, `stability`, `retrievability`, `nextIntervalDays`, and `nextDueAt` values. Its versioned state retains the FSRS card fields required by the selected library version: card state (`new`, `learning`, `review`, or `relearning`), due time, stability, difficulty, elapsed days, scheduled days, repetitions, lapses, and last review time.

`topic-afactor-v1` is the first Topic Adapter. It accepts only `TopicNextInput`, returns a candidate from the previous interval and effective Topic parameters, and writes no raw grade, mapped rating, pass/fail, lapse, or Item-memory state.

The adapter must not persist the third-party Go struct by blindly marshaling it. `fsrs-v1` owns a stable versioned state schema and explicitly maps that schema to and from the library's `fsrs.Card`, allowing library upgrades and adapter-state migration without leaking FSRS types through the Scheduler interface. It must be able to run as primary or shadow without changing Topic Reader, Element Browser, or note integration.

A later `dsr-v1` adapter may use D/S/R-compatible state directly, grades or mapped button ratings, forgetting index, interval from stability, repetition/lapse tracking, and post-lapse handling. It should be tested from deterministic review histories.

Algorithm Arena remains separate future work and does not constrain this Module beyond reusing the proven Algorithm Adapter seam.

## SchedulingLedger And Concurrent Reviews

`SchedulingLedger` is an internal deep module of the Learning Engine, not a frontend interface and not a scheduling-algorithm Adapter. It hides monthly file writes, immutable event identity, causal concurrency, branch validation, adopted-chain selection, index projection, and rebuild behind the Scheduler's action-oriented interface.

Every schedule-changing `Apply` runs inside one collection-wide Scheduler write lease nested within the host's bounded Runtime lease. Answer display and learner think time do not hold the write lease. For `GradeItem`, the lease covers target-branch state resolution, Adapter evaluation, candidate selection, event construction, Ledger commit, and projection publication. Other local scheduling writes queue behind it, while post-sync Runtime replacement drains active leases before rebuilding. No public lock, retry token, or additional Scheduler operation is exposed.

Every event that changes an Element's scheduling projection records `baseSchedulingEventId`. This includes Topic `Next`, Item Grade, Postpone, Reschedule, Remember, Forget, Dismiss, and future equivalent actions. The base is the ID of that Element's adopted scheduling terminal observed before the action, or `null` for a root scheduling event. Content-only, move-only, and audit-summary events do not participate in this scheduling chain unless they also change scheduling state.

The commit sequence inside that write lease is fixed:

1. Read the current adopted scheduling state and terminal event ID for the target.
2. Run applicable lifecycle logic and the fixed compatible Adapters against that same state.
3. Construct one transition event containing the observed base, normalized review input, candidates, adopted result, and before/after state.
4. Atomically replace the target monthly `.smr` under the storage lock.
5. Reproject `memo.db` and advance the session only after projection publication succeeds.

The `.smr` replacement is the acceptance point. If projection publication fails afterward, return `projection-refresh-failed` with `reviewAccepted = true` and the event ID so the caller cannot duplicate the accepted grade.

Sync may reveal legal concurrency. Events are first merged by `eventId` set union, independent of file arrival order, then evaluated as a causal graph per Element:

- Different event IDs with one `baseSchedulingEventId` are concurrent sibling branches, not duplicate reviews in sequence.
- A valid root has a null base. A valid descendant references an existing valid scheduling event for the same Element, contains no cycle, and has a transition compatible with its referenced parent's after-state.
- A branch terminal is a valid event with no valid child. The terminal with the greatest `(occurredAt, eventId)` tuple wins. Its complete ancestor path is the adopted chain.
- Only the adopted chain updates current lifecycle, due state, Adapter state, and queue membership.
- Other valid events remain immutable and derive `concurrent-superseded` status. Malformed, cyclic, missing-base, cross-Element-base, or incompatible events derive `invalid` status. Repeated same-ID/same-payload records derive `duplicate` status.
- The same event ID with different payloads is never resolved by timestamp. Both raw inputs are retained in local repair history, the identity is excluded from adoption, and the affected Element/month requires explicit repair.

If a later event extends a previously superseded branch, branch selection is recalculated over the complete graph; the deterministic terminal rule may adopt that full branch. Selection therefore chooses a complete branch, never an isolated event. The classifications `adopted`, `concurrent-superseded`, `invalid`, and `duplicate` live only in `memo.db` and diagnostics, never in `.smr` source events.

The MVP has one conflict rule and no pluggable `ConflictStrategy`. It never uses device identity as a winner signal and never prompts for ordinary review conflicts. This is separate from `active-session.json`: the session snapshot restores only local scoped-queue navigation and has no authority over scheduling adoption.

## Storage Rules

The authoritative file layout and sync policy are defined in `0008-element-storage-sync-recovery-design.md`. Root `.sme` trees define Element existence, type, content, structure, relations, and `processingState`. Complete monthly `.smr` events define review, scheduling, `lifecycleState`, and deletion history. `memo.db` materializes due queues, priority position, lifecycle and processing projections, full text, relations, backlinks, and current scheduling state under `workspace/temp/siyuanmemo/memo.db`.

Review and scheduler actions are appended as immutable events to `workspace/data/storage/siyuanmemo/reviews/YYYY-MM.smr`. Events are partitioned by month, not by device ID. Do not replace history with only the latest state; corrections use compensating events. A scheduling-changing correction also references the adopted terminal it intends to extend through `baseSchedulingEventId`.

Review events must include enough state to reconstruct an adopted branch and audit scheduler decisions. The authoritative `GradeItem` and `NextTopic` JSON shapes are maintained only in `0008-element-storage-sync-recovery-design.md`; this scheduler document does not duplicate them. In summary, a graded event records the observed causal base, normalized outcome, both Adapter candidates, selected result, and before/after scheduling state.

Postpone events record the observed base, resulting `scheduleAfter`, reason, actor, command source, scope, and postpone count. They may retain command-specific previous/next values needed to explain the accepted postpone intent, but do not copy a second generic preceding-state snapshot. Batch postpone writes one event per affected Element plus an optional summary event for the subset command.

Algorithm-specific collection state should live outside individual Topic HTML, for example:

```text
workspace/data/storage/siyuanmemo/
  scheduler/
    collection.json
    simple-v1.json
    fsrs-v1.json
    topic-afactor-v1.json
    learning-day.json
```

Built-in defaults are only a versioned pre-initialization read fallback. Once persisted, scheduler files are synchronized collection authority; current binary defaults cannot silently replace missing historical configuration.

The rebuildable index layout is separate:

```text
workspace/temp/siyuanmemo/
  memo.db
  active-session.json
```

No scheduler query may depend on temp-only state that cannot be rebuilt after the temp directory is cleared. Losing `active-session.json` loses only the convenience of resuming a scoped queue; completed review outcomes remain in `.smr`, and the default due queue remains rebuildable from complete history. `.smr` conflicts merge by immutable `eventId` set union and then resolve one adopted causal chain per Element. Same-ID/different-payload conflicts are preserved for explicit repair instead of silently choosing one version. Missing or corrupt raw history fails affected scheduling closed. Feature 003 retains `.smr` indefinitely and performs no automatic compaction.

## MVP Acceptance

The first Scheduler core is acceptable when these behaviors work without `riff`:

- Importing a Topic creates an active schedulable Element due now.
- Extracting a child Topic inherits Concept defaults, becomes memorized by default, and enters the queue.
- `AddNewTopic` creates a Topic with zero or one primary `boundTo` relation to the current Concept or Element and makes it due now by default; the relation never implies a structural child Topic.
- `CreateTopicFromBlock` creates a Topic from a SiYuan block when no back-side answer is supplied, without rewriting the source `.sy` block.
- `CreateItemFromBlock` and `CreateClozeItemFromBlock` create Item Elements only from explicit Q/A or cloze commands and keep source block provenance.
- Default `Learn` starts or resumes `IncrementalLearningQueue`; queue type IDs use `incremental-learning`, `retrieval-practice`, `filter-group`, `final-drill`, `neural-roam`, and `leech`.
- Queue sessions store `ReviewTarget` IDs and can later include `note.block`, `media.audio`, and `media.video` without changing scheduler callers.
- `GetElementSubset(scope=due)` returns due memorized Topics/Items ordered by due time and priority.
- `GradeItem` records raw grade `0..5`, derives `passed = grade >= 3`, records lapse/final-drill flags, and updates `dueAt`, interval, repetition/lapse state, and Item algorithm state.
- `GradeItem` calls the FSRS and Simple Adapters, records every valid candidate, applies fixed primary/fallback/shadow behavior, and preserves Adapter-specific state.
- Every scheduling-changing action records the currently adopted terminal as `baseSchedulingEventId`, commits through `SchedulingLedger`, and advances the queue only after the monthly event write succeeds.
- Different-ID events sharing one base are retained as concurrent branches; rebuilding from every permutation of the same event set selects the same complete adopted chain and produces the same queue-visible state.
- Only the adopted chain drives Algorithm Adapter state. Concurrent superseded, invalid, and duplicate events remain queryable for audit without acting as extra repetitions.
- Active-session Topic `Next` records `reviewKind = nextTopic` and applies Topic interval growth; Topic `Next` outside active learning is navigation only and records no scheduler event.
- Normal queue construction skips Elements reviewed earlier on the same local learning day unless an explicit override command is used.
- Pending introduction respects configurable daily/session limits.
- `PostponeElement` records an event and moves `dueAt`.
- `DismissElement` records an event, removes the Element from active queues, and preserves its adopted schedule and adapter states.
- `RememberElement` initializes fresh adapter state for a pending Item, but restores preserved state for a dismissed Item and makes it due when its preserved due time is past.
- `ForgetElement` returns a memorized Element to pending, clears current adopted and adapter state, and retains immutable review history.
- `MarkElementDone` finishes a Topic and either dismisses or deletes it according to whether it has children and user confirmation.
- `DeleteElement` writes local history and an immutable deletion event with ID, type, title, deleted time, and optional replacement metadata before removing the internal subtree or root document tree; it does not create one tombstone file per Element.
- `GetElementSubset` returns browser-ready subsets for due, all, pending, memorized, dismissed, priority, branch, search, and filter views.
- Rebuilding `memo.db` from `.sme`, complete `.smr`, sort, and scheduler configuration files restores queue-visible state plus derived event adoption classifications.
- Default due sessions resume semantically by rebuilding the queue from authoritative state; interrupted scoped queues may resume from the local disposable `workspace/temp/siyuanmemo/active-session.json` snapshot.
- `fsrs-v1` drives Item scheduling as the primary adapter while `simple-v1` remains available as fallback and shadow comparator.
- `topic-afactor-v1` drives Topic and explicitly enrolled Concept scheduling; `reviewKind = nextTopic` remains ungraded.

## Non-Goals

The first implementation does not need runtime third-party plugin loading, exact parity with any external scheduler, propagated priority, adaptive ensemble scheduling, personal optimizer training, continuous quality input, weighted consensus decisions, or advanced postpone settings. The first implementation does need the correct module shape so those features can be added without replacing storage, Topic Reader, note references, or public Engine actions.
