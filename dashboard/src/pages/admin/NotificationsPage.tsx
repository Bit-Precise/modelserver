import { useState } from "react";
import type { FormEvent } from "react";
import { PageHeader } from "@/components/layout/PageHeader";
import { DataTable, type Column } from "@/components/shared/DataTable";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { UserCombobox } from "@/components/shared/UserCombobox";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from "@/components/ui/dialog";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Plus, Pencil, Trash2, Loader2, TriangleAlert } from "lucide-react";
import { toast } from "sonner";
import {
  useAdminNotifications,
  useCreateNotification,
  useUpdateNotification,
  useDeleteNotification,
} from "@/api/notifications";
import type { AudienceType, Notification, NotificationCreatePayload } from "@/api/types";

interface FormState {
  title: string;
  body: string;
  audienceType: AudienceType;
  audienceID: string; // stringly for form; converted to null when audienceType==='global'
}

const EMPTY_FORM: FormState = {
  title: "",
  body: "",
  audienceType: "global",
  audienceID: "",
};

function formToPayload(f: FormState): NotificationCreatePayload {
  return {
    title: f.title,
    body: f.body,
    audience_type: f.audienceType,
    audience_id: f.audienceType === "global" ? null : f.audienceID || null,
  };
}

function notificationToForm(n: Notification): FormState {
  return {
    title: n.title,
    body: n.body,
    audienceType: n.audience_type,
    audienceID: n.audience_id ?? "",
  };
}

function audienceCell(n: Notification): string {
  if (n.audience_type === "global") return "Broadcast";
  if (n.audience_type === "project") return `Project: ${n.audience_name ?? n.audience_id ?? "?"}`;
  return `User: ${n.audience_id ?? "?"}`;
}

export function AdminNotificationsPage() {
  const [page, setPage] = useState(1);
  const perPage = 20;
  const [includeDeleted, setIncludeDeleted] = useState(false);
  const { data, isLoading } = useAdminNotifications(page, perPage, includeDeleted);
  const items = data?.data ?? [];
  const total = data?.meta?.total ?? 0;
  const totalPages = data?.meta?.total_pages ?? 1;

  const createMut = useCreateNotification();
  const updateMut = useUpdateNotification();
  const deleteMut = useDeleteNotification();

  const [dialogOpen, setDialogOpen] = useState(false);
  const [editing, setEditing] = useState<Notification | null>(null);
  const [form, setForm] = useState<FormState>(EMPTY_FORM);
  const [deleteTarget, setDeleteTarget] = useState<Notification | null>(null);

  function openCreate() {
    setEditing(null);
    setForm(EMPTY_FORM);
    setDialogOpen(true);
  }
  function openEdit(n: Notification) {
    setEditing(n);
    setForm(notificationToForm(n));
    setDialogOpen(true);
  }
  async function handleSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const payload = formToPayload(form);
    if (payload.audience_type !== "global" && !payload.audience_id) {
      toast.error("audience_id is required for project/user notifications");
      return;
    }
    try {
      if (editing) {
        await updateMut.mutateAsync({ id: editing.id, body: payload });
        toast.success("Notification updated");
      } else {
        await createMut.mutateAsync(payload);
        toast.success("Notification created");
      }
      setDialogOpen(false);
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "Save failed");
    }
  }
  async function handleDelete() {
    if (!deleteTarget) return;
    try {
      await deleteMut.mutateAsync(deleteTarget.id);
      toast.success("Notification deleted");
      setDeleteTarget(null);
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "Delete failed");
    }
  }

  const columns: Column<Notification>[] = [
    {
      header: "Title",
      accessor: (n) => (
        <button className="text-left hover:underline" onClick={() => openEdit(n)}>
          {n.title}
          {n.deleted_at && <Badge variant="secondary" className="ml-2 text-[10px]">deleted</Badge>}
        </button>
      ),
    },
    { header: "Audience", accessor: (n) => audienceCell(n) },
    { header: "Reads", accessor: (n) => `${n.read_count ?? 0}` },
    { header: "Created", accessor: (n) => new Date(n.created_at).toLocaleString() },
    {
      header: "",
      accessor: (n) => (
        <div className="flex gap-1 justify-end">
          <Button size="icon-sm" variant="ghost" onClick={() => openEdit(n)}>
            <Pencil className="h-3.5 w-3.5" />
          </Button>
          <Button size="icon-sm" variant="ghost" onClick={() => setDeleteTarget(n)}>
            <Trash2 className="h-3.5 w-3.5" />
          </Button>
        </div>
      ),
    },
  ];

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <PageHeader title="Notifications" description="Manage platform announcements." />
        <div className="flex items-center gap-2">
          <label className="text-xs text-muted-foreground flex items-center gap-1">
            <input
              type="checkbox"
              checked={includeDeleted}
              onChange={(e) => { setIncludeDeleted(e.target.checked); setPage(1); }}
            />
            Show deleted
          </label>
          <Button size="sm" onClick={openCreate}>
            <Plus className="h-4 w-4 mr-1" /> New notification
          </Button>
        </div>
      </div>

      {isLoading ? (
        <div className="flex items-center gap-2 text-muted-foreground text-sm">
          <Loader2 className="h-4 w-4 animate-spin" /> Loading…
        </div>
      ) : (
        <DataTable columns={columns} data={items} keyFn={(n) => n.id} />
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

      {/* Create / Edit dialog */}
      <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>{editing ? "Edit notification" : "New notification"}</DialogTitle>
          </DialogHeader>
          {editing && (
            <div className="rounded border border-yellow-500/40 bg-yellow-500/10 text-yellow-900 dark:text-yellow-100 px-3 py-2 text-xs flex items-start gap-2">
              <TriangleAlert className="h-4 w-4 shrink-0 mt-0.5" />
              <span>
                Editing does not re-notify users who have already read this notification. Delete and create anew if you need to reach them again.
              </span>
            </div>
          )}
          <form onSubmit={handleSubmit} className="space-y-4">
            <div className="space-y-1">
              <Label htmlFor="title">Title</Label>
              <Input
                id="title"
                value={form.title}
                onChange={(e) => setForm({ ...form, title: e.target.value })}
                maxLength={200}
                required
              />
            </div>
            <div className="space-y-1">
              <Label htmlFor="body">Body</Label>
              <Textarea
                id="body"
                value={form.body}
                onChange={(e) => setForm({ ...form, body: e.target.value })}
                maxLength={20000}
                rows={6}
                required
              />
              <p className="text-[10px] text-muted-foreground">Plain text; line breaks preserved.</p>
            </div>
            <div className="space-y-1">
              <Label>Audience</Label>
              <RadioGroup
                value={form.audienceType}
                onValueChange={(v) => setForm({ ...form, audienceType: v as AudienceType, audienceID: "" })}
                className="flex gap-4"
              >
                <label className="flex items-center gap-1 text-sm">
                  <RadioGroupItem value="global" /> Global
                </label>
                <label className="flex items-center gap-1 text-sm">
                  <RadioGroupItem value="project" /> Project
                </label>
                <label className="flex items-center gap-1 text-sm">
                  <RadioGroupItem value="user" /> User
                </label>
              </RadioGroup>
            </div>
            {form.audienceType === "project" && (
              <div className="space-y-1">
                <Label htmlFor="proj">Project UUID</Label>
                <Input
                  id="proj"
                  value={form.audienceID}
                  onChange={(e) => setForm({ ...form, audienceID: e.target.value })}
                  placeholder="00000000-0000-0000-0000-000000000000"
                />
                <p className="text-[10px] text-muted-foreground">
                  Copy the project ID from the Admin → Projects page.
                </p>
              </div>
            )}
            {form.audienceType === "user" && (
              <div className="space-y-1">
                <Label>User</Label>
                <UserCombobox
                  value={form.audienceID || null}
                  onChange={(id) => setForm({ ...form, audienceID: id ?? "" })}
                  placeholder="Search user…"
                />
              </div>
            )}
            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => setDialogOpen(false)}>
                Cancel
              </Button>
              <Button type="submit" disabled={createMut.isPending || updateMut.isPending}>
                {(createMut.isPending || updateMut.isPending) && <Loader2 className="h-4 w-4 mr-1 animate-spin" />}
                {editing ? "Save" : "Send"}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      {/* Delete confirmation */}
      <AlertDialog open={!!deleteTarget} onOpenChange={(open) => !open && setDeleteTarget(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete notification?</AlertDialogTitle>
            <AlertDialogDescription>
              Users who haven't read it yet will no longer see it. Read history is preserved for audit.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction onClick={handleDelete}>Delete</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
