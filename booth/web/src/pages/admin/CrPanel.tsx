import { useState, type ReactNode } from "react";
import {
  Button,
  Card,
  CardBody,
  CardHeader,
  Dialog,
  DialogBody,
  DialogContent,
  DialogFooter,
  Field,
  Input,
  Textarea,
  useToast,
} from "../../ui";
import { api, ApiError } from "../../app/api";
import { useResource } from "./useResource";
import { PanelForbidden, PanelError, PanelLoading, PanelEmpty } from "./panelStates";

// A raw Kubernetes CR as returned by the Booth CRUD routes. Admin panels read
// metadata/spec/status generically; typed detail editing stays in GitOps/kubectl.
export interface CrObject {
  metadata: { name: string; creationTimestamp?: string };
  spec?: Record<string, unknown>;
  status?: Record<string, unknown>;
}

export interface CrColumn {
  header: string;
  cell: (o: CrObject) => ReactNode;
}

export interface CrCreateField {
  key: string;
  label: string;
  required?: boolean;
  hint?: string;
  multiline?: boolean;
}

export interface CrCreateSpec {
  fields: CrCreateField[];
  /** Assemble the CR JSON from the entered field values. */
  build: (values: Record<string, string>) => unknown;
}

export interface CrPanelConfig {
  path: string;
  title: string;
  subtitle: string;
  columns: CrColumn[];
  /** Simple create form; omit for kinds authored via GitOps (e.g. ReferenceRepo). */
  create?: CrCreateSpec;
  /** Shown when create is omitted, to explain how the kind is managed. */
  createNote?: string;
}

// CrPanel is the shared list/create/delete surface for the CR-backed admin
// screens (Personas / Model Profiles / Reference Repos). The server gates every
// call through the §6.3 store, so a DevRel reaching an admin-only kind gets a 403
// → PanelForbidden; owner-scoped kinds (Personas) simply list what the caller owns.
export function CrPanel(cfg: CrPanelConfig) {
  const { items, loading, error, refresh } = useResource<CrObject>(cfg.path);
  const { toast } = useToast();
  const [adding, setAdding] = useState(false);

  async function remove(name: string) {
    try {
      await api.del(`${cfg.path}/${encodeURIComponent(name)}`);
      toast({ title: `Deleted ${name}`, tone: "success" });
      refresh();
    } catch (err) {
      toast({ title: "Could not delete", description: msg(err), tone: "warning" });
    }
  }

  if (error === "forbidden") return <PanelForbidden />;

  return (
    <Card>
      <CardHeader
        title={cfg.title}
        subtitle={cfg.subtitle}
        action={
          cfg.create ? (
            <Button size="sm" onClick={() => setAdding(true)}>
              New
            </Button>
          ) : undefined
        }
      />
      <CardBody className="p-0">
        {loading ? (
          <PanelLoading />
        ) : error ? (
          <PanelError message={error} onRetry={refresh} />
        ) : items.length === 0 ? (
          <PanelEmpty label={cfg.createNote ?? "Nothing here yet."} />
        ) : (
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-border text-left text-xs uppercase tracking-wide text-text-muted">
                {cfg.columns.map((c) => (
                  <th key={c.header} className="px-5 py-3 font-medium">
                    {c.header}
                  </th>
                ))}
                <th className="px-5 py-3 text-right font-medium">Actions</th>
              </tr>
            </thead>
            <tbody>
              {items.map((o) => (
                <tr key={o.metadata.name} className="border-b border-border last:border-0">
                  {cfg.columns.map((c) => (
                    <td key={c.header} className="px-5 py-3 text-text">
                      {c.cell(o)}
                    </td>
                  ))}
                  <td className="px-5 py-3 text-right">
                    <Button variant="danger" size="sm" onClick={() => remove(o.metadata.name)}>
                      Delete
                    </Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </CardBody>
      {adding && cfg.create && (
        <CreateDialog
          title={cfg.title}
          spec={cfg.create}
          path={cfg.path}
          onClose={() => setAdding(false)}
          onCreated={() => {
            setAdding(false);
            refresh();
          }}
        />
      )}
    </Card>
  );
}

function CreateDialog({
  title,
  spec,
  path,
  onClose,
  onCreated,
}: {
  title: string;
  spec: CrCreateSpec;
  path: string;
  onClose: () => void;
  onCreated: () => void;
}) {
  const { toast } = useToast();
  const [values, setValues] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const missing = spec.fields.some((f) => f.required && !values[f.key]?.trim());

  async function submit() {
    setBusy(true);
    setErr(null);
    try {
      await api.post(path, spec.build(values));
      toast({ title: "Created", tone: "success" });
      onCreated();
    } catch (e) {
      setErr(msg(e));
      setBusy(false);
    }
  }

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent title={`New ${title.replace(/s$/, "")}`}>
        <DialogBody className="space-y-4">
          {spec.fields.map((f) => (
            <Field key={f.key} label={f.label} hint={f.hint} required={f.required}>
              {({ id }) =>
                f.multiline ? (
                  <Textarea
                    id={id}
                    value={values[f.key] ?? ""}
                    onChange={(e) => setValues((v) => ({ ...v, [f.key]: e.target.value }))}
                  />
                ) : (
                  <Input
                    id={id}
                    value={values[f.key] ?? ""}
                    onChange={(e) => setValues((v) => ({ ...v, [f.key]: e.target.value }))}
                  />
                )
              }
            </Field>
          ))}
          {err && <p className="text-sm text-warn">{err}</p>}
        </DialogBody>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={submit} loading={busy} disabled={missing}>
            Create
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function msg(err: unknown): string {
  if (err instanceof ApiError) return err.message;
  return err instanceof Error ? err.message : "request failed";
}
