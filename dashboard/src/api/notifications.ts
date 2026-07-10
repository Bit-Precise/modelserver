import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "./client";
import type {
  DataResponse,
  ListResponse,
  Notification,
  NotificationCreatePayload,
  NotificationUpdatePayload,
} from "./types";

// ==== User-facing hooks ====

export function useMyNotifications(page = 1, perPage = 20) {
  return useQuery({
    queryKey: ["my-notifications", page, perPage],
    queryFn: () =>
      api.get<ListResponse<Notification>>(
        `/api/v1/notifications?page=${page}&per_page=${perPage}`,
      ),
  });
}

// useUnreadNotificationCount polls every 45s so the sidebar badge stays
// current without flooding the API. staleTime 30s absorbs same-window
// tab-focus revisits without a fresh network call.
export function useUnreadNotificationCount() {
  return useQuery({
    queryKey: ["notifications-unread-count"],
    queryFn: () =>
      api.get<DataResponse<{ count: number }>>(
        "/api/v1/notifications/unread_count",
      ),
    refetchInterval: 45_000,
    staleTime: 30_000,
  });
}

export function useMarkNotificationRead() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      api.post<void>(`/api/v1/notifications/${id}/read`, undefined),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["notifications-unread-count"] });
      qc.invalidateQueries({ queryKey: ["my-notifications"] });
    },
  });
}

export function useMarkAllNotificationsRead() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () =>
      api.post<DataResponse<{ marked: number }>>(
        "/api/v1/notifications/read_all",
        undefined,
      ),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["notifications-unread-count"] });
      qc.invalidateQueries({ queryKey: ["my-notifications"] });
    },
  });
}

// ==== Admin hooks (used by Task 6's admin page) ====

export function useAdminNotifications(page = 1, perPage = 20, includeDeleted = false) {
  return useQuery({
    queryKey: ["admin-notifications", page, perPage, includeDeleted],
    queryFn: () =>
      api.get<ListResponse<Notification>>(
        `/api/v1/admin/notifications?page=${page}&per_page=${perPage}` +
          (includeDeleted ? "&include_deleted=1" : ""),
      ),
  });
}

export function useCreateNotification() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: NotificationCreatePayload) =>
      api.post<DataResponse<Notification>>("/api/v1/admin/notifications", body),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["admin-notifications"] }),
  });
}

export function useUpdateNotification() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, body }: { id: string; body: NotificationUpdatePayload }) =>
      api.put<DataResponse<Notification>>(`/api/v1/admin/notifications/${id}`, body),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["admin-notifications"] }),
  });
}

export function useDeleteNotification() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      api.delete<void>(`/api/v1/admin/notifications/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["admin-notifications"] }),
  });
}
