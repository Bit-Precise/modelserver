-- 075_add_gpt_image_2_5.sql
--
-- Register GPT Image 2.5 in the image-model catalog. OpenAI's pricing page
-- lists the GPT Image 2.5 Sunburst and Flare variants at the same token rates
-- as this family entry; operators can map the catalog model to either variant
-- in the upstream configuration.
--
-- Official OpenAI API prices per 1M tokens (fetched 2026-09-16):
--   image input          $8.00  -> 1.067 credits
--   cached image input   $2.00  -> 0.267 credits
--   image output        $30.00  -> 4.000 credits
--   text input           $5.00  -> 0.667 credits
--   cached text input    $1.25  -> 0.167 credits
--   text output            n/a  -> 0 credits
--
-- Credit values use the project convention of API USD price / 7.5. Image
-- models are billed from default_image_credit_rate, so no plan-level model
-- rate is seeded here.
-- Source: https://developers.openai.com/api/docs/pricing

INSERT INTO models (
    name,
    display_name,
    description,
    aliases,
    default_credit_rate,
    default_image_credit_rate,
    status,
    publisher,
    metadata
)
VALUES (
    'gpt-image-2.5',
    'GPT Image 2.5',
    'OpenAI GPT Image 2.5 image generation and editing model.',
    '{}',
    NULL,
    '{"text_input_rate":0.667,"text_cached_input_rate":0.167,"text_output_rate":0,"image_input_rate":1.067,"image_cached_input_rate":0.267,"image_output_rate":4.0}'::jsonb,
    'active',
    'openai',
    '{"capabilities":["image_generation","image_edit"],"provider_hint":"openai","category":"image"}'::jsonb
)
ON CONFLICT (name) DO UPDATE
SET default_image_credit_rate = COALESCE(
        models.default_image_credit_rate,
        EXCLUDED.default_image_credit_rate
    ),
    publisher = CASE
        WHEN models.publisher = '' THEN EXCLUDED.publisher
        ELSE models.publisher
    END,
    metadata = CASE
        WHEN models.metadata = '{}'::jsonb THEN EXCLUDED.metadata
        ELSE models.metadata
    END,
    updated_at = CASE
        WHEN models.default_image_credit_rate IS NULL
          OR models.publisher = ''
          OR models.metadata = '{}'::jsonb
        THEN NOW()
        ELSE models.updated_at
    END;
