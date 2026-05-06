-- 036_responses_compact_request_kind.sql
--
-- Adds the openai_responses_compact request kind, used by the new
-- POST /v1/responses/compact route. The route is a thin pass-through
-- to the OpenAI Responses API compact endpoint
-- (https://api.openai.com/v1/responses/compact) and to the equivalent
-- ChatGPT-subscription codex endpoint
-- (https://chatgpt.com/backend-api/codex/responses/compact).
--
-- Auto-extends every existing route that already serves
-- openai_responses AND whose upstream group is purely openai/codex,
-- so callers get compact support without an admin action. Routes
-- whose group includes vertex-openai or bedrock-openai (neither of
-- which exposes a real compact endpoint) are skipped — operators
-- who want compact on those routes must opt in manually.
--
-- Idempotent: the NOT … = ANY guard skips already-extended routes.
-- Forward-only: removing the kind from the CHECK in a future
-- migration would require first stripping it from every route that
-- references it.

BEGIN;

ALTER TABLE routes DROP CONSTRAINT routes_request_kinds_valid;
ALTER TABLE routes ADD CONSTRAINT routes_request_kinds_valid CHECK (
    request_kinds <@ ARRAY[
        'anthropic_messages',
        'anthropic_count_tokens',
        'openai_chat_completions',
        'openai_responses',
        'openai_responses_compact',
        'google_generate_content',
        'openai_images_generations',
        'openai_images_edits'
    ]::TEXT[]
    AND array_length(request_kinds, 1) >= 1
);

WITH eligible AS (
    SELECT rt.id
    FROM routes rt
    WHERE 'openai_responses' = ANY(rt.request_kinds)
      AND NOT 'openai_responses_compact' = ANY(rt.request_kinds)
      AND NOT EXISTS (
          SELECT 1
          FROM upstream_group_members m
          JOIN upstreams u ON u.id = m.upstream_id
          WHERE m.upstream_group_id = rt.upstream_group_id
            AND u.provider NOT IN ('openai', 'codex')
      )
)
UPDATE routes
SET request_kinds = array_append(request_kinds, 'openai_responses_compact')
WHERE id IN (SELECT id FROM eligible);

COMMIT;
