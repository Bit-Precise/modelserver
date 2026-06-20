export async function adminFetch<T>(path: string, init?: RequestInit): Promise<T> {
  const r = await fetch(`/admin${path}`, {
    ...init,
    headers: { "Content-Type": "application/json", Accept: "application/json", ...(init?.headers ?? {}) },
  });
  if (r.status === 401) {
    window.location.href = "/admin/login";
    throw new Error("unauthenticated");
  }
  if (!r.ok) {
    let msg = `HTTP ${r.status}`;
    let code: string | undefined;
    try {
      const body = await r.json();
      if (body?.error) msg = body.error;
      if (body?.code) code = body.code;
    } catch {
      /* not JSON */
    }
    const err = new Error(msg) as Error & { code?: string };
    err.code = code;
    throw err;
  }
  return r.json() as Promise<T>;
}
