# Phase-2 persona/RAG telemetry — Weaver registry (O11y-1 / ISI-7158)

This directory holds the [OpenTelemetry Weaver](https://github.com/open-telemetry/weaver)
semantic-convention registry for the `contentremixer.persona.*` and `contentremixer.rag.*`
telemetry, plus the codegen that emits type-safe Go metric stubs.

It is seeded from the machine-readable observability spec
[`../phase2-personas-rag.observability.yaml`](../phase2-personas-rag.observability.yaml)
(ISI-7156, strategy §4.7/§3) and is the authoritative source for metric names, units,
instrument kinds, attribute enums and cardinality classes.

## Layout

```
weaver/
├── registry/              # the semantic-convention registry (checked by `weaver registry check`)
│   ├── manifest.yaml       # registry manifest (definition_manifest/2.0)
│   ├── common.yaml         # shared attributes + bounded enums
│   ├── otel-referenced.yaml# OTel attrs referenced (not owned) by custom metrics
│   ├── persona.yaml        # contentremixer.persona.* metrics
│   └── rag.yaml            # contentremixer.rag.* metrics
├── templates/go/          # Weaver codegen for the Go stubs
│   ├── weaver.yaml         # target config + JQ shaping filter
│   └── metricconv.go.j2    # MiniJinja template
└── generate.sh            # check + generate + gofmt
```

Generated output: [`app/internal/telemetry/metricconv/metricconv.go`](../../../app/internal/telemetry/metricconv/metricconv.go)
(`// Code generated … DO NOT EDIT`). The package is dependency-free (stdlib only): it
exposes metric-name constants (`MetricPersonaReconcileTotal`, …), attribute-key constants
(`AttrResult`, …), bounded enum types (`type Result string`, …), and a `Metrics` registry
with a `Lookup` helper. Instrument construction against an OTel meter is wired by the
reconciler/RAG instrumentation (O11y-2 / O11y-3) on top of these constants.

## Validate & regenerate

Requires the Weaver CLI (`weaver >= 0.28.0`):

```sh
# gate (must pass):
weaver registry check -r docs/observability/weaver/registry --future

# regenerate the Go stubs:
docs/observability/weaver/generate.sh
```

## Cardinality policy (spec §3)

| attribute        | class          | notes                                                          |
| ---------------- | -------------- | -------------------------------------------------------------- |
| `persona_name`   | bounded-at-p2  | metric label only while DevRel count is small; series-budget capped |
| `owner_subject`  | bounded-at-p2  | pseudonymous OIDC sub; hashed where cross-tenant-visible; never in log body |
| `source_url`     | unbounded      | span/log/exemplar ONLY — rejected from metric pipelines        |
| `db.query.text`  | unbounded      | span-only, parameterized                                       |
| `chunk.id`       | unbounded      | span/log/exemplar only                                         |

`owner_subject`, `source_url`, `db.query.text` and `chunk.id` are **not** metric attributes
and are therefore not emitted as metric labels by these stubs; they belong on spans/logs/
exemplars and are documented here for completeness.

## OTel-owned metrics (not redefined here)

The observability spec also lists metrics that follow upstream OpenTelemetry conventions and
are emitted by instrumentation libraries, not by hand. They are intentionally **not**
redefined in this registry (to avoid forking upstream semantics):

- `gen_ai.client.operation.duration`, `gen_ai.client.token.usage` (OTel gen_ai)
- `db.client.operation.duration`, `db.client.connection.{count,pending_requests,max}` (OTel db)

The two OTel attributes that custom metrics *do* reference (`gen_ai.request.model`,
`db.operation.name`) are mirrored in `otel-referenced.yaml` so the registry resolves and
`weaver registry check` passes offline without cloning the upstream semconv registry.
