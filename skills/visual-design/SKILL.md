---
name: visual-design
description: Build images, illustrations, and technical schemas (diagrams) for blogs, tutorials, and social - brand-consistent, accessible, lightweight. Use for any visual asset in content.
---
# Visual design skill

## Match visual to intent
- Architecture / components -> SVG diagram (brand palette)
- Interaction over time -> sequence diagram
- Decision logic -> flowchart
- UI proof -> annotated screenshot
- Concept / emotion -> illustration (flat style)

## Diagram rules
- One message per diagram; label every box and arrow (verb-labeled arrows: "sends OTLP")
- Brand palette: violet #7C3AED, magenta #EC4899, orange #F97316 on dark #1A1235 or light #F5F3FF
- Prefer Mermaid for in-blog technical diagrams (versionable in git); SVG for hero schemas
- Max 7 boxes per diagram - if more, split into two

## Screenshots
- Consistent window size (1280x800), light theme, clean profile (no personal data)
- Annotate: red box or arrow on the ONE thing to look at; caption under image
- Scrub rules apply: no tenant IDs, tokens, IPs - blur or placeholder

## Illustrations
- Flat/modern, 2-3 brand colors max, no stock-photo cliches (handshakes, glowing brains)
- Text IN images only for labels - keep prose in the article (accessibility + translation)

## Technical constraints
- WebP first, PNG for screenshots; < 200KB per image
- Alt text mandatory (function, not appearance: "Collector pipeline dropping PII before export")
- OG image per post: 1200x630, mark + title
- Render SVGs via sharp/libvips (cairosvg broken on this host)

## Checklist
- [ ] Visual type matches intent
- [ ] Brand palette respected; alt text written
- [ ] Size < 200KB; scrub rules applied
