# Learning Engine Design

Date: 2026-07-19

## Decision

SiYuanMemo adds an independent progressive reading and spaced repetition system inside the SiYuan fork. The system is centered on Element objects and keeps Topic material separate from SiYuan note blocks.

The Learning Engine is a deep module. Its external interface is based on user actions, not CRUD over storage records. The implementation hides file layout, HTML cleaning, annotation storage, scheduling state, indexes, history, and SiYuan note insertion details behind a small action interface.

## Core Concepts

Element is the core learning object. The first supported Element types are:

- `topic`: a progressive reading material unit.
- `item`: a memory/review object.
- `concept`: an organizing node that provides default learning parameters.

Concepts are Elements, but they are not scheduled by default. A Concept can contain child Topics, Items, and Concepts. Effective defaults resolve in this order: the Element's explicit override, its primary `boundTo` Concept context, its nearest structural Concept ancestor, then collection defaults. Explicit Element links and backlinks never participate in inheritance.

When `boundTo` targets a Concept, that Concept is the primary context. When it targets another Element, resolution uses that target Element's effective Concept, but never inherits the target Element's own per-Element overrides. Resolution tracks visited Element IDs; a missing target or binding cycle is ignored and falls back to the nearest structural Concept. Effective defaults are derived rather than copied and may be materialized in `memo.db` for lookup.

Items are Elements too. They are not SiYuan blocks and they should not be created by directly writing files from the UI. The Learning Engine owns Item creation from selected Topic material, manual Q/A input, future cloze workflows, source range tracking, scheduling initialization, indexes, and event history.

SiYuanMemo keeps separate learning and note domains:

- SiYuanMemo Element domain: material, learning, scheduling, Concepts, Topics, and Items, stored through the dual-tree model defined below.
- SiYuan block tree: user-authored notes, documents, block references, backlinks.

Topics are not SiYuan blocks. Notes are SiYuan blocks. Topic material enters the note system only through explicit user actions.

## Storage Model

The authoritative storage design is `0008-element-storage-sync-recovery-design.md`. The Learning Engine follows SiYuan's file-first, dual-tree model instead of storing one directory or several files per Element.

One root Element document is one `.sme` JSON file. Topic, Item, Concept, and future Element nodes use one versioned envelope and nested `children`; their type-specific HTML, prompt/answer, annotations, read point, source provenance, relations, and asset references live in the node payload. Internal nodes derive parent, root, and path while loading instead of persisting redundant IDs.

Root `.sme` files form a second tree through ID-named directories plus `elements/.siyuan/sort.json`. Promoting an internal Element to a root file preserves its ID, removes it from the source, and leaves no mount placeholder. The storage trees remain distinct, but their children share one custom-order coordinate at root-document boundaries: `sort.json` assigns ranks to both direct internal children and child root documents so the unified Elements tree can freely interleave them.

Monthly `reviews/YYYY-MM.smr` JSON files contain immutable scheduling, review, lifecycle, move, and deletion events. They are partitioned by month, never by device ID. The event set is authoritative for learning history; for each Element, one causally valid adopted scheduling chain is authoritative for current scheduler derivation.

`workspace/temp/siyuanmemo/memo.db` materializes Element lookup, derived parent/root/path, full text, explicit references, backlinks, current scheduling state, and queues. It is excluded from sync and rebuildable from `.sme`, `.smr`, sort, and scheduler configuration files.

SiYuanMemo reuses `workspace/data/assets/`. Asset indexing and unused-asset cleanup must scan both `.sy` and `.sme`. Local Element history follows SiYuan's snapshot layout under `workspace/history/<timestamp>-<operation>/storage/siyuanmemo/` and is excluded from sync.

SiYuan encrypted notebooks are outside the MVP. Any command that would persist decrypted encrypted-notebook content in the plaintext Element store must fail before writing.

## Element Lifecycle And Processing

Element workflow is split into three independent state dimensions. They must not be collapsed into one `status` field:

- `lifecycleState`: `pending | memorized | dismissed`. This controls participation in learning queues.
- `processingState`: `new | reading | processed`. This describes material-processing progress and does not control scheduling.
- scheduler adapter state: versioned private state such as FSRS `new | learning | review | relearning`, due time, difficulty, stability, repetitions, and lapses.

`processingState` is persisted with the Element in its owning `.sme`. It is primarily meaningful for Topic-like material; unsupported Element types retain it without making it an algorithm input. Lifecycle changes and scheduler state are authoritative in monthly `.smr` events and materialized into `memo.db`.

The pending queue is the order in which new Elements are introduced. `RememberElement` introduces a pending or dismissed Element into learning and removes it from the pending queue. Remembering a never-scheduled or explicitly forgotten pending Item initializes its FSRS adapter state as `new`. Remembering a dismissed Item preserves its previous adapter state and review history; if its preserved due time has passed, it becomes due immediately without invoking FSRS until the user actually grades it.

`ForgetElement` removes a memorized Element from learning, clears its current adopted and adapter scheduling states, and puts it at the end of the pending queue. Historical review events remain immutable for audit and future model training, but the next introduction starts a new active scheduling state. `DismissElement` removes an Element from active learning and pending introduction while keeping it browsable in its original tree location; it preserves due time, adapter state, and history. None of these lifecycle commands changes `processingState`.

`ImportTopic`, `ExtractTopic`, `CreateTopicFromBlock`, `CreateItem`, `CreateClozeItem`, `CreateItemFromBlock`, `CreateClozeItemFromBlock`, and `AddNewTopic` are deliberate learning actions, so their created Elements default to `memorized` and due now unless the user explicitly chooses to create pending material later. Bulk import, reset, and `ForgetElement` may create or restore pending Elements.

## DeleteElement And History

Deleting an internal Element removes its internal subtree from the owning `.sme`. Deleting a root Element removes its `.sme` and descendant root-document directory recursively. The operation first writes local history, then appends an immutable deletion event, updates files and sorting, and removes index rows.

SiYuanMemo does not create one tombstone file per Element. The synchronized deletion event retains the minimal identity needed for missing-reference rendering and index rebuild; local history retains recoverable content.

## Media and Formula Storage

Images, screenshots, and other binary media are file-first data, not SQLite blobs. Import and paste workflows store them through SiYuan's existing `workspace/data/assets/` machinery. Topic and Item payloads keep SiYuan-compatible asset references.

Unused-asset detection must scan `.sme` payloads in addition to `.sy` documents before deleting an asset.

Formula content should be preserved as editable source, not only rendered pixels. Cleaned HTML may contain MathML, TeX, or SiYuan-compatible math spans. During import, extraction, and Item creation, the sanitizer should normalize formulas into stable math nodes that preserve:

- original source text;
- format, such as `tex`, `mathml`, or `asciimath`;
- display mode, such as inline or block;
- optional rendered fallback for previews.

The Topic Reader may render formulas through SiYuan's existing KaTeX-compatible rendering path, but storage must keep the formula source so formula cards remain editable and searchable.

This avoids opaque binary state for core data while preserving an independent Element model. It also avoids putting Topic material into SiYuan `.sy` documents.

## Scheduling Model

The Learning Engine owns an independent Scheduler. It does not use SiYuan's existing `riff` deck/card/block scheduling model as its foundation.

Scheduler design follows the baseline in `docs/siyuanmemo/0003-spaced-repetition-scheduler-core.md`. Scheduling is treated as a layered system with ReviewTarget queues, Topic reading queues, Item memory state, priority propagation, postpone policy, review history, and versioned algorithm adapters.

The MVP Scheduler is intentionally minimal. Its job is to make progressive reading queues work, not to reproduce every advanced scheduling behavior. It must support:

- due time for schedulable Elements;
- priority-position-based ordering;
- pending, memorized, and dismissed lifecycle states;
- reviewing or processing a ReviewTarget;
- postponing an Element;
- remembering, forgetting, dismissing, and completing an Element;
- subset learning modes for due-only learning, all-Element review, and topic-only review;
- one active learning session per running workspace, with local-only recovery for interrupted scoped queues;
- user-configurable pending introduction limits for daily and session learning;
- same-day review skipping for normal queues, with explicit override commands for deliberate extra review;
- pluggable algorithm adapters behind the Scheduler Engine, selected by schedule profile and target kind;
- profile-specific arena scoring so compatible enabled algorithms can compare predictions on the same adopted review chain;
- queue lookup for due Topic and Item targets;
- monthly event records for scheduling actions.

Even the minimal Scheduler stores DSR-compatible fields where applicable: difficulty, stability, retrievability, forgetting index, interval, repetition count, lapse count, raw grade `0..5`, derived pass/fail, last review time, due time, priority position, algorithm name, active algorithm name, arena decision metadata, and algorithm-specific state. These fields let `simple-v1` and `fsrs-v1` evolve toward `dsr-v1` and later `adaptive-v1` without changing the Engine interface.

The first Scheduler implementation uses simple rules around the pluggable adapter seam:

- a new imported Topic is due immediately;
- an extracted child Topic is due immediately or soon;
- a newly added Topic is due immediately unless created by a future explicit pending-only command;
- Topic interval growth defaults to `topicAFactor = 2.5` and only runs from Topic `Next` inside an active learning session;
- `PostponeElement` moves `dueAt` by the requested duration;
- `DismissElement` removes the Element from active queues until the user explicitly remembers it again;
- `GradeItem` records raw grade `0..5`, rating label, mapping version, `passed = grade >= 3`, lapse/final-drill flags, before/after scheduler state, calls compatible Item adapters, records candidate schedules, and adopts the schedule chosen by the Item arena policy;
- `NextTopic` records an ungraded Topic transition only inside an active learning session and runs `topic-afactor-v1`; it never supplies a synthetic Item grade or enters the Item memory arena;
- `fsrs-v1` drives Item scheduling from the first implementation; `simple-v1` remains deterministic fallback and shadow comparator;
- `topic-afactor-v1` drives HTML-backed Topics, Block-backed Topics, and explicitly enrolled Concepts; Concepts otherwise use schedule profile `none`;
- queue order uses due time first and priority position as the main tie-breaker. Priority position follows SuperMemo semantics: `0%` is highest priority and larger values are lower priority.

The Scheduler is a replaceable internal module behind the Engine. Future algorithms can plug in through adapters without changing Topic Reader, API handlers, storage callers, or SiYuan note integration. Queue selection, adapter execution, arena decision, memory update, priority propagation, postpone, scheduling-ledger commit, and optimizer state are separate internal policies.

## Scheduling Ledger

The Learning Engine contains a deep internal `SchedulingLedger` module. It owns atomic `.smr` replacement under the storage lock, event de-duplication, causal merge, valid-branch selection, current scheduling projection into `memo.db`, and full projection rebuild. UI callers and algorithm adapters never read or write `baseSchedulingEventId`, choose conflict branches, or assemble event history themselves.

Every scheduling-changing event, including Topic `Next`, Item grade, Postpone, Reschedule, Remember, Forget, Dismiss, and equivalent future actions, records `baseSchedulingEventId`. The value is the event ID of the Element's adopted scheduling terminal observed when the action began, or `null` when no prior scheduling event exists. The action flow is:

1. The Scheduler reads the current adopted state from `SchedulingLedger`.
2. Queue, Topic, lifecycle, and algorithm policies compute one proposed transition against that state.
3. `SchedulingLedger` assigns an immutable event ID, records the observed base, writes the event to the monthly `.smr`, and only then updates the materialized projection.
4. The UI advances only after the commit succeeds.

After sync, the ledger unions events by `eventId` and builds a causal graph per Element. Different event IDs with the same base are legal concurrent siblings. Among complete valid branches, the terminal with the greatest `(occurredAt, eventId)` tuple wins deterministically; that terminal and all of its ancestors form the adopted chain. Only this adopted chain drives current scheduling state, FSRS or other algorithm Adapter state, arena training, and queues. Other valid branches remain immutable audit history but are marked `concurrent-superseded` only in rebuildable `memo.db` projection data.

The same event ID with the same payload is a duplicate. The same event ID with different payloads, malformed bases, missing bases, cycles, and incompatible state transitions require repair and cannot become adopted. `adopted`, `concurrent-superseded`, `invalid`, and `duplicate` are derived ledger classifications and must never be written back into immutable `.smr` events.

This concurrency rule has one implementation in the MVP. It is not an algorithm Adapter and does not justify a public `ConflictStrategy` seam. Active-session recovery remains a separate local concern: `active-session.json` may restore a scoped queue cursor, while `SchedulingLedger` alone decides synchronized scheduling truth.

## Deep Module Interface

The authoritative Interface is defined in `0010-deep-module-interface-design.md`. One concrete Engine exposes five operation families: `CreateElement`, `ChangeElement`, `RunLearningAction`, `SendToNote`, and `Query`. Named actions such as Import Topic, Extract Topic, Grade Item, or Get Element Backlinks are closed variants and stable HTTP route names, not separate internal Modules.

The family shape does not make commands generic CRUD. Each variant still expresses one user intent and owns its complete transaction. API handlers and frontend clients must not decompose a family command into direct storage, Scheduler, Session, Ledger, or index calls.

Callers must not assemble business workflows by directly creating and mutating Element records. For example, `ExtractTopic` is responsible for creating the child Element, copying the selected HTML fragment, updating the parent annotation, inheriting Concept defaults, updating indexes, and appending a monthly event.

`CreateItem` and `CreateClozeItem` are responsible for creating the Item Element, storing prompt/answer material, linking the Item to its source Topic and source range, inheriting Concept defaults, initializing scheduler state, updating indexes, and appending a monthly event. Generated Items are children of the source Topic by default. MVP Item creation includes manual Q/A and a single-blank cloze. Multi-cloze and image occlusion are later features.

`AddNewTopic` creates a new Topic with at most one primary `boundTo` relation. The target may be the current Concept or any current Element when the workflow is intentionally anchored to it. This binding is contextual metadata, not structural parentage, not a mount, and not an Element backlink. Rebinding replaces the previous `boundTo` relation instead of accumulating multiple primary contexts.

Additional associations are authored as explicit Element links. Those links participate in normal Element backlinks with source context; they must not be encoded as additional `boundTo` values. `AddNewTopic` does not create an Item, Concept, or note block. More specialized creation stays in explicit commands such as `CreateItem`, `CreateClozeItem`, `CreateConcept`, or future import actions.

`CreateTopicFromBlock` creates a Block-backed Topic Element from one explicit SiYuan block. A source block with no back-side answer is a Topic, not an Item. The command stores canonical `material.kind = siyuanBlock`, the stable block ID, optional source-notebook provenance, Topic metadata, scheduler initialization, temp index rows, and an event. It does not copy the live block body into HTML or rewrite the original `.sy` block. MVP range snapshots are reserved for explicit Item Q/A or cloze commands; a future range-backed Topic requires its own versioned live-range contract rather than silently changing this whole-block reference.

`CreateItemFromBlock` and `CreateClozeItemFromBlock` create Item Elements only when the user explicitly supplies a prompt/answer split or cloze blank. They store snapshot prompt/answer HTML plus `source = note.block:<blockId> + sourceRange`, and the original block remains the editable note source. Refreshing a note-derived Element from the live block is a later explicit command, not automatic synchronization.

The Engine uses `ReviewTarget` internally so queues and renderers are not hard-coded to Elements only. MVP targets are `element.topic`, `element.item`, `element.concept`, and `note.block`; future targets may include `media.audio`, `media.video`, and other progressive learning objects. Callers submit learning variants through `RunLearningAction`; `ReviewTarget` remains an internal Scheduler and Session DTO rather than another public review command.

The intended call path is:

```text
app/src/symemo/TopicReader
  -> /api/symemo/extractTopic
    -> kernel/api/symemo.go
      -> model/symemo.Engine.CreateElement(ExtractTopicCommand)
        -> root Element tree store / temp index / monthly event store
```

Topic HTML editing uses the same pattern:

```text
app/src/symemo/TopicHtmlEditorAdapter
  -> /api/symemo/saveTopicHtml
    -> kernel/api/symemo.go
      -> model/symemo.Engine.ChangeElement(SaveTopicHTMLCommand)
        -> sanitize / normalize / stable node IDs / Topic payload / annotation remap / temp index / monthly event store
```

## Topic Reader

Topic material uses a dedicated Topic Reader and HTML material editor. It does not use Protyle.

Topic navigation, Element Browser views, and embedded learning controls are defined in `docs/siyuanmemo/0005-topic-ui-integration-design.md`. The short version is: Topics appear in a native left dock tree, Elements open in main editor-area tabs, Element subsets open in a table-like browser tab, and review starts from the `Learn` button inside an opened Element.

The Topic Reader supports:

- rendering cleaned HTML material;
- preserving headings, paragraphs, lists, tables, links, images, code, and important inline marks;
- tracking scroll and reading position;
- setting, jumping to, and clearing a stable read point;
- selecting ranges;
- extracting child Topics;
- splitting a Topic into child Topics;
- creating Items from selected material;
- sending a Topic or selected range to SiYuan notes;
- adjusting priority position, status, and Concept assignment.

The MVP uses TinyMCE as the HTML material editor Adapter. TinyMCE is an implementation detail of the Topic Reader surface, not the Learning Engine interface. The Learning Engine still owns saving, sanitizing, normalization, stable node IDs, annotation remapping, temp indexing, and event logging.

The Topic Reader has two modes:

- reading mode: optimized for selection, extraction, cloze/item creation, splitting, highlights, read-point actions, queue actions, and `SendToNote`;
- editing mode: opens the current Topic HTML in TinyMCE for material cleanup and source correction.

Editing mode may modify Topic material, but it must not create note blocks. Protyle remains the editor for user-authored notes.

Editable Topic HTML must contain stable `data-symemo-node-id` attributes on block-level material nodes. Selection ranges and extracted highlights should anchor to these stable IDs plus offsets, not only to fragile DOM paths. `SaveTopicHtml` must preserve or regenerate stable node IDs and then remap or invalidate annotations deliberately.

TinyMCE must be isolated behind `TopicHtmlEditorAdapter` so another editor can replace it without changing storage, scheduler, note integration, or core Topic actions.

## ImportTopic

The MVP supports importing:

- clipboard HTML;
- local HTML files.

It does not initially support URL fetching, PDF, EPUB, or a browser extension.

Import uses medium HTML cleaning. It removes scripts, styles, unsafe attributes, dangerous protocols, and obvious page chrome where possible. It preserves reading structure such as headings, paragraphs, blockquotes, lists, tables, images, links, code, and mathematical markup where possible.

The import action creates a Topic node in the selected root `.sme`, writes cleaned HTML and initial annotations into its payload, stores local media through SiYuan's asset machinery, appends a monthly event, and updates the temp `memo.db` indexes.

## ExtractTopic

`ExtractTopic(parentElementId, selectedRange)` creates a child Topic from a selected HTML range.

The parent Topic keeps its original content. The selected range is marked in the parent Topic payload as an extracted range pointing to the child Element. In the Topic Reader, extracted ranges render as lightweight highlights by default. Hovering or selecting the highlight can show actions such as opening the child Topic.

The child Topic:

- has `type = topic`;
- is nested under the parent Topic unless the command explicitly creates a root Element document;
- gets its title from the selected range's first sentence or leading text;
- stores the selected HTML fragment in its Topic payload;
- inherits Concept defaults;
- enters the learning queue.

After extraction, the UI remains in the parent Topic Reader and shows a toast with an optional action to open the child Topic.

## CreateItem

`CreateItem(parentElementId, prompt, answer, sourceRange)` creates an Item from the current Topic or Element context.

The Item:

- has `type = item`;
- is nested under the source Element by default;
- stores prompt and answer material in its Item payload rather than a separate file;
- records source Element and optional source range metadata;
- may reference images, screenshots, tables, code, and formula nodes through the same media/formula storage rules as Topics;
- inherits Concept defaults;
- enters the learning process according to scheduler policy.

`CreateClozeItem(parentElementId, selectedRange)` creates a cloze-style Item from selected Topic HTML. Cloze is distinct from Q/A: the prompt keeps the surrounding source HTML and replaces the selected range with a blank marker, while the answer stores the removed selection. The first MVP may use one blank marker, but the storage model must leave room for richer prompt/answer rendering, multiple clozes, and future media-backed Items.

A minimal Item payload looks like:

```json
{
  "schemaVersion": 1,
  "kind": "cloze",
  "promptHtml": "The capital of France is <span data-symemo-cloze=\"1\">[...]</span>.",
  "answerHtml": "<span>Paris</span>",
  "clozeHtml": "The capital of France is <span data-symemo-cloze-answer=\"1\">Paris</span>.",
  "clozes": [
    {
      "id": "c1",
      "blankHtml": "<span data-symemo-cloze=\"1\">[...]</span>",
      "answerHtml": "<span>Paris</span>"
    }
  ],
  "assetRefs": [],
  "mathRefs": [],
  "source": {
    "elementId": "20260719010101-abcdefg",
    "rangeId": "20260719010200-aaaaaaa",
    "textQuote": "The capital of France is Paris."
  }
}
```

Creating an Item must not create a SiYuan note block. If the user wants to write their own note about the Item, they use `SendToNote`.

Manual Q/A Items use `kind = "qa"` with explicit `promptHtml` and `answerHtml`. They do not use the cloze replacement model unless the user explicitly creates a cloze Item.

## SendToNote

`SendToNote(elementId, selectedRange, target)` creates a SiYuan note anchor that references any Element type. It does not convert the Element into a note and does not insert the source text as note body by default.

Supported targets:

- a new SiYuan document;
- today's Daily Note.

The action creates an outline-style reference structure:

```text
NodeList
└─ NodeListItem  # @Element title
   └─ empty child block
```

The `NodeListItem` is the Element anchor. It contains a visible `@Element` reference and hidden stable attributes such as:

- `custom-symemo-element-id`
- `custom-symemo-element-type`
- `custom-symemo-relation = note_about`
- optional source range metadata

The empty child block is where the user writes their own note. No template prompt is inserted.

When sending to Daily Note:

- if today's Daily Note already has a `note_about` anchor for the Element, reuse that `NodeListItem`;
- otherwise append a new `NodeListItem` to the last list block if possible;
- otherwise create a new list block at the end of the Daily Note;
- every `SendToNote` call appends a new empty child block under the anchor.

After `SendToNote`, the UI stays in the current Element surface. A toast offers an action to open the target note and focus the newly created empty child block.

## Element References And Backlinks

SiYuanMemo introduces explicit references from SiYuan blocks to Elements:

```text
SiYuan Block --note_about--> Element
```

The user-facing syntax is `@Element title`. Typing `@` in a note should search Elements and insert an Element reference. This is separate from SiYuan's native document and block reference syntax:

- `[[...]]`: SiYuan document/resource-style linking.
- `((...))`: SiYuan block reference.
- `@...`: Element reference.

Element references can point to Topics, Items, Concepts, and later Element types. They are supported in SiYuan Note blocks and in Element content. Topic-to-Topic structure still uses Element parent/child, extract, and split relationships, but explicit Element links inside Element content are backlinks.

The Element Backlinks panel displays explicit references to the current Element from SiYuan Note blocks and from other Elements. It does not show parent/child Topic structure, knowledge-tree paths, sibling relationships, inherited Concept membership, the primary `boundTo` relation, or full-text search hits. Child and bound Elements belong in Element Context, not backlinks, unless their content explicitly links the current Element.

Backlinks should show source type, source title, and a short context snippet around the reference. When the referenced block is a list item, the backlinks panel should show the list item and its child note context. This matches the common SiYuan outline pattern:

```text
- @Element title
  - user's note
  - user's question
```

Clicking an Element reference follows SiYuan-style tab behavior: if a tab for the Element is already open, focus it; otherwise reuse the current reusable/unmodified tab when possible; if no reusable tab exists, create a new Element tab. Dirty Topic HTML editing state, pinned tabs, and non-reusable tabs must not be overwritten.

Hovering an Element reference should show a lightweight read-only popover preview. The MVP preview includes title, type, Concept/path, scheduling status, a short content excerpt, and actions for `Open` and `SendToNote`. The preview does not open TinyMCE, mutate scheduling state, or load a full editor.

Element search in the MVP indexes Element title, cleaned Topic/Concept plain text, Item prompt/answer text, and editable formula source. Image OCR text is out of scope for the MVP.

## SiYuan Integration Seams

The Learning Engine should not directly write `.sy` files or depend on Protyle implementation details. It should depend on narrow adapters for note creation, anchor management, and block snapshot reads.

The first important adapter is `NoteAnchorWriter`. Its implementation uses SiYuan's existing block and document machinery, but the Learning Engine sees only the note-writing interface.

The adapter is responsible for:

- finding or creating a Daily Note Element anchor;
- creating a new document Element anchor;
- appending an empty child block under an anchor;
- writing hidden SiYuanMemo attributes;
- returning anchor and target block IDs.

`BlockSnapshotReader` is the read-side adapter for explicit block-to-Item snapshot commands. It resolves a block or selection and returns sanitized HTML, plain text, title candidates, source range metadata, asset references, and math source. `CreateTopicFromBlock` instead validates and stores a live block identity through the separate `BlockReferenceReader` path; neither adapter infers learning semantics.

The Learning Engine remains responsible for Element state, Element event history, and Element link indexes.

## Non-Goals For MVP

The first version does not include:

- URL fetching;
- browser extension clipping;
- PDF or EPUB import;
- complete original webpage snapshot archiving;
- replacing or enhancing SiYuan's existing `riff` system;
- storing Topics as SiYuan blocks;
- using Protyle for Topic reading or Topic HTML editing;
- full advanced scheduler parity.

SiYuan's existing `riff` module may be studied as an example of how SiYuan wires learning features into kernel routes and frontend UI, but it is not the foundation of the Learning Engine or its Scheduler.
