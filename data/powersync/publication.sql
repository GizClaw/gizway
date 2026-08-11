-- Apply with the PowerSync replication role during production provisioning.
-- Views are not directly publishable by PostgreSQL logical replication; these
-- are the minimal source tables from which the five sync-rule views derive.
-- The application migration deliberately does not CREATE PUBLICATION because
-- normal runtime credentials must not have replication-level privileges.
CREATE PUBLICATION gizway_powersync FOR TABLE
    accounts,
    gateway_requests,
    credit_transfers,
    topups,
    refunds,
    payment_intents,
    ledger_accounts,
    ledger_transactions,
    ledger_entries;
