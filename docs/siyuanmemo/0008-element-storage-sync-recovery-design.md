# Element Storage, Sync, And Recovery Design

Date: 2026-07-19

## Decision

SiYuanMemo adopts SiYuan's file-first, dual-tree storage model for Elements. One root Element document is stored in one `.sme` JSON file, internal Elements are nested inside that file, and root Element documents form a second tree through ID-named directories plus `sort.json`. SQLite remains a rebuildable index. Review and scheduling history is stored as immutable events in monthly `.smr` JSON files and retained in full so `memo.db` can always be rebuilt from synchronized authority.

This document supersedes the earlier one-Element-directory layout using `element.json`, `content.html`, `item.json`, and `annotations.json`, the separate SiYuanMemo asset store, and standalone tombstone files described in older design drafts.

SiYuan encrypted notebooks are explicitly excluded. SiYuanMemo must fail closed instead of copying decrypted notebook content into its plaintext Element store.

## Storage Domains

```text
workspace/data/storage/siyuanmemo/
  elements/
    .siyuan/
      sort.json
    <root-element-id>.sme
    <root-element-id>/
      <child-root-element-id>.sme
      <child-root-element-id>/
        <grandchild-root-element-id>.sme
  reviews/
    YYYY-MM.smr
  scheduler/
    collection.json
    simple-v1.json
    fsrs-v1.json
    topic-afactor-v1.json
    learning-day.json

workspace/data/assets/
  <SiYuan-managed assets>

workspace/temp/siyuanmemo/
  memo.db
  active-session.json

workspace/history/
  <timestamp>-<operation>/
    storage/siyuanmemo/
```

`workspace/data/storage/siyuanmemo/` and `workspace/data/assets/` participate in SiYuan data synchronization. `workspace/temp/siyuanmemo/` and all snapshots under `workspace/history/` do not participate in sync.

`data/storage` is a SiYuan core data domain, not a plugin-only directory: core Riff uses `storage/riff`, attribute views use `storage/av`, kernel stores use direct `storage/*.json`, and plugins use the separate `storage/petal` subtree. SiYuanMemo therefore remains under `storage/siyuanmemo` instead of introducing a new workspace top-level or pretending Elements are notebook `.sy` documents.

All authoritative reads and replacements reuse SiYuan `filelock`; `filelock.WriteFile` already combines its per-path process lock with safer replacement. Complete read-modify-write operations retain SiYuanMemo package/Runtime serialization. Model entry points wait through SiYuan's `waitForSyncingStorages` before bootstrap or Runtime lease acquisition, matching Riff and attribute-view behavior and covering background storage merge that can continue after `BootSyncData` returns. Generic `storage` JSON has no automatic document-history generation. Missing-only bootstrap has no previous source to snapshot; later destructive SiYuanMemo operations explicitly enter the SiYuanMemo history integration described below.

The bounded Runtime lease protects Engine lifetime; an internal collection-wide Scheduler write lease separately serializes schedule-changing `Apply` work. For `GradeItem` and `NextTopic`, latest adopted target state, pure Adapter execution, event serialization, monthly `.smr` replacement, and projection publication occur before that Scheduler lease is released. Answer display and learner think time hold neither scheduling write ownership nor a long-lived Engine pointer. Other scheduling writes wait, and post-sync close/rebuild drains active Runtime leases.

The scheduler configuration files are collection-level files, not one file per Element. Feature 003 fixes Item scheduling to `fsrs-v1` with `simple-v1` fallback/shadow and Topic scheduling to `topic-afactor-v1`; it does not add profiles, policy registries, activation pointers, or Algorithm Arena configuration. `learning-day.json` remains independent mutable collection configuration.

Scheduler configuration follows SiYuan's load/default/save separation. Engine construction and queries build effective configuration from versioned built-in defaults overlaid by valid persisted files and perform no authoritative write. A separate host-owned startup bootstrap runs before API availability only when the SiYuan workspace is writable; it creates missing files through locked writes, skips byte-identical content, and never replaces an existing invalid, unreadable, or unsupported source. SiYuan read-only mode skips bootstrap. Persisted effective configuration is required before the first scheduling-changing event; if `.smr` history exists while required configuration is missing or invalid, reads and diagnostics remain available where safe but new scheduling writes fail until recovery.

Feature 003 retains complete `.smr` history indefinitely and performs no automatic compaction. A Checkpoint becomes justified only after measured full replay is too slow, history compaction is introduced, or the product explicitly requires scheduling continuation after raw-history loss. That future work requires a separate specification.

## Dual-Tree Model

SiYuanMemo has two Element storage trees, mirroring the distinction between SiYuan's document-internal block tree and document file tree.

### Internal Element Tree

Each `.sme` contains one root Element and its nested `children`. Topic, Item, Concept, and future Element types share the same node envelope. Editing any internal Element rewrites its owning root `.sme`, just as editing a block rewrites its owning `.sy` document.

Internal nodes do not persist redundant `parentId`, `rootId`, or file `path` fields. These values are derived while loading the tree and materialized in `memo.db` for lookup.

### Root Element Document Tree

Root `.sme` files form a separate hierarchy through the filesystem. If `<A>.sme` has a child root document `<B>.sme`, the child is stored as `<A>/<B>.sme`. File discovery determines which root documents exist. `elements/.siyuan/sort.json` only stores sibling display order and must never be treated as the existence authority.

### Missing Root-Document Ancestors

SiYuanMemo follows SiYuan's parent-document completion rule. For a descendant such as `A/B/C.sme`, every ID-named ancestor directory implies an expected sibling root source, including `A/B.sme`. A directory alone is not an Element, mount, or source of truth.

The read-only projection phase validates the complete ancestor chain. If an expected parent `.sme` is absent, it emits one deterministic `missing-root-parent` diagnostic per missing expected path, excludes the complete orphaned subtree from the normal Elements tree, and performs no authoritative write.

The first writable workspace load or post-sync repair phase completes the chain from the highest missing ancestor downward. Each missing source is created as a real empty Topic root with the directory's Element ID, title `Untitled`, current supported source/payload versions, no children, and no synthesized review event. After all writes succeed, the Engine rebuilds the projection. This is structural recovery, not a mount placeholder or conversion of the descendant.

Absence must be conclusive. An existing parent that is unreadable, malformed, safely decodable only as a future read-only format, excluded by encryption, or temporarily uncertain during sync is never overwritten or replaced by parent completion. Normal history/conflict handling applies before any destructive replacement, but creating a conclusively absent parent has no previous source to snapshot.

The two structural storage domains remain distinct, but they share one persisted custom-order coordinate where they meet. Internal-only sibling sets use their `children` array order. When a root Element has both direct internal children and child root documents, `sort.json` assigns an integer rank to every visible child regardless of storage kind. The unified projection sorts that mixed sibling set by those ranks, allowing arbitrary interleaving instead of grouping by storage kind.

`sort.json` follows SiYuan's flat ID-to-integer custom-sort pattern. Element IDs are globally unique, and ranks are compared only among children of the same logical parent, so one map can cover the whole Element collection:

```json
{
  "20260719010102-internal": 1,
  "20260719010103-rootdoc": 2,
  "20260719010104-internal": 3
}
```

An entry is ordering metadata, not structural membership and not a mount. Root-document membership still comes from the filesystem and internal membership still comes from `.sme` nesting. Stale IDs are ignored. Missing ranks sort after ranked siblings in their source-local order with Element ID as the final deterministic tie-breaker; a repair or the next explicit reorder normalizes the complete sibling set to contiguous integer ranks.

## Unified Virtual Elements Tree

The user-facing `Elements` tree is a unified virtual projection over both storage trees. Every internal Topic, Item, Concept, future Element type, and root `.sme` document is visible in the same navigation surface, preserving the progressive-reading workflow where extracts and Items remain discoverable beside other Elements.

The projection is built by the Learning Engine and `memo.db`; the database projection is not source data. Each tree DTO node includes at least its Element ID, type, logical parent, storage kind (`internal` or `rootDocument`), owning root ID, and root-document path when applicable. Root documents use a distinct icon or badge, but they are not placed in a separate user-facing tree.

Opening, searching, reviewing, and referencing an Element is independent of storage kind. Moving an internal Element within one root rewrites one `.sme`; moving it across roots rewrites source and target `.sme`; promoting it to a root document preserves its ID and removes it from the source; moving a root document relocates its file subtree. These operations all appear as normal Element tree commands and are owned by the Learning Engine.

At a mixed root boundary, users may drag an internal Element or child root document before, after, or between either storage kind. The reorder command writes normalized ranks for the complete mixed sibling set to `sort.json`. It also keeps the internal `children` array in the same relative order as the internal subsequence, providing a deterministic recovery fallback without turning the array into authority over root-document existence. Reordering an internal-only sibling set continues to rewrite only its owning `.sme`.

## No Mount Placeholder

SiYuanMemo does not store a `mount`, proxy, or placeholder node when an internal Element is promoted into an independent root `.sme`.

Promotion follows SiYuan's heading-to-document behavior:

1. Preserve the promoted Element ID as the new `.sme` root ID.
2. Move the Element payload and internal descendants into the new `.sme`.
3. Remove the Element completely from the source `.sme`.
4. Place the new file in the selected root-document directory.
5. Update the shared sibling-order ranks.

Demoting a root `.sme` back into another `.sme` as an internal Element is allowed only when the source root has no child root-document directory. This matches SiYuan's refusal to convert a document with child documents into an internal heading.

## `.sme` Format

`.sme` is structured JSON. The root file carries a format version, while each type payload carries its own version so Topic, Item, Concept, and future types can evolve independently.

```json
{
  "spec": 1,
  "id": "20260719010101-abcdefg",
  "type": "topic",
  "title": "Example topic",
  "processingState": "new",
  "createdAt": "2026-07-19T04:30:00+08:00",
  "updatedAt": "2026-07-19T04:30:00+08:00",
  "payloadSpec": 1,
  "payload": {
    "material": {
      "kind": "html",
      "html": "<p data-symemo-node-id=\"n1\">Cleaned material</p>"
    },
    "annotations": [],
    "reading": {},
    "source": {},
    "assetRefs": []
  },
  "relations": [],
  "children": []
}
```

The node envelope is type-neutral. `processingState` records `new`, `reading`, or `processed` material workflow state in `.sme`; it is independent from learning lifecycle and algorithm state. The `payload` changes by Element type:

- Topic payload stores cleaned HTML, annotations, read point, source provenance, and asset/formula references.
- Item payload stores Q/A or cloze prompt and answer HTML, source provenance, and asset/formula references.
- Concept payload stores organization metadata and inherited learning defaults.
- Unknown future types retain their raw payload and children even when the current binary cannot render them.

If the root `spec` is newer than the current binary supports, the file opens read-only when its common envelope can still be decoded safely. If the root format is understood but a payload version or Element type is unknown, the loader preserves the raw JSON and returns the Element as `unsupportedReadOnly` instead of treating it as missing or invalid. Tree and Element queries succeed with common identity, title, type, structure, and opaque payload metadata; rendering, scheduling, conversion, and mutation are disabled until a compatible Adapter exists. Rewriting a containing root must preserve every unknown field and child rather than dropping data.

Topic imports keep medium-cleaned reading HTML and source provenance. The MVP does not retain a complete original webpage snapshot.

### Block-backed Topic Payloads

An explicit `CreateTopicFromBlock` action may create a Block-backed Topic. Topic payload version 1 uses a required discriminated `material` union. The canonical native-block value is:

```json
{
  "material": {
    "kind": "siyuanBlock",
    "blockId": "20260720123000-abcdefg",
    "sourceNotebookId": "20260720000000-notebook"
  }
}
```

`blockId` is required and uses SiYuan's node-ID syntax. `sourceNotebookId` is optional creation-time provenance and a lookup hint, never identity. The payload stores no current notebook/path, material status, diagnostic, resolved content, or HTML snapshot. Following SiYuan block-reference semantics, moving the block to another ordinary notebook does not break the Topic. The referenced `.sy` block remains the material authority and is edited through native Protyle transactions. Imported and extracted HTML Topics use `material.kind = html`; future audio/video material extends this versioned union instead of relying on field-shape inference.

Block-backed material resolution is tri-state and derived; it is never persisted as authoritative truth in `.sme`:

- `available`: the stable block ID resolves through SiYuan's current global block state, regardless of whether the block moved to another ordinary notebook;
- `unavailable`: SiYuan's reindex-aware lookup can run in stable host state and conclusively reports that the block does not exist;
- `unresolved`: the stable block ID is valid, but notebook loading, synchronization, indexing, a closed notebook, or temporary resolver availability prevents a conclusive answer.

The read path uses the narrow `BlockReferenceReader` Adapter and mirrors SiYuan's two existing lookup paths. `LookupMany` maps to the current global `treenode.GetBlockTrees` index query and resolves all Block-backed rows for an Elements-tree request in one batch. A batch miss remains `unresolved`; projection rebuild and tree query do not invoke rate-limited filesystem recovery once per reference. `Load` maps to `model.LoadTreeByBlockIDWithReindex` only when one Block-backed Topic is opened or loaded for review. That native path may repair SiYuan's disposable block index, returns `unavailable` only after stable conclusive absence, and maps indexing, synchronization, a closed notebook, rate limiting, or other temporary uncertainty to `unresolved`.

Derived status and current location may be cached only in `memo.db`, and every tree/detail response overlays that cache with the applicable live `LookupMany` or `Load` result. The Adapter must not turn `unresolved` into `unavailable`, treat a changed notebook as a broken reference, write derived location or status back to `.sme`, create an HTML snapshot, or implement a second filesystem scanner. An explicit later conversion or repair command may create a new snapshot after a conclusive result. Block-backed Topic content changes do not create scheduling events by themselves.

SiYuanMemo validates `blockId` with SiYuan's node-ID rule before calling the Adapter. A syntactically invalid ID produces `invalid-block-reference`; a reference resolved to an encrypted notebook produces `encrypted-source-unsupported`. Both are outside the material tri-state, omit material content/status, and leave the referring Element visible through tree and detail queries so it can be located and repaired. Material opening, review, conversion, mutation, and queue eligibility remain blocked without producing a scheduling event. Creation/write commands reject the same conditions before any partial write.

Item creation from a block is different: `CreateItemFromBlock` reads a source snapshot and stores prompt/answer or cloze content in `.sme`. The source `.sy` block remains unchanged, and later refresh is an explicit command.

## Relations And Structure

Structural parent-child membership is represented only by nested `children` or the root document directory tree. Non-structural relationships use versioned typed entries in `relations`, including source/extract provenance, explicit Element links that are not embedded in content, and the primary contextual binding used by `Add new`.

`Add new` may bind a new Topic to the current Concept or Element without implying that it is an internal child. The canonical relation is:

```json
{
  "spec": 1,
  "type": "boundTo",
  "targetElementId": "20260719010101-abcdefg",
  "createdAt": "2026-07-19T04:30:00+08:00"
}
```

A Topic has zero or one `boundTo` relation. Rebinding atomically replaces the old entry. The relation is contextual metadata used by `Add new`, Element Context, bound-context queries, and Concept-default resolution; it does not establish tree membership, create a mount, or appear in Element Backlinks. Deleting the target leaves a resolvable missing-target binding until the user rebinds or clears it, following the same recoverable-reference policy as other stable Element IDs.

Effective defaults resolve in this order:

1. Explicit override on the Topic.
2. Primary `boundTo` Concept context.
3. Nearest structural Concept ancestor.
4. Collection defaults.

If `boundTo` targets a Concept, that Concept supplies step 2. If it targets another Element, step 2 uses that Element's effective Concept but does not copy or inherit the target's own per-Element overrides. Resolution tracks visited Element IDs; a missing target or cycle skips the binding and falls back to structural ancestry. The resolved Concept/default projection may be cached in `memo.db`, but `.sme` relations, structural ancestry, and collection configuration remain authoritative.

Additional associations must use explicit Element links, either as links in Element content or typed `elementLink` relations where a payload has no link-capable content surface. Explicit links are indexed as backlinks with source context. They are not additional bindings, so a Topic never has multiple competing primary contexts.

## Monthly `.smr` Event Files

Review, scheduling, lifecycle, deletion, move, and repair events are immutable records stored by local learning month in `reviews/YYYY-MM.smr`. The file is JSON with a versioned envelope and an event array.

```json
{
  "spec": 1,
  "month": "2026-07",
  "events": [
    {
      "eventId": "20260719043000-aaaaaaa",
      "occurredAt": "2026-07-19T04:30:00+08:00",
      "type": "reviewElement",
      "reviewKind": "gradeItem",
      "elementId": "20260719010101-abcdefg",
      "sessionId": "20260719040000-session",
      "baseSchedulingEventId": "20260716083000-previous",
      "rawGrade": 4,
      "passed": true,
      "before": {},
      "after": {},
      "algorithmCandidates": []
    }
  ]
}
```

A Topic event uses the same causal envelope but a different payload:

```json
{
  "eventId": "20260719043100-bbbbbbb",
  "occurredAt": "2026-07-19T04:31:00+08:00",
  "type": "reviewElement",
  "reviewKind": "nextTopic",
  "elementId": "20260719010102-hijklmn",
  "sessionId": "20260719040000-session",
  "baseSchedulingEventId": "20260716083100-previous",
  "before": {
    "scheduleProfile": "topic-afactor-v1",
    "intervalDays": 4,
    "topicAFactor": 2.5
  },
  "after": {
    "scheduleProfile": "topic-afactor-v1",
    "intervalDays": 10
  }
}
```

The `.smr` event set is authoritative for review and scheduling history. The compatible MVP envelope may retain `type = reviewElement`, but `reviewKind` is a required closed discriminator. An Item review uses `reviewKind = gradeItem`, records raw grade `0..5`, rating mapping, the FSRS and Simple candidates, the selected result, and complete before/after scheduling state. A Topic review uses `reviewKind = nextTopic`, has no grade or pass/fail fields, and carries the `topic-afactor-v1` transition. A Topic `Next` must never be serialized with the graded Item payload merely to reuse the outer event envelope. A later source-format migration may promote these review kinds to distinct top-level event types without changing their domain semantics.

`memo.db` materializes current due dates, lifecycle state, schedule profile and Adapter state, priority position, queue eligibility, and summary statistics from one causally valid adopted scheduling chain per Element together with `.sme` Elements and scheduler configuration. Events outside the adopted chain remain history but do not act as additional repetitions.

Events are never edited in place after publication. A correction is a new compensating event that references the original `eventId`. The format is partitioned by month, not by device ID. Persistent paths must not depend on a device identifier.

### Scheduling Causality And Adoption

Every event that changes an Element's scheduling projection carries `baseSchedulingEventId`. The base is the event ID of that Element's adopted scheduling terminal observed immediately before the command, or `null` when the Element has no preceding scheduling event. Scheduling-changing events include Topic `Next`, Item Grade, Postpone, Reschedule, Remember, Forget, Dismiss, and future equivalent transitions. Move-only, content-only, and summary events are not placed in this chain unless they also change scheduler state.

Element type determines its default schedule profile, while explicit enrollment and supported configuration may refine it:

| Element target | Schedule profile | Accepted review action |
| --- | --- | --- |
| Item | `fsrs-v1` primary, `simple-v1` fallback/shadow | `GradeItem(0..5)` after answer reveal |
| HTML-backed Topic | `topic-afactor-v1` | ungraded `NextTopic` |
| Block-backed Topic | `topic-afactor-v1` | ungraded `NextTopic`; content renders in Protyle |
| Concept | `none` by default | explicit enrollment selects `topic-afactor-v1` and `NextTopic` |
| Future target kinds | declared future profile | profile-declared action |

`topic-afactor-v1` starts with an effective A-Factor of `2.5`, resolved from an Element override, primary Concept context, structural Concept, then collection defaults. Its normal transition is `nextInterval = previousInterval * effectiveAFactor`, constrained by Topic-specific minimum and maximum interval settings; an explicit skip interval is a separate Topic policy input. It does not accept Item grades.

The kernel's internal `SchedulingLedger` is the sole owner of this field and its semantics. It commits monthly events, de-duplicates input, builds a causal graph per Element, selects the adopted branch, updates `memo.db`, and rebuilds the projection. Frontend code and algorithm Adapters see one current state and normalized action input; they never choose branches or inspect synchronization conflicts.

After loading or merging events, the ledger applies these order-independent rules:

1. Canonically identical records with the same `eventId` are duplicates and contribute one graph node.
2. The same `eventId` with different payloads is invalid until explicit repair; neither payload may silently win.
3. Different event IDs with the same base are legal concurrent siblings. They are not replayed as consecutive reviews.
4. A valid root has a null base. A valid descendant references an existing valid scheduling event for the same Element, contains no cycle, and has a transition compatible with the parent's recorded after-state.
5. A valid event with no valid child is a terminal. The terminal with the greatest `(occurredAt, eventId)` tuple wins deterministically, and that terminal plus all of its ancestors is the adopted chain.
6. A later child can extend a previously superseded branch. The ledger then compares complete branch terminals again and may adopt that entire branch; it never adopts an isolated descendant without its ancestors.

Only the adopted chain drives lifecycle, due dates, Adapter state, and queue membership. Other valid graph nodes remain immutable history. `memo.db` may classify ingested records as `adopted`, `concurrent-superseded`, `invalid`, or `duplicate`, but those labels are derived and must never be written back into `.smr`.

The MVP has one fixed adoption rule, no `ConflictStrategy` Adapter, no device-based winner, and no ordinary conflict dialog. Active learning session recovery is separate: a local session snapshot can restore scoped-queue navigation but cannot select or override a scheduling branch.

## Write And Move Semantics

All Element mutations pass through the kernel Learning Engine and its storage queue. Scheduling-changing mutations additionally hold the collection Scheduler write lease and pass through `SchedulingLedger`, which writes a fully serialized monthly `.smr` by atomic replacement under the storage lock before publishing the new projection and releasing the lease. The target's `baseSchedulingEventId` remains the state observed when its action began. UI code must never write `.sme`, `.smr`, `sort.json`, or `memo.db` directly.

Before writing, the store serializes and validates the complete intended result for every affected authority file. Destructive operations then call the narrow host `HistorySnapshotWriter` once with all existing affected `.sme`, `.smr`, `sort.json`, configuration, or root-directory paths. Its production Adapter reuses SiYuan's workspace history root and timestamped operation layout; its deterministic test Adapter writes under a temporary workspace. Snapshot failure aborts before the first authority write. This is a real SiYuan host seam, not a public Engine family or a generic history service.

Within one `.sme`, a move changes one in-memory tree and writes one root file. If the move enters, leaves, or reorders a mixed root-boundary sibling set, the same logical transaction also updates `sort.json`. A cross-root move removes the subtree from the source tree, inserts it into the target tree, preserves every moved Element ID, and runs under the serialized Element storage queue. Like SiYuan cross-document transactions, every authority file is replaced safely on its own but the group is not physically atomic.

After the complete pre-image snapshot succeeds, cross-root move, promotion, and demotion write additive/destination authority first, destructive/source authority second, affected `sort.json` last, and `memo.db` only after all source writes succeed. For an internal subtree move `A.sme -> B.sme`, the fixed order is `B.sme`, `A.sme`, `sort.json`, then projection publication. The operation holds the Element storage queue and Scheduler write lease in the implementation's fixed local order. A mid-write failure can therefore leave a detectable duplicate rather than remove the only current content. The implementation never chooses source-first ordering merely to reduce temporary duplication.

If an authority write fails after any earlier authority write completed, the command returns stable `element-write-partial`; it does not attempt an automatic inverse write from history. The Runtime stops serving the prior projection, waits for the triggering lease to release, and performs one host-owned rebuild from the actual files. A successful rebuild may publish isolated duplicate/incomplete diagnostics; a failed rebuild retains `projection-rebuild-failed`. No request sees the old in-memory tree as if the complete command succeeded.

A root-document move relocates its `.sme` and descendant directory together, then updates `sort.json`. Moving a child root outside a parent subtree before deleting the parent makes the moved subtree independent; it must not be deleted with the former parent.

## Delete Semantics

Deleting an internal Element removes its entire internal subtree from the owning `.sme`. Deleting a root `.sme` also removes its `<root-id>/` child root-document directory recursively, matching SiYuan document-tree deletion semantics.

Deletion performs these steps:

1. Serialize and validate all intended results, then snapshot every existing affected `.sme`, `.smr`, `sort.json`, and relevant root directory through `HistorySnapshotWriter`.
2. Remove or rewrite the authoritative internal subtree/root file tree.
3. Append the immutable deletion event containing the deleted IDs and minimal reference metadata.
4. Remove obsolete sort entries.
5. Rebuild or incrementally update projection rows only after all authority writes succeed.

SiYuanMemo does not create a separate tombstone file per Element. The immutable deletion event is the synchronized deletion record, while local history is the recovery source. References to deleted IDs render a recoverable missing/deleted state from event and history metadata.

## Assets And Formulas

SiYuanMemo reuses SiYuan's global `workspace/data/assets/` directory. It does not maintain a parallel content-addressed asset tree.

`.sme` Topic and Item payloads store asset references using SiYuan-compatible paths. Import, extraction, paste, block snapshot, export, asset indexing, unused-asset detection, and cleanup must be extended to scan `.sme` references as well as `.sy` references. An asset cannot be considered unused until both stores have been scanned.

Formula source remains editable in `.sme` payloads as normalized TeX, MathML, or compatible math nodes. Rendered images or HTML are caches or fallbacks, not the only formula source.

## Rebuildable Index

`workspace/temp/siyuanmemo/memo.db` is a disposable materialized index. It stores Element-to-root/path lookup, derived parent/root relationships, full-text search, explicit references and backlinks, current schedule state, queue eligibility, priority position, summary statistics, and derived adoption classification for ingested scheduler events.

Startup indexing scans `.sme`, complete `.smr` history, `sort.json`, and effective scheduler configuration. A missing, corrupt, or schema-incompatible `memo.db` triggers a rebuild from these synchronized sources. Engine/index opening itself never persists missing scheduler defaults; that responsibility belongs only to the earlier writable host bootstrap.

Projection replacement follows SiYuan's index lifecycle and is visible only after complete success. CGO commits the complete replacement in one SQLite transaction. Non-CGO builds a separate complete next value, atomically writes it, and only then swaps the in-memory snapshot. If a scheduling-changing command committed its authoritative event before projection publication failed, that triggering command returns `projection-refresh-failed`, `reviewAccepted = true`, and the event ID. The Engine then returns stable `projection-rebuild-failed` for subsequent queries and learning actions until a complete rebuild succeeds. It does not serve the previous, empty, or partial projection, and SiYuanMemo adds no stale-projection mode, fallback database, or freshness UI.

Following SiYuan's model-level full-reindex ownership, `symemoRuntime` owns projection bootstrap, the current Engine handle, failure latching, and internal close/recreate rebuild. Its state is only `uninitialized | available | unavailable` in memory. Ordinary requests in `unavailable` return `projection-rebuild-failed` without reopening; internal rebuild publishes one replacement Engine only after complete construction, and workspace restart resets the Runtime. Projection refresh remains package-internal, with no sixth Engine family, public rebuild route, or MVP UI.

API and other host callers execute exactly one Engine operation through a bounded Runtime lease instead of retaining `*Engine`. Close/rebuild marks the Runtime draining and unavailable, rejects new leases, waits for active leases to reach zero, and only then closes the old Engine. The triggering accepted-event call returns its `projection-refresh-failed` facts before releasing; rebuild cannot pass the drain barrier until that response path has finished. Schedule-changing operations additionally serialize behind the Engine-internal Scheduler write lease; this is not a second host lifecycle mechanism. Both lease states are local process memory and never participate in sync.

Isolated source failures remain different from a rebuild failure. A malformed, unreadable, duplicate, or unsupported source that can be excluded safely is represented by a deterministic diagnostic in the complete projection while unrelated valid Elements remain queryable. Failure to construct or publish that complete result makes the whole Engine unavailable. All authoritative `.sme`, `.smr`, sort, scheduler, asset, and `.sy` bytes remain unchanged by either outcome.

A missing or corrupt `.smr` partition means current scheduling state cannot be proven from complete authority. Feature 003 fails closed for affected scheduling work and requires explicit recovery of the raw file; it does not invent a continuation baseline. Content, tree, relation, backlink, and other schedule-independent reads remain available where the failure can be isolated safely.

The following repository-sync hook and type-aware recovery contract is retained for a later sync-integration feature. Feature 003 may keep its storage compatible with this contract but does not implement the hook, `SyncRecovery`, or post-sync Runtime replacement.

Before a full repository sync can replace synchronized SiYuanMemo files, the host places `symemoRuntime` behind one sync gate: it rejects new Engine leases, drains in-flight operations, and keeps the Runtime unavailable. The Engine-internal Scheduler lease alone is insufficient because DejaVu synchronization is outside that Module. This ordering gives a deterministic local result when a grade overlaps sync without claiming any cross-device transaction.

After DejaVu completes merge and native history generation, recovery discovery runs inside `processSyncMergeResult` before its no-upsert/no-remove/no-conflict early return. It unions relevant paths from `mergeResult.Upserts`, `mergeResult.Removes`, and `mergeResult.Conflicts` with files physically present under the exact `workspace/history/<mergeResult.Time>-sync/storage/siyuanmemo/` directory. This history scan is mandatory: DejaVu can create a losing-file snapshot while omitting `MergeResult.Conflicts`, including when the cloud object already exists in the local object store. Relevant discovery sets `needReloadSymemo` even when the three result lists are otherwise empty.

The current fork clears synchronized-storage state and then calls `incReindex(upserts, removes)`, whose remove-before-upsert order repairs native block-tree locations for moved or deleted `.sy` files. Only after that native incremental reindex completes should one deep internal `SyncRecovery` Module perform deterministic type-aware repair, after which `symemoRuntime` closes/recreates the Engine and completely rebuilds `memo.db`. The gate reopens only after valid scheduler authority and one complete projection are published. This ordering prevents Block-backed projections from observing pre-reindex native locations and prevents any query or schedule-changing write from observing partially recovered authority. The later sync feature needs no incremental threshold and preserves one all-or-nothing projection publication path.

The sync gate, discovery, recovery, and rebuild hooks are host maintenance, not a sixth Engine family or frontend command. Normal facade calls still honor `waitForSyncingStorages` before leases, but that wait is not cited as the pre-replacement ordering guarantee. If Runtime is still `uninitialized` during boot sync, recovery may repair authority without opening an Engine and the waiting `InitSymemo` later initializes from the merged files. If Runtime was open, the host attempts one replacement. Recovery or rebuild failure is logged and leaves Runtime unavailable without retroactively changing SiYuan's completed repository sync result.

No queue or backlink query may depend on state that exists only in `memo.db` and cannot be reconstructed.

## Local Learning Session Recovery

Review results and scheduling transitions are accepted only after their immutable `.smr` events are written. Normal UI advancement waits for projection publication; a later publication failure reports the accepted event ID so the action is not submitted again. An activity session never owns completed review state.

The default due `IncrementalLearningQueue` is rebuilt from `.sme`, complete `.smr` history, scheduler configuration, and same-learning-day processing state. It does not persist or synchronize an exact queue cursor.

Filtered branches, temporary practice, and other scoped queues may store one lightweight `workspace/temp/siyuanmemo/active-session.json` snapshot for same-device restart recovery. Its versioned shape contains only the session ID, queue type, scope or filter definition, remaining target IDs, current target ID, and timestamps. Recovery reconciles those IDs against current authoritative state and drops targets that are completed, deleted, dismissed, or ineligible.

The snapshot is disposable, is not source data, and does not participate in SiYuan sync. Clearing `workspace/temp/` may remove the resume prompt but cannot lose a completed grade or change a schedule. MVP has no active-session revision counter, cross-device merge, or active-session conflict dialog. The `sessionId` retained in `.smr` events is audit and statistics metadata, not a synchronized queue cursor.

## Sync Conflict Policy

This section defines later sync-integration behavior, not Feature 003 implementation scope.

Sync uses SiYuan's existing data repository and file-level conflict detection. DejaVu selects complete files by path; it does not merge JSON fields, MsgPack records, scheduler settings, or `.smr` events. Ordinary bidirectional same-path conflicts generally retain the selected current file and place the other version in native `-sync` history, subject to DejaVu's modification-time heuristic; download-only sync instead restores the cloud file and preserves the local version in history. SiYuanMemo then adds the following idempotent type-aware domain recovery after native selection:

- `.sme`: keep the DejaVu-selected version at the original path and never structurally merge mutable HTML trees. SiYuan's current global `Conf.Sync.GenerateConflictDoc` value controls whether recovery also materializes the losing valid history version as a visible conflict root. When enabled, regenerate IDs and internal references consistently, attach conflict provenance, and use a stable key containing original path, losing content digest, and merge time so retries do not duplicate the root. When disabled, perform no `.sme` authority write for the losing tree: native `-sync` history remains recoverable and `SyncRecovery` returns a typed non-blocking local diagnostic. The setting is sampled for that recovery; enabling it later does not retroactively scan old history to create conflict roots. This mirrors SiYuan's visible-conflict-document choice, although applying it to `.sme` is SiYuanMemo behavior because the native implementation handles only `.sy`.
- `.smr`: parse current and native-history versions and merge by immutable `eventId` set union, then sort deterministically for serialization. Canonically identical same-ID records collapse as duplicates. Different IDs with the same `baseSchedulingEventId` remain legal concurrent siblings and are projected through the adopted-chain rule. If the same `eventId` has different payloads, preserve both raw inputs in local conflict history, exclude that identity from adoption, and close the smallest provable scheduling scope pending repair. If complete raw history cannot be recovered, affected scheduling fails closed.
- `sort.json`: merge known IDs where possible. Its ranks cover root documents and internal children participating in mixed sibling sets. Loss or corruption affects order only; scanning `.sme` files and their internal arrays reconstructs existence and a deterministic fallback order, and missing or duplicate ranks are normalized after recovery.
- other mutable collection settings: accept DejaVu's selected complete file, retain losing history, validate it as one value, and never synthesize a review or grade.

DejaVu creates native losing-file snapshots at `workspace/history/<timestamp>-sync/<original-relative-path>` as part of its file-level merge. A snapshot may exist even when `MergeResult.Conflicts` is empty, so `SyncRecovery` consumes the physical `storage/siyuanmemo/...` subtree selected by `mergeResult.Time` instead of relying on that list or creating a parallel conflict directory. The semantic rules above are SiYuanMemo extensions layered after native file selection; they must not be described as DejaVu behavior. They do not use device-specific files or assume that a SiYuan device ID is permanent.

## History And Recovery

SiYuanMemo history follows SiYuan's existing snapshot layout at `workspace/history/<timestamp>-<operation>/storage/siyuanmemo/...` and is not synchronized. `HistorySnapshotWriter.Snapshot(operation, relativePaths)` creates one native history entry for all existing pre-images of a logical update, move, promotion, demotion, delete, or repair operation, preserving paths relative to `data/`. Production operation names reuse SiYuan's `update`, `delete`, or `replace` categories. Placement under the native root makes these entries participate in SiYuan's directory-based retention and `ClearWorkspaceHistory` lifecycle without a second history root. Current `indexHistoryDir` indexes `.sy`, assets, and attribute-view files only; MVP must not misclassify `.sme` as a document or extend that disposable database merely to claim registration. A later visible Element-history browser may add a dedicated history type, while recovery continues to inspect authoritative snapshot files directly. Sync conflicts use snapshots already generated by DejaVu's `genSyncHistory` and are not copied into a second SiYuanMemo-only history root.

The Adapter validates that every requested source lies under SiYuanMemo authority, copies directories recursively through SiYuan's locked file helpers, and returns an error if any pre-image cannot be captured. The Element storage implementation owns which paths belong to the transaction and the destination-first write plan; the Adapter owns only native history placement and snapshot completion. No UI caller writes history directly.

Startup and post-sync recovery never replay history, complete a move, or roll back files by guessing user intent. They validate the actual authority currently on disk and publish deterministic diagnostics for duplicate IDs, missing roots, incomplete event/source combinations, and stale sort entries. A later explicit repair workflow may inspect or restore a selected native history entry. SiYuanMemo adds no WAL, two-phase commit, per-operation journal, automatic startup rollback, or separate history root.

`SyncRecovery` is one concrete deep internal Module at the model/SiYuanMemo seam. Its narrow operation accepts the completed merge time, discovered relevant paths, and the host-resolved `GenerateConflictDoc` value, then returns one recovery outcome with typed diagnostics and domain availability. Its implementation alone scans the exact native history directory, performs `.smr` union, conditionally creates idempotent `.sme` conflict roots, normalizes `sort.json`, validates fixed scheduler configuration, and applies locked canonical replacements. Its outcome closes only the smallest scheduling scope that cannot be proven from complete authority. These helpers are not public Engine operations, caller workflows, or Adapter seams.

On startup or after sync, validation checks at least:

- file basename and root ID consistency;
- unique Element IDs across loaded roots;
- valid root and payload specs;
- valid child arrays and no internal cycles;
- valid `.smr` envelopes, immutable event identity, and same-ID payload equality;
- valid scheduling bases, same-Element causal links, acyclic branches, and compatible parent/child transitions;
- references to missing Elements;
- missing root-document ancestor `.sme` files;
- stale `sort.json` entries;
- invalid Block-backed node IDs and encrypted Block-backed material, retained as visible Elements with blocked-material diagnostics;
- missing or corrupt `.smr` partitions required to rebuild current scheduling state.

Unsupported future specs open read-only. Invalid or uncertain existing files are not automatically overwritten during indexing. Conclusively absent root-document parents are the narrow exception: the read-only phase diagnoses them, and the later writable workspace load/repair phase creates empty `Untitled` Topic roots parent-first before rebuilding the projection. Other recovery operates from history or an explicit repair action.

## Encrypted Notebook Exclusion

SiYuanMemo does not support operations across encrypted notebook content. Commands such as block-to-Topic, block-to-Item, `SendToNote`, backlink indexing, or asset promotion must reject an encrypted notebook source or target before entering normal material resolution or writing SiYuanMemo data. A pre-existing reference resolved to an encrypted source remains a successful Element tree/detail result with the stable `encrypted-source-unsupported` material diagnostic, no material tri-state/content, and no learning-queue eligibility. It is not reported as transient `unresolved`, and material-dependent actions fail without changing schedule or source data.

The failure mode is explicit and closed: no decryption, encrypted index, key-management UI, plaintext fallback, background extraction, or partial write before the rejection.

## File Count Consequence

File growth is bounded primarily by root Element documents, months of review history, scheduler configuration, and shared assets. Extracted Topics and Items nested inside an existing root do not create extra filesystem files. Users can promote a large internal subtree to a new root `.sme` when independent document boundaries, smaller rewrites, or separate root organization are useful.

This deliberately accepts whole-root rewrites in exchange for far fewer files, coherent subtree recovery, and behavior aligned with SiYuan `.sy` documents.

## Adopted SiYuan Patterns

The implementation should reuse or closely follow these existing repository patterns:

- `.sy` serialization, validation, unchanged-write avoidance, and locked writes in `kernel/filesys/tree.go`.
- temporary block-to-root/path indexing in `kernel/treenode/blocktree.go`.
- writing both source and target roots during cross-document moves in `kernel/model/transaction.go`.
- heading-to-document ID preservation and source unlinking in `kernel/model/heading.go`.
- root document ordering through `.siyuan/sort.json` in `kernel/model/file.go`.
- startup full indexing in `kernel/model/index.go`.
- sync remove-before-upsert incremental indexing in `kernel/model/sync.go`.
- conflict-document preservation, DejaVu sync-history generation, and the post-merge `incReindex` ordering in `kernel/model/repository.go`.
- local history snapshots under the workspace history directory in `kernel/model/history.go` and `kernel/model/file.go`.

## Invariants

1. Files and immutable events are synchronized source data; `memo.db` is disposable.
2. One root Element document owns one `.sme`; internal Elements do not own files.
3. Root document hierarchy and internal Element hierarchy are separate storage trees projected into one user-facing Elements tree.
4. Promotion leaves no mount placeholder.
5. Element IDs survive moves and promotion; conflict copies receive new IDs.
6. Scheduling and review history is authoritative in complete monthly `.smr` events.
7. Persistent event partitioning never depends on device ID.
8. Assets live in SiYuan's global asset store and are considered referenced by both `.sy` and `.sme`.
9. Internal children and child root documents can be freely interleaved through shared `sort.json` ranks without changing structural ownership.
10. Temp indexes and local history do not sync.
11. Encrypted notebook integration fails closed.
12. Every Element has at most one derived adopted scheduling chain; concurrent history is retained but never treated as sequential review input.
13. Missing root-document ancestors are excluded and diagnosed by read-only projection, then auto-completed parent-first as real empty `Untitled` Topic roots by the writable workspace lifecycle; uncertain or existing invalid sources are never overwritten by this rule.
14. Scheduling adoption is deterministic for an event set regardless of file arrival or iteration order, and no derived adoption label is written into `.smr`.
15. A disposable projection is served only after complete publication; rebuild failure blocks Engine queries and learning actions instead of exposing an old or partial projection.
16. Block-backed tree queries use one live batch block-tree lookup; only opening or reviewing one target uses SiYuan's native reindex-aware load, and no SiYuanMemo projection rebuild scans the filesystem once per missing reference.
17. Invalid and encrypted Block-backed material blocks material-dependent capabilities, not Element discovery; the Element remains visible with one stable diagnostic and no material tri-state value.
18. Destructive multi-file operations snapshot every existing pre-image before writing, copy destination content before removing the source, write sort before projection, and recover partial failure through Runtime rebuild plus explicit native history repair rather than automatic rollback or a private transaction journal.
19. An accepted scheduling event is published before `memo.db`; a later projection failure cannot make the grade retryable.
20. Feature 003 retains `.smr` payloads indefinitely and performs no automatic history compaction.
