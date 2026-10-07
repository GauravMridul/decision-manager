-- +goose Up
-- SQL in this section is executed when the migration is applied

ALTER TABLE partner_service_mapping
    ADD COLUMN IF NOT EXISTS sales_channel_partner_name VARCHAR(255) DEFAULT '',
    ADD COLUMN IF NOT EXISTS sourcing_channel VARCHAR(255) DEFAULT '',
    ADD COLUMN IF NOT EXISTS name_of_consolidator VARCHAR(255) DEFAULT '';

-- +goose Down
-- SQL in this section is executed when the migration is rolled back

ALTER TABLE partner_service_mapping
    DROP COLUMN IF EXISTS sales_channel_partner_name,
    DROP COLUMN IF EXISTS sourcing_channel,
    DROP COLUMN IF EXISTS name_of_consolidator;
