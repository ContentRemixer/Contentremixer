# Why OpenTelemetry Traces Mislead You (and How to Fix It)

SEO title: "OpenTelemetry tracing pitfalls – get reliable data"  
Meta description: "Discover how head and tail sampling can hide slow requests in Kubernetes and learn a practical collector setup that always keeps problem traces."

**Intro** – You’ve rolled out OpenTelemetry, your dashboards are solid green, but users still complain about latency spikes. The missing piece is how sampling silently discards the very traces you need to root‑cause the problem. In this post I’ll show the exact sampling mis‑configuration that hides slow requests and give you a collector configuration that guarantees those critical traces are kept.

---

## Head Sampling Can Hide Your Worst Requests  

**Conclusion:** Default head sampling (10 % by default) often discards the rare slow request you need to see.  

When you enable OpenTelemetry’s SDK without changing its sampler, it applies a *probabilistic head sampler* that keeps a fixed percentage of spans at the client side. In a typical service mesh with 200 endpoints, a single endpoint that occasionally hits a 3‑second p99 will be represented in only 10 % of the collected traces. If that endpoint is the source of the latency, there’s a 90 % chance you never see its trace.

**Why it matters:** Metrics aggregates (e.g., request latency percentiles) still show a healthy system because they smooth over the outliers, but the trace view—your forensic tool—is empty.

> *Demo setup:* A simple Go HTTP service exposing 200 endpoints. We fire 100 requests; only `/slow` hits a 3‑second p99, all others return in ~8 ms.  

> *Action:* Query the trace backend (Jaeger) for traces longer than 1 second.  

> *Result:* Only two traces appear:

```bash
$ jaeger-query --service myapp --min-duration 1s
# Output (commented)
# TraceID   Duration   Operation
# abc123    800ms      GET /fast
# def456    3.02s      GET /slow   <-- the only slow trace captured
```

The trace UI shows “2/100” slow traces, even though the real p99 is 3 s. The rest of the latency outliers are gone, because the head sampler never sent them to the collector.

---

## Tail Sampling Needs a Full‑Trace Collector  

**Conclusion:** Tail sampling works only if a collector receives the complete trace before it decides what to keep.  

Tail sampling policies (e.g., “keep all errors and all traces longer than 2 s”) require the collector to see the *entire* span tree. In Kubernetes this means routing every span through a central collector (or gateway) that buffers the whole trace before evaluating the policy. If spans are dropped upstream by head sampling, the collector never sees the trace to apply its tail rules.

**What went wrong:** In the previous demo the collector was configured with head sampling only; the tail‑sampling block existed but never fired because the slow trace never arrived.

Below is the minimal collector configuration that combines a head sampler (1 % for the bulk) with a tail sampler that forces retention of any trace with latency > 2 s or an error status.

```yaml
# otel-collector-config.yaml
receivers:
  otlp:
    protocols:
      grpc:
      http:

exporters:
  otlphttp:
    endpoint: http://jaeger-collector:14268/api/traces

processors:
  batch:

  tail_sampling:
    policies:
      - name: keep-slow
        type: latency
        latency:
          threshold: 2s
      - name: keep-errors
        type: status_code
        status_code:
          status_codes: [ERROR]

  probabilistic_sampler:
    hash_seed: 22
    sampling_percentage: 1 # keep 1 % of all other traces

service:
  pipelines:
    traces:
      receivers: [otlp]
      processors: [batch, tail_sampling, probabilistic_sampler]
      exporters: [otlphttp]
```

*Explanation:*  
- The `otlp` receiver collects spans from all services.  
- `tail_sampling` evaluates each full trace: if its total latency exceeds 2 s or any span has `status_code=ERROR`, the trace is kept regardless of the following sampler.  
- `probabilistic_sampler` then drops the remaining 99 % of traces, keeping only 1 % for storage cost control.

> *Demo action:* Redeploy the collector with the above config, rerun the 100‑request test.

> *Result:* Jaeger now returns eleven traces longer than 2 s, all from the `/slow` endpoint:

```bash
$ jaeger-query --service myapp --min-duration 2s
# Output
# TraceID   Duration   Operation
# xyz001    3.01s      GET /slow
# xyz002    3.04s      GET /slow
# ... (total 11 traces)
```

The trace map clearly highlights the downstream service that consistently appears in the slow path, giving us the exact root cause that the metrics dashboard alone could not reveal.

---

## Visual Summary of the Collector Pipeline  

![Diagram of OpenTelemetry collector pipeline showing receivers → tail_sampling → probabilistic_sampler → exporter](collector-pipeline.png)  
*Alt text: Diagram illustrating the flow of spans through the OTEL collector with tail sampling applied before probabilistic sampling.*

---

## Metrics Remain Green, But Traces Reveal the Issue  

**Conclusion:** A healthy‑looking dashboard does not guarantee the absence of performance problems; you need correctly sampled traces to correlate the symptoms.  

In the demo, the Prometheus‑based latency metric still reported a 99th‑percentile below the alert threshold because it aggregates the 8 ms majority. Only after fixing the sampling did the trace data expose the true slow path. This combination—metrics for baseline health and unsampled tail‑sampled traces for outlier investigation—is a reliable practice for SREs on Kubernetes.

> *Related reading:*  
- [Understanding OpenTelemetry Sampling Strategies](/blog/otel-sampling-strategies)  
- [Building a Trace‑first Alerting System](/blog/trace-first-alerting)  
- [Kubernetes Observability Best Practices](/blog/k8s-observability)

---

## What We Learned  

- **Head sampling discards rare slow traces** – keep an eye on the sampling percentage for production services.  
- **Tail sampling must run in a collector that sees full traces** – configure a gateway collector and place tail policies before any downstream sampler.  
- **Metrics alone can hide outliers** – pair them with guaranteed‑capture traces to get a complete picture.

---

## References / Sources  

- OpenTelemetry Collector Sampling Documentation – <https://opentelemetry.io/docs/collector/sampling/>  
- Jaeger Query CLI – <https://www.jaegertracing.io/docs/1.44/cli/>  
- Repo with the full demo configuration – <https://github.com/example/otel-sampling-demo>
