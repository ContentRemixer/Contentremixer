# ContentRemixer 🎛️

**Create once. Publish everywhere.**

An agentic content-remixing platform for a DevRel team: reference content lives in this repo (The Crate), a GitHub issue with recycle:* labels asks the agent for new formats, and the agent opens a PR with the remixed content (The Mix). A web UI (The Booth) administers models, style profiles and publishing.

## Brand language
- **The Crate** — this repo: the reference content library (crate digging = the scraper finding gems)
- **Sample** — a source extract | **Mix/Remix** — the recycled output
- **Mastering** — the checker step (brand voice, scrub rules)
- **Sample clearance** — source attribution in every PR
- **The Booth** — the admin web UI | **The Drop** — publication
- **The Weekly Remix** — the newsletter module (the weekly set)

## Repo layout
- content/ — reference content by type (videos, blog, livestreams, podcasts), one folder per piece with meta.yaml
- styles/ — per-format style guides the writer agent follows + styles/authors/ per-author voice cards
- recycled/ — generated mixes, one folder per request, traceable to its source
- .github/ISSUE_TEMPLATE/recycle.yml — the recycle-request form

## The flow
Issue (labels: recycle:blog / recycle:short / recycle:teleprompter / recycle:tutorial) -> GitHub App -> agent (analyze -> write -> check) -> PR -> human review -> merge -> Publishing Hub suggestions.

Scrub rules (hard): no tenant IDs, no tokens, no IPs, no internal hostnames in published content.
