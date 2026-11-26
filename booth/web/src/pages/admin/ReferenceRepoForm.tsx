import { useState } from "react";
import {
  Badge,
  Button,
  Checkbox,
  Dialog,
  DialogBody,
  DialogContent,
  DialogFooter,
  Field,
  Input,
  Label,
  useToast,
} from "../../ui";
import { api, ApiError } from "../../app/api";
import type { CrObject } from "./CrPanel";
import {
  RoutesFields,
  emptyRoute,
  routesSpec,
  toRoutes,
  validateRoutes,
  type Route,
} from "./LabelRoutesEditor";

// GITHUB_APP_SLUG is the ContentRemixer org's recycler App (app-id 5934686,
// ISI-7836). Onboarding a repo = installing this App on it, which fires the
// `installation` webhook the operator records as status.appInstalled (ISI-7895).
// One stable App → a constant is the single source of truth; a build-time
// override covers a future rename without a code change. The install surface is
// GitHub's standard App-install flow (there is no per-repo preselect URL param).
const GITHUB_APP_SLUG =
  (import.meta.env.VITE_GITHUB_APP_SLUG as string | undefined)?.trim() || "contentremixer-recycler";
const INSTALL_APP_URL = `https://github.com/apps/${GITHUB_APP_SLUG}/installations/new`;

function isAppInstalled(repo: CrObject): boolean {
  return (repo.status as Record<string, unknown> | undefined)?.appInstalled === true;
}

// Bespoke create/edit form for ReferenceRepo (ISI-7782, W1 of ISI-7776). The
// generic CrPanel flat-create cannot express the nested tokenRef /
// webhookSecretRef / prTarget / labelRoutes, so this form owns those sections.
//
// Boundary with W2 (ISI-7783, Secret writing): secret *values* are never entered
// here. On CREATE the form references pre-existing Secret name+key (the Secret is
// provisioned by W2/GitOps). On EDIT the Secret refs are preserved untouched and
// not surfaced — matching the ISI-7514 round-trip contract — so this form only
// edits repo / prTarget / labelRoutes. The server stays authoritative: the §6.3
// store gates writes to Admins and the admission webhook re-validates; its
// errors surface inline below.

interface FormState {
  name: string;
  repo: string;
  tokenName: string;
  tokenKey: string;
  webhookName: string;
  webhookKey: string;
  baseBranch: string;
  branchPrefix: string;
  draft: boolean;
  routes: Route[];
}

// REPO_PATTERN mirrors the CRD: owner/name, no slashes or whitespace in either.
const REPO_PATTERN = /^[^/\s]+\/[^/\s]+$/;
// NAME_PATTERN is the metadata.name (RFC 1123 label) the apiserver enforces.
const NAME_PATTERN = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/;

function initialState(repo: CrObject | null): FormState {
  const spec = repo?.spec ?? {};
  const pr = (spec.prTarget as Record<string, unknown> | undefined) ?? {};
  return {
    name: repo?.metadata.name ?? "",
    repo: typeof spec.repo === "string" ? spec.repo : "",
    tokenName: "",
    tokenKey: "",
    webhookName: "",
    webhookKey: "",
    baseBranch: typeof pr.baseBranch === "string" && pr.baseBranch ? pr.baseBranch : "main",
    branchPrefix: typeof pr.branchPrefix === "string" && pr.branchPrefix ? pr.branchPrefix : "recycle/",
    draft: pr.draft === true,
    routes: repo ? toRoutes(repo) : [emptyRoute()],
  };
}

// validate returns the first blocking problem, or null when the form is savable.
// create requires the Secret refs; edit preserves them so they are not checked.
function validate(f: FormState, mode: "create" | "edit"): string | null {
  if (mode === "create") {
    if (!f.name.trim()) return "A name is required.";
    if (!NAME_PATTERN.test(f.name.trim())) {
      return "Name must be lowercase alphanumeric or '-', starting and ending alphanumeric.";
    }
  }
  if (!REPO_PATTERN.test(f.repo.trim())) return 'Repo must be "owner/name".';
  if (mode === "create") {
    if (!f.tokenName.trim() || !f.tokenKey.trim()) return "The GitHub token Secret name and key are required.";
    if (!f.webhookName.trim() || !f.webhookKey.trim()) return "The webhook Secret name and key are required.";
  }
  return validateRoutes(f.routes);
}

export function NewReferenceRepoButton({ path, onSaved }: { path: string; onSaved: () => void }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button size="sm" onClick={() => setOpen(true)}>
        New
      </Button>
      {open && (
        <ReferenceRepoDialog
          mode="create"
          repo={null}
          path={path}
          onClose={() => setOpen(false)}
          onSaved={() => {
            setOpen(false);
            onSaved();
          }}
        />
      )}
    </>
  );
}

export function EditReferenceRepoButton({
  repo,
  path,
  onSaved,
}: {
  repo: CrObject;
  path: string;
  onSaved: () => void;
}) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button variant="ghost" size="sm" onClick={() => setOpen(true)}>
        Edit
      </Button>
      {open && (
        <ReferenceRepoDialog
          mode="edit"
          repo={repo}
          path={path}
          onClose={() => setOpen(false)}
          onSaved={() => {
            setOpen(false);
            onSaved();
          }}
        />
      )}
    </>
  );
}

// InstallGithubAppButton is the first-class "Install GitHub App" step (ISI-7895,
// W2 of ISI-7887). When the App is already installed (status.appInstalled, set
// by the operator's installation webhook) it shows the confirmed state with a
// quiet "Manage" link; otherwise it deep-links the standard App-install flow in
// a new tab. The click-through needs no server round-trip — install happens on
// GitHub and the webhook flips status asynchronously, so refresh() re-reads it.
export function InstallGithubAppButton({ repo }: { repo: CrObject }) {
  const installed = isAppInstalled(repo);
  const open = () => window.open(INSTALL_APP_URL, "_blank", "noopener,noreferrer");
  if (installed) {
    return (
      <span className="inline-flex items-center gap-2">
        <Badge tone="success" dot>
          App installed
        </Badge>
        <Button variant="ghost" size="sm" onClick={open}>
          Manage
        </Button>
      </span>
    );
  }
  return (
    <Button variant="secondary" size="sm" onClick={open}>
      Install GitHub App
    </Button>
  );
}

// SeedTemplateIssuesButton seeds the versioned content-template pack into the
// repo as GitHub issues (ISI-7896, W3 of ISI-7887). The booth endpoint is
// idempotent — templates already present are skipped — so the action is safe to
// click more than once; the toast reports the created/skipped split. Requires a
// GitHub token (spec.tokenRef) on the repo; the server returns 412 otherwise.
type SeedResult = {
  created: { id: string; number: number; url: string }[];
  skipped: string[];
};

export function SeedTemplateIssuesButton({ repo }: { repo: CrObject }) {
  const { toast } = useToast();
  const [busy, setBusy] = useState(false);
  const name = repo.metadata.name;

  async function seed() {
    setBusy(true);
    try {
      const res = await api.post<SeedResult>(
        `/v1/referencerepos/${encodeURIComponent(name)}/seed-issues`,
      );
      const created = res.created?.length ?? 0;
      const skipped = res.skipped?.length ?? 0;
      toast({
        title: created > 0 ? `Seeded ${created} template issue${created === 1 ? "" : "s"}` : "Template issues already present",
        description: skipped > 0 ? `${skipped} already existed and were skipped.` : undefined,
        tone: "success",
      });
    } catch (e) {
      toast({
        title: "Could not seed template issues",
        description: e instanceof ApiError ? e.message : e instanceof Error ? e.message : "request failed",
        tone: "warning",
      });
    } finally {
      setBusy(false);
    }
  }

  return (
    <Button variant="ghost" size="sm" onClick={seed} loading={busy}>
      Seed template issues
    </Button>
  );
}

function ReferenceRepoDialog({
  mode,
  repo,
  path,
  onClose,
  onSaved,
}: {
  mode: "create" | "edit";
  repo: CrObject | null;
  path: string;
  onClose: () => void;
  onSaved: () => void;
}) {
  const { toast } = useToast();
  const [f, setF] = useState<FormState>(() => initialState(repo));
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const invalid = validate(f, mode);
  function set<K extends keyof FormState>(key: K, value: FormState[K]) {
    setF((prev) => ({ ...prev, [key]: value }));
  }

  async function submit() {
    const problem = validate(f, mode);
    if (problem) {
      setErr(problem);
      return;
    }
    setBusy(true);
    setErr(null);
    try {
      const prTarget = {
        baseBranch: f.baseBranch.trim(),
        branchPrefix: f.branchPrefix.trim(),
        draft: f.draft,
      };
      if (mode === "create") {
        await api.post(path, {
          metadata: { name: f.name.trim() },
          spec: {
            repo: f.repo.trim(),
            tokenRef: { name: f.tokenName.trim(), key: f.tokenKey.trim() },
            webhookSecretRef: { name: f.webhookName.trim(), key: f.webhookKey.trim() },
            prTarget,
            labelRoutes: routesSpec(f.routes),
          },
        });
      } else {
        // Re-fetch so the PUT carries a fresh resourceVersion and the untouched
        // Secret refs / owner — we only change repo, prTarget, and labelRoutes.
        const name = repo!.metadata.name;
        const current = await api.get<CrObject>(`${path}/${encodeURIComponent(name)}`);
        await api.put(`${path}/${encodeURIComponent(name)}`, {
          ...current,
          spec: {
            ...(current.spec ?? {}),
            repo: f.repo.trim(),
            prTarget,
            labelRoutes: routesSpec(f.routes),
          },
        });
      }
      toast({ title: mode === "create" ? "Reference repo created" : "Reference repo saved", tone: "success" });
      onSaved();
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : e instanceof Error ? e.message : "request failed");
      setBusy(false);
    }
  }

  const title = mode === "create" ? "New reference repo" : `Edit reference repo — ${repo!.metadata.name}`;

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent title={title}>
        <DialogBody className="space-y-6">
          {/* Repo identity */}
          <div className="space-y-4">
            {mode === "create" && (
              <Field label="Name" required hint="Kubernetes object name, e.g. contentremixer-docs.">
                {({ id }) => (
                  <Input
                    id={id}
                    value={f.name}
                    placeholder="contentremixer-docs"
                    onChange={(e) => set("name", e.target.value)}
                  />
                )}
              </Field>
            )}
            <Field label="Repo" required hint='GitHub "owner/name", e.g. ContentRemixer/Contentremixer.'>
              {({ id }) => (
                <Input
                  id={id}
                  value={f.repo}
                  placeholder="owner/name"
                  onChange={(e) => set("repo", e.target.value)}
                />
              )}
            </Field>
          </div>

          {/* Secret refs — create only; preserved untouched on edit (W2 boundary). */}
          {mode === "create" && (
            <div className="space-y-4">
              <div>
                <h3 className="text-sm font-medium text-text">Secrets</h3>
                <p className="mt-1 text-sm text-text-muted">
                  Reference existing Kubernetes Secrets (name &amp; key). Secret values are managed by
                  GitOps — they are not entered here.
                </p>
              </div>
              <div className="grid grid-cols-2 gap-3">
                <Field label="GitHub token Secret" required hint="Secret name holding the PAT.">
                  {({ id }) => (
                    <Input id={id} value={f.tokenName} placeholder="gh-token" onChange={(e) => set("tokenName", e.target.value)} />
                  )}
                </Field>
                <Field label="Token key" required hint="Key within that Secret.">
                  {({ id }) => (
                    <Input id={id} value={f.tokenKey} placeholder="pat" onChange={(e) => set("tokenKey", e.target.value)} />
                  )}
                </Field>
                <Field label="Webhook Secret" required hint="Secret name holding the HMAC secret.">
                  {({ id }) => (
                    <Input id={id} value={f.webhookName} placeholder="gh-hook" onChange={(e) => set("webhookName", e.target.value)} />
                  )}
                </Field>
                <Field label="Webhook key" required hint="Key within that Secret.">
                  {({ id }) => (
                    <Input id={id} value={f.webhookKey} placeholder="secret" onChange={(e) => set("webhookKey", e.target.value)} />
                  )}
                </Field>
              </div>
            </div>
          )}

          {/* PR target */}
          <div className="space-y-4">
            <h3 className="text-sm font-medium text-text">PR target</h3>
            <div className="grid grid-cols-2 gap-3">
              <Field label="Base branch" hint="PR base (default main).">
                {({ id }) => (
                  <Input id={id} value={f.baseBranch} placeholder="main" onChange={(e) => set("baseBranch", e.target.value)} />
                )}
              </Field>
              <Field label="Branch prefix" hint="Prefixes generated PR branches.">
                {({ id }) => (
                  <Input id={id} value={f.branchPrefix} placeholder="recycle/" onChange={(e) => set("branchPrefix", e.target.value)} />
                )}
              </Field>
            </div>
            <label className="flex items-center gap-2">
              <Checkbox checked={f.draft} onCheckedChange={(c) => set("draft", c === true)} />
              <Label className="cursor-pointer">Open PRs as drafts</Label>
            </label>
          </div>

          {/* Label routes */}
          <div className="space-y-3">
            <div>
              <h3 className="text-sm font-medium text-text">Label routes</h3>
              <p className="mt-1 text-sm text-text-muted">
                Map each GitHub label to an input/output format (at least one).
              </p>
            </div>
            <RoutesFields routes={f.routes} onChange={(next) => set("routes", next)} />
          </div>

          {err && <p className="text-sm text-warn">{err}</p>}
        </DialogBody>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={submit} loading={busy} disabled={!!invalid}>
            {mode === "create" ? "Create" : "Save"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
