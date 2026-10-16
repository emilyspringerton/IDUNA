-- Secondary market for limited-edition hats. Once a limited hat sells out on the WOTAN hat store
-- (hats.quantity_limit reached), owners can resell their copies to each other here. Only limited
-- editions can be listed. A sale moves the character_hats row from seller to buyer; the buyer
-- pays the asking price, the seller receives 88% and 12% is burned (same split as the store).
-- seller_amount/burn_amount are recorded on the listing when it sells.
CREATE TABLE IF NOT EXISTS hat_listings (
    listing_id           CHAR(36)    NOT NULL PRIMARY KEY,
    hat_id               CHAR(36)    NOT NULL,
    seller_character_id  CHAR(36)    NOT NULL,
    price                INTEGER     NOT NULL,
    status               VARCHAR(12) NOT NULL DEFAULT 'active',
    buyer_character_id   CHAR(36)    DEFAULT NULL,
    seller_amount        INTEGER     NOT NULL DEFAULT 0,
    burn_amount          INTEGER     NOT NULL DEFAULT 0,
    created_at           DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    sold_at              DATETIME    DEFAULT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
CREATE INDEX idx_hat_listings_active ON hat_listings (status, hat_id, price);
