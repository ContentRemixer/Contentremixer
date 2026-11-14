import { useCallback, useEffect, useMemo, useState } from "react";
import { Link, useParams } from "react-router-dom";
import {
  Badge,
  Button,
  Card,
  CardBody,
  CardHeader,
  Field,
  Input,
  Label,
  Textarea,
  cx,
  useToast,
} from "../../ui";
import { CheckIcon } from "../../components/icons";
import { ApiError } from "../../app/api";
import { toggleKind, makePrimary } from "../onboarding/contentKinds";
import { getPersona, updatePersona } from "./api";
import {
  CONTENT_FORMAT_META,
  CONTENT_FORMAT_TYPES,
  PRIMARY_CONTENT_KINDS,
  type ContentFormatType,
  type DevRelPersona,
  type PrimaryContentKind,
} from "./types";

// kindsOf derives the editable multi-select set from a persona: prefer the
// stored contentKinds (ISI-7424), falling back to the legacy single primary so
// personas created before the multi-select still populate the control.
function kindsOf(spec: DevRelPersona["spec"]): PrimaryContentKind[] {
  if (spec.contentKinds && spec.contentKinds.length > 0) {
    const set = spec.contentKinds;
    // Keep the stored primary first so "primary = first selected" holds on load.
    const primary = spec.primaryContentKind;
    if (primary && set.includes(primary)) {
      return [primary, ...set.filter((k) => k !== primary)];
    }
    return set;
  }
  return spec.primaryContentKind ? [spec.primaryContentKind] : [];
}

// P3 — Edit extra context / bio (ISI-7362 D7, plan §6 from the D0 mocks): the
// persona's free-text voice anchor (spec.selfDescription — the onboarding O4
// box, reused per the D1 contract), its dominant output medium
// (spec.primaryContentKind, O2), and the per-content-type output length/format
// targets (spec.contentFormatTargets, D1b — "editable later" is this screen).
// Full-PUT semantics: the complete spec round-trips with only these fields
// replaced.
export function PersonaBioPanel() {
  const { name = "" } = useParams();
  const { toast } = useToast();
  const [persona, setPersona] = useState<DevRelPersona | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [bio, setBio] = useState("");
  // Multi-select set (ISI-7424); kinds[0] is the derived primary.
  const [kinds, setKinds] = useState<PrimaryContentKind[]>([]);
  // One lengthSpec per content type; empty = target omitted.
  const [specs, setSpecs] = useState<Record<ContentFormatType, string>>({
    blog: "",
    "youtube-short": "",
    "youtube-video": "",
    "linkedin-post": "",
  });
  const [saving, setSaving] = useState(false);

  const load = useCallback(() => {
    setError(null);
    getPersona(name)
      .then((p) => {
        setPersona(p);
        setBio(p.spec.selfDescription ?? "");
        setKinds(kindsOf(p.spec));
        setSpecs({
          blog: "",
          "youtube-short": "",
          "youtube-video": "",
          "linkedin-post": "",
          ...Object.fromEntries(
            (p.spec.contentFormatTargets ?? []).map((t) => [t.contentType, t.lengthSpec]),
          ),
        });
      })
      .catch((err) =>
        setError(err instanceof ApiError ? err.message : "Failed to load persona"),
      );
  }, [name]);

  useEffect(load, [load]);

  const dirty = useMemo(() => {
    if (!persona) return false;
    if (bio !== (persona.spec.selfDescription ?? "")) return true;
    // Order-sensitive compare: re-designating the primary (reorder) is a change
    // even when the set is identical.
    if (JSON.stringify(kinds) !== JSON.stringify(kindsOf(persona.spec))) return true;
    const saved = (persona.spec.contentFormatTargets ?? [])
      .map((t) => `${t.contentType}=${t.lengthSpec}`)
      .sort();
    const next = Object.entries(specs)
      .filter(([, v]) => v.trim() !== "")
      .map(([k, v]) => `${k}=${v.trim()}`)
      .sort();
    return JSON.stringify(saved) !== JSON.stringify(next);
  }, [bio, persona, kinds, specs]);

  async function save() {
    if (!persona) return;
    // lengthSpec has MinLength=1 on the wire; blank rows are omitted targets.
    const targets = CONTENT_FORMAT_TYPES.filter((t) => specs[t].trim() !== "").map((t) => ({
      contentType: t,
      lengthSpec: specs[t].trim(),
    }));
    setSaving(true);
    try {
      const updated = await updatePersona(name, {
        ...persona,
        spec: {
          ...persona.spec,
          selfDescription: bio.trim() === "" ? undefined : bio,
          // Send BOTH: the full set and the derived primary (= first). The CRD
          // drops set order, so the primary rides along explicitly; the webhook
          // requires it to be a member of a non-empty contentKinds.
          contentKinds: kinds.length === 0 ? undefined : kinds,
          primaryContentKind: kinds[0] ?? undefined,
          contentFormatTargets: targets.length === 0 ? undefined : targets,
        },
      });
      setPersona(updated);
      toast({ title: "Extra context saved", tone: "success" });
    } catch (err) {
      toast({
        tone: "warning",
        title: "Could not save extra context",
        description: err instanceof ApiError ? err.message : "Unexpected error",
      });
    } finally {
      setSaving(false);
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

  return (
    <div className="flex flex-col gap-6">
      <header className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 className="text-xl font-semibold text-text">Extra context — {name}</h1>
          <p className="mt-1 text-sm text-text-muted">
            Who you are and how you write — used by every generation.
          </p>
        </div>
        <div className="flex gap-2">
          <Button variant="secondary" onClick={load}>
            Reset
          </Button>
          <Button loading={saving} disabled={!dirty} onClick={save}>
            Save changes
          </Button>
        </div>
      </header>

      <div className="grid gap-4 xl:grid-cols-2">
        <Card>
          <CardHeader
            title="Bio / extra context"
            subtitle="The free-text persona box (onboarding O4) — everything the agents should know about you and your voice."
          />
          <CardBody>
            <Field
              label="Bio"
              hint="e.g. “I’m a developer advocate producing deep-dive content to help the community understand how to measure systems…”"
            >
              {(ctrl) => (
                <Textarea
                  {...ctrl}
                  rows={8}
                  value={bio}
                  onChange={(e) => setBio(e.target.value)}
                  placeholder="Introduce yourself the way you would to your audience…"
                />
              )}
            </Field>
          </CardBody>
        </Card>

        <div className="flex flex-col gap-4">
          <Card>
            <CardHeader
              title="Content mediums"
              subtitle="Everything you produce — conditions style synthesis and content tests. Your first pick is primary; re-designate it any time."
            />
            <CardBody>
              <div className="grid gap-2 sm:grid-cols-2">
                {PRIMARY_CONTENT_KINDS.map((k) => {
                  const selected = kinds.includes(k);
                  const isPrimary = kinds[0] === k;
                  const showPrimary = kinds.length >= 2;
                  return (
                    <div
                      key={k}
                      className={cx(
                        "flex flex-col rounded-md border transition-colors",
                        selected
                          ? "border-accent bg-surface-2 ring-1 ring-accent"
                          : "border-border bg-surface hover:border-border-strong",
                      )}
                    >
                      <button
                        type="button"
                        aria-pressed={selected}
                        onClick={() => setKinds((prev) => toggleKind(prev, k))}
                        className="flex items-center justify-between gap-2 rounded-md px-3 py-2 text-left text-sm capitalize text-text"
                      >
                        <span className="flex items-center gap-2">
                          {k}
                          {showPrimary && isPrimary ? <Badge tone="primary">Primary</Badge> : null}
                        </span>
                        {selected ? <CheckIcon width={15} height={15} /> : null}
                      </button>
                      {showPrimary && selected && !isPrimary ? (
                        <button
                          type="button"
                          onClick={() => setKinds((prev) => makePrimary(prev, k))}
                          className="mx-3 mb-2 self-start text-xs text-text-muted hover:text-text"
                        >
                          ☆ Set primary
                        </button>
                      ) : null}
                    </div>
                  );
                })}
              </div>
              <p className="mt-3 text-xs text-text-muted">
                {kinds.length === 0 ? "No mediums selected." : null}
              </p>
            </CardBody>
          </Card>

          <Card>
            <CardHeader
              title="Content-type targets"
              subtitle="Soft, aim-for length/format per output medium — the writer steers toward them, nothing is hard-truncated."
            />
            <CardBody className="flex flex-col gap-3">
              {CONTENT_FORMAT_TYPES.map((t) => (
                <div key={t}>
                  <Label htmlFor={`spec-${t}`} className="text-sm font-medium text-text">
                    {t} <span className="font-normal text-text-muted">— {CONTENT_FORMAT_META[t]}</span>
                  </Label>
                  <Input
                    id={`spec-${t}`}
                    className="mt-1"
                    placeholder='e.g. "~1500 words"'
                    value={specs[t]}
                    onChange={(e) => setSpecs((s) => ({ ...s, [t]: e.target.value }))}
                  />
                </div>
              ))}
              <p className="text-xs text-text-muted">
                Added targets:{" "}
                {CONTENT_FORMAT_TYPES.filter((t) => specs[t].trim() !== "").length > 0
                  ? CONTENT_FORMAT_TYPES.filter((t) => specs[t].trim() !== "").map((t) => (
                      <Badge key={t} className="ml-1">
                        {t}
                      </Badge>
                    ))
                  : "none"}
              </p>
            </CardBody>
          </Card>
        </div>
      </div>

      <p className="text-xs text-text-muted">
        Changes here don't re-ingest sources — use{" "}
        <Link to={`/personas/${name}/sources`} className="text-primary hover:underline">
          Sources
        </Link>{" "}
        for that.
      </p>
    </div>
  );
}
