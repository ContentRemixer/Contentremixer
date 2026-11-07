---
name: technical-tutorial
description: Build structured, reproducible technical tutorials - prerequisites, step-by-step labs with expected output, troubleshooting. Use for how-to content.
---
# Technical tutorial skill

## Skeleton
1. Goal statement (what you will build/achieve - with a screenshot of the end state)
2. Prerequisites box: versions, accounts, cost estimate, time estimate
3. Steps: numbered, each with (a) the command, (b) expected output, (c) one-line why
4. Verification step at the end ("success looks like...")
5. Troubleshooting: the 3 most common failures with symptoms and fixes
6. Cleanup/revert section

## Rules
- One new concept per step; progressive difficulty
- Commands idempotent where possible; full paths; no "cd around" assumptions
- Every step tested in a clean environment (container/VM) before publishing
- Never "simply" / "just" - if it needs a qualifier, it needs an explanation

## Checklist
- [ ] A reader with only the prerequisites reaches the end state without external help
- [ ] Expected output shown for every command
- [ ] Versions pinned (no "latest" in critical steps)
