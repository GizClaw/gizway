import { column, Schema, Table } from "@powersync/web";

export const gizPaySchema = new Schema({
  product_listings: new Table({ product_id: column.text, site: column.text, title: column.text, description: column.text, billing_mode: column.text, price_text: column.text, display_order: column.integer, status: column.text, created_at: column.text, updated_at: column.text }),
  my_profile: new Table({ email: column.text, display_name: column.text, merchant_id: column.text, status: column.text, created_at: column.text }),
  my_accounts: new Table({ owner_user_id: column.text, status: column.text, created_at: column.text }),
  my_balances: new Table({ account_id: column.text, balance_microcredits: column.integer }),
  my_merchants: new Table({ settlement_account_id: column.text, legal_name: column.text, public_name: column.text, is_default: column.integer, status: column.text, created_at: column.text, updated_at: column.text }),
  available_products: new Table({ merchant_id: column.text, name: column.text, billing_mode: column.text, status: column.text, terms_version: column.text }),
  my_products: new Table({ merchant_id: column.text, name: column.text, billing_mode: column.text, status: column.text, terms_version: column.text }),
  my_subscriptions: new Table({ account_id: column.text, product_id: column.text, status: column.text, terms_version: column.text, created_at: column.text }),
  my_subscription_keys: new Table({ subscription_id: column.text, name: column.text, key: column.text, status: column.text, created_at: column.text, last_used_at: column.text, revoked_at: column.text }),
  my_topups: new Table({ account_id: column.text, amount_microcredits: column.integer, channel: column.text, external_reference: column.text, status: column.text, created_at: column.text, credited_at: column.text }),
  my_charges: new Table({ account_id: column.text, subscription_id: column.text, external_order_id: column.text, gross_microcredits: column.integer, order_snapshot: column.text, created_at: column.text }),
  my_transactions: new Table({ account_id: column.text, transaction_type: column.text, amount_microcredits: column.integer, created_at: column.text }),
  my_commissions: new Table({ merchant_id: column.text, charge_id: column.text, amount_microcredits: column.integer, created_at: column.text }),
  my_service_accounts: new Table({ name: column.text, roles: column.text, status: column.text, created_at: column.text, revoked_at: column.text }),
});
