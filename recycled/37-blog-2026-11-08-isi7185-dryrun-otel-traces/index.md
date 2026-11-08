# OpenTelemetry Pitfalls: Why Your Traces Lie to You
SEO title: "Traces Lie: OpenTelemetry Sampling Pitfalls"
Meta description: "Discover why your OpenTelemetry traces might be misleading you, even with green dashboards. Learn about head and tail sampling pitfalls in this SRE guide."

You deployed OpenTelemetry, your dashboards are green, and yet users are angry. Today, we find out why your traces lie to you.

## The Problem with Sampling
Instrumentation is easy, but sampling is where it gets messy. Default head sampling keeps only 10% of your traces, which means you might miss critical information, like a slow endpoint out of two hundred.

```yml
# Example of head sampling configuration
sampling:
  head:
    probability: 0.1
```

![Head sampling configuration](/head-sampling-config.png)

## The Missing Trace
Let's demo this issue. Our setup includes a demo app, a hundred requests, and one endpoint with a p99 latency of three seconds. When searching the trace backend for slow ones, we find only two traces.

```bash
# Searching for slow traces
$ otel-trace search -- latency > 1s
2 traces found
```

![Missing slow traces](/missing-slow-traces.png)

## The Issue with Tail Sampling
Tail sampling needs the whole trace in one place, which in Kubernetes, requires a collector layer. If not configured correctly, you'll miss critical traces.

```yml
# Example of tail sampling configuration
sampling:
  tail:
    policy: always
    collector: otel-collector
```

## Fixing the Issue
One change: route everything through a gateway collector with tail sampling. Now, slow traces and errors are always kept, and the rest are sampled at 1%.

```diff
# Diff of collector config
-  sampling:
-    head:
-      probability: 0.1
+  sampling:
+    tail:
+      policy: always
+      collector: otel-collector-gateway
```

![Fixed collector configuration](/fixed-collector-config.png)

## What We Learned
* Head sampling can hide your worst traces.
* Tail sampling needs a collector that sees full traces.
* Green dashboards are not the absence of problems.

## References
* [OpenTelemetry documentation](https://github.com/open-telemetry/opentelemetry)
* [OpenTelemetry Collector configuration](https://github.com/open-telemetry/opentelemetry-collector)
* [Kubernetes documentation](https://kubernetes.io/docs/home/)
