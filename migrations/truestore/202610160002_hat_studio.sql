-- WOTAN_HAT_STORE_NORTHSTAR.md Phase 4 ("player-drawn hats"), shipped 2026-10-16.
-- Founder real-time: players draw hats in a pixel editor, set the price themselves, and get 88%
-- of every sale; the other 12% is burned (removed from circulation) to offset inflation.
--
--   png_data      the hat's 32x32 RGBA PNG, validated + re-encoded server-side (NULL = a curated
--                 hat that still uses the legacy emoji placeholder in image_asset).
--   status        'listed' (buyable), 'unlisted' (creator pulled it; owners keep it),
--                 'review' (auto-held after enough player reports), 'removed' (taken down).
--   report_count  player reports; 3 distinct-enough reports hold a hat for review.
--   sales_count   number of sales, for the creator dashboard and price guide.
--
-- creator = hats.generated_by_character_id when user_generated=1 and flow_cost>0 (the 4.5
-- surprise-box hats reuse those columns at flow_cost=0 and are never for sale).
ALTER TABLE hats ADD COLUMN png_data BLOB DEFAULT NULL;
ALTER TABLE hats ADD COLUMN status VARCHAR(16) NOT NULL DEFAULT 'listed';
ALTER TABLE hats ADD COLUMN report_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE hats ADD COLUMN sales_count INTEGER NOT NULL DEFAULT 0;

-- One row per sale: the full split is recorded so "how much Flow has been burned" is a SUM, not
-- a guess. Curated (house) hats have no seller; the whole price is burned.
CREATE TABLE IF NOT EXISTS hat_sales (
    sale_id              CHAR(36)    NOT NULL PRIMARY KEY,
    hat_id               CHAR(36)    NOT NULL,
    buyer_character_id   CHAR(36)    NOT NULL,
    seller_character_id  CHAR(36)    DEFAULT NULL,
    price                INTEGER     NOT NULL,
    creator_amount       INTEGER     NOT NULL,
    burn_amount          INTEGER     NOT NULL,
    created_at           DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- Distinct reporters, so one player cannot hold a hat for review alone.
CREATE TABLE IF NOT EXISTS hat_reports (
    hat_id        CHAR(36)  NOT NULL,
    character_id  CHAR(36)  NOT NULL,
    created_at    DATETIME  NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (hat_id, character_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
