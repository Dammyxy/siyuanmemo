# SiYuanMemo Design Documents

Date: 2026-07-19

## Reading Order

1. `0009-confirmed-design-baseline.md`: compact inventory of confirmed decisions and unresolved items.
2. `0010-deep-module-interface-design.md`: authoritative Learning Engine, Scheduler, Session, Ledger, query, transport, and Adapter Interface design.
3. `0008-element-storage-sync-recovery-design.md`: authoritative storage, dual-tree, sync, conflict, history, and recovery design.
4. `0002-learning-engine-design.md`: Element domain and Learning Engine workflows.
5. `0003-spaced-repetition-scheduler-core.md`: queues, ReviewTarget, algorithm adapters, FSRS, and arena.
6. `0004-topic-reader-html-editor-design.md`: Topic Reader, TinyMCE adapter, selection, read point, and context menu.
7. `0005-topic-ui-integration-design.md`: SiYuan shell integration, docks, tabs, Browser, learning controls, references, and backlinks.
8. `0006-element-browser-and-learning-gap-audit.md`: feature coverage and deferred command inventory.
9. `0007-mvp-implementation-decisions.md`: first implementation defaults and placement.
10. `0001-fork-strategy.md`: fork and upstream strategy.

## Precedence

Later confirmed decisions supersede older drafts. In particular, `0008` replaces every older one-Element/multi-file storage example, `0009` records the current cross-document baseline, and `0010` replaces older method-per-action internal Interface inventories. Older documents must be interpreted through these files until their detailed sections are fully reconciled.

HTML prototypes under local references are interaction previews, not architecture authorities. When a prototype conflicts with `0008` or `0009`, the design documents win and the prototype remains stale until it is explicitly updated.

Private research inventories, local reference paths, and source-specific investigation notes must not be added to this public directory. They belong only in ignored local directories.
