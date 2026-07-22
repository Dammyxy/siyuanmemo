# Browser Order And Learning Plan Design

Date: 2026-07-21

Status: Working representation. The two-core taxonomy and Browser stage behavior are confirmed in `0009-confirmed-design-baseline.md`; the value shapes below remain subject to Spec Kit planning.

## Decision

Browser sorting is an input to one `LearningPlan`. Manual drag ordering is deferred and is not part of the current Browser or Scheduler contract. Browser ordering is not an additional queue implementation, schedule algorithm, or session cursor. The Scheduler remains the only module that turns a plan into an ordered set of `ReviewTarget` values and the only module that applies schedule-changing actions.

The product keeps two internal workflow models:

- `LearningPlan` describes a staged learning workflow. Each stage declares its own population timing, eligibility, target policy, ordering, execution policy, and completion transition; a static Browser stage remains bounded while an explicitly deferred Final Drill stage resolves the global pool only when accepted.
- `NeuralRoamSession` describes graph exploration and remains independent from ordinary plan compilation. A roam target may dispatch its target-native review action, but visiting or skipping it is schedule-neutral by default.

The external product vocabulary is now four primary workflows: default staged Learn, direct stage entry (outstanding, new/pending, final drill), Browser filtered review, and neural roam. “Retrieval practice” is retained only as a Browser subset preset with an Item-only target policy; it is not a peer queue or internal Scheduler module.

## Deep Module Audit

The deep module is the plan compiler behind `Scheduler.BuildQueue`:

```text
Scheduler
  BuildQueue(plan) -> QueuePlan
  Apply(action) -> ScheduleResult
```

`BuildQueue` hides Workset validation, population lookup, mode-specific eligibility, target/profile compatibility, stage compilation, execution classification, and plan fingerprinting. All ordinary subset-learning modes exclude Dismissed and same-day-processed targets. Learn and Review All then deliberately compile different workflows: Learn is Outstanding followed by Pending, while Review All is one ordered pass over the entire eligible Workset, including future-due memorized targets. Browser queries and learning sessions do not implement those rules themselves. The Browser is a transport and query view, not a `BrowserService` Interface.

The deletion test is satisfied: deleting this implementation would force Browser, Session, and API callers to duplicate eligibility, ordering, and invalidation rules. Deleting a hypothetical Browser module would remove no domain complexity, so no such public module is introduced.

## LearningPlan

`ElementWorkset` is the complete public subset input. It carries identity and current order, not query provenance or freshness metadata:

```text
ElementWorkset
  orderedElementIds []ElementID

LearningPlan
  version
  mode              // learn, review-all
  workset ElementWorkset
  targetPolicy      // mixed by default; item-only or topic-only preset when declared

StageSpec
  stageKind         // outstanding, pending, final drill, later workflow stages
  scopeSpec         // frozen workset membership or global pool resolved at stage entry
  eligibility       // including execution-policy-specific same-day behavior
  targetPolicy      // item-only, topic-only, mixed, target-kind allowlist
  orderSpec         // Browser snapshot, Pending Queue, typed fields, or dynamic drill
  executionPolicy   // normal review, introduction, deliberate mid-interval, drill-until-pass
  completionPolicy  // stop, continue, or ask before entering the next stage
```

`StageSpec` is a compiled internal value, not a caller-authored policy language. `mode = learn` compiles a Workset-ordered Outstanding stage, a global-Pending-Queue-ordered Pending stage, and an ask-before-entering transition to global Final Drill. `mode = review-all` compiles one Workset-ordered stage whose targets are classified when they become current as scheduled repetition, future-due mid-interval repetition, or target-native Pending introduction. The dimensions remain independent within each compiled stage; none creates a new schedule profile.

Topic lifecycle is independent from these stage definitions. Ordinary Topic creation/import/extraction initializes a `Memorized` Topic, so its first later `NextTopic` in Outstanding or Review All is a normal Topic repetition. An explicitly queued or forgotten Topic remains `Pending` and receives its target-native introduction only when a Pending-capable stage reaches it. Stage compilation does not reinterpret a newly created Memorized Topic as Pending. The target-native initialization uses `siyuanmemo-topic-initial-v1`: a replay-safe recorded random integer in `1..15` days unless an explicit Schedule interval was supplied; Queue defers that decision until introduction. `siyuanmemo-topic-day-arithmetic-v1` stores day-granular `Last` and `Next` values from the accepted action's resolved `learningDayId`, while the collection learning-day boundary determines when a stored `Next` becomes Outstanding.

`QueuePlan` contains the compiled stages, their ordered targets, the validated plan, a canonical internally derived `planFingerprint`, typed diagnostics, and continuation metadata. A session stores each compiled static stage's frozen target ID order and fingerprint. Static stages do not silently append newly eligible Elements. Final Drill is the explicit exception: after confirmation it resolves the then-current global Drill pool and uses dynamic drill selection, including Items added by the just-completed Browser run.

Final Drill membership is one collection-global synchronized projection, not a Browser- or session-owned queue. A formal or Pending Item grade below `4` admits the Item atomically with that accepted `.smr` review event. Schedule-neutral Drill results, manual additions, and Cut Drills are immutable `.smr` membership events outside Item scheduling adoption chains. A Drill grade of `4` or `5` removes the Item; `0..3` keeps it eligible for dynamic reselection. Restart and sync rebuild membership, but the active target, shuffle order, and cursor remain local; remote changes take effect only after the current target at the next dynamic selection boundary.

The local execution policy is `siyuanmemo-final-drill-flip-v1`, corresponding to the documented SuperMemo 19 `FlipElement(5,3,6)`. Failed targets append to the local tail; passed targets leave synchronized membership. Before each next selection, a queue of at least five uniformly moves one target from 1-based `[5,n]` into `[3,min(6,n)]`; equal positions advance the source once and become a no-op if that is out of range. The head is then served. Short queues only rotate failures through the tail. Seed and transient order remain disposable local state, so Browser order never governs Drill and synchronized clients share membership without sharing cursor order.

`siyuanmemo-drill-inactivity-v1` resets collection-global inactivity on accepted automatic admission/readmission, any Drill grade, and manual Add/Save Drill. Cut Drills empties the generation. After three complete subsequent collection learning days with no activity, the generation expires before the next day's first projection or plan build; expiry is derived, not an event. The first later activity starts a new generation without resurrecting expired members. This is recorded as a SiYuanMemo interpretation until direct compatibility testing establishes SuperMemo's unspecified reset details.

## Browser Order

`OrderSpec` uses typed terms rather than a free-form field name and direction map. The initial terms cover schedule fields (due time, interval, stability, retrievability, difficulty, repetition/lapse/postpone counts, priority, A-Factor, forgetting index, U-Factor), Element fields (first/last/next review time, title, type, tree position, ordinal), and material fields (text/image size). Randomization is represented by a distinct order variant with a recorded seed.

Every term declares direction and missing-value placement. The comparator applies terms left to right and always adds a deterministic `ElementID` tie-breaker. Locale-sensitive display formatting is never used as the comparison value. New sortable attributes require a versioned projection/query field; callers do not inspect `memo.db` or parse rendered text.

The Browser query applies `OrderSpec` and emits an `ElementWorkset` containing the resulting ordered IDs. Starting Learn freezes that membership and order for Outstanding. Pending filters the same frozen membership through the global Pending Queue order, so temporary Browser sorting cannot put Pending ahead of Outstanding or reorder Pending. Review All preserves Workset order for its single pass. `Save pending`, when implemented, remains the explicit authority mutation for changing future global Pending order.

## Priority, Outstanding, And Auto-Sort

Three order-bearing concepts remain independent:

```text
GlobalPriorityOrder       // all Elements; lower percent/rank is higher priority
DailyOutstandingOrder    // today's due/overdue targets plus explicit temporary overlays
AutoSortedReviewOrder    // a derived daily execution order
```

The daily boundary comes from synchronized collection `LearningDayConfigV1 { timeZoneIana, midnightShiftHours }`. The shift is an integer `0..16` with SiYuanMemo default `4`; wall-clock times before the configured hour resolve to the previous date's `learningDayId`. At the boundary, date-based due eligibility advances and a later plan build derives the new Daily Outstanding Order. Stored `Next` values remain dates rather than per-Element timestamps, and every accepted event keeps its already resolved `learningDayId`.

The default global Learn plan derives `AutoSortedReviewOrder` from today's Outstanding population, the global relative priority order, configured Topic proportion, and separate Item/Topic randomization. Browser Learn does not use that derived order for its Outstanding stage; its explicit Workset order is authoritative. Browser `Sort: For Review` may apply the same configured criteria while producing a Workset, but this still does not mutate global priority or the current global Outstanding order.

The only public configuration shape for this compiler is the persisted and synchronized `OutstandingAutoSortV1 { enabled, topicProportionPercent, itemRandomizationPercent, topicRandomizationPercent }`, with integer percentages in `0..100`. It is deliberately not a caller-authored comparator graph or opaque preset. Priority supplies each type's base stream; randomization controls within-type displacement, and Topic proportion interleaves streams without removing population. If one stream cannot satisfy the requested share, compilation continues from the available stream. The generated seed belongs to the disposable QueuePlan/session, and `Sort now` records a fresh one. Explicit Browser sorting bypasses the configuration unless `Sort: For Review` is chosen; Final Drill never consumes it. `siyuanmemo-auto-sort-defaults-v1` sets `enabled=true`, Topic proportion `20`, Item randomization `10`, and Topic randomization `20`. It is explicitly a versioned product default: SuperMemo documents only the enabled state, recommends Topic ratio 1:4 or less, and does not publish numeric randomization defaults.

`siyuanmemo-auto-sort-v1` implements the percentage semantics. It filters global Priority Order into Item and Topic streams. For each stream it normalizes zero-based rank to `[0,1]`, uses zero for a singleton, derives a stable `[0,1)` random score from policy version plus local seed plus Element ID, and sorts ascending by `(1-r)*priorityScore + r*randomScore`, followed by original Daily Outstanding position and Element ID. It then merges streams with an Item-first integer accumulator: add Topic proportion at each slot, choose Topic and subtract 100 when the accumulator reaches 100, otherwise choose Item; if that type is empty, choose the available type. This gives exact edge behavior: randomization `0` preserves Priority, `100` is a seeded random permutation, Topic `20` starts `I,I,I,I,T`, Topic `0` defers Topics until Items exhaust, and Topic `100` emits Topics first. The formula is versioned product behavior rather than an asserted SuperMemo implementation.

`Sort now` recompiles only the remaining eligible part of the global Outstanding session with the effective sorting criteria and a recorded fresh seed. Scheduled and explicit overlay members remain in the population, but Add-to-Outstanding spacing positions cease to have authority after the sort. The current target does not jump; the replacement order takes effect on the next advance. A successful sort changes only the QueuePlan/disposable session state, while a compilation failure leaves the prior remaining order intact. It writes no `.smr` event and changes no due date, priority rank, overlay membership, or completed-review history.

`Add to outstanding` mutates `DailyOutstandingOrder` for the current collection learning day without changing formal due time. A learning day is identified by `YYYY-MM-DD` at midnight in the persisted collection IANA time zone. The synchronized transition stores that identity and intersperses the ordered Workset at spacing `1..100`; existing targets may only move earlier, and unused future-due overlays disappear after the recorded learning-day boundary. Same-day restart or sync can rebuild the overlay, while an event first observed after expiry contributes only its permanent priority adjustment. A later accepted review is classified as mid-interval when the target was not formally due.

The overlay event is synchronized authority, but the session cursor and processed position remain disposable local state. A remote overlay does not silently splice into an active queue; it joins at the next plan build or explicit `Sort now`. A local explicit Add-to-Outstanding command may recompile the remaining global Outstanding stage immediately without moving the current target. Frozen scoped Worksets do not receive unrelated targets through this path.

For each successful target, the overlay change and the documented slight global priority increase are computed and committed as one Scheduler transition. For one target, the interim product policy `siyuanmemo-relative-step-v1` moves one global relative position toward Position 1 and saturates there. Batch adjustment preserves the targets' prior relative priority order and shifts each maximal contiguous selected run across one preceding non-selected Element; an already leading selected run is front-blocked. This is deliberately labeled as SiYuanMemo behavior because current first-party evidence does not define SuperMemo's formula. The policy contract forbids deriving it from interval mutation, explicit bulk Priority Increase, Topic A-Factor, or memory-algorithm state. `Add all to outstanding` is a separate execution mode that may bypass the ordinary same-day exclusion.

Every accepted event stores the policy identity/version plus before/after rank and saturation. A future evidence-backed policy applies prospectively. Projection rebuild always uses the transition already recorded in each event; it never recomputes old priority changes with a newer policy. Correcting historical priority positions, if ever desired, is an explicit compensating migration rather than a silent semantic rewrite.

## Workset Invalidation

The current Browser Workset is an ordered identity snapshot produced by field sorting, filtering, search, and `leave only` selection. Branch, collection, and saved-subset flows can produce the same value. Manual drag ordering is deferred; no current plan, priority field, `.sme`, `.smr`, or Element-tree `sort.json` stores a hand-authored row order.

Before compilation, Scheduler rejects malformed or duplicate IDs. Well-formed IDs that are deleted, unsupported, unavailable, Dismissed, same-day-processed, or otherwise ineligible are skipped with typed diagnostics rather than invalidating the rest of the Workset. A failed Runtime projection still returns `projection-rebuild-failed`; ordinary Workset execution never introduces a stale/revision-conflict UX. Learn's Outstanding and Pending stages filter the frozen membership under their own order authorities, while Review All accepts future-due memorized members for mid-interval repetition. Newly matching IDs absent from the snapshot are not appended.

During a session, a target is revalidated before it becomes current. If it became unavailable or ineligible after plan compilation, the Session skips it and records a queue diagnostic without re-sorting the remaining snapshot. A schedule mutation caused by a completed review does not reorder the frozen remainder.

## Explicit Scheduling Operation

Browser `Learn` is a read/execute operation over a `QueuePlan`; it does not persist the Browser order. SuperMemo-like `Schedule repetitions` is a separate, explicit Scheduler action. If implemented, it must carry the validated plan fingerprint, ordered target IDs, and learning date and commit a distinct schedule-order event. It must never be inferred from opening Browser, sorting a column, dragging a row, or starting Learn.

This separation prevents an accidental conversion of a presentation order into priority or interval state. An explicit scheduling operation can use its own conflict contract, while ordinary Workset learning proceeds with target-level validation and diagnostics.

## Testing And Scope

The first implementation should test the deep interface rather than Browser-to-Scheduler calls:

- field ordering, direction, null placement, and deterministic ties;
- malformed and duplicate Workset IDs rejecting launch while missing or newly ineligible targets are skipped with diagnostics;
- Browser Outstanding preserving Browser order while Browser Pending uses global Pending Queue order;
- Review All making one Workset-order pass across due, future-due memorized, and Pending targets with the correct target-native execution action;
- Learn and Review All both excluding Dismissed and same-day-processed targets;
- Browser Learn returning a Final Drill confirmation and resolving the global dynamic Drill pool only after acceptance;
- Final Drill grade `0..3` tail rotation, grade `4..5` removal, `FlipElement(5,3,6)` bounds and overlap handling, short-queue behavior, deterministic seed replay, and synchronized-membership/local-order separation;
- Worksets from different producers compiling identically when their ordered IDs and mode are identical;
- plan fingerprints changing when mode, target policy, membership, or order changes;
- static plans not receiving newly eligible Elements;
- session target revalidation without reordering the frozen remainder;
- Runtime projection failure remaining `projection-rebuild-failed` rather than becoming a Workset freshness prompt;
- global Priority Order, Daily Outstanding Order, and AutoSorted Review Order changing independently under their respective commands;
- default global auto-sort combining priority, Topic proportion, and recorded randomization while Browser Learn preserves Workset order;
- `siyuanmemo-auto-sort-v1` zero/full-random boundaries, deterministic seed replay, stable tie-breaks, monotone configuration validation, Item-first `20%` prefix, `0%`/`100%` Topic edges, type exhaustion, and population preservation;
- Sort Now retaining due/overlay membership, replacing only the remaining order after successful compilation, preserving the current target, and leaving all authoritative scheduling state unchanged;
- Sort Now discarding Add-to-Outstanding spacing authority while a failure preserves the previous remaining order;
- Add to Outstanding spacing boundaries, append overflow, earlier-only moves, learning-day expiry, and mid-interval classification;
- Add to Outstanding atomically committing or rejecting both daily placement and versioned priority adjustment without changing due time, interval, or Topic A-Factor;
- `siyuanmemo-relative-step-v1` single-target movement, Position 1 saturation, batch run movement, selected-prefix blocking, and invariance to Workset iteration order;
- a later policy version affecting new commands while historical event rebuild preserves its recorded before/after priority transition;
- priority updates remaining distinct from manual order;
- default due ordering remaining unchanged when no explicit plan is supplied.

This design does not implement Browser UI, manual drag ordering, Neural Roam traversal, saved plans, or `Schedule repetitions`. It establishes the seam that those later workflows can use without replacing Scheduler or exposing a shallow Browser-specific interface.
