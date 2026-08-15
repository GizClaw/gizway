DO $contract$
DECLARE attempt integer;
BEGIN
    IF EXISTS (
        SELECT transaction_id
        FROM ledger_entries
        GROUP BY transaction_id
        HAVING count(*) < 2
           OR sum(CASE direction WHEN 'debit' THEN amount_microcredits ELSE -amount_microcredits END) <> 0
    ) THEN
        RAISE EXCEPTION 'ledger transaction is not balanced';
    END IF;

    IF EXISTS (
        SELECT settlement_account_id
        FROM merchants
        WHERE is_default
        GROUP BY settlement_account_id
        HAVING count(*) <> 1
    ) THEN
        RAISE EXCEPTION 'an account has more than one default Merchant';
    END IF;

    IF EXISTS (SELECT 1 FROM subscription_keys WHERE key IS NULL OR key = '') THEN
        RAISE EXCEPTION 'Subscription Key plaintext is missing';
    END IF;

    IF EXISTS (
        SELECT 1 FROM payg_charges charge
        LEFT JOIN ledger_transactions transaction ON transaction.id = charge.ledger_transaction_id
        WHERE transaction.id IS NULL
    ) THEN
        RAISE EXCEPTION 'Charge lacks its ledger transaction';
    END IF;

    IF EXISTS (
        SELECT 1 FROM payg_charges charge
        LEFT JOIN client_sync.charges projection ON projection.id = charge.id
        WHERE projection.id IS NULL OR projection.order_snapshot IS NULL
    ) THEN
        RAISE EXCEPTION 'Charge lacks its user-visible projection or order snapshot';
    END IF;

	IF EXISTS (SELECT 1 FROM topups WHERE external_reference='m03-bootstrap') THEN
		FOR attempt IN 1..100 LOOP
			EXIT WHEN EXISTS (
				SELECT 1 FROM payg_charges
				WHERE order_snapshot #>> '{order,execution_mode}' = 'realtime'
			);
			PERFORM pg_sleep(0.05);
		END LOOP;
		IF NOT EXISTS (
			SELECT 1 FROM payg_charges charge
			JOIN ledger_transactions transaction ON transaction.id=charge.ledger_transaction_id
			WHERE charge.order_snapshot #>> '{order,execution_mode}' = 'realtime'
			  AND transaction.status='posted'
		) THEN
			RAISE EXCEPTION 'Realtime Usage did not reach Charge and posted Ledger state';
		END IF;
	END IF;
END
$contract$;
