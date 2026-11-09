import { Avatar, Badge, Button } from "../../ui";
import type { BadgeTone } from "../../ui";
import { PlusIcon } from "../../components/icons";
import { timeAgo } from "./time";
import type { IllustrationRequest, IllustrationStatus } from "./types";

// Illustration-requests rail (plan §3.2 B3): the "request illustration" work
// items filed against this document, each assigned to an agent (default Graphic
// Designer). The requester can accept a delivery or cancel an open request;
// status transitions go through PATCH /v1/illustration-requests/{id}.

const statusMeta: Record<IllustrationStatus, { label: string; tone: BadgeTone }> = {
  requested: { label: "Requested", tone: "info" },
  in_progress: { label: "In progress", tone: "warning" },
  delivered: { label: "Delivered", tone: "primary" },
  accepted: { label: "Accepted", tone: "success" },
  cancelled: { label: "Cancelled", tone: "neutral" },
};

export interface IllustrationListProps {
  requests: IllustrationRequest[];
  busyId: string | null;
  onUpdateStatus: (id: string, status: IllustrationStatus) => void;
  onRequestNew: () => void;
}

export function IllustrationList({
  requests,
  busyId,
  onUpdateStatus,
  onRequestNew,
}: IllustrationListProps) {
  return (
    <div className="flex flex-col gap-3">
      <Button
        variant="secondary"
        size="sm"
        className="self-start"
        iconLeft={<PlusIcon size={15} />}
        onClick={onRequestNew}
      >
        Request illustration
      </Button>

      {requests.length === 0 ? (
        <p className="rounded-lg border border-dashed border-border px-4 py-8 text-center text-sm text-text-muted">
          No illustration requests yet.
        </p>
      ) : (
        requests.map((r) => {
          const meta = statusMeta[r.status];
          const open = r.status === "requested" || r.status === "in_progress";
          return (
            <div
              key={r.id}
              className="flex flex-col gap-2 rounded-lg border border-border bg-surface-raised p-3 shadow-card"
            >
              <div className="flex items-start justify-between gap-2">
                <p className="min-w-0 flex-1 whitespace-pre-wrap break-words text-sm text-text">{r.brief}</p>
                <Badge tone={meta.tone} dot>
                  {meta.label}
                </Badge>
              </div>

              {r.anchor?.quote ? (
                <p className="line-clamp-2 border-l-2 border-accent/40 pl-2 text-xs italic text-text-muted">
                  “{r.anchor.quote}”
                </p>
              ) : null}

              <div className="flex items-center justify-between gap-2">
                <span className="flex items-center gap-1.5 text-xs text-text-muted">
                  <Avatar name={r.assignedAgent} size="sm" agent />
                  {r.assignedAgent}
                  <span aria-hidden>·</span>
                  {timeAgo(r.createdAt)}
                </span>

                <div className="flex items-center gap-2">
                  {r.status === "delivered" ? (
                    <Button
                      size="sm"
                      loading={busyId === r.id}
                      onClick={() => onUpdateStatus(r.id, "accepted")}
                    >
                      Accept
                    </Button>
                  ) : null}
                  {open ? (
                    <Button
                      variant="ghost"
                      size="sm"
                      loading={busyId === r.id}
                      onClick={() => onUpdateStatus(r.id, "cancelled")}
                    >
                      Cancel
                    </Button>
                  ) : null}
                </div>
              </div>
            </div>
          );
        })
      )}
    </div>
  );
}
