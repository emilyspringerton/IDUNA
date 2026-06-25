-- M1: Extend items table with def_id and flags
ALTER TABLE items ADD COLUMN def_id   INTEGER NOT NULL DEFAULT 0;
ALTER TABLE items ADD COLUMN flags    INTEGER NOT NULL DEFAULT 0;

-- M2: Character equipment — one row per (character, slot), NULL item_id = empty slot
CREATE TABLE IF NOT EXISTS character_equipment (
    character_id  CHAR(36)     NOT NULL,
    slot          VARCHAR(16)  NOT NULL,
    item_id       CHAR(36)     DEFAULT NULL,
    PRIMARY KEY (character_id, slot),
    CONSTRAINT fk_chreq_char FOREIGN KEY (character_id) REFERENCES characters(character_id) ON DELETE CASCADE,
    CONSTRAINT fk_chreq_item FOREIGN KEY (item_id) REFERENCES items(item_id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- M3: Character inventory bags — one row per occupied slot
-- bag: 'inventory' | 'storage' | 'temporary'
CREATE TABLE IF NOT EXISTS character_inventory (
    id            INTEGER      NOT NULL AUTO_INCREMENT PRIMARY KEY,
    character_id  CHAR(36)     NOT NULL,
    bag           VARCHAR(16)  NOT NULL,
    slot_index    INTEGER      NOT NULL,
    item_id       CHAR(36)     NOT NULL,
    def_id        INTEGER      NOT NULL,
    quantity      INTEGER      NOT NULL DEFAULT 1,
    UNIQUE KEY uq_bag_slot (character_id, bag, slot_index),
    CONSTRAINT fk_chrinv_char FOREIGN KEY (character_id) REFERENCES characters(character_id) ON DELETE CASCADE,
    CONSTRAINT fk_chrinv_item FOREIGN KEY (item_id) REFERENCES items(item_id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- M4: Character key items — non-spatial tab
CREATE TABLE IF NOT EXISTS character_key_items (
    character_id  CHAR(36)  NOT NULL,
    def_id        INTEGER   NOT NULL,
    acquired_at   DATETIME  NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (character_id, def_id),
    CONSTRAINT fk_chrki_char FOREIGN KEY (character_id) REFERENCES characters(character_id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- M5: Inventory bag capacity per character (Gobbiebag expansion state)
CREATE TABLE IF NOT EXISTS character_bag_capacity (
    character_id  CHAR(36)  NOT NULL,
    bag           VARCHAR(16) NOT NULL,
    capacity      INTEGER   NOT NULL DEFAULT 30,
    PRIMARY KEY (character_id, bag),
    CONSTRAINT fk_chrbag_char FOREIGN KEY (character_id) REFERENCES characters(character_id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
