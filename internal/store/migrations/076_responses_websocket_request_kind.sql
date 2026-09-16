-- Responses WebSocket is independently routable from HTTP Responses.
-- Register the kind without enabling it on existing routes: operators must
-- explicitly select openai_responses_websocket on the intended routes.

ALTER TABLE routes DROP CONSTRAINT routes_request_kinds_valid;
ALTER TABLE routes ADD CONSTRAINT routes_request_kinds_valid CHECK (
    request_kinds <@ ARRAY[
        'anthropic_messages',
        'anthropic_count_tokens',
        'openai_chat_completions',
        'openai_responses',
        'openai_responses_websocket',
        'openai_responses_compact',
        'google_generate_content',
        'openai_images_generations',
        'openai_images_edits'
    ]::TEXT[]
    AND array_length(request_kinds, 1) >= 1
);
