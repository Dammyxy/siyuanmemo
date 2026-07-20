# Topic Navigation And Element Browser Design

Date: 2026-07-19

## Decision

SiYuanMemo will add Element navigation as a native left dock panel, expose Element subsets as main editor-area browser tabs, and place learning controls directly inside every opened Element.

The MVP UI has these native surfaces plus an embedded learning mode:

- `ElementsDock`: a left dock panel with a knowledge-tree style hierarchy of Concepts, Topics, Items, and later Element types.
- `ElementTab`: a main editor-area tab for reading or reviewing one Element.
- `ElementBrowser`: a main editor-area tab that displays due queues, branch views, search results, priority queues, and other Element subsets in a table-like browser.
- `ElementBacklinksDock`: an independent dock for explicit Element references to the current Element.
- Embedded learning mode: started from the `Learn` button at the bottom of any opened Element.
- Dock controls: left Elements, right Inspector, right Backlinks, and bottom Element Context can be hidden or shown from stable dock buttons.

Topics are still not SiYuan blocks. These UI surfaces are Adapters over the Learning Engine. They must not directly mutate Element files, scheduler state, indexes, or SiYuan note blocks.

## Repository Investigation

SiYuan's desktop UI already has the right seams for this design:

- `app/src/layout/dock/index.ts` owns dock button registration and model creation. Internal dock types are controlled by the `TYPES` list and the `toggleModel` switch. SiYuanMemo should add `elements` as a native internal dock type, not as a plugin-only dock.
- `app/src/constants.ts` defines the default UI layout. The `elements` dock should be added to the left dock near the file tree so it survives layout reset and first-run boot.
- `app/src/layout/util.ts` serializes and restores layout. Dock state is saved through `dockToJSON`; center tabs are restored through `JSONToCenter` and lazy `data-initdata`.
- `app/src/layout/Tab.ts` and `app/src/layout/Model.ts` are the generic tab/model seam. `ElementTab` and `ElementBrowser` should be native `Model` implementations opened in normal center tabs.
- `app/src/editor/util.ts` shows how native center tabs are opened, deduplicated, split right/bottom, titled, and focused. SiYuanMemo should add small open helpers rather than routing Topic tabs through document-opening code.
- `app/src/util/Tree.ts` provides reusable tree interaction behavior and SiYuan-native list styling. `ElementsDock` may reuse it internally, but the Learning Engine should return an Element tree DTO, not `IBlockTree`.
- `app/src/search/index.ts` and `app/src/card/newCardTab.ts` are useful examples of custom tab surfaces, but SiYuanMemo should avoid making the core Element UI depend on plugin `Custom` models.

## SiYuan-Fused Main Menu And Navigation

SiYuanMemo keeps the SuperMemo-style command map, but the default UI must be fused into SiYuan's compact shell instead of adding a separate desktop-style menu strip.

Interface modes:

- `Compact / SiYuan fused` is the default mode.
- `Classic / SuperMemo` is an optional user preference for users who want a visible menu/navigation strip.
- The official switch lives under `SiYuanMemo > Window > Interface mode`.
- The HTML prototype may keep a small `Classic` shortcut only to preview the mode switch.

Main menu integration:

- SiYuanMemo commands are nested under the native workspace menu as `SiYuanMemo >`.
- Under that entry, preserve the command groups `File`, `Edit`, `Search`, `Learn`, `View`, `Toolkit`, `Window`, and `Help`.
- Do not add a second top-level `SiYuanMemo` menu button that competes with SiYuan's workspace name/menu.
- In Compact mode, the main menu is a command model, not a permanent visible row.
- In Classic mode, the same command groups may be shown as a visible SuperMemo-like menu strip.

Navigation integration:

- Compact mode must not add a full-width navigation row above the reader/editor.
- Compact Element navigation belongs in the fused tab/top area as a small cluster: Contents, Back, Forward, Parent, Next, Origin, Concept, and Commander/Nav.
- The navigation cluster should reserve its layout position, but Element-only commands are disabled on Browser, Daily Note, and other non-Element tabs.
- The compact cluster must stay visually small; it must never consume the reader/editor grid row.
- Learn/Add new remain large Element toolbar buttons below the reader/editor and above Element Context.

Icon model:

- Use familiar progressive-learning semantics for Element type, lifecycle, and processing state, but redraw icons in SiYuan-native style instead of copying bitmap assets.
- The active Element tab shows one type icon plus one status badge.
- Types: Concept as a yellow folder/lightbulb-like icon, Topic as a green document icon, Item as a blue recall/card icon, and Task as a reserved grey-blue task icon.
- Status badges: Dismissed yellow pause, Pending grey, Review/Memorized green, Due orange, Leech red, and Neural purple.

## Elements Dock

`ElementsDock` is a native dock `Model` with a compact header and a tree body.

The header should include:

- title: `Elements`;
- import button: imports clipboard/local HTML as a Topic;
- collapse button: collapses the tree;
- more menu: sort, show dismissed Elements, rebuild index, and settings.

The dock is one unified navigation projection over the dual-tree storage model:

- Root Element documents follow the filesystem hierarchy of `.sme` files and ID-named child directories.
- Internal Concepts, Topics, and Items follow the nested `children` hierarchy inside their owning root `.sme`.
- Every internal Element and root document appears in the same `ElementsDock`; extracts and generated Items are not hidden in a separate Outline-only tree.
- The Learning Engine returns storage metadata with each node so the UI can distinguish an internal Element from a root `.sme` without splitting them into separate navigation trees.
- Concepts render as organizing nodes, Topics as material entries, and Items as review entries. Root documents add a small root-document icon or badge.
- The projection must not invent mount placeholders. Internal Elements and child root documents may be freely interleaved under the same root Element. The Learning Engine merges them by shared integer ranks from `sort.json`; drag-and-drop may insert either storage kind at any sibling position.
- New imports without a Concept or parent enter an Inbox/Uncategorized root.
- Dismissed Topics remain in their original tree location. The tree shows a yellow dismissed icon state instead of moving them to an automatic category.
- Count badges may show due descendants or child counts, but they must be computed by the Learning Engine/index.

Tree actions:

- click opens a Topic in `ElementTab`;
- modifier-click should follow SiYuan's existing split conventions where practical;
- context menu supports open, rename, move, set priority, dismiss/remember, create internal child Concept, import internal child Topic, and promote an internal Element to a root document;
- drag/drop move can be added after the MVP, but the Engine must own `MoveElement` and the command must identify internal-tree versus root-document-tree placement.

`ElementsDock` should call `GetElementTree(query)` and `OpenElement(command)`. The unified tree DTO must include `storageKind` and `ownerRootId`; the dock must not assemble or merge storage trees by reading files directly.

## Element Browser

`ElementBrowser` is a center tab, not a dock panel. Element subsets need horizontal space, filters, sorting, selection, and row actions, so the browser belongs in the main editor area.

 The default Element Browser entry point is the workspace menu path `SiYuanMemo > View > Element Browser` plus command palette/hotkey access. Compact navigation may include a small Browser/Commander affordance if it does not expand into a full row. Later entry points can include Element tree context menu actions and search results. Duplicate browser buttons should not appear in `ElementsDock`, `ElementInspector`, or queue summary cards. The top bar should not carry queue counts, current interval chips, postpone controls, or global search for the MVP.

MVP browser views:

- `Due now`: due/current memorized Elements.
- `All`: all Elements.
- `Pending`: Elements waiting in the pending queue to be introduced.
- `Memorized`: Elements active in the learning process.
- `Dismissed`: Elements removed from active learning.
- `Priority queue`: all memorized Elements ordered by priority position.
- `Branch`: descendants of a selected Element tree node.
- `Search results`: Elements returned by Element search over titles, cleaned Topic/Concept text, Item prompt/answer text, and editable formula source.
- `Filter`: Elements matching type, status, date, interval, priority position, repetition, lapse, and difficulty criteria.

MVP columns:

- selection;
- row number;
- priority position;
- title;
- type;
- Concept or parent;
- due time;
- status;
- interval;
- repetitions;
- lapses;
- postpone count;
- last review time;
- action buttons.

MVP Element Browser toolbar:

- `Menu`: opens the browser menu for advanced subset/search/export operations later;
- `Learn subset`: starts learning from due/outstanding memorized Elements in the current browser rows, using browser order, then introduces pending Elements from the subset by pending-queue order and user-configured introduction limits;
- `Review all`: reviews all non-dismissed Elements in the current subset, including mid-interval repetitions;
- `Review topics`: reviews non-dismissed Topics only and skips Items;
- `Sync with Element`: when active, row selection navigates the current Element tab;
- `Due now`: shows due/current memorized Elements;
- `All`: shows all Elements;
- `Items`: filters the current browser to Items;
- `Topics`: filters the current browser to Topics;
- `Dismissed`: filters to dismissed Elements;
- `For Review`: default sort preset for review, ordered by due/overdue status, priority position, difficulty/interval pressure, and stable tree order fallback;
- `Postpone`: postpones the current browser subset.

Later versions may expand `For Review` into a sort menu with choices such as priority position, interval, difficulty, next repetition date, and tree order. The MVP should not hard-code a single priority-only sort.

SiYuanMemo uses the same priority direction as SuperMemo: priority position `0%` means highest priority, and larger values mean lower priority. UI labels should say `priority position` or `优先级位置`, not just `priority`, so the percentage is not mistaken for an importance score.

MVP row behavior and actions:

- row click navigates the current reusable Element tab to that Element;
- postpone;
- dismiss;
- remember or forget depending on status;
- send to note for any Element type.

The browser is an Adapter over `GetElementSubset(query)` and scheduling actions. Sorting and filtering can be local for already-loaded rows, but due eligibility, status, priority, and scheduler mutations belong to the Learning Engine.

Dismissed Elements are excluded from `Learn subset`, `Review all`, and due queues. They can re-enter learning only through an explicit user action such as `Remember`.

## Embedded Learning Mode

Every opened Element has an Element toolbar below the reader/editor and above Element Context. The primary actions are `Learn` and `Add new`.

`Learn` starts review in the current Element tab. It does not open a new tab or a separate learning surface. When review advances to another Element, the current Element tab navigates to that Element, the same way normal editor navigation changes the current content.

Learning start behavior:

- the MVP keeps one active learning session per running workspace;
- if an in-process session exists, `Learn` returns to that session's next target;
- if a locally recoverable interrupted scoped queue exists after restart, prompt the user to continue or discard it;
- if the user declines or no recoverable scoped queue exists, rebuild and start the default `IncrementalLearningQueue` (`incremental-learning`) from authoritative Element and review-event state;
- if the user manually navigates to another Element while a learning session exists, that Element is previewed context, not the active review card;
- previewed Elements always show the primary `Learn` button, never `Next` or `Show Answer`;
- clicking `Learn` from a previewed Element returns to the next target in the active queue without scheduling the previewed Element;
- recoverable interrupted queues come from filtered branches, temporary practice, or other scoped queue sessions and are stored only in a disposable local snapshot;
- the active queue state is owned by the Learning Engine, not by the UI tab;
- when learning starts from Element Browser, the active queue is a snapshot of the current browser rows in their current sort order.
- each accepted Topic `Next` or Item grade is persisted before advancing; the local queue snapshot never owns scheduling truth and is never synchronized;
- `Learn subset` uses due/outstanding memorized Elements first, then pending Elements in that subset within daily/session introduction limits; a later `Review all` command can process all non-dismissed Elements in the subset, including mid-interval repetition.
- normal learning queues skip Elements already reviewed on the same local learning day; explicit commands such as `Execute repetition` may override that guard.

Learning controls depend on active ReviewTarget type:

- Topic: if and only if the displayed Topic is the current active learning card, show the normal Topic surface and a primary `Next` action at the bottom. `Next` records a Topic repetition event and updates `dueAt`/interval using `topicAFactor`; ordinary browsing, preview navigation, or non-learning `Next` actions do not call the scheduler.
- Item: if and only if the displayed Item is the current active learning card, show the prompt and a primary `Show Answer` action at the bottom; after answer reveal, show grading actions backed by raw grades `0..5`. The UI may use five compact SuperMemo-style visible buttons, but it must still let the user record grade `0` distinctly. Grades `3..5` pass, grades `0..2` fail/lapse, and grades below Good can be reserved for final drill.
- Note block: future `NeuralRoamQueue` or note-review sessions may render a native Protyle block through a renderer Adapter, but normal MVP scheduling does not mutate `.sy` block scheduling state. If the user explicitly turns a block into a Topic or Item, the created Element owns scheduling.
- Concept: Concepts are not schedulable by default and should not appear in review unless explicitly made schedulable later.

Element tools such as extract, cloze/item creation, split, send to note, read point, postpone, reschedule, later today, remember, forget, dismiss, and done live in the same Element toolbar row as `Learn` and `Add new` or in its overflow menu. The toolbar is the visible MVP surface for common actions.

The tools area switches among compact Learn, Edit, Read, Tools, and Alarm groups. Actions stay on one row when space permits and wrap to a second row only at narrower widths. The toolbar must not reserve a permanent two-row height on wide screens or push the reader out of the content area.

Topic HTML also has a right-click `TopicReaderContextMenu`, based on the SuperMemo `Component menu` pattern. This menu is scoped to the current reader/editor surface and must not be confused with `ElementBrowser`'s browser menu. It may mirror toolbar actions such as extract, cloze, split, read point, highlight, ignore, send to note, find in article, insert Element link, download images, and mode switching. Selection-dependent actions are disabled or hidden when no valid text range is selected.

Native SiYuan block context actions may expose `Create Topic from block`, `Create Item from block`, and `Create Cloze Item from block`. A whole block with no explicit back-side answer creates a Block-backed Topic whose `.sme` payload stores the stable block ID and whose live material remains in `.sy`. Explicit prompt/answer or cloze intent creates an Item snapshot through `BlockSnapshotReader`. None of these commands replaces the native note block. MVP does not treat an arbitrary selected range as a live Block-backed Topic; that would require a later versioned range-reference contract.

`Add new` creates only a new Topic with one primary `boundTo` target. The target may be the current Element's Concept or the current Element itself when the workflow is intentionally anchored there. It does not imply a structural child Topic, does not open an Element type picker, and does not create Items, Concepts, or notes. Rebinding replaces the primary target. Additional associations use explicit Element links and therefore appear as normal backlinks. Manual Q/A and cloze creation remain separate explicit commands.

## Element Inspector

`ElementInspector` is an independent right dock for scheduler, lifecycle, and processing state. It must not own backlinks and it must not read Element files directly.

MVP fields:

- Element type, `lifecycleState`, and `processingState` without collapsing them into one status;
- Concept/group and parent;
- priority position;
- next, last, and first review dates;
- due queue position when applicable;
- interval, previous interval, and passed time;
- repetitions, lapses, and postpone count;
- last rating, raw grade `0..5`, pass/lapse state, and final-drill flag;
- forgetting index;
- Topic A-Factor or equivalent topic interval growth field, defaulting to `2.5` unless overridden;
- Item difficulty, stability, and retrievability when available;
- ordinal or pending-order field when available;
- source/origin link.

Mutations in the Inspector must call Learning Engine actions such as `RescheduleElement`, `SetPriorityPosition`, `RememberElement`, `ForgetElement`, or `DismissElement`.

## Element Context

`ElementContext` is the bottom dock for structural context. It is not the backlinks panel.

MVP context groups:

- primary `boundTo` target;
- parent Element;
- child Topics;
- child Items;
- source/origin Element;
- extracted ranges generated from this Topic;
- sibling Elements;
- branch path.

Clicking a context row opens the target Element using normal Element tab reuse rules.

## Dock Behavior

The MVP uses dock semantics for the side and context regions:

- left `ElementsDock` can hide/show without closing the active Element tab;
- right `ElementInspector` can hide/show without owning backlinks;
- right `ElementBacklinksDock` can hide/show independently from `ElementInspector`;
- bottom `ElementContext` can hide/show under the reader/editor;
- all dock buttons must remain reachable when a dock is hidden;
- hiding any dock releases its grid track so the center reader/editor expands naturally without a fixed blank column or an unnatural horizontal offset;
- hidden dock state should be serialized with the normal layout state once native integration begins.

## Note Integration

SiYuanMemo should not replace or wrap the native daily note editor.

- the Daily Note tab remains a normal SiYuan document/editor tab;
- sending an Element to today's Daily Note inserts or focuses a normal block reference workflow;
- the Daily Note tab should not show SiYuanMemo-specific reminder chips, return buttons, or custom navigation controls;
- sending to Daily Note inserts an `@Element` anchor plus an empty child block for the user's own note; it must not copy Topic material into the note.

## Element Backlinks

`ElementBacklinksDock` displays explicit references to the current Element.

Backlink sources:

- SiYuan note blocks that reference the current Element;
- other Elements, including Topics, Items, Concepts, and later Element types, when their content explicitly references the current Element.

Not backlinks:

- parent/child Topic structure;
- knowledge-tree path;
- sibling relationships;
- inherited Concept membership;
- the primary `boundTo` relation unless the source content also contains an explicit Element link;
- full-text search hits without an explicit Element reference.

The primary binding belongs in `ElementContext`, not `ElementBacklinksDock`. Additional explicit links use the normal backlink path and retain their source context.

Each backlink row should show:

- source type, such as Note, Topic, Item, or Concept;
- source title;
- a short context snippet around the reference;
- a jump action.

Click behavior:

- if the source is an Element, open the source Element in an Element tab;
- if the source is a note block, open the SiYuan document/block through the native editor.

Backlinks are rendered in `ElementBacklinksDock`, not inside `ElementInspector`.

## Element Tabs

`ElementTab` is a native center tab `Model`. For Topics it hosts `TopicReader`. For Items it hosts the item prompt/answer review surface. Future Element types can add their own renderers behind the same Element tab seam.

Opening rules:

- opening an already-open Element focuses the existing `ElementTab`;
- opening an Element from a note reference follows SiYuan-style tab reuse: reuse a current reusable/unmodified tab when possible; create a new tab when no reusable tab exists;
- standard split options should open the Element to the right or bottom;
- tab title follows the Element title;
- layout restore must be able to reopen Element tabs by `elementId`;
- if an Element is missing, deleted, or unavailable, the tab should show a recoverable error surface instead of silently closing.

Suggested layout JSON:

```json
{
  "instance": "SymemoElement",
  "elementId": "20260719010101-abcdefg",
  "title": "Element title",
  "icon": "iconSymemoElement"
}
```

`ElementBrowser` also needs layout persistence:

```json
{
  "instance": "SymemoElementBrowser",
  "query": {
    "scope": "due",
    "types": ["topic", "item"]
  },
  "title": "Elements",
  "icon": "iconSymemoBrowser"
}
```

These instances should be restored by `JSONToCenter` and serialized by `layoutToJSON`, just like native Search/Asset/Graph models.

## Deep Module Interfaces

The frontend uses named TypeScript methods corresponding to stable `/api/symemo/*` routes, including methods such as `getElementTree()`, `extractTopic()`, `startLearning()`, `gradeItem()`, `postponeElement()`, and `sendToNote()`. This client is a typed transport Adapter, so method-per-route naming is intentional and does not reproduce the kernel Module structure.

Each HTTP handler maps one named route to one of the five Learning Engine families defined in `0010-deep-module-interface-design.md`: `CreateElement`, `ChangeElement`, `RunLearningAction`, `SendToNote`, or `Query`. The frontend client may decode DTOs and normalize transport errors, but it must not coordinate storage, scheduling, references, indexing, event history, or session advancement.

Recommended frontend module layout:

```text
app/src/symemo/
  api/client.ts
  tabs/openElement.ts
  tabs/openElementBrowser.ts
  tabs/ElementTab.ts
  tabs/ElementBrowser.ts
  dock/ElementsDock.ts
  dock/ElementInspector.ts
  dock/ElementBacklinksDock.ts
  dock/ElementContext.ts
  reader/TopicReader.ts
  reader/TopicHtmlEditorAdapter.ts
  ui/elementTreeView.ts
```

The public UI seams are `openElement`, `openElementBrowser`, and `ElementsDock`. TinyMCE, table rendering, tree rendering, selection handling, context menus, toolbar actions, and keyboard details stay inside the UI implementation.

## MVP Build Order

1. Add backend DTOs and actions for `GetElementTree` and `GetElementSubset`.
2. Add `ElementsDock` as a native left dock type and default layout entry.
3. Add `openElement` and `ElementTab` with read-only Topic rendering first.
4. Add the Element toolbar with `Learn`, `Add new`, extract, cloze/item creation, split, send to note, read point, postpone, remember/forget, dismiss, and done controls.
5. Add embedded learning mode with local scoped-queue recovery, active-session-only Topic `Next`, and Item `Show Answer` plus raw grade `0..5` recording.
6. Add block context actions for `CreateTopicFromBlock`, `CreateItemFromBlock`, and `CreateClozeItemFromBlock`.
7. Add `openElementBrowser` and `ElementBrowser` with the MVP browser toolbar, due/all/branch/search/dismissed views, row sync, and postpone/dismiss/remember actions.
8. Add dock hide/show behavior plus layout serialization/restore for `SymemoElement` and `SymemoElementBrowser`.
9. Add TinyMCE editing and richer Topic Reader actions after the navigation loop works.

The tracer-bullet MVP is acceptable when a user can import or seed a Topic, create a Topic from a block with no back-side answer, create an Item from an explicit block Q/A or cloze, see it in the Elements tree, open it as an Element tab, maintain a read point, extract child Topics, create at least basic Items, start review from the Element toolbar `Learn` button, continue or discard a locally recoverable interrupted scoped queue, process Topics with active-session-only `Next`, process Items with `Show Answer` and grades `0..5`, send any Element to notes, see Elements in the browser, postpone/dismiss/remember/forget them, hide/show left/right/bottom docks, and return to Elements from both surfaces.

## Non-Goals For MVP

The first UI pass does not need drag/drop tree reparenting, custom queue formulas, advanced table grouping, full keyboard parity, mobile layout, plugin packaging, or replacing SiYuan's file tree. It only needs the correct native seams so the progressive reading loop is usable without coupling Topics to SiYuan document blocks.
