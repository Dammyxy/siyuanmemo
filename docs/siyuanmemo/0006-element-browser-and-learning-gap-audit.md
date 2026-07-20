# Element Browser And Learning Gap Audit

Date: 2026-07-19

## Scope

This document records the product and architecture gaps found while comparing SiYuanMemo's current MVP design with public SuperMemo-style incremental learning documentation around menus, toolbars, element windows, contents trees, browsers, subset learning, priority queues, and scheduling controls.

The goal is not feature parity. The goal is to preserve the parts that matter for a SiYuan-native progressive reading product:

- Element-centered reading and review.
- A knowledge tree plus reusable subset browsers.
- Explicit separation between Topic material and SiYuan note blocks.
- Independent scheduling that can evolve without coupling to SiYuan `riff`.
- Small UI and kernel interfaces that keep the Learning Engine deep.

Non-public reference names and local provenance must not be named in public docs, code comments, issue titles, changelogs, or UI strings. Public docs may refer only to public concepts such as adaptive scheduling, DSR-compatible state, progressive reading, Element Browser, Topic, Item, Concept, priority position, and subset learning.

## Main Finding

The current SiYuanMemo architecture is sound after the storage amendment: root `.sme` trees and monthly `.smr` events are source data, `memo.db` is rebuildable, the Learning Engine owns scheduling and references, Topic material stays outside `.sy`, and SiYuan blocks remain the note layer.

The main MVP gap is that the current `QueueBrowser` is too narrow. It is designed like a due-queue table, while the target workflow needs a general `ElementBrowser` Adapter. Due queue, priority queue, branch view, search results, dismissed list, and filtered subsets should all be browser views over the same underlying Element subset interface. Queue names and code identifiers should follow the existing plugin queue vocabulary: `IncrementalLearningQueue`, `RetrievalPracticeQueue`, `FilterGroupQueue`, `FinalDrillQueue`, `NeuralRoamQueue`, and `LeechReviewQueue`.

## MVP Must Add

### SiYuan-Fused Menu And Navigation

The MVP should preserve the SuperMemo-style command vocabulary without copying the whole classic desktop chrome into the default UI.

- Keep the command groups `File`, `Edit`, `Search`, `Learn`, `View`, `Toolkit`, `Window`, and `Help`.
- In the default Compact mode, expose them through SiYuan's workspace menu as `SiYuanMemo > ...`, command palette entries, hotkeys, and contextual menus.
- Keep `Classic / SuperMemo` as an optional interface mode that can show the full menu and navigation strips for users who prefer that workflow.
- Do not let the classic navigation strip occupy the Topic Reader or editor row in Compact mode.
- Compact Element navigation should live in the fused tab/top area as a small cluster, with Element-only commands disabled on non-Element tabs.

### SuperMemo UI Reference Coverage

Local public references scanned:

- `Menus, buttons, dialogs, etc. - SuperMemo Help.md`
- `Main menu - SuperMemo Help.md`
- `File menu`, `Edit menu`, `Search menu`, `Learn menu`, `View menu`, and `Toolkit menu`
- `Element menu`, `Component menu`, `Browser menu`, `Contents menu`, and `Subset operations`
- `Navigation bar`, `Learnbar`, `Read toolbar`, `Edit toolbar`, `Compose toolbar`, `Tools toolbar`, `Alarm toolbar`, `Browser toolbar`, `Contents toolbar`, and `Toolbar dock`

Coverage result:

| SuperMemo surface | SiYuanMemo decision |
| --- | --- |
| Main menu | Covered as a command map under `SiYuanMemo > File/Edit/Search/Learn/View/Toolkit/Window/Help`, with Compact mode hiding the classic strip. |
| File menu | Partially mapped. Import/export/repair/reset/scheduler settings belong in workspace commands; SuperMemo collection file management maps to SiYuan workspace/notebook behavior and is not MVP UI chrome. |
| Edit menu | Covered for Element creation, import, block-to-Element conversion, concept creation, delete/move/rename, and note send. SuperMemo component insertion is not copied because Topics use one HTML material surface. |
| Search menu | Partially mapped. `Find elements`, `Find in article`, web search, Element links, and backlinks are in scope. SuperMemo registries become search/index views or asset/reference stores, not registry windows. |
| Learn menu | Covered for Learn, Go neural as reserved queue, stages/final drill, sorting, postpone, random review/test as reserved commands. |
| View menu | Mostly mapped to `ElementBrowser` views: due/outstanding, all, memorized, dismissed, concepts, topics, items, priority, search results, filter, branch, and leeches. Still reserve `Last browser`, recent reviewed/postponed subsets, link list, tasks, and durable subset files. |
| Toolkit menu | Partially mapped. Commander, calendar, options, statistics/analysis, algorithm arena, mercy/postpone, neural analysis, random review, and tasklist are command entries or reserved modules. Sleep chart and plan are not MVP. |
| Window menu | Only covered from the main-menu index summary. It maps to SiYuan layout, dock visibility, saved layouts, interface mode, and theme/background commands. A local `Window menu` page is not currently clipped. |
| Help menu | Only covered from the main-menu index summary. It maps to local help, command list, public reference links, and about/version information. A local `Help menu` page is not currently clipped. |
| Element menu | Partially covered. Learning, priority, concepts, references, type, delete, go-to, and component-independent actions are in scope. Add-to-subset, undo repetition, repetition history, memory status, concept map, and statistics need explicit reserved command names. |
| Component menu | Newly covered as `TopicReaderContextMenu`. Its `Browser menu` entry is only the embedded browser/native webview menu and must not be confused with `ElementBrowser` commands. |
| Browser menu | Partially covered by `ElementBrowser`. Learn subset, review all/topics, child views, select, search, sort, filter, postpone, and export are in scope. Missing/reserved details: subset algebra, checked/unchecked child browsers, sources/articles/extracts child views, save pending/priority/drill/repetitions, randomize, and subset knowledge graphs. |
| Contents menu | Partially covered by `ElementsDock`. Open/view, create, rename, move, delete, dismiss, branch browser, and learn branch are in scope. Missing/reserved details: duplicate, set concept root/hook, branch export, branch process, tree save, collapse/sync/select-current details. |
| Subset operations | Partially covered. Learn, Review all, Postpone, Advance, priority spreading, and filter queues are in scope. Missing/reserved details: batch forgetting index, A-Factor, ordinal, move/type/title/template, and export operations. |
| Navigation bar | Covered as compact top/tab navigation plus Classic mode navigation. Keep Search/Find/Reference/Commander/Layout as commands, not always-visible reader row controls. |
| Learnbar | Partially covered by bottom Element toolbar. Learn/Add new, scheduling, priority, interval, execute repetition, reschedule, later today, dismiss/remember/forget, cloze, copy/paste, import, file/source, and help map to toolbar modes or overflow. |
| Read toolbar | Covered by Topic Reader actions: paste/import article, extract, schedule/queue extract, cloze, schedule/queue cloze, split, highlight, ignore, delete before/after cursor, and read-point operations. |
| Edit and Compose toolbars | Mostly non-goal for MVP because SuperMemo component layout is not copied. Formatting maps to TinyMCE; component add/order/drag/tile/template operations are reserved or rejected. |
| Tools toolbar | Partially mapped to command entries: open/backup, find, web search, layout, calendar, outstanding, ancestors, asset views, tasklists, and hints. |
| Alarm toolbar | Reserved. Stopwatch/alarm can become a future learning-session timer module; it should not affect scheduler correctness. |
| Browser and Contents toolbars | Partially mapped as compact toolbar/overflow actions in `ElementBrowser` and `ElementsDock`; synchronization is modeled as Browser row sync with the reusable Element tab. |
| Registry menu/toolbar | Not locally clipped and not MVP. SuperMemo registry concepts map to SiYuanMemo asset store, reference store, Element references, and search/index views; do not introduce registry windows unless a later feature needs them. |
| Dialogs: Options, Element parameters, Sleep Chart, Analysis | Partially mapped. Options become SiYuan settings; Element parameters map to `ElementInspector`; Analysis/statistics are reserved reports; Sleep Chart is not MVP. |

Net gap from this pass: the previous docs covered the main workflow surfaces, but under-specified `Element menu`, `Contents menu`, `Browser menu` advanced subset operations, and registry/dialog surfaces. Those should be tracked as reserved command names so the MVP does not paint itself into a shallow UI-only corner.

### General Element Browser

Replace the narrow `QueueBrowser` concept with a general `ElementBrowser`.

The browser should support these MVP views:

- `due`: due and overdue memorized Elements.
- `all`: all Elements.
- `pending`: Elements waiting in the pending queue to be introduced.
- `memorized`: Elements active in the learning process.
- `dismissed`: Elements explicitly removed from learning.
- `priority`: Elements ordered by priority position.
- `branch`: descendants of a selected tree node.
- `search`: results from Element search over titles, cleaned Topic/Concept text, Item prompt/answer text, and editable formula source.
- `filter`: saved or ad hoc filter criteria.

All views should share one tab implementation, one table model, and one subset command path. Specialized views are queries, not separate UI Modules.

### Subset Learning Modes

The MVP needs three distinct learning commands:

- `Learn subset`: process due/outstanding memorized Elements in the current browser order, then introduce pending Elements from that subset within user-configured daily/session introduction limits.
- `Review all`: process all non-dismissed Elements in the subset, including mid-interval repetition for Elements that are not due.
- `Review topics`: process only non-dismissed Topics in the subset; Items are skipped to avoid unnecessary active-recall workload.

Dismissed Elements must be excluded from all three unless the user explicitly runs `Remember` first. Normal learning queues must also skip Elements already reviewed on the same local learning day unless the user invokes an explicit override action such as `Execute repetition`.

### Item Creation

The current design specifies Item review but not Item creation. MVP must include at least one route from selected Topic material to Item:

- create a cloze Item from a selected range by preserving surrounding HTML and replacing the selected range with a blank marker;
- create a manual Q/A Item from the current Element context;
- create a Block-backed Topic from one whole SiYuan block when there is no explicit back-side answer;
- create an Item from a SiYuan block only when the user supplies explicit Q/A or cloze intent;
- store Item prompt, answer, source Element, source range, and explicit backlinks;
- make generated Items children of the source Topic by default.

`Add new` is not the Item creation path in the MVP. It creates only a new Topic with zero or one primary `boundTo` relation, which may target the current Concept or the current Element. The relation is non-structural; additional associations use explicit Element links and backlinks.

This should be implemented as Learning Engine actions, not as UI-side file creation.

### Grade Model

The scheduler storage should support a richer internal grade, even if the MVP UI exposes simplified buttons.

Required model:

- store `grade` as an integer or nullable value with room for all six values `0..5`;
- allow a compact SuperMemo-like grading UI, including five visible buttons if desired, but preserve a distinct path for grade `0`;
- derive recall pass/fail as `passed = grade >= 3`, so grades `0..2` are failures/lapses and grades `3..5` are passes;
- keep final-drill eligibility separate from pass/fail; in SuperMemo-like mode, grades below Good (`grade < 4`) may enter final drill even when grade `3` is a pass;
- keep the mapping versioned in scheduler state or review history;
- do not make any simplified button set the permanent scheduler interface.

### Topic Scheduling Defaults

Topic scheduling needs an explicit A-Factor default before implementation:

- collection default `topicAFactor = 2.5`;
- Concepts and individual Topics may override it;
- normal UI range should start around `1.3..5.0`;
- advanced users may set values down to `1.01`, but the UI should warn that low values make Topics recur very frequently;
- active-session Topic `Next` grows the interval; ordinary navigation `Next` does not schedule anything.

This prevents the early MVP from producing dense `0/1/2/3` day Topic loops for ordinary reading material.

### Pluggable Scheduler Adapters

The MVP must not hard-code one memory formula into `GradeItem` or route Topic `Next` through Item grading.

Required model:

- `simple-v1` remains deterministic fallback and shadow comparator;
- `fsrs-v1` is the primary Item scheduling adapter from the first implementation;
- `topic-afactor-v1` is the independent ungraded Topic scheduling adapter; Concepts use it only after explicit enrollment;
- raw grades map to FSRS ratings as `0/1/2 -> Again`, `3 -> Hard`, `4 -> Good`, `5 -> Easy`;
- the arena records candidate schedules and prediction scores for enabled adapters on each Item review;
- `weightedConsensus` is reserved for later and disabled until there is enough review history and an explainable report;
- the first adapter seam uses compiled Go adapters; runtime-loaded adapters may be added later through the same DTOs.

### Read Point

Topic reading needs an explicit read-point model:

- set read point from current selection/cursor;
- jump to read point;
- clear read point;
- automatically move read point after extract, cloze, split, highlight, or ignore actions when appropriate.

The read point belongs in Topic annotations and should be stored with stable node IDs plus offsets, not as scroll position alone.

### Topic Reader Context Menu

The local `Component menu` reference shows that SuperMemo's HTML reading surface has a right-click menu that is separate from the browser/table menu. SiYuanMemo should model this as `TopicReaderContextMenu`.

Required MVP consequences:

- the Reader menu opens from right-click inside Topic HTML whether or not text is selected;
- selected-text actions include extract, cloze, send to note, highlight, ignore, insert link, parse/convert selected HTML, and set read point;
- no-selection actions include find in article, paste, go/clear read point, download images, open source/origin, file/source operations, and read/edit mode switching;
- `Browser menu` remains reserved for `ElementBrowser` subset/table operations and must not be used as the name for the Topic HTML right-click menu;
- SuperMemo component-layout features such as display position, dragging mode, OLE, registry member operations, answer flags, and MCT flags are not MVP features.

### Element Learning Actions

The Element toolbar and context menu need MVP semantics for these actions:

- `Learn`: start or continue the current learning session; if the currently displayed Element was reached by manual preview/navigation, return to the next target in the active queue rather than turning the previewed Element into the active review card. The default user-facing Learn queue is `IncrementalLearningQueue`.
- `Execute repetition`: review the current Element now, including mid-interval repetition when not due.
- `Reschedule`: choose a next review date or interval explicitly.
- `Later today`: place the Element later in today's active queue.
- `Remember`: introduce a pending or dismissed Element into learning.
- `Forget`: remove a memorized Element from learning and return it to pending.
- `Dismiss`: remove an Element from active learning until explicitly remembered while preserving its schedule and adapter state.
- `Done`: finish a Topic; if it has children, propose dismissing the parent Topic, otherwise propose deletion.

The MVP can expose only the common actions visibly and put the rest in the Element menu, but the Learning Engine needs named actions for them.

### Inspector Data

`ElementInspector` should not be a generic metadata panel. It should show scheduler, lifecycle, and processing data that users need while learning:

- Element type, `lifecycleState`, and `processingState`.
- Concept/group and parent.
- priority position.
- next, last, and first review dates.
- due queue position when applicable.
- interval, previous interval, and passed time.
- repetitions, lapses, and postpone counts.
- last grade and internal grade history summary.
- forgetting index.
- Topic A-Factor or equivalent topic interval growth setting.
- Item difficulty, stability, and retrievability when available.
- ordinal or pending-order field for the pending queue.
- source/origin link.

### Element Context

`ElementContext` should own structural context, not backlinks:

- parent Element;
- child Topics;
- child Items;
- source/origin Element;
- extracted ranges generated from this Topic;
- sibling Elements;
- branch path;
- optional recently opened related Elements.

Explicit references still belong in `ElementBacklinksDock`.

## MVP Should Reserve

The MVP does not need complete implementations for these, but the interfaces should not make them hard later:

- `Advance subset`: move future reviews earlier for a subset.
- `Add to outstanding`: temporarily intersperse selected future Elements into today's active queue without changing the persistent due date.
- `Postpone subset` and `Postpone branch`: batch delay outstanding Elements.
- `Priority spread`: distribute priority positions across a subset.
- `Filter dialog`: query by priority position, type, status, due dates, repetitions, lapses, interval, forgetting index, difficulty, stability, retrievability, and postpone count.
- `Repetition history`: inspect and undo recent review effects.
- `Leech browser`: show difficult Items with many lapses.
- `Statistics`: collection, subset, workload, retention, and priority-protection reports.
- `Random review` and randomized browser order.
- `Saved subsets`: durable named Element ID lists.
- `Subset algebra`: add, subtract, intersect, intersperse, and saved subset files/lists.
- `Browser child views`: selected, unselected, sources, articles, extracts, descendants, outstanding, pending, memorized, dismissed, items, topics, tasks, branch, and filter.
- `Browser save operations`: save pending order, save priority order, save final drill queue, and save repetitions from current browser sort.
- `Batch subset operations`: forgetting index, A-Factor, ordinal, move, type, title, template, and export.
- `Contents advanced operations`: duplicate, set concept root/hook, tree save, branch export, branch process, collapse/sync/select-current, and branch statistics.
- `Element advanced operations`: add to subset, undo repetition, repetition history, memory status, best forgetting index, concept map, and locate extracts.
- `Registry-derived operations`: asset/reference/text/link registry views should be backed by SiYuanMemo stores and indexes, not by SuperMemo-style registry windows.
- `Session timer/alarm`: stopwatch and alarm state may become a future learning-session utility, but scheduler state must not depend on it.
- `Final drill`: reserve session-level queue state for reviews below Good without making it a required MVP surface.
- `Weighted algorithm arena`: combine compatible same-profile Adapter candidates into one adopted schedule after enough local review history exists.

## Non-Goals

SiYuanMemo should not copy these SuperMemo-era surfaces into the MVP:

- component layout and component drag modes;
- template registry and visual template management;
- e-mail workflows;
- OLE or arbitrary binary component editing;
- tasklist manager;
- standalone registry windows for fonts, sounds, images, templates, or scripts;
- full window layout manager outside SiYuan's existing dock/tab layout system;
- exact scheduling parity with any external implementation.

SiYuan equivalents should be used where they already exist, especially tabs, docks, command palette, Protyle, block references, assets, and workspace layout persistence.

## Applied Doc Updates

The following corrections have been incorporated into the detailed design documents:

- Rename conceptual `QueueBrowser` to `ElementBrowser`; `QueueBrowser` may remain as a view alias for `scope=due`.
- `sendToNote` must apply to all Element types, not only Topic.
- Add `CreateItem` and `CreateClozeItem` to the Learning Engine interface.
- Add live `CreateTopicFromBlock` through `BlockReferenceReader`, and add snapshot `CreateItemFromBlock` plus `CreateClozeItemFromBlock` through `BlockSnapshotReader`.
- Add `AddNewTopic` as the only MVP behavior behind the `Add new` button.
- Add read-point actions to the Topic Reader interface.
- Add `TopicReaderContextMenu` as the Topic HTML right-click surface and keep it distinct from `ElementBrowser`'s browser menu.
- Add subset learning modes and general browser views to the UI design.
- Add grade, Topic A-Factor, same-day skip, pending limits, pluggable algorithm adapters, arena reporting, and lifecycle actions to the scheduler design.
- Add `ElementInspector` and `ElementContext` responsibilities explicitly.
- Replace the one-Element/multi-file layout with SiYuan-style dual-tree `.sme` storage, monthly `.smr` events, shared SiYuan assets, local history, sync conflict policies, and a rebuildable temp index.
- Record that promotion to a root Element document preserves the Element ID and leaves no mount placeholder.
- Keep dual-tree storage internal while exposing one virtual Elements navigation tree containing internal Elements and root documents.
- Remove public references to local private paths and inventories.

## MVP Acceptance Update

The MVP is acceptable when a user can:

1. Import or seed a Topic.
2. See it in the Elements navigation projection with root-document and internal-tree placement preserved.
3. Open any Element in an Element tab.
4. Read Topic HTML and maintain a read point.
5. Extract child Topics and create at least basic Items from selected Topic material.
6. Create a Topic from a note block when there is no back-side answer, or an Item when explicit Q/A or cloze intent exists.
7. Send any Element as an `@Element` note anchor to a new document or Daily Note.
8. Open an Element Browser for due, all, branch, search, and dismissed views.
9. Start `Learn` from any opened Element or `Learn subset` from the browser.
10. Process Topics with active-session-only `Next` and Items with `Show Answer` plus raw grades `0..5`.
11. Dismiss, remember, forget, reschedule, and postpone Elements through Learning Engine actions.
12. Run `fsrs-v1` as the first Item scheduling adapter while keeping `simple-v1` as fallback/shadow comparator.
13. Run `topic-afactor-v1` for active-session Topic `Next` without grading or Item-arena participation.
14. Inspect Element scheduling data and structural context without confusing those with backlinks.
