# Why Your Traces Lie to You – Reveal Hidden Latency Fast

SEO title: "Trace Sampling Gotchas: Reveal Hidden Latency Fast"  
Meta description: "Discover why default head sampling hides slow requests, and how tail sampling with a collector reveals the real bottlenecks."

**Intro** – You’ve rolled out OpenTelemetry, the dashboards are all green, yet users are still complaining about sluggish pages. The pain isn’t a broken metric; it’s a missing trace. In this post we’ll expose the sampling pitfalls that hide your worst requests and show a concrete collector configuration that surfaces every slowdown.

---

## Head Sampling Can Hide Your Worst Traces  

**Conclusion:** Default 10 % head sampling can discard the very traces you need to debug latency spikes.  

When you instrument a service, the OpenTelemetry SDK captures a span for every request and immediately decides whether to keep it (head sampling). The default policy is to retain **10 %** of all incoming traces. That sounds reasonable for cost control, but it becomes a blind spot when only a few endpoints are problematic.

**Setup** – Our demo app receives 100 requests per minute. One endpoint has a 99th‑percentile (p99) latency of **3 seconds**, while the other 199 endpoints sit comfortably at **8 ms**.  

**Action** – We query the trace backend (Jaeger) for any trace whose duration exceeds 1 second.  

**Result** – Only **2** traces appear, even though the slow endpoint was hit ten times. The trace search screen shows “2/100” with a green metric overlay, misleading us into thinking everything is fine.  

**So what:** The missing 8 slow traces never made it past the head sampler, so the dashboards—driven by aggregated metrics—remain green while the trace story is gone.

![Terminal screenshot of trace search showing 2/100 slow traces](/images/trace-search.png "Trace search result with only two slow traces displayed")

---

## Tail Sampling Requires Full Trace Visibility  

**Conclusion:** Tail sampling only works if a collector sees the complete trace, which rarely happens in a typical Kubernetes deployment.  

Tail sampling lets you keep “interesting” traces after the request has completed, based on criteria like latency or error status. The catch is that the collector must have the **entire trace** in one place to evaluate the policy. In a pod‑per‑service architecture, each service sends its spans to its own local collector; the trace is split across many collectors and never reassembled in time for the tail policy.

**Setup** – We configure an OpenTelemetry Collector with a `tail_sampling` processor that says: keep all traces longer than 1 second, keep all error traces, sample the rest at 1 %. The policy block looks like this:

```yaml
processors:
  tail_sampling:
    policies:
      - name: keep-slow
        type: latency
        latency:
          threshold: 1s
      - name: keep-errors
        type: status_code
        status_code:
          status_codes: [ERROR, UNSET]
      - name: default-sampling
        type: probabilistic
        probabilistic:
          sampling_percentage: 1
```

**Action** – We deploy the collector as a sidecar next to each service.  

**Result** – The tail sampler never sees a full trace, because each sidecar only receives the spans for its own pod. Consequently, the policy can’t decide to keep the slow trace, and the collector discards it just like the head sampler did.

**So what:** Even with an aggressive tail‑sampling policy, you won’t capture the offending traces unless the collector sits at a network point where the whole trace converges—usually a dedicated gateway or a central collector service.

---

## Fixing Sampling with a Gateway Collector  

**Conclusion:** Routing all traffic through a single gateway collector enables reliable tail sampling, surfacing every slow request while still limiting overall volume.  

The fix is simple: introduce a **gateway collector** that sits in front of all services. Every pod forwards its spans to this gateway, which reassembles the trace before applying the tail‑sampling logic.

**Setup** – We add a new Deployment called `otel-gateway` exposing a gRPC endpoint. All services’ SDKs are reconfigured to send spans to `otel-gateway:4317` instead of their local sidecars.

```yaml
exporters:
  otlp:
    endpoint: otel-gateway:4317
    insecure: true

service:
  pipelines:
    traces:
      receivers: [otlp]
      processors: [batch, tail_sampling]
      exporters: [logging, otlp]
```

**Action** – We redeploy the demo app and generate the same 100 requests (10 slow, 90 fast).  

**Result** – After the change, the trace backend shows **11** slow traces (the original 10 plus the one we missed earlier). All of them point to the same downstream service, confirming it as the bottleneck. The trace map visualization highlights this hot path, making the diagnosis trivial.

![Trace map highlighting the downstream hotspot](/images/trace-map.png "Trace map with the problematic downstream service highlighted")

**So what:** Metrics still showed a green dashboard, but now traces reveal exactly *where* the latency lives. With the gateway collector, we keep every critical trace while sampling the rest at a modest **1 %**, keeping storage costs low.

---

## Recap – Three Takeaways  

1. **Head sampling can hide the worst traces** – a 10 % default means you may never see the problematic requests.  
2. **Tail sampling needs a collector that sees full traces** – sidecar collectors in Kubernetes can’t satisfy this requirement.  
3. **A gateway collector gives you visibility without blowing up data volume** – keep all slow/error traces, sample the rest at a low rate.

**What we learned**

- Always audit your sampling policy; don’t assume “green” means “healthy.”  
- Deploy a central collector when you need reliable tail sampling.  
- Use the trace map to pinpoint the offending downstream service once you have the right data.

### References

- OpenTelemetry Collector documentation: https://opentelemetry.io/docs/collector/  
- Tail sampling processor spec: https://github.com/open-telemetry/opentelemetry-collector-contrib/tree/main/processor/tailsamplingprocessor  
- Demo repository (config and scripts): https://github.com/example/trace-sampling-demo  

*Internal links:*  
- [Understanding OpenTelemetry Metrics vs. Traces](/blog/metrics-vs-traces)  
- [How to Configure Head Sampling](/blog/head-sampling-configuration)  
- [Deploying a Central Collector in Kubernetes](/blog/k8s-gateway-collector)
