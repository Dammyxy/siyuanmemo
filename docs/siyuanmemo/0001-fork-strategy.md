# SiYuanMemo Fork Strategy

Date: 2026-07-19

## Decision

SiYuanMemo starts as a SiYuan-derived AGPL-3.0 project instead of a clean-room reimplementation.

The project will reuse SiYuan's existing local kernel, desktop shell, block editor, block model, backlinks, indexing, and workspace foundations. SiYuanMemo's first major product addition is a SuperMemo-like progressive reading layer.

## Product Boundary

SiYuanMemo keeps two separate knowledge layers:

- Topic layer: progressive reading materials, imported articles, extracted child topics, split topics, scheduling, and source state.
- Note layer: SiYuan-like block notes, backlinks, block references, documents, and user-authored knowledge.

Topics are not note blocks. Imported material and extracted topics must not automatically enter the note block tree. A topic or selected range can be promoted into note blocks only through an explicit user action.

## Implementation Direction

Initial development should preserve SiYuan's existing architecture and avoid early broad rewrites:

- Keep the Go kernel and TypeScript frontend structure.
- Keep the Electron desktop shell.
- Keep the existing editor and backlink system intact while validating the fork.
- Add Topic as a new module rather than forcing Topic into the existing block model.
- Use existing block APIs when promoting Topic material into notes.

## GitHub Fork Setup

`Dammyxy/siyuanmemo` is the renamed GitHub fork of `siyuan-note/siyuan`. The local repository keeps:

- `origin`: `https://github.com/Dammyxy/siyuanmemo.git`
- `upstream`: `https://github.com/siyuan-note/siyuan.git`

This preserves GitHub's fork relationship while keeping upstream tracking explicit in local Git config.
