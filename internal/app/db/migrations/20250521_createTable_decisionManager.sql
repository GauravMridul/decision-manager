-- +goose Up
-- SQL in this section is executed when the migration is applied

CREATE TABLE IF NOT EXISTS service_sfdc_field_mapping (
    id BIGSERIAL PRIMARY KEY,
    service_name VARCHAR(255) NOT NULL,
    request_body JSONB DEFAULT '{}'::jsonb,
    created_date TIMESTAMP NOT NULL,
    created_by VARCHAR(255) NOT NULL,
    modified_date TIMESTAMP,
    modified_by VARCHAR(255),
    is_deleted BOOLEAN DEFAULT FALSE
);

CREATE TABLE IF NOT EXISTS query_object_relationship_map (
    id BIGSERIAL PRIMARY KEY,
    query_object VARCHAR(255) NOT NULL,
    query_relation VARCHAR(255) NOT NULL,
    additional_fields TEXT,
    created_date TIMESTAMP NOT NULL,
    created_by VARCHAR(255) NOT NULL,
    modified_date TIMESTAMP,
    modified_by VARCHAR(255),
    is_deleted BOOLEAN DEFAULT FALSE
);

CREATE TABLE IF NOT EXISTS partner_service_mapping (
    id BIGSERIAL PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    partner_name VARCHAR(255) NOT NULL,
    program_type VARCHAR(255) DEFAULT '',
    business_type VARCHAR(255) DEFAULT '',
    sourcing_program VARCHAR(255) DEFAULT '',
    loan_category VARCHAR(255) DEFAULT '',
    customer_type VARCHAR(255) DEFAULT '',
    product_line VARCHAR(255) DEFAULT '',
    stage VARCHAR(255) NOT NULL,
    service_sequence_string VARCHAR(255) NOT NULL,
    created_date TIMESTAMP NOT NULL,
    created_by VARCHAR(255) NOT NULL,
    modified_date TIMESTAMP,
    modified_by VARCHAR(255),
    is_deleted BOOLEAN DEFAULT FALSE
);

-- +goose Down
-- SQL in this section is executed when the migration is rolled back

DROP TABLE IF EXISTS partner_service_mapping;

DROP TABLE IF EXISTS query_object_relationship_map;

DROP TABLE IF EXISTS service_sfdc_field_mapping;
