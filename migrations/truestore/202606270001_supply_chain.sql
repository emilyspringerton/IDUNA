-- S136-02 + S136-03: Emily Supply Chain AGI tables
-- vendors: registry of known product vendors (print, apparel, packaging, etc.)
-- supply_orders: purchase order lifecycle from draft through QC.

CREATE TABLE IF NOT EXISTS vendors (
    vendor_id   VARCHAR(36) NOT NULL DEFAULT (lower(hex(randomblob(4))) || '-' || lower(hex(randomblob(2))) || '-4' || substr(lower(hex(randomblob(2))),2) || '-' || substr('89ab',abs(random()) % 4 + 1, 1) || substr(lower(hex(randomblob(2))),2) || '-' || lower(hex(randomblob(6)))),
    name                VARCHAR(120) NOT NULL,
    category            VARCHAR(64)  NOT NULL,
    url                 TEXT         NOT NULL DEFAULT '',
    moq                 INTEGER      NOT NULL DEFAULT 1,
    unit_cost_cents     INTEGER      NOT NULL DEFAULT 0,
    lead_days           INTEGER      NOT NULL DEFAULT 14,
    quality_tier        VARCHAR(16)  NOT NULL DEFAULT 'standard',
    last_evaluated_at   DATETIME,
    notes               TEXT         NOT NULL DEFAULT '',
    status              VARCHAR(16)  NOT NULL DEFAULT 'active',
    created_at          DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (vendor_id)
);

CREATE TABLE IF NOT EXISTS supply_orders (
    order_id            VARCHAR(36) NOT NULL DEFAULT (lower(hex(randomblob(4))) || '-' || lower(hex(randomblob(2))) || '-4' || substr(lower(hex(randomblob(2))),2) || '-' || substr('89ab',abs(random()) % 4 + 1, 1) || substr(lower(hex(randomblob(2))),2) || '-' || lower(hex(randomblob(6)))),
    vendor_id           VARCHAR(36)  NOT NULL,
    product             VARCHAR(120) NOT NULL,
    quantity            INTEGER      NOT NULL DEFAULT 1,
    unit_cost_cents     INTEGER      NOT NULL DEFAULT 0,
    total_cost_cents    INTEGER      NOT NULL DEFAULT 0,
    status              VARCHAR(16)  NOT NULL DEFAULT 'pending',
    ordered_at          DATETIME,
    received_at         DATETIME,
    notes               TEXT         NOT NULL DEFAULT '',
    created_at          DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (order_id),
    FOREIGN KEY (vendor_id) REFERENCES vendors(vendor_id)
);

CREATE INDEX IF NOT EXISTS idx_vendors_category ON vendors(category);
CREATE INDEX IF NOT EXISTS idx_vendors_status ON vendors(status);
CREATE INDEX IF NOT EXISTS idx_supply_orders_vendor ON supply_orders(vendor_id);
CREATE INDEX IF NOT EXISTS idx_supply_orders_status ON supply_orders(status);
