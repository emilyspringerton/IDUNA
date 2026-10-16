package handlers_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"
)

// soldOutLimited publishes a limited hat (limit 1), has `owner` buy the single copy, and returns its id.
func soldOutLimited(t *testing.T, db *sql.DB, owner string) string {
	t.Helper()
	rec := studioDo(t, db, "POST", "/api/v1/characters/maker/hats/design", map[string]any{
		"name": "Ltd", "flow_cost": 100, "png_base64": studioPNG(t, 32, 100), "quantity_limit": 1,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("publish: %d %s", rec.Code, rec.Body.String())
	}
	var hat map[string]any
	json.Unmarshal(rec.Body.Bytes(), &hat)
	id := hat["hat_id"].(string)
	if r := studioDo(t, db, "POST", "/api/v1/characters/"+owner+"/hats/buy", map[string]string{"hat_id": id}); r.Code != http.StatusNoContent {
		t.Fatalf("buy: %d %s", r.Code, r.Body.String())
	}
	return id
}

func marketSetup(t *testing.T) (*sql.DB, string) {
	db := newInventoryDB(t)
	for _, id := range []string{"maker", "seller", "buyer", "poor"} {
		seedCharacterForInv(t, db, id)
	}
	db.Exec(`UPDATE characters SET gold_balance=1000 WHERE character_id IN ('seller','buyer')`)
	return db, soldOutLimited(t, db, "seller")
}

func TestHatMarket_ResaleMovesHatAndSplits88_12(t *testing.T) {
	db, hat := marketSetup(t)
	defer db.Close()
	before := gold(db, "seller")     // 900 after buying the store copy
	makerBefore := gold(db, "maker") // already holds the 88% store-sale proceeds
	rec := studioDo(t, db, "POST", "/api/v1/characters/seller/hats/market/list", map[string]any{"hat_id": hat, "price": 201})
	if rec.Code != http.StatusCreated {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	var l map[string]any
	json.Unmarshal(rec.Body.Bytes(), &l)

	m := studioDo(t, db, "GET", "/api/v1/hats/market", nil)
	var listings []map[string]any
	json.Unmarshal(m.Body.Bytes(), &listings)
	if len(listings) != 1 || listings[0]["price"].(float64) != 201 {
		t.Fatalf("market = %v", listings)
	}

	if r := studioDo(t, db, "POST", "/api/v1/characters/buyer/hats/market/buy", map[string]string{"listing_id": l["listing_id"].(string)}); r.Code != http.StatusNoContent {
		t.Fatalf("market buy: %d %s", r.Code, r.Body.String())
	}
	if g := gold(db, "buyer"); g != 1000-201 {
		t.Errorf("buyer = %d", g)
	}
	// 201 Flow: seller floor(176.88)=176, designer royalty floor(16.08)=16, burned 201-176-16=9.
	if g := gold(db, "seller"); g != before+176 {
		t.Errorf("seller = %d, want %d", g, before+176)
	}
	if g := gold(db, "maker"); g != makerBefore+16 {
		t.Errorf("designer royalty: maker = %d, want %d", g, makerBefore+16)
	}
	var burn, roy int
	db.QueryRow(`SELECT burn_amount, royalty_amount FROM hat_listings WHERE status='sold'`).Scan(&burn, &roy)
	if burn != 9 || roy != 16 {
		t.Errorf("recorded burn=%d royalty=%d, want 9 and 16", burn, roy)
	}
	var owns int
	db.QueryRow(`SELECT COUNT(*) FROM character_hats WHERE hat_id=? AND character_id='buyer'`, hat).Scan(&owns)
	var sellerOwns int
	db.QueryRow(`SELECT COUNT(*) FROM character_hats WHERE hat_id=? AND character_id='seller'`, hat).Scan(&sellerOwns)
	if owns != 1 || sellerOwns != 0 {
		t.Errorf("ownership buyer=%d seller=%d", owns, sellerOwns)
	}
	// Resale is not a store sale: sales_count stays at 1 and the listing is gone from the market.
	var sc int
	db.QueryRow(`SELECT sales_count FROM hats WHERE hat_id=?`, hat).Scan(&sc)
	if sc != 1 {
		t.Errorf("sales_count = %d, want 1", sc)
	}
	// Second purchase of the same listing fails.
	if r := studioDo(t, db, "POST", "/api/v1/characters/poor/hats/market/buy", map[string]string{"listing_id": l["listing_id"].(string)}); r.Code != http.StatusNotFound {
		t.Errorf("rebuy = %d, want 404", r.Code)
	}
}

func TestHatMarket_Rules(t *testing.T) {
	db, hat := marketSetup(t)
	defer db.Close()
	// Unlimited hats cannot be listed.
	unl := publish(t, db, "maker", 100)
	db.Exec(`INSERT INTO character_hats (character_id, hat_id, acquired_at) VALUES ('seller', ?, '')`, unl)
	if r := studioDo(t, db, "POST", "/api/v1/characters/seller/hats/market/list", map[string]any{"hat_id": unl, "price": 100}); r.Code != http.StatusConflict {
		t.Errorf("unlimited list = %d, want 409", r.Code)
	}
	// Can't list what you don't own; price bounds.
	if r := studioDo(t, db, "POST", "/api/v1/characters/buyer/hats/market/list", map[string]any{"hat_id": hat, "price": 100}); r.Code != http.StatusNotFound {
		t.Errorf("unowned list = %d, want 404", r.Code)
	}
	if r := studioDo(t, db, "POST", "/api/v1/characters/seller/hats/market/list", map[string]any{"hat_id": hat, "price": 5}); r.Code != http.StatusBadRequest {
		t.Errorf("low price = %d, want 400", r.Code)
	}
	rec := studioDo(t, db, "POST", "/api/v1/characters/seller/hats/market/list", map[string]any{"hat_id": hat, "price": 500})
	var l map[string]any
	json.Unmarshal(rec.Body.Bytes(), &l)
	lid := l["listing_id"].(string)
	if r := studioDo(t, db, "POST", "/api/v1/characters/seller/hats/market/list", map[string]any{"hat_id": hat, "price": 400}); r.Code != http.StatusConflict {
		t.Errorf("double list = %d, want 409", r.Code)
	}
	if r := studioDo(t, db, "POST", "/api/v1/characters/seller/hats/market/buy", map[string]string{"listing_id": lid}); r.Code != http.StatusConflict {
		t.Errorf("self buy = %d, want 409", r.Code)
	}
	// Insufficient Flow: nothing moves.
	if r := studioDo(t, db, "POST", "/api/v1/characters/poor/hats/market/buy", map[string]string{"listing_id": lid}); r.Code != http.StatusConflict {
		t.Errorf("poor buy = %d, want 409", r.Code)
	}
	var sellerOwns int
	db.QueryRow(`SELECT COUNT(*) FROM character_hats WHERE hat_id=? AND character_id='seller'`, hat).Scan(&sellerOwns)
	if sellerOwns != 1 {
		t.Errorf("failed buy moved the hat")
	}
	// Only the seller can cancel; cancelled listings can't be bought.
	if r := studioDo(t, db, "POST", "/api/v1/characters/buyer/hats/market/cancel", map[string]string{"listing_id": lid}); r.Code != http.StatusNotFound {
		t.Errorf("foreign cancel = %d, want 404", r.Code)
	}
	if r := studioDo(t, db, "POST", "/api/v1/characters/seller/hats/market/cancel", map[string]string{"listing_id": lid}); r.Code != http.StatusNoContent {
		t.Errorf("cancel = %d, want 204", r.Code)
	}
	if r := studioDo(t, db, "POST", "/api/v1/characters/buyer/hats/market/buy", map[string]string{"listing_id": lid}); r.Code != http.StatusNotFound {
		t.Errorf("buy cancelled = %d, want 404", r.Code)
	}
}
