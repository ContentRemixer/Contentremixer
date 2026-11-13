import { useCallback, useEffect, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { Badge, Button, Card, CardBody, CardHeader, useToast } from "../../ui";
import { EmptyState } from "../../components/states/EmptyState";
import { ApiError } from "../../app/api";
import { PersonaIcon } from "../../components/icons";
import { useAuth } from "../../app/auth";
import { getStyleProfile, isNoProfileYet, listPersonas } from "./api";
import { exemplarCount, isReady, isValidated, phase, phaseTone } from "./persona";
import type { DevRelPersona, StyleProfile } from "./types";

// P1 — My Persona overview (ISI-7362 D7, plan §6 from the D0 mocks): the
// returning DevRel's persona library. Lists the caller's own personas (the
// store scopes by §6.3), renders the ingestion phase + Ready/Validated badges,
// and expands the selected persona into its sources, synthesized style
// profile, and extra-context summary, with direct links into the P2/P3/P4
// editors. Server-side authz is authoritative; every underlying call is gated.
export function PersonaOverview() {
  const { session } = useAuth();
  const { toast } = useToast();
  const navigate = useNavigate();
  const [personas, setPersonas] = useState<DevRelPersona[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [selected, setSelected] = useState<string | null>(null);

  const load = useCallback(() => {
    setPersonas(null);
    setError(null);
    listPersonas()
      .then((rows) => {
        setPersonas(rows ?? []);
        // Self-service owns at most a handful; default-select the first so the
        // overview reads "My Persona" without an extra click.
        setSelected((cur) => cur ?? rows?.[0]?.metadata.name ?? null);
      })
      .catch((err) =>
        setError(err instanceof ApiError ? err.message : "Failed to load personas"),
      );
  }, []);

  useEffect(load, [load]);

  const persona = personas?.find((p) => p.metadata.name === selected) ?? null;

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

  if (!personas) {
    return <div className="py-12 text-center text-sm text-text-muted">Loading personas…</div>;
  }

  return (
    <div className="flex flex-col gap-6">
      <header className="flex items-start justify-between gap-3">
        <div className="flex items-start gap-3">
          <span className="mt-0.5 grid h-10 w-10 shrink-0 place-items-center rounded-md bg-primary/10 text-primary">
            <PersonaIcon size={22} />
          </span>
          <div>
            <h1 className="text-xl font-semibold text-text">My Personas</h1>
            <p className="mt-1 text-sm text-text-muted">
              Your creative identity — sources, synthesized writing style, and content tests.
            </p>
          </div>
        </div>
        {session?.role === "Admin" ? (
          <Badge tone="info">Admin view — every persona</Badge>
        ) : undefined}
      </header>

      {personas.length === 0 ? (
        <EmptyState
          icon={PersonaIcon}
          title="No persona yet"
          description="Your persona is created by the DevRel onboarding wizard — add your content sources and the platform will learn your writing style."
        />
      ) : (
        <div className="flex flex-col gap-4 lg:flex-row">
          <div className="flex w-full shrink-0 flex-col gap-2 lg:w-72">
            {personas.map((p) => (
              <button
                key={p.metadata.name}
                className={
                  "rounded-lg border p-4 text-left transition-colors " +
                  (p.metadata.name === selected
                    ? "border-primary bg-primary/5"
                    : "border-border bg-surface hover:border-border-strong")
                }
                onClick={() => setSelected(p.metadata.name)}
              >
                <div className="flex items-center justify-between gap-2">
                  <span className="truncate text-sm font-semibold text-text">
                    {p.metadata.name}
                  </span>
                  <Badge tone={phaseTone(p)} dot>
                    {phase(p)}
                  </Badge>
                </div>
                <div className="mt-2 flex gap-1.5">
                  {isReady(p) ? (
                    <Badge tone="success">Ready</Badge>
                  ) : (
                    <Badge tone="neutral">Not ready</Badge>
                  )}
                  {isValidated(p) ? (
                    <Badge tone="success">Validated</Badge>
                  ) : (
                    <Badge tone="warning">Not validated</Badge>
                  )}
                </div>
              </button>
            ))}
          </div>

          {persona ? <PersonaDetail persona={persona} toast={toast} navigate={navigate} /> : null}
        </div>
      )}
    </div>
  );
}

function PersonaDetail({
  persona,
  toast,
  navigate,
}: {
  persona: DevRelPersona;
  toast: ReturnType<typeof useToast>["toast"];
  navigate: ReturnType<typeof useNavigate>;
}) {
  const name = persona.metadata.name;
  const [profile, setProfile] = useState<StyleProfile | null>(null);
  const [profileLoading, setProfileLoading] = useState(true);

  // The synthesized style profile (D3) is a separate read; 404 means
  // "no profile yet" (O7 pre-synthesis) which renders inline, not as an error.
  useEffect(() => {
    let alive = true;
    setProfileLoading(true);
    getStyleProfile(name)
      .then((sp) => alive && setProfile(sp))
      .catch((err) => {
        if (!alive) return;
        if (!isNoProfileYet(err)) {
          toast({
            tone: "warning",
            title: "Could not load the style profile",
            description: err instanceof ApiError ? err.message : "Unexpected error",
          });
        }
        setProfile(null);
      })
      .finally(() => alive && setProfileLoading(false));
    return () => {
      alive = false;
    };
  }, [name, toast]);

  const refs = persona.spec.references ?? [];
  const targets = persona.spec.contentFormatTargets ?? [];

  return (
    <div className="flex min-w-0 flex-1 flex-col gap-4">
      <Card>
        <CardBody className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h2 className="text-base font-semibold text-text">{name}</h2>
            <p className="mt-0.5 text-xs text-text-muted">
              {refs.length} source{refs.length === 1 ? "" : "s"} ·{" "}
              {exemplarCount(persona)} embedded exemplar
              {exemplarCount(persona) === 1 ? "" : "s"}
            </p>
          </div>
          <div className="flex flex-wrap gap-1.5">
            <Badge tone={phaseTone(persona)} dot>
              {phase(persona)}
            </Badge>
            {isReady(persona) ? (
              <Badge tone="success">Ready</Badge>
            ) : (
              <Badge tone="neutral">Not ready</Badge>
            )}
            {isValidated(persona) ? (
              <Badge tone="success">Validated</Badge>
            ) : (
              <Badge tone="warning">Not validated</Badge>
            )}
          </div>
        </CardBody>
      </Card>

      <div className="grid gap-4 xl:grid-cols-2">
        <Card>
          <CardHeader
            title="Sources"
            subtitle="Style exemplars the platform ingests"
            action={
              <Button size="sm" variant="secondary" onClick={() => navigate(`/personas/${name}/sources`)}>
                Edit / re-ingest
              </Button>
            }
          />
          <CardBody className="flex flex-col gap-2">
            {refs.length === 0 ? (
              <p className="text-sm text-text-muted">No sources yet — add some via Edit.</p>
            ) : (
              refs.map((r, i) => (
                <div key={`${r.url}-${i}`} className="flex items-start justify-between gap-3">
                  <span className="min-w-0 flex-1 truncate text-sm text-text">{r.url}</span>
                  <Badge>{r.kind}</Badge>
                </div>
              ))
            )}
          </CardBody>
        </Card>

        <Card raised>
          <CardHeader
            title="Style profile"
            subtitle="What the platform recognized in your writing"
            action={
              profile?.validated ? (
                <Badge tone="success">Validated</Badge>
              ) : (
                <Badge tone="warning">Awaiting validation</Badge>
              )
            }
          />
          <CardBody>
            {profileLoading ? (
              <p className="text-sm text-text-muted">Loading style profile…</p>
            ) : profile ? (
              <div className="flex flex-col gap-3">
                <div>
                  <p className="text-xs font-medium uppercase tracking-wide text-text-muted">Traits</p>
                  <p className="mt-1 whitespace-pre-wrap text-sm text-text">{profile.traits}</p>
                </div>
                <div>
                  <p className="text-xs font-medium uppercase tracking-wide text-text-muted">Sample</p>
                  <p className="mt-1 whitespace-pre-wrap rounded-md bg-surface-2 p-3 text-sm text-text">
                    {profile.sample}
                  </p>
                </div>
                <p className="text-xs text-text-muted">
                  Synthesized from {profile.sourceCount} source
                  {profile.sourceCount === 1 ? "" : "s"}
                </p>
              </div>
            ) : (
              <div className="flex flex-col items-start gap-2">
                <p className="text-sm text-text-muted">
                  No style profile yet — once ingestion finishes, style synthesis produces your
                  traits and sample here.
                </p>
                <Button size="sm" onClick={() => navigate(`/personas/${name}/sources`)}>
                  Review sources
                </Button>
              </div>
            )}
          </CardBody>
        </Card>

        <Card>
          <CardHeader
            title="Extra context"
            subtitle="Bio, primary medium, and output targets"
            action={
              <Button size="sm" variant="secondary" onClick={() => navigate(`/personas/${name}/bio`)}>
                Edit
              </Button>
            }
          />
          <CardBody className="flex flex-col gap-2">
            <p className="whitespace-pre-wrap text-sm text-text">
              {persona.spec.selfDescription?.trim() || (
                <span className="text-text-muted">No bio yet — add one via Edit.</span>
              )}
            </p>
            <div className="flex flex-wrap gap-1.5">
              {persona.spec.primaryContentKind ? (
                <Badge tone="primary">Primary: {persona.spec.primaryContentKind}</Badge>
              ) : null}
              {targets.map((t) => (
                <Badge key={t.contentType}>
                  {t.contentType}: {t.lengthSpec}
                </Badge>
              ))}
            </div>
          </CardBody>
        </Card>

        <Card>
          <CardHeader title="Content test" subtitle="Try your persona on a live topic" />
          <CardBody>
            <p className="text-sm text-text-muted">
              Generate a sample in your voice, validate the style, and get a suggested LinkedIn
              post.
            </p>
            <Button className="mt-3" size="sm" onClick={() => navigate(`/personas/${name}/test`)}>
              Run a content test
            </Button>
          </CardBody>
        </Card>
      </div>

      <p className="text-xs text-text-muted">
        Need to review scraped chunks?{" "}
        <Link to="/scraper-review" className="text-primary hover:underline">
          Scraper review
        </Link>{" "}
        accepts them into your corpus.
      </p>
    </div>
  );
}
