import { useCallback, useEffect, useState } from "react";
import { useParams } from "react-router-dom";
import { Badge, Button, Card, CardBody, CardHeader, Field, Input, Textarea, useToast } from "../../ui";
import { ApiError } from "../../app/api";
import {
  getPersona,
  runContentTest,
  suggestPromotion,
  validatePersona,
} from "./api";
import { isValidated } from "./persona";
import type { ContentTestResult, DevRelPersona, PromoteResult } from "./types";

// P4 — Standalone content test (ISI-7362 D7, plan §6 from the D0 mocks): pick a
// topic, generate a sample in the persona's synthesized voice (D4 endpoint,
// honors the persona's content-format targets), then validate the style
// profile and get a suggested LinkedIn post (D9 promote — degraded when the
// deployment doesn't mount the promote route). Server-side §6.3 gates every
// call; the test itself is rate-limited (429 surfaces as a toast).
export function PersonaTestPanel() {
  const { name = "" } = useParams();
  const { toast } = useToast();
  const [persona, setPersona] = useState<DevRelPersona | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [topic, setTopic] = useState("");
  const [testing, setTesting] = useState(false);
  const [result, setResult] = useState<ContentTestResult | null>(null);
  const [validating, setValidating] = useState(false);
  const [promoting, setPromoting] = useState(false);
  const [promo, setPromo] = useState<PromoteResult | null>(null);
  const [promoDraft, setPromoDraft] = useState("");
  // Local override of the Validated condition: the last action's answer wins so
  // validate/test responses update the badge without refetching the CR.
  const [validatedOverride, setValidatedOverride] = useState<boolean | null>(null);

  const load = useCallback(() => {
    setError(null);
    getPersona(name)
      .then(setPersona)
      .catch((err) =>
        setError(err instanceof ApiError ? err.message : "Failed to load persona"),
      );
  }, [name]);

  useEffect(load, [load]);

  const validated = validatedOverride ?? (persona ? isValidated(persona) : false);

  async function runTest() {
    if (topic.trim() === "") {
      toast({ tone: "warning", title: "Enter a topic first" });
      return;
    }
    setTesting(true);
    setResult(null);
    setPromo(null);
    try {
      const out = await runContentTest(name, topic.trim());
      setResult(out);
      setValidatedOverride(out.validated);
    } catch (err) {
      toast({
        tone: err instanceof ApiError && err.status === 429 ? "neutral" : "warning",
        title: "Content test failed",
        description: err instanceof ApiError ? err.message : "Unexpected error",
      });
    } finally {
      setTesting(false);
    }
  }

  async function doValidate() {
    setValidating(true);
    try {
      await validatePersona(name);
      setValidatedOverride(true);
      toast({
        title: "Style validated",
        description: "Your persona can now be used for content generation.",
        tone: "success",
      });
      load();
    } catch (err) {
      toast({
        tone: "warning",
        title: "Could not validate",
        description: err instanceof ApiError ? err.message : "Unexpected error",
      });
    } finally {
      setValidating(false);
    }
  }

  // Promote turns the validated content into an editable LinkedIn draft (D9,
  // suggestion-only). The route is optional on a Booth without the promote
  // store: a 404 degrades to a hint instead of an error.
  async function doPromote() {
    if (!result) return;
    setPromoting(true);
    try {
      const out = await suggestPromotion({
        personaName: name,
        content: result.sample,
        title: topic.trim(),
      });
      setPromo(out);
      setPromoDraft(out.post);
    } catch (err) {
      if (err instanceof ApiError && err.status === 404) {
        toast({
          tone: "neutral",
          title: "Promotion suggestions are not available on this deployment",
          description: "The Booth build has no promote route mounted.",
        });
      } else if (err instanceof ApiError && err.status === 409) {
        toast({
          tone: "warning",
          title: "Validate your style first",
          description: err.message,
        });
      } else {
        toast({
          tone: "warning",
          title: "Could not suggest a promotion post",
          description: err instanceof ApiError ? err.message : "Unexpected error",
        });
      }
    } finally {
      setPromoting(false);
    }
  }

  if (error) {
    return (
      <div className="flex flex-col items-center gap-3 py-12">
        <p className="text-sm text-text-muted">{error}</p>
        <Button variant="secondary" size="sm" onClick={load}>
          Retry
        </Button>
      </div>
    );
  }

  if (!persona) {
    return <div className="py-12 text-center text-sm text-text-muted">Loading persona…</div>;
  }

  const targets = persona.spec.contentFormatTargets ?? [];

  return (
    <div className="flex flex-col gap-6">
      <header className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 className="text-xl font-semibold text-text">Content test — {name}</h1>
          <p className="mt-1 text-sm text-text-muted">
            Generate a topic sample in your voice, then validate it (or ask for changes in
            Sources / Extra context).
          </p>
        </div>
        <Badge tone={validated ? "success" : "warning"}>
          {validated ? "Style validated" : "Style not validated"}
        </Badge>
      </header>

      <Card>
        <CardHeader
          title="Pick a topic"
          subtitle="Each run is a live model call conditioned on your synthesized style profile."
        />
        <CardBody className="flex flex-col gap-3">
          <Field label="Topic">
            {(ctrl) => (
              <Input
                {...ctrl}
                value={topic}
                onChange={(e) => setTopic(e.target.value)}
                placeholder="e.g. Observability for serverless workloads"
                onKeyDown={(e) => {
                  if (e.key === "Enter" && !testing) runTest();
                }}
              />
            )}
          </Field>
          {targets.length > 0 ? (
            <p className="text-xs text-text-muted">
              Aiming for your {persona.spec.primaryContentKind || "content"} targets:{" "}
              {targets.map((t) => `${t.contentType} (${t.lengthSpec})`).join(" · ")}
            </p>
          ) : null}
          <div>
            <Button loading={testing} onClick={runTest}>
              Generate sample
            </Button>
          </div>
        </CardBody>
      </Card>

      {result ? (
        <Card raised>
          <CardHeader
            title="Generated sample"
            subtitle={`Served by ${result.modelUsed}`}
            action={
              <div className="flex gap-2">
                {!result.validated ? (
                  <Button size="sm" variant="secondary" loading={validating} onClick={doValidate}>
                    Validate my style
                  </Button>
                ) : null}
                <Button size="sm" variant="secondary" loading={promoting} onClick={doPromote}>
                  Suggest LinkedIn post
                </Button>
              </div>
            }
          />
          <CardBody>
            <p className="whitespace-pre-wrap rounded-md bg-surface-2 p-4 text-sm text-text">
              {result.sample}
            </p>
            {result.validated ? (
              <p className="mt-3 text-xs text-text-muted">
                Your style profile is validated — generation for your persona is enabled.
              </p>
            ) : (
              <p className="mt-3 text-xs text-text-muted">
                Happy with the voice above? Validate to accept the synthesized style profile.
              </p>
            )}
          </CardBody>
        </Card>
      ) : null}

      {promo ? (
        <Card>
          <CardHeader
            title="Suggested LinkedIn post"
            subtitle={`Editable draft by ${promo.modelUsed} — v1 never auto-publishes.`}
            action={
              <Button
                size="sm"
                variant="secondary"
                onClick={() => navigator.clipboard?.writeText(promoDraft)}
              >
                Copy draft
              </Button>
            }
          />
          <CardBody className="flex flex-col gap-3">
            <Field label="Draft — edit freely before posting" hint="Nothing leaves Booth; paste it into LinkedIn yourself.">
              {(ctrl) => (
                <Textarea {...ctrl} rows={10} value={promoDraft} onChange={(e) => setPromoDraft(e.target.value)} />
              )}
            </Field>
            <p className="whitespace-pre-wrap border-l-2 border-border pl-3 text-xs text-text-muted">
              Hook: {promo.hook}
              {"\n"}CTA: {promo.cta}
            </p>
          </CardBody>
        </Card>
      ) : null}
    </div>
  );
}
