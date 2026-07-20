# Topic Reader And HTML Editor Design

Date: 2026-07-19

## Decision

SiYuanMemo will ship a dedicated Topic Reader with an editable HTML material surface. The MVP uses TinyMCE as the HTML editor Adapter for Topic material. Protyle remains the editor for user-authored SiYuan notes and is not used for Topic HTML.

This is a Module and Adapter decision:

- `TopicMaterial` is the deep Module that owns Topic HTML actions.
- `TopicReader` is the reading surface Adapter.
- `TopicHtmlEditorAdapter` wraps TinyMCE and converts editor events into Learning Engine actions.

TinyMCE must not become the Learning Engine interface. If TinyMCE is replaced later, storage, scheduling, note references, and core Topic actions should remain unchanged.

## MVP Scope

The Topic Reader MVP supports:

- opening a Topic and rendering cleaned HTML from its `.sme` payload;
- reading mode with scroll position tracking;
- setting, jumping to, clearing, and persisting a read point;
- selecting a range and running `ExtractTopic`;
- splitting material into child Topics;
- creating cloze or basic Q/A Items from selected material;
- highlighting and ignoring selected material without creating notes;
- sending a Topic or selected range to notes with `SendToNote`;
- showing extracted highlights and opening child Topics;
- editing Topic material HTML through TinyMCE;
- saving edited Topic HTML through `SaveTopicHtml`;
- preserving queue, status, priority position, and Concept assignment controls.
- opening a Topic Reader context menu from right-click inside Topic HTML, with or without selected text.

The MVP does not need a full browser, URL fetcher, PDF/EPUB reader, source snapshot archive, or Protyle-based material editor.

Topic navigation and Element Browser views are specified separately in `docs/siyuanmemo/0005-topic-ui-integration-design.md`. The Topic Reader only owns the opened Topic surface; the left Topic tree and Element Browser are separate UI Adapters over the Learning Engine.

## Topic Reader Context Menu

Public progressive-reading documentation distinguishes the right-click menu inside an HTML reading surface from the Element Browser table menu. In SiYuanMemo, this becomes a Topic Reader context menu, not an Element Browser menu.

Naming:

- implementation name: `TopicReaderContextMenu`;
- user-facing name: `Reader menu` or `Element menu`;
- do not call it `Browser menu`, because `ElementBrowser` already owns browser subset/table commands.

Behavior:

- right-click inside Topic HTML opens the same context menu shell whether text is selected or not;
- actions that require a selection should be disabled or hidden when there is no valid selection;
- actions that mutate Topic material must call `TopicMaterial` actions, not write HTML directly in the UI;
- actions that only inspect or navigate, such as `Find in article`, `Go to read-point`, `Open source`, or `Open in new window`, may stay UI-local when they do not change storage;
- context-menu actions should mirror the visible Element toolbar/overflow actions, but the toolbar remains the main discoverable MVP surface.

MVP command groups:

- `Reading`: extract, schedule extract, queue extract, cloze, schedule cloze, queue cloze, split, insert splitline, set/go/clear read point, highlight, ignore, delete before cursor, delete after cursor, and delete processed text.
- `Text`: basic style/format commands routed through TinyMCE, plus safe conversion commands such as filter HTML, plain text, parse HTML, clean lead HTML, and Markdown-to-HTML when supported.
- `Copy/Cut/Paste`: native clipboard operations through TinyMCE/editor selection, preserving sanitized HTML where possible.
- `Links`: insert web link, insert Element link, insert link to source/origin, open current link, and copy link target.
- `Reference`: set or edit source metadata such as title, author, date, source URL, comment, and source download state where available.
- `Find in article`: local search inside the current Topic HTML.
- `Download images`: import remote images referenced by Topic HTML into the SiYuanMemo media store.
- `File`: view source, edit source through TinyMCE/source adapter, copy stored file path or Element path, and open local media where applicable.
- `Mode`: switch between read mode and edit mode; dragging/component layout modes are not part of the MVP because SiYuanMemo has one Topic HTML surface, not SuperMemo's multi-component layout.

Reserved or remapped SuperMemo commands:

- `Explain extract` and `AI explain` are reserved until local AI policy and provenance rules are defined.
- `Task extract` is reserved for the future `Task` Element type.
- `Decompose` is reserved for later batch Item generation from collective clozes.
- `Answer` and `MCT` are legacy component/test flags; SiYuanMemo should model recall through Element type and renderer adapters instead.
- `Color` and `Default color` map to annotation/highlight styling, not to whole-component background colors.
- `Display at`, component dragging, component ordering, OLE operations, and registry member operations are not MVP features.
- SuperMemo's `Browser menu` entry maps at most to a `Native webview menu` debug/advanced command. It must not replace the SiYuanMemo Reader menu by default.

Selection-sensitive defaults:

- selected text enables extract, cloze, send to note, highlight, ignore, insert link, parse selected HTML, and set read point;
- no selection still enables find in article, paste, go to read point, clear read point, download images, open source/origin, mode switch, and file/source operations;
- right-clicking an existing Element link should add open, preview, copy Element link, and show backlinks actions.

## Deep Module Interface

Topic material has no separate action Interface. As defined in `0010-deep-module-interface-design.md`, HTML sanitization, stable-node handling, selection extraction, validation, and annotation remapping are internal pure implementation used by the Learning Engine.

Extract, Split, and Item creation are `CreateElement` variants. HTML saves, read points, annotations, and renames are `ChangeElement` variants. Opening and impact previews are `Query` variants. `SendToNote` remains its own Engine operation. Fine-grained TinyMCE edits such as inserting a web link or deleting around the cursor stay local until the editor submits one coherent `SaveTopicHTML` command.

`Alt+X` invokes the named Extract Topic HTTP route, which maps to `CreateElement(ExtractTopic)`. Callers must not directly write `.sme` payloads, queue state, event files, or index rows.

## Data Flow

Opening a Topic:

```text
TopicReader -> /api/symemo/getElement -> Query(GetElement)
  -> root .sme node payload + materialized queue metadata
```

Editing a Topic:

```text
TopicHtmlEditorAdapter -> /api/symemo/saveTopicHtml -> ChangeElement(SaveTopicHTML)
  -> sanitize HTML
  -> normalize supported structure
  -> preserve/regenerate stable node IDs
  -> remap or invalidate affected annotations
  -> write the owning root .sme
  -> append event
  -> rebuild Topic index rows
```

Adding a bound Topic:

```text
Element toolbar Add new -> /api/symemo/addNewTopic -> CreateElement(AddNewTopic)
  -> create Topic in the selected/default storage location
  -> add zero or one boundTo relation to the current Concept or Element
  -> initialize an empty or starter Topic payload
  -> resolve defaults from Topic override, boundTo Concept context, structural Concept, then collection defaults
  -> schedule Topic due now
  -> append event
```

Extracting a Topic:

```text
TopicReader selection -> /api/symemo/extractTopic -> CreateElement(ExtractTopic)
  -> create child Element
  -> copy selected HTML fragment
  -> annotate parent range
  -> schedule child Topic
  -> append event
```

Creating an Item:

```text
TopicReader selection -> named item route -> CreateElement(CreateClozeItem/CreateItem)
  -> create child Item
  -> store prompt and answer
  -> for cloze, replace selected HTML range with a blank marker in the prompt
  -> record source Element and source range
  -> schedule Item
  -> append event
```

## Stable Selection Model

Editable imported HTML makes DOM paths fragile. The MVP should assign stable `data-symemo-node-id` attributes to block-level material nodes such as headings, paragraphs, list items, blockquotes, table cells, code blocks, and figures.

Annotations should store:

- start node ID and offset;
- end node ID and offset;
- text quote for recovery;
- optional structural path fallback;
- child Element ID for extracted ranges.
- read-point metadata for the resume location.

When `SaveTopicHtml` changes material, the backend should preserve existing stable node IDs where possible. If an edit deletes or rewrites an annotated range, the action should either remap the annotation using quote/context matching or mark it as stale. Silent loss is not acceptable.

Read point is not the same as scroll position. Scroll position is a viewport convenience. Read point is semantic state: where the user should resume processing the Topic. Extract, cloze/item creation, split, highlight, and ignore actions may advance it automatically.

## TinyMCE Adapter Rules

TinyMCE may provide editing UI, toolbar behavior, paste handling, table editing, image rendering, formula rendering/edit widgets, and inline formatting. It must not own project facts.

The adapter must:

- load only sanitized Topic HTML;
- disable unsafe embed/script behavior;
- call `SaveTopicHtml` instead of writing files;
- route pasted or dropped images through the Learning Engine's SiYuan asset adapter instead of embedding base64 blobs or raw local paths;
- preserve formula source in stable math nodes instead of saving only rendered HTML;
- expose dirty state to the Topic Reader;
- support cancel/reload without corrupting Element state;
- keep selection/extraction actions routed through the Learning Engine.

The adapter should keep the toolbar modest for MVP: headings, bold/italic, links, lists, blockquote, table basics, image display/edit metadata, formula display/edit metadata, undo/redo, and save/cancel.

## Safety And Testing

Tests should cover:

- sanitizing hostile HTML before save;
- preserving stable node IDs across simple edits;
- remapping or staling annotations after edited ranges change;
- extracting a child Topic from edited HTML;
- creating Item/cloze content that preserves image references and editable formula source;
- keeping notes as explicit references rather than copied Topic material;
- rebuilding the temp `memo.db` after edited Topic content changes.

The important invariant: Topic material can be edited, but edited material still remains Topic material. User-authored knowledge enters SiYuan notes only through explicit `SendToNote`.
