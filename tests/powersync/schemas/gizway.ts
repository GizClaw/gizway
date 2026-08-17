import { column, Schema, Table } from '@powersync/node';

export const GizWaySchema = new Schema({
  model_listings: new Table({
    model_id: column.text, title: column.text, description: column.text, family: column.text,
    context: column.text, latency: column.text, accent: column.text, featured: column.integer,
    display_order: column.integer, availability: column.text, created_at: column.text, updated_at: column.text
  }),
  models: new Table({
    provider_id: column.text,
    name: column.text,
    status: column.text
  }),
  providers: new Table({
    name: column.text,
    kind: column.text,
    status: column.text
  }),
  model_customer_prices: new Table({
    model_id: column.text,
    metric: column.text,
    unit_size: column.integer,
    price_microcredits: column.integer
  }),
  my_provider_keys: new Table({
    provider_id: column.text,
    key: column.text,
    merchant_id: column.text,
    name: column.text,
    last_used_at: column.text,
    earned_microcredits: column.integer,
    status: column.text,
    prices_json: column.text,
    created_at: column.text,
    updated_at: column.text
  }),
  my_ai_orders: new Table({
    external_order_id: column.text,
    account_id: column.text,
    subscription_id: column.text,
    subscription_key_id: column.text,
    product_id: column.text,
    model_id: column.text,
    provider_id: column.text,
    gross_microcredits: column.integer,
    status: column.text,
    created_at: column.text,
    completed_at: column.text
  }),
  my_ai_usage: new Table({
    account_id: column.text,
    order_id: column.text,
    model_id: column.text,
    metric: column.text,
    quantity: column.integer,
    status: column.text,
    created_at: column.text
  })
});
