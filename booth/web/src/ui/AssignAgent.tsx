import { useState } from "react";
import { cx } from "./cx";
import { Avatar } from "./Avatar";
import { Button } from "./Button";
import { Input } from "./Field";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "./DropdownMenu";
import { CheckIcon, SparkleIcon, UserPlusIcon } from "../components/icons";

// "Assign an agent" control (B4 peer-review + B3 illustration-request). Maps to
// POST /v1/illustration-requests?assignee=… and reviewer assignment (W5).

export interface Assignee {
  id: string;
  name: string;
  /** e.g. "Graphic Designer", "DevRel". */
  role?: string;
  avatarUrl?: string;
  isAgent?: boolean;
}

export interface AssignAgentProps {
  assignees: Assignee[];
  selectedId?: string;
  busy?: boolean;
  /** Trigger label when nobody is assigned. */
  label?: string;
  align?: "start" | "end";
  onAssign: (assignee: Assignee) => void;
}

export function AssignAgent({
  assignees,
  selectedId,
  busy,
  label = "Assign",
  align = "end",
  onAssign,
}: AssignAgentProps) {
  const [query, setQuery] = useState("");
  const selected = assignees.find((a) => a.id === selectedId);
  const filtered = assignees.filter(
    (a) =>
      a.name.toLowerCase().includes(query.toLowerCase()) ||
      (a.role ?? "").toLowerCase().includes(query.toLowerCase()),
  );
  const agents = filtered.filter((a) => a.isAgent);
  const people = filtered.filter((a) => !a.isAgent);

  return (
    <DropdownMenu onOpenChange={(open) => !open && setQuery("")}>
      <DropdownMenuTrigger asChild>
        <Button variant="secondary" size="sm" loading={busy} iconLeft={<UserPlusIcon size={15} />}>
          {selected ? selected.name : label}
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align={align} className="w-64 p-1.5">
        <div className="px-1 pb-1.5">
          <Input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Search people & agents…"
            className="h-8"
          />
        </div>

        {agents.length > 0 && (
          <>
            <DropdownMenuLabel>Agents</DropdownMenuLabel>
            {agents.map((a) => (
              <AssigneeItem key={a.id} assignee={a} selected={a.id === selectedId} onSelect={() => onAssign(a)} />
            ))}
          </>
        )}

        {people.length > 0 && (
          <>
            {agents.length > 0 ? <DropdownMenuSeparator /> : null}
            <DropdownMenuLabel>People</DropdownMenuLabel>
            {people.map((a) => (
              <AssigneeItem key={a.id} assignee={a} selected={a.id === selectedId} onSelect={() => onAssign(a)} />
            ))}
          </>
        )}

        {filtered.length === 0 && (
          <p className="px-2.5 py-3 text-center text-sm text-text-muted">No matches</p>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function AssigneeItem({
  assignee,
  selected,
  onSelect,
}: {
  assignee: Assignee;
  selected?: boolean;
  onSelect: () => void;
}) {
  return (
    <DropdownMenuItem onSelect={onSelect} className="gap-2.5">
      <Avatar name={assignee.name} src={assignee.avatarUrl} size="sm" agent={assignee.isAgent} />
      <span className="min-w-0 flex-1">
        <span className="flex items-center gap-1 truncate text-sm text-text">
          {assignee.name}
          {assignee.isAgent ? <SparkleIcon size={12} className="text-primary" /> : null}
        </span>
        {assignee.role ? <span className="block truncate text-xs text-text-muted">{assignee.role}</span> : null}
      </span>
      {selected ? <CheckIcon size={15} className={cx("text-primary")} /> : null}
    </DropdownMenuItem>
  );
}

export default AssignAgent;
