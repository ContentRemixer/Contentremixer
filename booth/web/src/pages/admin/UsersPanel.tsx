import { useState } from "react";
import {
  Badge,
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
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  useToast,
} from "../../ui";
import { api, ApiError } from "../../app/api";
import type { Role } from "../../app/auth";
import { useResource } from "./useResource";
import { PanelForbidden, PanelError, PanelLoading, PanelEmpty } from "./panelStates";

interface AdminUser {
  username: string;
  role: Role;
}

const ROLES: Role[] = ["Admin", "DevRel"];

// UsersPanel is the C4 Users & Roles screen. It is the one admin surface backed
// by a brand-new API (W6): list, create, change-role, delete — all Admin-gated
// server-side (§6.3 clause 1). The last-Admin guard lives in the store, so a
// delete/demote that would orphan the install returns 409 → surfaced as a toast.
export function UsersPanel() {
  const { items, loading, error, refresh } = useResource<AdminUser>("/v1/users");
  const { toast } = useToast();
  const [adding, setAdding] = useState(false);

  async function setRole(u: AdminUser, role: Role) {
    if (role === u.role) return;
    try {
      await api.patch(`/v1/users/${encodeURIComponent(u.username)}`, { role });
      toast({ title: `${u.username} is now ${role}`, tone: "success" });
      refresh();
    } catch (err) {
      toast({ title: "Could not change role", description: msg(err), tone: "warning" });
    }
  }

  async function remove(u: AdminUser) {
    try {
      await api.del(`/v1/users/${encodeURIComponent(u.username)}`);
      toast({ title: `Deleted ${u.username}`, tone: "success" });
      refresh();
    } catch (err) {
      toast({ title: "Could not delete user", description: msg(err), tone: "warning" });
    }
  }

  if (error === "forbidden") return <PanelForbidden />;

  return (
    <Card>
      <CardHeader
        title="Users & roles"
        subtitle="Platform accounts. Admin-only; the server enforces access regardless of the nav."
        action={
          <Button size="sm" onClick={() => setAdding(true)}>
            Add user
          </Button>
        }
      />
      <CardBody className="p-0">
        {loading ? (
          <PanelLoading />
        ) : error ? (
          <PanelError message={error} onRetry={refresh} />
        ) : items.length === 0 ? (
          <PanelEmpty label="No users." />
        ) : (
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-border text-left text-xs uppercase tracking-wide text-text-muted">
                <th className="px-5 py-3 font-medium">Username</th>
                <th className="px-5 py-3 font-medium">Role</th>
                <th className="px-5 py-3 text-right font-medium">Actions</th>
              </tr>
            </thead>
            <tbody>
              {items.map((u) => (
                <tr key={u.username} className="border-b border-border last:border-0">
                  <td className="px-5 py-3 font-medium text-text">{u.username}</td>
                  <td className="px-5 py-3">
                    <Badge tone={u.role === "Admin" ? "primary" : "neutral"}>{u.role}</Badge>
                  </td>
                  <td className="px-5 py-3">
                    <div className="flex items-center justify-end gap-2">
                      <Select value={u.role} onValueChange={(v) => setRole(u, v as Role)}>
                        <SelectTrigger className="h-8 w-28" aria-label={`Role for ${u.username}`}>
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          {ROLES.map((r) => (
                            <SelectItem key={r} value={r}>
                              {r}
                            </SelectItem>
                          ))}
                        </SelectContent>
                      </Select>
                      <Button variant="danger" size="sm" onClick={() => remove(u)}>
                        Delete
                      </Button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </CardBody>
      {adding && (
        <AddUserDialog
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

function AddUserDialog({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const { toast } = useToast();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [role, setRole] = useState<Role>("DevRel");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  async function submit() {
    setBusy(true);
    setErr(null);
    try {
      await api.post("/v1/users", { username, password, role });
      toast({ title: `Created ${username}`, tone: "success" });
      onCreated();
    } catch (e) {
      setErr(msg(e));
      setBusy(false);
    }
  }

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent title="Add user" description="Create a platform account and assign its role.">
        <DialogBody className="space-y-4">
          <Field label="Username" required>
            {({ id }) => (
              <Input id={id} value={username} onChange={(e) => setUsername(e.target.value)} autoFocus />
            )}
          </Field>
          <Field label="Password" hint="8–72 characters." required error={err ?? undefined}>
            {({ id, invalid }) => (
              <Input
                id={id}
                type="password"
                value={password}
                invalid={invalid}
                onChange={(e) => setPassword(e.target.value)}
              />
            )}
          </Field>
          <Field label="Role" required>
            {({ id }) => (
              <Select value={role} onValueChange={(v) => setRole(v as Role)}>
                <SelectTrigger id={id}>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {ROLES.map((r) => (
                    <SelectItem key={r} value={r}>
                      {r}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            )}
          </Field>
        </DialogBody>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={submit} loading={busy} disabled={!username || !password}>
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
