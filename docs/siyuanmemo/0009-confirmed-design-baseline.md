# Confirmed Design Baseline

Date: 2026-07-19

## Purpose And Precedence

This document is the compact, authoritative inventory of decisions confirmed before implementation. It exists so conversation compaction, a new agent session, or an older draft cannot silently restore a rejected design.

Detailed behavior remains in documents `0001` through `0010`. When documents conflict, the more specific later decision wins. For storage, sync, recovery, deletion, and file layout, `0008-element-storage-sync-recovery-design.md` is authoritative. For kernel, Scheduler, Session, Ledger, query, transport, and Adapter Interface shape, `0010-deep-module-interface-design.md` is authoritative. A proposal may not convert an item under "Not Yet Decided" into an implementation assumption without a new decision record.

## Product And Platform

- SiYuanMemo is an AGPL-3.0 fork of SiYuan, not a plugin and not a clean-room replacement.
- The application keeps SiYuan's Go kernel, TypeScript frontend, webpack build, Electron shell, Protyle block editor, block model, backlinks, workspace, assets, history, and synchronization foundations.
- The kernel toolchain follows `kernel/go.mod`; the current fork requires Go 1.26 before implementation verification.
- The learning system is an independent Learning Engine. It does not extend or depend on SiYuan `riff`; `riff` is only an integration reference.
- The product name and display spelling are `SiYuanMemo`.

## Knowledge Layers

- Topics, Items, Concepts, and future learning types are SiYuanMemo Elements.
- SiYuan blocks remain user-authored notes. Topic material is not automatically converted into blocks.
- An explicit `CreateTopicFromBlock` action may create a Block-backed Topic whose live content source is one SiYuan block. This is an explicit source mode, not the default Topic storage model.
- Topic, Item, and Concept share one extensible Element envelope. New Element types must be addable through versioned payloads and renderer/scheduler policies instead of hard-coded switches spread through callers.
- Concept organizes Elements and provides inherited defaults. It is not schedulable by default.
- An Item is an Element, not a SiYuan block. Manual Q/A and single-blank cloze are MVP Item forms.
- Queues operate on `ReviewTarget`, not only Element, reserving `note.block`, progressive audio, progressive video, and future target kinds.
- Element workflow has independent dimensions: `.sme` stores `processingState = new | reading | processed`; monthly events derive `lifecycleState = pending | memorized | dismissed`; each Scheduler adapter owns versioned private algorithm state.
- Effective defaults resolve as Element override, primary `boundTo` Concept context, nearest structural Concept, then collection defaults. Binding to another Element uses its effective Concept but not its per-Element overrides; missing or cyclic bindings fall back structurally. Explicit links and backlinks do not participate.

## Element Storage

- SiYuanMemo uses SiYuan's dual-tree model: nested internal Elements inside a root file plus a filesystem tree of root Element documents.
- One root Element document is one `.sme` JSON file. Internal Topic, Item, and Concept descendants are nested in `children` and do not create separate files.
- Root `.sme` documents use ID-named directories and `sort.json`, analogous to SiYuan's document tree.
- Root-document ancestor chains follow SiYuan's missing-parent rule. Read-only projection diagnoses `missing-root-parent` and excludes the orphaned subtree without writing; the first writable workspace load/repair creates each conclusively absent ancestor parent-first as a real empty `Untitled` Topic root using the directory ID, emits no review event, and rebuilds the projection. Existing unreadable, unsupported, or sync-uncertain sources are never replaced by this rule.
- The user-facing `Elements` tree is a unified virtual projection over both storage trees. Internal extracts and Items remain visible beside root documents, with storage kind shown only through a restrained icon or badge.
- Direct internal children and child root documents may be freely interleaved under the same root Element. `sort.json` gives both storage kinds integer ranks in one sibling-order coordinate; it never creates structural membership or mount nodes.
- Internal-only sibling sets retain `.sme` array order. Mixed reorder commands normalize the complete sibling set in `sort.json` and keep the internal subsequence in matching array order as a recovery fallback.
- Promoting an internal Element to a root `.sme` preserves its ID, moves its descendants, removes it from the source, and leaves no mount placeholder.
- Demoting a root `.sme` to an internal Element is rejected while it still owns child root documents.
- Cross-root moves rewrite source and target `.sme` files while preserving moved Element IDs.
- Destructive multi-file changes are serialized by Element storage and call the narrow host `HistorySnapshotWriter` once before any authority write. The production Adapter uses `workspace/history/<timestamp>-update|delete|replace/storage/siyuanmemo/...`; snapshot failure causes zero authority writes, and sync conflicts reuse DejaVu `-sync` history.
- Cross-root moves, promotion, and demotion write destination/additive authority first, source/destructive authority second, affected `sort.json` last, and `memo.db` only after all authority writes succeed. A partial failure returns `element-write-partial`, blocks the stale Runtime projection, and triggers one rebuild from actual files; history is not replayed automatically.
- `.sme` has a root format version and per-type payload versions. Safely decodable future formats are read-only. A valid unknown Element type remains visible and queryable with `supportStatus = unsupportedReadOnly` and opaque payload data; it is not returned as missing or invalid. Rendering, scheduling, conversion, and mutation remain disabled, and later writes to the containing root must preserve unknown fields and descendants.
- Topic HTML, Item prompt/answer, annotations, read point, source provenance, relations, and asset references live in the owning `.sme` payload rather than separate per-Element files.
- HTML-backed Topic content is stored in `.sme`; a Block-backed Topic stores its stable block ID, optional source-notebook provenance, and Topic metadata in `.sme`, while the source content remains in the native `.sy` block. The block ID alone is authoritative identity, so moving the block across ordinary notebooks preserves the reference. Its derived material-source status is `available`, `unavailable`, or `unresolved`. Elements-tree queries use one live global block-tree batch lookup and leave misses `unresolved` without per-reference filesystem recovery. Opening or reviewing one target uses SiYuan's native reindex-aware load, which alone may establish conclusive `unavailable` or repair the disposable native index. Current location and status may be cached only in `memo.db`, are overlaid by live resolution before return, and are not written back to `.sme`.
- Topic payload version 1 uses a required `material` union. Block-backed Topics persist `material.kind = siyuanBlock`, a syntactically valid `blockId`, and optional `sourceNotebookId` provenance/hint; HTML Topics use `material.kind = html`. Invalid IDs and resolved encrypted sources remain visible as successful Element results with `invalid-block-reference` or `encrypted-source-unsupported`, omit material content/tri-state, and are blocked from material actions and learning queues until repaired or supported.
- `memo.db` lives under `workspace/temp/siyuanmemo/`, never participates in sync, and is fully rebuildable.
- Projection recovery follows SiYuan's index lifecycle: CGO transactionally publishes one complete replacement and non-CGO atomically persists a separate complete next value before swapping memory. If an accepted scheduling event triggers publication failure, that command returns `projection-refresh-failed` with accepted-event recovery facts; every subsequent query and learning action returns `projection-rebuild-failed` until a complete retry succeeds. No old/partial projection, stale mode, fallback database, or freshness UI is exposed. Isolated invalid sources remain deterministic diagnostics rather than whole-rebuild failures.
- SiYuan is the architectural skeleton: learning semantics are translated into SiYuan kernel ownership, workspace lifecycle, storage/index, sync/recovery, history/assets, and UI integration patterns. The model-owned Runtime latches projection failure, avoids ordinary-request reopen loops, and closes/recreates the Engine for internal rebuild; this is not a sixth Engine family or public route.
- API handlers retain no naked Engine pointer. Each host call runs one Engine operation under a bounded Runtime lease; close/rebuild rejects new work, drains active operations, and only then closes the old Engine.
- Scheduler defaults follow SiYuan's configuration lifecycle: Engine construction and queries use versioned in-memory defaults overlaid by valid files without writing; a host-owned writable startup bootstrap persists only missing files before API availability, skips SiYuan read-only mode and byte-identical data, and never replaces invalid existing sources. No scheduling-changing `.smr` event may precede persisted effective configuration, and missing historical configuration blocks new learning writes.
- Scheduling and review events live in monthly `.smr` JSON files, are immutable, and merge by `eventId` set union. They are not partitioned by device ID. Every scheduling-changing event references its observed adopted terminal through `baseSchedulingEventId`, or `null` for a root event.
- Different event IDs with the same scheduling base are legal concurrent siblings. The complete valid branch whose terminal is greatest by `(occurredAt, eventId)` is adopted; only that chain drives current schedule and algorithm state. Other branches remain immutable audit history.
- Same-ID/same-payload records de-duplicate. Same-ID/different-payload records, missing or cross-Element bases, cycles, and incompatible transitions are excluded from adoption pending repair. Adoption classifications exist only in rebuildable `memo.db`.
- Root existence comes from scanning `.sme` files. Internal membership comes from `.sme` nesting. `sort.json` controls mixed and root-document display order only.
- SiYuanMemo reuses `workspace/data/assets/`; asset indexing and cleanup must scan both `.sy` and `.sme` references.
- Local recovery history follows SiYuan's `workspace/history/<timestamp>-<operation>/storage/siyuanmemo/...` snapshot layout and does not sync; this reuses native directory-based retention and workspace-history clearing, while current `.sme` snapshots deliberately remain outside `indexHistoryDir` because its disposable schema only understands `.sy`, assets, and attribute views. A later visible Element-history browser may add a dedicated history type rather than misclassifying `.sme` as a document. Sync conflicts reuse DejaVu-generated `-sync` snapshots. Startup validates current authority and never silently rolls back or completes an operation from history. Recovery is an explicit later command, with no WAL, two-phase commit, operation journal, or separate SiYuanMemo history root.
- Sync result processing detects upserts, removes, and conflicts under `/storage/siyuanmemo/`, waits for SiYuan's native remove-before-upsert `incReindex` to finish, and only then performs type-aware conflict recovery plus one Runtime drain/close/recreate rebuild. An uninitialized boot Runtime is left for `InitSymemo`, and no public rebuild route is added.
- Completed Topic `Next` and Item grade actions are authoritative monthly `.smr` events. Default due queues are rebuilt from authoritative state; only interrupted scoped queues may use disposable local recovery at `workspace/temp/siyuanmemo/active-session.json`.
- The local session snapshot is not synchronized and has no cross-device revision or merge protocol. Its `sessionId` may group `.smr` events for audit and statistics without making the queue cursor authoritative.
- Encrypted notebooks are not supported. Commands reject encrypted sources or targets before normal material resolution or any write; pre-existing references receive `encrypted-source-unsupported`, not a transient material-source status. SiYuanMemo implements no decryption, encrypted index, key management, or plaintext fallback.

## Topic Reading And Editing

- HTML-backed Topic material is medium-cleaned HTML. The MVP preserves useful reading structure and source provenance but not a complete original webpage snapshot.
- HTML-backed Topics use a dedicated HTML Reader/Editor surface. Block-backed Topics use a Protyle surface and edit the referenced native block directly; Protyle remains the only editor for that block content.
- TinyMCE is the first `TopicHtmlEditorAdapter`; it is replaceable and does not own storage or domain rules.
- Reading and editing modes share the same Topic payload. Editing remains material cleanup, not note authoring.
- Stable block-level node IDs plus offsets and text quotes anchor extraction highlights, read points, and annotations.
- `Alt+X` performs explicit extraction from the current selection.
- Extract creates a child Topic and highlights the source range; it does not create a note block and does not automatically open the child.
- Split is an explicit child-Topic operation. MVP split is selection-driven; automatic heading/paragraph splitting is later.
- Extracted or split Topics enter the learning process according to the Topic scheduling policy.
- Images and formulas remain usable in Topics and Items; formula source is preserved, not only rendered pixels.

## Notes And References

- Only an explicit user command sends Element material into the note workflow. Import, extract, split, highlight, and ordinary reading never create note blocks automatically.
- `SendToNote` can target a new SiYuan document or today's Daily Note.
- The default note shape is a SiYuan list block containing a parent list-item Element reference and an empty child block where the user writes their own note.
- The list block wrapper is retained because a SiYuan list item always belongs to a list block.
- Sending the same Element to the same Daily Note reuses its anchor list item and appends another empty child note block.
- The Daily Note remains an unmodified native Protyle document. It receives no SiYuanMemo return button, reminder banner, or custom editor chrome.
- Every Element type can be referenced. Clicking follows SiYuan tab reuse rules; hovering shows a lightweight read-only preview.
- Element backlinks include explicit references from note blocks and explicit links from other Elements, with source context snippets.
- Parent/child structure, siblings, Concept inheritance, the primary `boundTo` relation, and full-text matches are not backlinks.
- Child and structural relations belong in Element Context, not the backlinks dock.
- A block without an explicit back-side answer can be explicitly promoted to a Block-backed Topic. Its schedule belongs to the Topic Element while its live material remains the referenced `.sy` block; the derived material source becomes `unavailable` only after conclusive absence and otherwise remains `unresolved`, never triggering an automatic HTML copy.
- Explicit Q/A or cloze intent creates an Item snapshot. Its prompt/answer payload is stored in `.sme`, edited through the HTML Item Editor, and the original `.sy` block remains unchanged. Refreshing an Item snapshot from its source block is a later explicit command, not automatic synchronization.

## Learning And Queues

- `Learn` exists at the bottom of every opened Element, next to the larger `Add new` button and above Element Context.
- Starting `Learn` returns to the active in-process session. After restart, only a locally recoverable interrupted scoped queue prompts the user to continue or discard it; otherwise the default due learning queue is rebuilt and started.
- Review stays in the current reusable Element tab. Advancing the queue navigates that tab to the next ReviewTarget; there is no separate review tab and no `Open Element` review command.
- Manually navigating away from the active card previews another Element. Its primary button returns to `Learn`; clicking it returns to the active queue without scheduling the previewed Element.
- An active Topic card shows `Next`. Only that `Next` records a repetition and reschedules the Topic. Any non-learning navigation `Next` is scheduler-neutral.
- An active Item card shows `Show Answer`, then grading. The scheduler stores all six raw grades `0..5`; grades `3..5` pass and `0..2` fail.
- Final-drill eligibility is separate from pass/fail. A review below Good may be queued for final drill even when grade `3` passes.
- Normal queues skip targets already reviewed on the same local learning day unless the user explicitly requests another repetition.
- `Dismiss` leaves an Element in its current structural location and changes its `lifecycleState`; it does not move it into an automatic category.
- Dismissing excludes an Element from queues but preserves its adopted schedule, adapter states, and immutable history. Remembering it restores that state and makes it due if overdue. Forgetting returns it to pending and clears current scheduling state while retaining historical events.
- `Add new` creates only a Topic with zero or one primary `boundTo` relation to the current Concept or Element context. Binding is non-structural and not a backlink; rebinding replaces it, while additional associations use explicit Element links and normal backlinks.
- `Learn subset` processes due/outstanding memorized targets in browser order, then introduces pending Elements under configured limits. `Review all` permits deliberate mid-interval review. `Review topics` is Topic-only.
- Canonical queue modules and IDs are `IncrementalLearningQueue` / `incremental-learning`, `RetrievalPracticeQueue` / `retrieval-practice`, `FilterGroupQueue` / `filter-group`, `FinalDrillQueue` / `final-drill`, `NeuralRoamQueue` / `neural-roam`, and `LeechReviewQueue` / `leech`.
- Neural roam and future queue types are reserved behind the common queue interface. Its note population is every note block that explicitly references an Element, not arbitrary notes. A referenced note block may remain a Neural target, or an explicit user action may promote it to a Block-backed Topic; neither path requires HTML conversion.

## Scheduler Core

- The Scheduler Engine is independent from `riff` and owns queue selection, repetition actions, lifecycle, priority position, postpone, event history, algorithm execution, and adopted schedule state.
- The internal `SchedulingLedger` owns atomic monthly event commits, de-duplication, causal merge, complete-branch adoption, scheduling projection, and rebuild. UI code and algorithm Adapters receive one adopted state and do not know conflict semantics.
- Scheduling algorithms are compiled Go adapters behind one stable internal interface in the MVP. Runtime-loaded third-party algorithm code is later.
- `fsrs-v1` is the primary Item adapter from the first real implementation. `simple-v1` remains deterministic fallback and shadow comparator.
- Raw grades map for FSRS as `0/1/2 -> Again`, `3 -> Hard`, `4 -> Good`, and `5 -> Easy`; both raw and mapped values are recorded.
- The algorithm arena runs only compatible same-profile Adapters against the same adopted review chain, records candidates and prediction error, and adopts one canonical schedule. Concurrent superseded and invalid branches are not extra training repetitions.
- Weighted consensus and adaptive ensemble decisions are reserved until enough review history and explainability exist.
- Element type determines the default schedule profile, while explicit enrollment may refine it. Items use `fsrs-v1` as primary with `simple-v1` fallback/shadow. HTML-backed and Block-backed Topics use `topic-afactor-v1`. Concepts use `none` by default and use `topic-afactor-v1` only after explicit enrollment. Future target kinds declare their own profile.
- Topic scheduling is distinct from Item memory scheduling. `NextTopic` is ungraded and records `reviewKind = nextTopic`; it is never encoded as `reviewKind = gradeItem`, `grade=4`, or a generic graded payload. `topic-afactor-v1` uses `nextInterval = previousInterval * effectiveAFactor`, with default A-Factor `2.5` plus Topic-specific minimum, maximum, and skip-interval policy.
- Only Item grades enter the Item memory arena. Topic candidates do not train or compete with Item memory algorithms; a later Topic-specific arena may compare compatible Topic profiles.
- Priority position follows the established direction: `0%` is highest priority and larger percentages are lower priority.
- Current schedule state is materialized in `memo.db` from `.sme`, each Element's adopted `.smr` chain, and scheduler configuration. The complete immutable event set remains authoritative history.

## Module Interfaces

- SiYuanMemo keeps stable named `/api/symemo/*` routes for the built-in frontend, plugins, automation, and diagnostics. HTTP handlers and named TypeScript client functions are transport Adapters, not the kernel Module Interface.
- One concrete Learning Engine exposes five operation families: `CreateElement`, `ChangeElement`, `RunLearningAction`, `SendToNote`, and `Query`. Route actions are closed typed variants; callers never pass unvalidated generic maps or coordinate partial workflows.
- `LearningSession` is a separate concrete internal Module with Start, Act, and Current. It owns current target, answer visibility, queue advancement, preview return, and disposable scoped-session recovery.
- Scheduler is a concrete internal Module with `BuildQueue` and `Apply`. It owns queue policy and committed scheduling transitions, but not session UI state, Element deletion, Browser queries, or note integration.
- `SchedulingLedger` is concrete and exposes Snapshot, Commit, and Refresh internally. A second hypothetical implementation is not created for mocking.
- Topic material processing is pure internal implementation behind Engine creation/change commands; it has no action Interface and never writes scheduling state.
- Algorithm Adapters expose Describe, Initialize, Predict, Review, and Migrate, and describe their supported target and action kinds. The first real Adapters are `simple-v1`, `fsrs-v1`, and `topic-afactor-v1`. Manual rescheduling and state persistence are Scheduler/Ledger responsibilities.
- SiYuan integration uses narrow `BlockReferenceReader`, `BlockSnapshotReader`, `NoteAnchorWriter`, `AssetStore`, and `HistorySnapshotWriter` Adapters. `BlockReferenceReader.LookupMany` mirrors SiYuan's global batch block-tree query; `Load` mirrors its reindex-aware single-target loader. `HistorySnapshotWriter` creates one complete native-layout pre-image entry while Element storage retains transaction/write-order ownership. A broad `SiYuanBridge`, separate SiYuanMemo filesystem scanner, and private history root are rejected.
- All Element, tree, subset, backlink, session, and arena reads enter through the Engine `Query` family. Query callers never depend on `memo.db` schema.

## UI Integration

- Compact SiYuan-fused mode is the default. Classic menu/navigation chrome is an optional preference.
- SiYuanMemo commands live under the native workspace menu as `SiYuanMemo > File/Edit/Search/Learn/View/Toolkit/Window/Help`; there is no competing top-level workspace button.
- Compact navigation is a small cluster in the fused tab/top area. It must not consume the reader/editor content row.
- The top area has no permanent global search, queue count chips, current interval chips, postpone button, extraction button, or send-to-note button.
- Common Element actions live in the toolbar below the Reader/Item surface. Tool groups switch among Learn, Edit, Read, Tools, and Alarm sets; action buttons wrap responsively only when width requires it.
- The workspace Shell, toolbar, navigation, docks, and learning controls remain stable while the content surface switches between HTML Topic, Protyle block, Item HTML editor, Item review renderer, and future media adapters.
- The left `ElementsDock` shows one virtual tree containing every internal Element and root `.sme`; right `ElementInspector`, right `ElementBacklinksDock`, and bottom `ElementContext` hide and show independently through persistent dock controls.
- `ElementBacklinksDock` is independent from Inspector.
- `ElementBrowser` opens in a center tab, not a dock. The workspace menu and command system are its canonical entry points; duplicate large Browser buttons are avoided.
- Element Browser supports due, all, pending, memorized, dismissed, priority, branch, search, and filtered subsets, plus subset learning and row synchronization.
- Element tabs follow SiYuan reuse, dirty-state, pinning, split, and layout restoration conventions.
- Type, lifecycle, and processing icons use familiar progressive-learning semantics but are redrawn in SiYuan-native style rather than copied bitmap assets.
- Internal implementation labels such as `ReviewTarget`, `QueueType`, renderer adapter, schedule owner, database paths, and algorithm config filenames are not shown in the normal UI.

## MVP Boundaries

- The MVP does include the independent scheduler core, Topic and Item ReviewTargets, FSRS adapter, shadow comparator, local interrupted scoped-session recovery, due/subset lookup, Topic `Next`, Item answer reveal and grading, rebuildable index, basic Topic HTML reading/editing, explicit extraction/split, explicit block-to-Element commands, SendToNote, Element references/backlinks, native Element tabs/docks, and a basic Element Browser.
- The MVP does not include automatic note generation, automatic note-to-card generation, image occlusion, multi-cloze generation, URL fetching, PDF/EPUB readers, browser-extension clipping, complete webpage snapshots, runtime algorithm plugins, weighted consensus, optimizer training, full neural roam behavior, progressive audio/video implementation, or encrypted notebook support.
- The first Spec Kit tracer is narrower than the MVP: one prepared root Q/A Item, due-only lookup, default Session Start, Show Answer, Grade `0..5`, `fsrs-v1` primary plus `simple-v1` shadow/fallback, durable `.smr` adoption, concurrent-history fixtures, and `memo.db` rebuild. It has named backend routes and no production UI.
- Topic behavior, creation/import, tree mutation, Browser, scoped-session recovery, lifecycle/postpone actions, note/block/assets integration, and actual SiYuan sync hooks are explicitly outside the first tracer.

## Not Yet Decided

- The exact visible gesture for grade `0` in the five-button grading surface is not fixed, although storage of grade `0` is mandatory.
- Neural roam scheduling policy, progressive audio/video renderers, and additional adaptive algorithms remain future design work. Block-backed Topic behavior and Item snapshot conversion are confirmed; Neural grading semantics remain future work.
