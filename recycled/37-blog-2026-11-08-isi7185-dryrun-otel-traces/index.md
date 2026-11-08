# Why Your Traces Lie to You: The Hidden Dangers of OpenTelemetry Sampling
SEO title: "OpenTelemetry sampling issues"
Meta description: "Discover why your OpenTelemetry traces may be misleading you, and how to fix sampling issues to get accurate insights into your application's performance."

You deployed OpenTelemetry, your dashboards are green, and yet users are angry. Today, we find out why your traces lie to you.

Instrumentation is easy, but sampling is where it gets messy. Default head sampling keeps only 10% of your traces, which means that if one endpoint out of 200 goes slow, you might just not have that trace. Tail sampling, on the other hand, needs the whole trace in one place, which can be challenging in Kubernetes without a collector layer.

## The Problem with Head Sampling
Head sampling can hide your worst traces. With a default 10% sampling rate, you might miss critical information about slow or problematic requests. For example, if you have 100 requests and one of them takes 3 seconds to complete, there's a good chance that this slow trace will be discarded by the sampling algorithm.

```bash
# Example of head sampling configuration
sampling:
  head:
    probability: 0.1
```

## The Challenge of Tail Sampling
Tail sampling, while more accurate, requires a collector layer that sees the full trace. Without it, you might end up with incomplete or inaccurate information. In Kubernetes, this means that you need to route your traces through a gateway collector that can perform tail sampling.

```bash
# Example of tail sampling configuration
sampling:
  tail:
    policy: 
      - always: [slow, error]
      - probabilistic: {probability: 0.01}
```

## The Missing Trace: A Demo
Let's demonstrate the issue with a simple demo app. We send 100 requests to our app, with one endpoint having a p99 latency of 3 seconds. We then search the trace backend for slow traces.

```bash
# Search for slow traces
$ otel-trace search -- latency>1000
```

Result:

```
Found 2 traces out of 100
 Latency: 8ms, 3s
```

As you can see, we only found two traces, and one of them has a latency of 3 seconds. The sampling configuration threw away the evidence of the slow trace, leaving us with incomplete information.

## Fixing the Issue: Tail Sampling with a Gateway Collector
To fix this issue, we make one change: we route everything through a gateway collector with tail sampling. This way, slow traces are always kept, errors are always kept, and the rest are sampled at 1%.

```bash
# Updated collector configuration
sampling:
  tail:
    policy: 
      - always: [slow, error]
      - probabilistic: {probability: 0.01}
```

With this updated configuration, we re-run the demo and find all 11 slow traces. We also discover that they share one thing: the same downstream service.

## Visualizing the Results
The following diagram illustrates the difference between head sampling and tail sampling:
![Head Sampling vs Tail Sampling](head-vs-tail-sampling.png)
*Head Sampling vs Tail Sampling*

## What We Learned
* Head sampling can hide critical traces, leading to incomplete information.
* Tail sampling requires a collector layer that sees the full trace.
* Green dashboards don't necessarily mean there are no problems.

## References/_sources
* [OpenTelemetry documentation](https://github.com/open-telemetry/opentelemetry)
* [OpenTelemetry collector configuration](https://github.com/open-telemetry/opentelemetry-collector)
* [Repo with full config](link on screen)
