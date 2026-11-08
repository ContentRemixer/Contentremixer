# Teleprompter — Why your traces lie to you (3:00)

**Meta:** format: teleprompter | author: henrik (synthetic dry-run, ISI-7185) | duration: 3:00 | words: ~430 | wpm: 150

## Hook (0:00-0:20)

**You deployed OpenTelemetry. Your dashboards are green. And yet — users are angry.** Today we find out why your traces lie to you.

[STAGE: open on laptop, dashboard green, cut to angry-user Slack screenshot — blurred]

## Context (0:20-1:00)

**Here's the thing. Instrumentation is easy. Sampling is where it gets messy.**

**Default head sampling keeps ten percent of your traces. Ten percent.** So when one endpoint out of two hundred goes slow, **you might just not have that trace.**

**And tail sampling? It needs the whole trace in one place. In Kubernetes, that means a collector layer.**

**Watch what happens when we get that wrong.**

[STAGE: terminal — show otel-collector config, tail_sampling block, highlight policy]

## Demo beat 1 — the missing trace (1:00-1:50)

**Setup: our demo app, a hundred requests, one endpoint p99 at three seconds.**

**Action: search the trace backend for slow ones.**

**Result: two traces. Two out of a hundred.**

**So what: the sampling config threw away the evidence. The dashboards stayed green because they aggregate metrics — but the trace story is gone.**

[STAGE: terminal, trace search showing 2/100; on-screen text: "8 ms vs 3 s — same green"]

## Demo beat 2 — fixing it (1:50-2:35)

**One change: route everything through a gateway collector with tail sampling.**

**Slow traces — always kept. Errors — always kept. The rest — one percent.**

**Same hundred requests. Now we see all eleven slow traces, and they share one thing: the same downstream service.**

[STAGE: diff of collector config; trace map highlighting the hot service]

**That's the diagnosis. Metrics told us something was wrong. Traces told us where.**

## Recap + CTA (2:35-3:00)

**Three takeaways. Head sampling can hide your worst traces. Tail sampling needs a collector that sees full traces. And green dashboards are not the absence of problems.**

**Full config is in the repo — link on screen. If this saved you an afternoon, you know what to do.**

[STAGE: end card with repo QR + subscribe prompt]
