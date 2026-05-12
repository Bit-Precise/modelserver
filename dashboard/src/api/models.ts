import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "./client";
import type { DataResponse, Model, ModelListRow, ModelMetadata } from "./types";

// ProjectModel is the read-only model row the project-scoped endpoint
// returns. Admin-only fields (credit rates, status, timestamps, reference
// counts) are stripped on the server.
export interface ProjectModel {
  name: string;
  display_name: string;
  description?: string;
  aliases: string[];
  publisher: string;
  metadata: ModelMetadata;
}

// useProjectModels returns the catalog of active models that any project
// member can call from this project.
export function useProjectModels(projectId: string) {
  return useQuery({
    queryKey: ["project-models", projectId],
    queryFn: () =>
      api.get<DataResponse<ProjectModel[]>>(
        `/api/v1/projects/${projectId}/models`,
      ),
    enabled: !!projectId,
  });
}

// useModels returns every catalog row with embedded reference counts.
// `?status=active` or `?status=disabled` narrows the server-side filter.
export function useModels(status?: "active" | "disabled") {
  const qs = status ? `?status=${status}` : "";
  return useQuery({
    queryKey: ["admin", "models", status ?? "all"],
    queryFn: () => api.get<DataResponse<ModelListRow[]>>(`/api/v1/models${qs}`),
  });
}

// useCatalogLookup is a thin wrapper callers use when they only need the set
// of canonical names for a combobox. Cached together with useModels so
// opening a form right after navigating from ModelsPage is instant.
export function useCatalogNames() {
  const query = useModels();
  const names = (query.data?.data ?? []).map((m) => m.name);
  return { ...query, names };
}

export function useModel(name: string | undefined) {
  return useQuery({
    queryKey: ["admin", "models", "one", name],
    queryFn: () => api.get<DataResponse<Model>>(`/api/v1/models/${name}`),
    enabled: !!name,
  });
}

export function useCreateModel() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: Partial<Model>) =>
      api.post<DataResponse<Model>>("/api/v1/models", body),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["admin", "models"] }),
  });
}

// The body is intentionally typed loosely so callers can send explicit null
// for clearable fields (e.g. default_credit_rate) — Partial<Model> would
// forbid that since the Model type has no nullable fields. The server
// validates the shape.
export function useUpdateModel() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ name, ...body }: { name: string } & Record<string, unknown>) =>
      api.patch<DataResponse<Model>>(`/api/v1/models/${name}`, body),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["admin", "models"] }),
  });
}

export function useDeleteModel() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (name: string) => api.delete(`/api/v1/models/${name}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["admin", "models"] }),
  });
}
