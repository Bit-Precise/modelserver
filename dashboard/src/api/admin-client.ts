import createClient from "openapi-fetch";
import type { paths } from "./generated/schema";
import {
  API_BASE,
  authenticatedFetch,
  toAPIError,
} from "./client";

/**
 * Type-safe client for the management API contract. Proxy/LLM endpoints are
 * deliberately absent because they are not part of admin.openapi.json.
 */
export const adminApi = createClient<paths>({
  baseUrl: API_BASE,
  fetch: authenticatedFetch,
});

type OpenAPIResponse = {
  response: Response;
  data?: unknown;
  error?: unknown;
};

type SuccessData<T> = T extends { data: infer Data } ? Data : never;

/**
 * Unwrap an openapi-fetch call for React Query and mutation handlers. HTTP
 * errors become the same APIError used by the existing dashboard client;
 * network errors continue to reject unchanged.
 */
export async function unwrapAdminResponse<T extends OpenAPIResponse>(
  request: Promise<T>,
): Promise<SuccessData<T>> {
  const result = await request;
  if (!result.response.ok) {
    throw toAPIError(result.response, result.error);
  }
  return result.data as SuccessData<T>;
}
