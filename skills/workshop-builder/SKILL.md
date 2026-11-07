---
name: workshop-builder
description: Design workshop instructions and GitHub Codespaces environments - timed agendas, checkpointed labs, reproducible devcontainers. Use for live training content.
---
# Workshop builder skill

## Instructions structure
- Objectives (3 max, measurable) + audience + prerequisites
- Timed agenda table (welcome, setup check, labs, wrap)
- Labs: numbered, each with a checkpoint ("you should see X") before moving on
- Solutions folded/separate (attendees attempt first)
- Cleanup instructions + what to keep (their codespace, their fork)

## Codespaces environment
- devcontainer.json reproducible: pin tool versions; postCreateCommand < 5 min on cold start
- Prebuild the image; test cold-start timing before the event
- Secrets via Codespaces secrets or runtime prompts - NEVER in the repo (see ContentRemixer pattern: dispenser + env vars)
- Every lab starts from a known state: git tag or branch per lab (lab-1, lab-2...)
- Include a smoke-test script attendees run first (proves the env is alive)

## Facilitation
- One helper prompt per lab displayed on slide (QR to the repo)
- Plan B for: no network, cold-start failures, attendee behind (skip-to tags)

## Checklist
- [ ] Cold start to first checkpoint < 10 minutes total
- [ ] Zero secrets in repo (grep for tokens/keys)
- [ ] Each lab verified from a fresh codespace
