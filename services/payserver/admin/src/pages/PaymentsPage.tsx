import { useMemo, useState } from "react";
import { useSearchParams } from "react-router";
import { usePayments, usePayment } from "@/api/payments";
import { useTenants } from "@/api/tenants";
import type { Payment } from "@/api/types";

const STATUSES = ["", "pending", "paid", "failed"];
const CHANNELS = ["", "wechat", "alipay", "stripe"];
const PAGE = 50;

export function PaymentsPage() {
  const [search, setSearch] = useSearchParams();
  const tenantID = search.get("tenant_id") ?? "";
  const status = search.get("status") ?? "";
  const channel = search.get("channel") ?? "";
  const offset = Number(search.get("offset") ?? "0");

  const { data: tenants } = useTenants();
  const { data, isLoading, error } = usePayments({
    tenant_id: tenantID, status, channel, offset, limit: PAGE,
  });
  const tenantsByID = useMemo(() => {
    const m = new Map<string, string>();
    (tenants?.items ?? []).forEach((t) => m.set(t.id, t.name));
    return m;
  }, [tenants]);

  const [openID, setOpenID] = useState<string | null>(null);

  function setFilter(k: string, v: string) {
    const sp = new URLSearchParams(search);
    if (v) sp.set(k, v); else sp.delete(k);
    sp.delete("offset");
    setSearch(sp);
  }
  function setOffset(n: number) {
    const sp = new URLSearchParams(search);
    sp.set("offset", String(n));
    setSearch(sp);
  }

  if (error) return <div className="text-sm text-destructive">Error: {String(error)}</div>;

  const items = data?.items ?? [];
  const total = data?.meta.total ?? 0;

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold">Payments</h1>
        <div className="text-sm text-muted-foreground">{total} total</div>
      </div>

      <div className="flex flex-wrap gap-2 items-end">
        <FilterSelect label="Tenant" value={tenantID} options={[["", "All"], ...(tenants?.items ?? []).map((t) => [t.id, t.name] as [string, string])]} onChange={(v) => setFilter("tenant_id", v)} />
        <FilterSelect label="Status" value={status} options={STATUSES.map((s) => [s, s || "All"])} onChange={(v) => setFilter("status", v)} />
        <FilterSelect label="Channel" value={channel} options={CHANNELS.map((c) => [c, c || "All"])} onChange={(v) => setFilter("channel", v)} />
      </div>

      {isLoading ? (
        <div className="text-sm text-muted-foreground">Loading…</div>
      ) : (
        <table className="w-full text-xs">
          <thead className="border-b">
            <tr>
              <th className="px-2 py-2 text-left">Created</th>
              <th className="px-2 py-2 text-left">Order</th>
              <th className="px-2 py-2 text-left">Tenant</th>
              <th className="px-2 py-2 text-left">Channel</th>
              <th className="px-2 py-2 text-right">Amount</th>
              <th className="px-2 py-2 text-left">Status</th>
              <th className="px-2 py-2 text-left">Callback</th>
              <th className="px-2 py-2 text-right">Retries</th>
            </tr>
          </thead>
          <tbody>
            {items.map((p: Payment) => (
              <tr key={p.id} onClick={() => setOpenID(p.id)} className="cursor-pointer border-b hover:bg-accent/30">
                <td className="px-2 py-1.5 text-muted-foreground">{new Date(p.created_at).toLocaleString()}</td>
                <td className="px-2 py-1.5 font-mono">{p.order_id.slice(0, 12)}…</td>
                <td className="px-2 py-1.5">{tenantsByID.get(p.tenant_id) ?? p.tenant_id.slice(0, 8)}</td>
                <td className="px-2 py-1.5">{p.channel}</td>
                <td className="px-2 py-1.5 text-right font-mono">{p.amount}</td>
                <td className="px-2 py-1.5">{p.status}</td>
                <td className="px-2 py-1.5">{p.callback_status}</td>
                <td className="px-2 py-1.5 text-right">{p.callback_retries}</td>
              </tr>
            ))}
            {items.length === 0 && (
              <tr><td colSpan={8} className="px-2 py-6 text-center text-muted-foreground">No payments match these filters</td></tr>
            )}
          </tbody>
        </table>
      )}

      <div className="flex items-center justify-between text-sm">
        <button onClick={() => setOffset(Math.max(0, offset - PAGE))} disabled={offset === 0}
          className="rounded border px-3 py-1 disabled:opacity-50">← Prev</button>
        <span className="text-muted-foreground">{offset + 1}–{Math.min(offset + PAGE, total)} of {total}</span>
        <button onClick={() => setOffset(offset + PAGE)} disabled={offset + PAGE >= total}
          className="rounded border px-3 py-1 disabled:opacity-50">Next →</button>
      </div>

      {openID && <PaymentDetailDialog id={openID} onClose={() => setOpenID(null)} />}
    </div>
  );
}

function FilterSelect({
  label, value, options, onChange,
}: { label: string; value: string; options: [string, string][]; onChange: (v: string) => void; }) {
  return (
    <div className="text-sm">
      <div className="text-xs text-muted-foreground mb-1">{label}</div>
      <select className="rounded border px-2 py-1 text-sm" value={value} onChange={(e) => onChange(e.target.value)}>
        {options.map(([v, l]) => <option key={v} value={v}>{l}</option>)}
      </select>
    </div>
  );
}

function PaymentDetailDialog({ id, onClose }: { id: string; onClose: () => void }) {
  const { data: p, isLoading } = usePayment(id);
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50">
      <div className="max-h-[80vh] w-[640px] overflow-auto space-y-3 rounded-md bg-background p-6 shadow-lg">
        <div className="flex items-center justify-between">
          <h2 className="text-lg font-semibold">Payment</h2>
          <button onClick={onClose} className="text-sm underline">Close</button>
        </div>
        {isLoading ? "Loading…" : p && (
          <dl className="space-y-1 text-sm">
            <Row label="ID" value={p.id} mono />
            <Row label="Tenant ID" value={p.tenant_id} mono />
            <Row label="Order ID" value={p.order_id} mono />
            <Row label="Channel" value={p.channel} />
            <Row label="Amount" value={String(p.amount)} />
            <Row label="Status" value={p.status} />
            <Row label="Callback status" value={p.callback_status} />
            <Row label="Callback retries" value={String(p.callback_retries)} />
            <Row label="Trade No" value={p.trade_no || "—"} mono />
            <Row label="Payment URL" value={p.payment_url || "—"} />
            <Row label="Paid at" value={p.paid_at ?? "—"} />
            <div>
              <dt className="text-muted-foreground text-xs mt-3">Raw notify</dt>
              <pre className="rounded bg-muted p-2 font-mono text-xs whitespace-pre-wrap break-all">
                {p.raw_notify ? prettyJSON(p.raw_notify) : "—"}
              </pre>
            </div>
          </dl>
        )}
      </div>
    </div>
  );
}

function Row({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="grid grid-cols-3 gap-2">
      <dt className="text-muted-foreground">{label}</dt>
      <dd className={`col-span-2 ${mono ? "font-mono text-xs" : ""}`}>{value}</dd>
    </div>
  );
}

function prettyJSON(s: string): string {
  try { return JSON.stringify(JSON.parse(s), null, 2); }
  catch { return s; }
}
