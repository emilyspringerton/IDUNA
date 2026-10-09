// hs-cards-import loads a Hearthstone card table (JSON array, the shape of hs_cards) into the
// truestore hs_cards table, then backfills existing hs_decks rows: a deck whose every dbf_id now
// resolves gets its display cards_json rebuilt from real names and names_resolved=1.
//
// Card data is an input file, never committed: run it against the live iduna.db with the JSON
// copied in beside it. Idempotent: re-running upserts the same rows and rewrites the same decks.
//
//	hs-cards-import -db /app/var/iduna.db -cards /app/var/hs-cards.json
package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"

	_ "modernc.org/sqlite"
)

// card mirrors one hs_cards row as it appears in the JSON input.
type card struct {
	DBFID    int    `json:"dbf_id"`
	CardID   string `json:"card_id"`
	Name     string `json:"name"`
	Cost     int    `json:"cost"`
	Attack   int    `json:"attack"`
	Health   int    `json:"health"`
	Class    string `json:"class"`
	Rarity   string `json:"rarity"`
	CardType string `json:"card_type"`
	Text     string `json:"text"`
	ImageURL string `json:"image_url"`
}

type deckCard struct {
	DBF   int `json:"dbf_id"`
	Count int `json:"count"`
}

type namedCard struct {
	Name  string `json:"name"`
	Cost  int    `json:"cost"`
	Count int    `json:"count"`
}

func main() {
	dbPath := flag.String("db", "", "iduna sqlite path, the live DB is var/iduna.db (required)")
	cardsPath := flag.String("cards", "", "card table JSON (required)")
	flag.Parse()
	if *dbPath == "" || *cardsPath == "" {
		flag.Usage()
		os.Exit(2)
	}

	raw, err := os.ReadFile(*cardsPath)
	if err != nil {
		log.Fatalf("read cards: %v", err)
	}
	var cards []card
	if err := json.Unmarshal(raw, &cards); err != nil {
		log.Fatalf("parse cards: %v", err)
	}

	db, err := sql.Open("sqlite", *dbPath+"?_foreign_keys=on&_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		log.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	tx, err := db.Begin()
	if err != nil {
		log.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()

	upsert, err := tx.Prepare(`INSERT INTO hs_cards (dbf_id,name,cost,class,rarity,card_set,text,image_url)
		VALUES (?,?,?,?,?,?,?,?)
		ON CONFLICT(dbf_id) DO UPDATE SET name=excluded.name,cost=excluded.cost,class=excluded.class,
		  rarity=excluded.rarity,text=excluded.text,image_url=excluded.image_url`)
	if err != nil {
		log.Fatalf("prepare upsert: %v", err)
	}
	defer upsert.Close()

	names := map[int]namedCard{}
	for _, c := range cards {
		if c.DBFID == 0 || c.Name == "" {
			continue
		}
		// card_set stays empty: the set is not in the CardDef/Dbf tables this import reads.
		if _, err := upsert.Exec(c.DBFID, c.Name, c.Cost, c.Class, c.Rarity, "", c.Text, c.ImageURL); err != nil {
			log.Fatalf("upsert dbf %d: %v", c.DBFID, err)
		}
		names[c.DBFID] = namedCard{Name: c.Name, Cost: c.Cost}
	}
	fmt.Printf("hs_cards: upserted %d rows\n", len(names))

	rows, err := tx.Query(`SELECT id, dbf_json FROM hs_decks WHERE deleted=0`)
	if err != nil {
		log.Fatalf("query decks: %v", err)
	}
	type deckRow struct {
		id      int64
		dbfJSON string
	}
	var decks []deckRow
	for rows.Next() {
		var d deckRow
		if err := rows.Scan(&d.id, &d.dbfJSON); err != nil {
			log.Fatalf("scan deck: %v", err)
		}
		decks = append(decks, d)
	}
	rows.Close()

	resolved, partial := 0, 0
	update, err := tx.Prepare(`UPDATE hs_decks SET cards_json=?, names_resolved=1 WHERE id=?`)
	if err != nil {
		log.Fatalf("prepare update: %v", err)
	}
	defer update.Close()
	for _, d := range decks {
		var dbf []deckCard
		if err := json.Unmarshal([]byte(d.dbfJSON), &dbf); err != nil {
			partial++
			continue
		}
		display := make([]namedCard, 0, len(dbf))
		complete := true
		for _, dc := range dbf {
			n, ok := names[dc.DBF]
			if !ok {
				// Not in this import: check the table already has it (earlier import).
				var name string
				var cost int
				err := tx.QueryRow(`SELECT name,cost FROM hs_cards WHERE dbf_id=?`, dc.DBF).Scan(&name, &cost)
				if err != nil {
					complete = false
					break
				}
				n = namedCard{Name: name, Cost: cost}
			}
			display = append(display, namedCard{Name: n.Name, Cost: n.Cost, Count: dc.Count})
		}
		if !complete {
			partial++
			continue
		}
		sort.Slice(display, func(i, j int) bool {
			if display[i].Cost != display[j].Cost {
				return display[i].Cost < display[j].Cost
			}
			return display[i].Name < display[j].Name
		})
		out, _ := json.Marshal(display)
		if _, err := update.Exec(string(out), d.id); err != nil {
			log.Fatalf("update deck %d: %v", d.id, err)
		}
		resolved++
	}
	if err := tx.Commit(); err != nil {
		log.Fatalf("commit: %v", err)
	}
	fmt.Printf("hs_decks: %d resolved, %d left with placeholders (missing cards)\n", resolved, partial)
}
