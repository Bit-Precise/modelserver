import { useState } from "react";
import { PageHeader } from "@/components/layout/PageHeader";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import {
  useMyNotifications,
  useMarkNotificationRead,
  useMarkAllNotificationsRead,
  useUnreadNotificationCount,
} from "@/api/notifications";
import type { Notification } from "@/api/types";
import { Loader2 } from "lucide-react";

function audienceLabel(n: Notification): string {
  if (n.audience_type === "global") return "Broadcast";
  if (n.audience_type === "user") return "You";
  // project — audience_name is populated server-side with display_name.
  return n.audience_name ? `Project: ${n.audience_name}` : "Project";
}

function timeAgo(iso: string): string {
  const diffMs = Date.now() - new Date(iso).getTime();
  const sec = Math.floor(diffMs / 1000);
  if (sec < 60) return `${sec}s ago`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min}m ago`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr}h ago`;
  const day = Math.floor(hr / 24);
  if (day < 30) return `${day}d ago`;
  return new Date(iso).toLocaleDateString();
}

function NotificationRow({ n }: { n: Notification }) {
  const [open, setOpen] = useState(false);
  const markRead = useMarkNotificationRead();
  const isUnread = !n.read_at;

  function handleToggle() {
    const nextOpen = !open;
    setOpen(nextOpen);
    if (nextOpen && isUnread) {
      // Fire-and-forget; hook invalidates queries on success. Errors
      // are ignored — the user's next 45s poll will resync the badge.
      markRead.mutate(n.id);
    }
  }

  return (
    <div className="border-b last:border-b-0">
      <button
        type="button"
        onClick={handleToggle}
        className="w-full flex items-center gap-2 px-4 py-3 text-left hover:bg-accent/50"
      >
        {isUnread ? (
          <span aria-hidden className="h-2 w-2 rounded-full bg-primary shrink-0" />
        ) : (
          <span aria-hidden className="h-2 w-2 shrink-0" />
        )}
        <span className={"flex-1 text-sm " + (isUnread ? "font-semibold" : "font-normal text-muted-foreground")}>
          {n.title}
        </span>
        <Badge variant="secondary" className="shrink-0 text-[10px]">
          {audienceLabel(n)}
        </Badge>
        <span className="text-xs text-muted-foreground shrink-0">{timeAgo(n.created_at)}</span>
      </button>
      {open && (
        <div className="px-4 pb-4 pt-1 text-sm text-muted-foreground whitespace-pre-wrap">
          {n.body}
        </div>
      )}
    </div>
  );
}

export function NotificationsPage() {
  const [page, setPage] = useState(1);
  const perPage = 20;
  const { data, isLoading } = useMyNotifications(page, perPage);
  const { data: countResp } = useUnreadNotificationCount();
  const markAll = useMarkAllNotificationsRead();
  const items = data?.data ?? [];
  const total = data?.meta?.total ?? 0;
  const totalPages = data?.meta?.total_pages ?? 1;
  const unread = countResp?.data?.count ?? 0;

  return (
    <div className="p-6 space-y-4 max-w-3xl mx-auto">
      <div className="flex items-center justify-between">
        <PageHeader title="Notifications" description="Platform announcements and updates." />
        {unread > 0 && (
          <Button variant="outline" size="sm" disabled={markAll.isPending} onClick={() => markAll.mutate()}>
            Mark all as read
          </Button>
        )}
      </div>

      {isLoading ? (
        <div className="flex items-center gap-2 text-muted-foreground text-sm">
          <Loader2 className="h-4 w-4 animate-spin" /> Loading…
        </div>
      ) : items.length === 0 ? (
        <p className="text-sm text-muted-foreground">No notifications yet.</p>
      ) : (
        <div className="rounded border bg-card">
          {items.map((n) => (
            <NotificationRow key={n.id} n={n} />
          ))}
        </div>
      )}

      {totalPages > 1 && (
        <div className="flex items-center justify-between text-xs text-muted-foreground">
          <span>
            Page {page} of {totalPages} · {total} total
          </span>
          <div className="flex gap-2">
            <Button size="sm" variant="outline" disabled={page <= 1} onClick={() => setPage(page - 1)}>
              Prev
            </Button>
            <Button size="sm" variant="outline" disabled={page >= totalPages} onClick={() => setPage(page + 1)}>
              Next
            </Button>
          </div>
        </div>
      )}
    </div>
  );
}
