-- WOTAN_HAT_STORE_NORTHSTAR.md Phase 1: real hat catalog + character-hat inventory model.
-- Phase 0's own blocking prerequisite (a real, external Flow balance-query + spend API) turned
-- out to already exist -- GET /api/v1/characters/by-player/:player_id already returns
-- gold_balance (GFD's own Flow, synced from apps2/mud/main.go's runHeadlessCommand via
-- CreditGold/DeductGold), and PATCH /api/v1/characters/:id/gold already spends it atomically.
-- This migration is the real, actually-new work: the catalog itself.

-- Hand-curated v0 catalog -- real user-generated content is Phase 4 (the pixel editor), not
-- this pass. image_asset is a plain string placeholder (an emoji codepoint today, matching
-- OKEMILY/hats.html's own real mockup this table's seed data is drawn from) -- a real image
-- asset pipeline doesn't exist yet, named honestly as later work, not solved here.
CREATE TABLE IF NOT EXISTS hats (
    hat_id       CHAR(36)     NOT NULL PRIMARY KEY,
    name         VARCHAR(80)  NOT NULL,
    description  VARCHAR(255) NOT NULL DEFAULT '',
    flow_cost    INTEGER      NOT NULL,
    image_asset  VARCHAR(64)  NOT NULL DEFAULT '',
    created_at   DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- One row per (character, hat) a character actually owns. `equipped` mirrors
-- character_equipment's own real "one active slot" convention, scoped to a single boolean here
-- since a fighter wears exactly one hat at a time (real, deliberate v0 -- no multi-hat layering).
CREATE TABLE IF NOT EXISTS character_hats (
    character_id  CHAR(36)  NOT NULL,
    hat_id        CHAR(36)  NOT NULL,
    acquired_at   DATETIME  NOT NULL DEFAULT CURRENT_TIMESTAMP,
    equipped      TINYINT(1) NOT NULL DEFAULT 0,
    PRIMARY KEY (character_id, hat_id),
    CONSTRAINT fk_chrhat_char FOREIGN KEY (character_id) REFERENCES characters(character_id) ON DELETE CASCADE,
    CONSTRAINT fk_chrhat_hat FOREIGN KEY (hat_id) REFERENCES hats(hat_id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- Real seed catalog, drawn directly from OKEMILY/hats.html's own already-designed mockup (not
-- invented fresh here) -- 6 hats, each lore-grounded in a real, already-tuned BRAWLPIT
-- character from this same session's own tuning pass (Rosie, Second Tree, Raccoon, Uncrowned,
-- Medusa) plus one generic classic (Top Hat, Second Casting).
INSERT IGNORE INTO hats (hat_id, name, description, flow_cost, image_asset) VALUES
    ('a1b2c3d4-0001-4000-8000-000000000001', 'Top Hat, Second Casting', 'A theatrical classic. Goes well with a bow to nobody.', 250, '&#127914;'),
    ('a1b2c3d4-0001-4000-8000-000000000002', 'Uncrowned''s Doubt', 'A crown that reads more like a question than an answer.', 400, '&#128081;'),
    ('a1b2c3d4-0001-4000-8000-000000000003', 'Joystick Cap', 'The prop nobody asked for, now wearable.', 150, '&#127905;'),
    ('a1b2c3d4-0001-4000-8000-000000000004', 'Second Growth Wreath', 'Bark-and-canopy, openly angry, not that one.', 300, '&#127981;'),
    ('a1b2c3d4-0001-4000-8000-000000000005', 'Scavenger''s Vest Cap', 'The vest says R. Nothing else does.', 150, '&#128007;'),
    ('a1b2c3d4-0001-4000-8000-000000000006', 'Most-Summoned Circlet', 'Called back 26 times. More than anyone.', 450, '&#128378;');
