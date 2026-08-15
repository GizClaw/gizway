DO $contract$
DECLARE
    missing text;
BEGIN
    SELECT string_agg(required.name, ', ' ORDER BY required.name)
      INTO missing
      FROM (VALUES
          ('gizway.provider_key_billing'),
          ('gizway.provider_key_prices'),
          ('gizway.ai_orders'),
          ('gizway.charge_outbox'),
          ('client_sync.provider_keys'),
          ('client_sync.ai_usage'),
          ('bifrost_config.config_keys'),
          ('bifrost_logs.logs')
      ) AS required(name)
      WHERE to_regclass(required.name) IS NULL;
    IF missing IS NOT NULL THEN
        RAISE EXCEPTION 'missing Milestone 03 regional tables: %', missing;
    END IF;

    IF EXISTS (
        SELECT 1
        FROM bifrost_config.config_keys key
        LEFT JOIN gizway.provider_key_billing billing ON billing.provider_key_id = key.key_id
        WHERE key.enabled IS TRUE AND key.status = 'active'
          AND (billing.provider_key_id IS NULL OR billing.status <> 'active' OR billing.merchant_id = '')
    ) THEN
        RAISE EXCEPTION 'active Bifrost Key lacks an active Merchant mapping';
    END IF;

    IF EXISTS (
        SELECT provider_key_id, model_id, metric
        FROM gizway.provider_key_prices
        GROUP BY provider_key_id, model_id, metric
        HAVING count(*) <> 1
    ) THEN
        RAISE EXCEPTION 'Provider Key price uniqueness is broken';
    END IF;

    IF EXISTS (SELECT 1 FROM client_sync.provider_keys WHERE key IS NULL OR key = '') THEN
        RAISE EXCEPTION 'Provider Key plaintext projection is missing';
    END IF;

    IF EXISTS (
        SELECT 1 FROM gizway.ai_orders
        WHERE account_id = '' OR subscription_id = '' OR product_id = ''
           OR owner_identity_issuer = '' OR owner_identity_subject = ''
           OR external_order_id = '' OR provider_id = '' OR completed_at IS NULL
    ) THEN
        RAISE EXCEPTION 'AI Order ownership snapshot is incomplete';
    END IF;

	IF EXISTS (
		SELECT 1 FROM bifrost_logs.logs
		WHERE selected_key_id = '' OR model = '' OR provider = '' OR latency IS NULL
		   OR status NOT IN ('success','error') OR metadata IS NULL
	) THEN
		RAISE EXCEPTION 'Bifrost execution log is incomplete';
	END IF;

	IF EXISTS (
		SELECT 1 FROM bifrost_logs.logs
		WHERE status = 'success' AND (prompt_tokens <= 0 OR completion_tokens <= 0)
	) THEN
		RAISE EXCEPTION 'successful Bifrost execution log lacks AI metric tokens';
	END IF;

	IF EXISTS (SELECT 1 FROM client_sync.providers WHERE id='provider_story_cn')
	   AND NOT EXISTS (
		SELECT 1
		FROM bifrost_logs.logs log
		JOIN gizway.ai_orders orders
		  ON orders.external_order_id = log.metadata::jsonb->>'external_order_id'
		WHERE log.metadata::jsonb->>'execution_mode' = 'realtime'
		  AND log.status = 'success' AND log.stream IS TRUE
		  AND log.prompt_tokens = 12 AND log.completion_tokens = 7
		  AND orders.gross_microcredits > 0
		  AND EXISTS (
			SELECT 1 FROM client_sync.ai_usage usage
			WHERE usage.order_id = orders.id AND usage.metric='input_tokens' AND usage.quantity=12
		  )
		  AND EXISTS (
			SELECT 1 FROM client_sync.ai_usage usage
			WHERE usage.order_id = orders.id AND usage.metric='output_tokens' AND usage.quantity=7
		  )
	) THEN
		RAISE EXCEPTION 'Realtime WebSocket did not produce complete Log, Usage, and AI Order state';
	END IF;
END
$contract$;
