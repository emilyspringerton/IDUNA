-- S252-00: real, correctly-shaped table for apps2/mud's own simple stackable-material
-- inventory (map[item_id]quantity) -- GFD-AH-93944's real root cause, per
-- GoblinFoxDragon/docs2/INVENTORY_PERSISTENCE_NORTHSTAR.md's own Phase 0 plan.
--
-- Deliberately NOT named `character_inventory` -- that name is already taken by a real,
-- different, slot-based bag/equipment system (202606250002_mmo_inventory.sql: bag +
-- slot_index + a UUID item_id referencing a real crafted `items` row, one row per occupied
-- slot). apps2/mud's own `p.inventory` is a flat map of shop-string item ids
-- ("earth-crystal") to a plain integer count, with no slots/bags/UUIDs at all -- a real,
-- checked structural mismatch, not a naming coincidence to paper over. No FK to
-- `characters` on purpose, matching this table's own established "don't couple to a
-- possibly-out-of-order write" precedent elsewhere in this schema.
CREATE TABLE IF NOT EXISTS gfd_stackable_items (
    character_id CHAR(36)    NOT NULL,
    item_id      VARCHAR(64) NOT NULL,
    quantity     INTEGER     NOT NULL DEFAULT 0,
    updated_at   DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (character_id, item_id)
);
