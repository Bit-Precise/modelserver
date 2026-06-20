import { useQuery } from "@tanstack/react-query";
import { adminFetch } from "./client";
import type { Payment } from "./types";

export type ListPaymentsParams = {
  tenant_id?: string; status?: string; channel?: string;
  limit?: number; offset?: number;
};

export function usePayments(params: ListPaymentsParams) {
  const qs = new URLSearchParams();
  if (params.tenant_id) qs.set("tenant_id", params.tenant_id);
  if (params.status) qs.set("status", params.status);
  if (params.channel) qs.set("channel", params.channel);
  qs.set("limit", String(params.limit ?? 50));
  qs.set("offset", String(params.offset ?? 0));
  return useQuery({
    queryKey: ["payments", params],
    queryFn: () => adminFetch<{ items: Payment[]; meta: { total: number; limit: number; offset: number } }>(`/payments?${qs}`),
  });
}

export function usePayment(id: string) {
  return useQuery({
    queryKey: ["payments", id],
    queryFn: () => adminFetch<{ payment: Payment }>(`/payments/${id}`).then((r) => r.payment),
    enabled: !!id,
  });
}
