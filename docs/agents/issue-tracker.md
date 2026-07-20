# Issue Tracker: GitHub

Issues and PRDs for this repository live in GitHub Issues. Run `gh` commands inside this repository so the remote is inferred automatically.

## Operations

- Create: `gh issue create --title "..." --body "..."`
- Read: `gh issue view <number> --comments`
- List: `gh issue list --state open --json number,title,body,labels,comments`
- Comment: `gh issue comment <number> --body "..."`
- Label: `gh issue edit <number> --add-label "..."`
- Close: `gh issue close <number> --comment "..."`

When a skill says to publish to the issue tracker, create a GitHub issue. When it asks for a ticket, retrieve the corresponding GitHub issue.

## Pull Requests

PRs as a request surface: no.

## Dependencies

Prefer GitHub sub-issues and native issue dependencies. If unavailable, record `Part of #<number>` and `Blocked by: #<number>` in issue bodies.
