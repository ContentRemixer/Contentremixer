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
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  Textarea,
  useToast,
} from "../../ui";
import { ApiError } from "../../app/api";
import { getPersona, updatePersona } from "./api";
import {
  CONTENT_FORMAT_META,
  CONTENT_FORMAT_TYPES,
  PRIMARY_CONTENT_KINDS,
  type ContentFormatType,
  type DevRelPersona,
  type PrimaryContentKind,
} from "./types";

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
  const [primary, setPrimary] = useState<PrimaryContentKind | "">("");
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
        setPrimary(p.spec.primaryContentKind ?? "");
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
    if (primary !== (persona.spec.primaryContentKind ?? "")) return true;
    const saved = (persona.spec.contentFormatTargets ?? [])
      .map((t) => `${t.contentType}=${t.lengthSpec}`)
      .sort();
    const next = Object.entries(specs)
      .filter(([, v]) => v.trim() !== "")
      .map(([k, v]) => `${k}=${v.trim()}`)
      .sort();
    return JSON.stringify(saved) !== JSON.stringify(next);
  }, [bio, persona, primary, specs]);

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
          primaryContentKind: primary === "" ? undefined : primary,
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
              title="Primary content medium"
              subtitle="Your dominant output medium — conditions style synthesis and content tests."
            />
            <CardBody>
              <div className="w-56">
                <Label className="text-xs text-text-muted">Medium</Label>
                <Select
                  value={primary || "unspecified"}
                  onValueChange={(v) => setPrimary(v === "unspecified" ? "" : (v as PrimaryContentKind))}
                >
                  <SelectTrigger className="mt-1" />
                  <SelectContent>
                    <SelectItem value="unspecified">Unspecified</SelectItem>
                    {PRIMARY_CONTENT_KINDS.map((k) => (
                      <SelectItem key={k} value={k}>
                        {k}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
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
