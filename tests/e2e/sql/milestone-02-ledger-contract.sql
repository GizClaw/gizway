-- Run against the central GizPay database after Charge stories.
DO $contract$
BEGIN
    IF EXISTS (
        SELECT transaction_id
        FROM ledger_entries
        GROUP BY transaction_id
        HAVING count(*) < 2
           OR sum(CASE direction
                    WHEN 'debit' THEN amount_microcredits
                    WHEN 'credit' THEN -amount_microcredits
                    ELSE 1
                  END) <> 0
    ) THEN
        RAISE EXCEPTION 'ledger transaction is not debit-credit balanced';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM payg_charges charge
        LEFT JOIN ledger_transactions ledger_tx
          ON ledger_tx.id = charge.ledger_transaction_id
        WHERE charge.ledger_transaction_id IS NULL OR ledger_tx.id IS NULL
    ) THEN
        RAISE EXCEPTION 'PAYG Charge lacks its posted ledger transaction';
    END IF;

    IF EXISTS (
        SELECT 1 FROM payg_charges charge
        WHERE NOT EXISTS (
            SELECT 1 FROM ledger_entries entry
            WHERE entry.transaction_id = charge.ledger_transaction_id
        )
    ) THEN
        RAISE EXCEPTION 'PAYG Charge transaction has no ledger entries';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM ledger_transactions ledger_tx
        LEFT JOIN payg_charges charge
          ON charge.ledger_transaction_id = ledger_tx.id
        WHERE ledger_tx.transaction_type = 'payg_charge'
          AND charge.id IS NULL
    ) THEN
        RAISE EXCEPTION 'orphan PAYG ledger transaction survived failed Charge';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM charge_commissions commission
        LEFT JOIN payg_charges charge ON charge.id = commission.charge_id
        WHERE charge.id IS NULL
    ) THEN
        RAISE EXCEPTION 'orphan Commission survived failed Charge';
    END IF;

    IF COALESCE((SELECT balance_microcredits FROM account_balances WHERE account_id='acct_platform'), 0)
       <> COALESCE((SELECT sum(platform_fee_microcredits) FROM payg_charges), 0) THEN
        RAISE EXCEPTION 'platform Credit balance does not equal exact cumulative Charge fee total';
    END IF;
END
$contract$;
