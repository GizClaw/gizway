-- Milestone 02 runtime database audit. Run once against CN and once against
-- Global after the Hurl stories, with the regional schemas on the same DB.
-- This file deliberately queries Bifrost's real Config Store tables; GizWay's
-- migration must not create substitutes for them.

DO $contract$
DECLARE
    missing text;
BEGIN
    SELECT string_agg(required.name, ', ' ORDER BY required.name)
      INTO missing
      FROM (VALUES
          ('gizway.models'),
          ('gizway.provider_key_billing'),
          ('gizway.provider_key_prices'),
          ('bifrost_config.config_providers'),
          ('bifrost_config.config_keys')
      ) AS required(name)
      WHERE to_regclass(required.name) IS NULL;
    IF missing IS NOT NULL THEN
        RAISE EXCEPTION 'missing Milestone 02 regional tables: %', missing;
    END IF;

    -- The Provider and callable credential must be persisted by Bifrost. The
    -- Admin API test separately proves Config Store reads return full Secret
    -- text; this audit proves GizWay did not keep the only copy itself.
    IF NOT EXISTS (SELECT 1 FROM bifrost_config.config_providers) THEN
        RAISE EXCEPTION 'Bifrost Config Store contains no Provider';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM bifrost_config.config_keys
        WHERE key_id IS NOT NULL AND value IS NOT NULL AND value <> ''
    ) THEN
        RAISE EXCEPTION 'Bifrost Config Store contains no Provider API Key value';
    END IF;

    -- Every callable Bifrost Key has a GizWay payment mapping. Aggregate
    -- creation failures may leave an inactive Bifrost row, never an enabled
    -- orphan that could be selected without a Merchant.
    IF EXISTS (
        SELECT 1
        FROM bifrost_config.config_keys key
        LEFT JOIN gizway.provider_key_billing billing
          ON billing.bifrost_key_id = key.key_id
        WHERE key.enabled IS TRUE
          AND COALESCE(key.status, 'active') = 'active'
          AND (billing.bifrost_key_id IS NULL OR billing.status <> 'active'
               OR billing.beneficiary_merchant_id IS NULL)
    ) THEN
        RAISE EXCEPTION 'active Bifrost Key lacks an active Merchant mapping';
    END IF;

    IF EXISTS (
        SELECT 1 FROM information_schema.tables
        WHERE table_schema = 'gizway'
          AND (table_name ILIKE '%quota%' OR table_name ILIKE '%reservation%'
               OR table_name ILIKE '%lease%')
    ) THEN
        RAISE EXCEPTION 'GizWay persisted forbidden Quota state';
    END IF;

    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'gizway'
          AND column_name = ANY (ARRAY[
              'api_key', 'provider_api_key', 'provider_secret', 'credential',
              'credential_ref', 'encrypted_key', 'secret'
          ])
    ) THEN
        RAISE EXCEPTION 'GizWay copied a Provider credential outside Bifrost Config Store';
    END IF;

    IF EXISTS (
        SELECT bifrost_key_id
        FROM gizway.provider_key_billing
        GROUP BY bifrost_key_id
        HAVING count(*) <> 1
    ) THEN
        RAISE EXCEPTION 'one Bifrost Key has multiple GizWay billing mappings';
    END IF;
END
$contract$;
