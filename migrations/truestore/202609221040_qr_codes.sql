-- Dynamic QR code registry (kanban card 21312343124, founder real-time: "QR CODE GENERATOR -
-- IDUNA INTEGRATED ALLOW US TO UPDATE A URL ON IDUNA BACKEND qr.okemily.com").
--
-- The real point of a "dynamic" QR code: the QR image itself, once printed/placed, is immutable
-- -- but it only ever encodes IDUNA's own redirect URL (BASE_URL + "/q/" + slug), never the real
-- destination directly. Changing target_url later (PATCH) changes where every already-printed
-- copy of that QR code sends people, with zero reprinting -- the whole reason this is worth
-- building instead of just generating a static QR for a URL by hand.
--
-- slug is the real, short, public identifier (what actually appears in the redirect URL and the
-- QR payload) -- unique, may be founder-chosen (a memorable slug) or left blank at creation time
-- and auto-generated, same real "sometimes I want to type it, sometimes I don't" affordance
-- kanban card 82821821 already established for kanban ticket ids
-- (202609221040 vs 202609220930_app_releases.sql's own file-naming precedent, not a functional
-- dependency between the two).
CREATE TABLE IF NOT EXISTS qr_codes (
    id           INTEGER  PRIMARY KEY AUTOINCREMENT,
    slug         VARCHAR(64)   NOT NULL,
    target_url   VARCHAR(2000) NOT NULL,
    label        VARCHAR(200)  NOT NULL DEFAULT '',
    hit_count    INTEGER  NOT NULL DEFAULT 0,
    created_by   VARCHAR(100)  NOT NULL DEFAULT '',
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_qr_codes_slug ON qr_codes(slug);
