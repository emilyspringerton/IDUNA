package handlers_test

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"

	"iduna/internal/http/handlers"
)

func studioPNG(t *testing.T, size, filled int) string {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for i := 0; i < filled; i++ {
		img.SetNRGBA(i%size, i/size, color.NRGBA{200, 30, 30, 255})
	}
	var b bytes.Buffer
	png.Encode(&b, img)
	return base64.StdEncoding.EncodeToString(b.Bytes())
}

func studioDo(t *testing.T, db *sql.DB, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		j, _ := json.Marshal(body)
		rd = bytes.NewReader(j)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rd)
	rec := httptest.NewRecorder()
	(&handlers.MMOHandler{DB: db}).ServeHTTP(rec, req)
	return rec
}

func gold(db *sql.DB, id string) int {
	var g int
	db.QueryRow(`SELECT gold_balance FROM characters WHERE character_id=?`, id).Scan(&g)
	return g
}

func publish(t *testing.T, db *sql.DB, who string, price int) string {
	t.Helper()
	rec := studioDo(t, db, "POST", "/api/v1/characters/"+who+"/hats/design", map[string]any{
		"name": "Cone Hat", "description": "pointy", "flow_cost": price, "png_base64": studioPNG(t, 32, 100),
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("publish: %d %s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	json.Unmarshal(rec.Body.Bytes(), &out)
	return out["hat_id"].(string)
}

func TestHatStudio_SaleSplits88_12AndBurnsRemainder(t *testing.T) {
	db := newInventoryDB(t)
	defer db.Close()
	seedCharacterForInv(t, db, "maker")
	seedCharacterForInv(t, db, "buyer")
	db.Exec(`UPDATE characters SET gold_balance=1000 WHERE character_id='buyer'`)
	hat := publish(t, db, "maker", 101) // odd price: creator floor(88.88)=88, burn 13

	rec := studioDo(t, db, "POST", "/api/v1/characters/buyer/hats/buy", map[string]string{"hat_id": hat})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("buy: %d %s", rec.Code, rec.Body.String())
	}
	if g := gold(db, "buyer"); g != 899 {
		t.Errorf("buyer balance = %d, want 899", g)
	}
	if g := gold(db, "maker"); g != 88 {
		t.Errorf("maker balance = %d, want 88", g)
	}
	var burn, creator, sales int
	db.QueryRow(`SELECT burn_amount, creator_amount FROM hat_sales`).Scan(&burn, &creator)
	db.QueryRow(`SELECT sales_count FROM hats WHERE hat_id=?`, hat).Scan(&sales)
	if burn != 13 || creator != 88 || sales != 1 {
		t.Errorf("ledger burn=%d creator=%d sales=%d, want 13/88/1", burn, creator, sales)
	}
	if rec.Header().Get("X-Flow-Burned") != "13" {
		t.Errorf("burn header = %q", rec.Header().Get("X-Flow-Burned"))
	}
	// conservation: nothing created from thin air (1000+0 -> 899+88+13 burned)
	if 899+88+13 != 1000 {
		t.Fatal("arithmetic")
	}
}

func TestHatStudio_CuratedHatBurnsWholePrice(t *testing.T) {
	db := newInventoryDB(t)
	defer db.Close()
	seedCharacterForInv(t, db, "buyer")
	db.Exec(`UPDATE characters SET gold_balance=300 WHERE character_id='buyer'`)
	seedHat(t, db, "house", "House Hat", 250)
	if rec := studioDo(t, db, "POST", "/api/v1/characters/buyer/hats/buy", map[string]string{"hat_id": "house"}); rec.Code != 204 {
		t.Fatalf("buy %d", rec.Code)
	}
	var burn int
	db.QueryRow(`SELECT burn_amount FROM hat_sales`).Scan(&burn)
	if burn != 250 {
		t.Errorf("burn = %d, want 250", burn)
	}
}

func TestHatStudio_Validation(t *testing.T) {
	db := newInventoryDB(t)
	defer db.Close()
	seedCharacterForInv(t, db, "maker")
	cases := map[string]map[string]any{
		"price too low": {"name": "Hat", "flow_cost": 5, "png_base64": studioPNG(t, 32, 100)},
		"wrong size":    {"name": "Hat", "flow_cost": 50, "png_base64": studioPNG(t, 16, 100)},
		"nearly empty":  {"name": "Hat", "flow_cost": 50, "png_base64": studioPNG(t, 32, 2)},
		"short name":    {"name": "H", "flow_cost": 50, "png_base64": studioPNG(t, 32, 100)},
		"not a png":     {"name": "Hat", "flow_cost": 50, "png_base64": base64.StdEncoding.EncodeToString([]byte("nope"))},
	}
	for name, body := range cases {
		if rec := studioDo(t, db, "POST", "/api/v1/characters/maker/hats/design", body); rec.Code != 400 {
			t.Errorf("%s: want 400, got %d", name, rec.Code)
		}
	}
}

func TestHatStudio_CreatorCanRepriceAndUnlist_OwnersKeepHat(t *testing.T) {
	db := newInventoryDB(t)
	defer db.Close()
	seedCharacterForInv(t, db, "maker")
	seedCharacterForInv(t, db, "buyer")
	db.Exec(`UPDATE characters SET gold_balance=500 WHERE character_id='buyer'`)
	hat := publish(t, db, "maker", 100)
	studioDo(t, db, "POST", "/api/v1/characters/buyer/hats/buy", map[string]string{"hat_id": hat})

	lp := "/api/v1/characters/maker/hats/" + hat + "/listing"
	if rec := studioDo(t, db, "PATCH", lp, map[string]any{"flow_cost": 40}); rec.Code != 204 {
		t.Fatalf("reprice %d %s", rec.Code, rec.Body.String())
	}
	if rec := studioDo(t, db, "PATCH", lp, map[string]any{"listed": false}); rec.Code != 204 {
		t.Fatalf("unlist %d", rec.Code)
	}
	// not buyable once unlisted
	seedCharacterForInv(t, db, "late")
	db.Exec(`UPDATE characters SET gold_balance=500 WHERE character_id='late'`)
	if rec := studioDo(t, db, "POST", "/api/v1/characters/late/hats/buy", map[string]string{"hat_id": hat}); rec.Code != 404 {
		t.Errorf("unlisted buy want 404, got %d", rec.Code)
	}
	// not in catalog, but the earlier buyer keeps it
	rec := studioDo(t, db, "GET", "/api/v1/hats", nil)
	var cat []map[string]any
	json.Unmarshal(rec.Body.Bytes(), &cat)
	if len(cat) != 0 {
		t.Errorf("catalog = %d, want 0", len(cat))
	}
	rec = studioDo(t, db, "GET", "/api/v1/characters/buyer/hats", nil)
	var owned []map[string]any
	json.Unmarshal(rec.Body.Bytes(), &owned)
	if len(owned) != 1 {
		t.Errorf("owned = %d, want 1", len(owned))
	}
	// somebody else cannot reprice
	if rec := studioDo(t, db, "PATCH", "/api/v1/characters/buyer/hats/"+hat+"/listing", map[string]any{"flow_cost": 10}); rec.Code != 403 {
		t.Errorf("non-creator reprice want 403, got %d", rec.Code)
	}
}

func TestHatStudio_ImageAndEconomyAndReports(t *testing.T) {
	db := newInventoryDB(t)
	defer db.Close()
	seedCharacterForInv(t, db, "maker")
	hat := publish(t, db, "maker", 100)
	rec := studioDo(t, db, "GET", "/api/v1/hats/"+hat+"/image", nil)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("image %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if _, err := png.Decode(bytes.NewReader(rec.Body.Bytes())); err != nil {
		t.Errorf("served image is not a PNG: %v", err)
	}
	rec = studioDo(t, db, "GET", "/api/v1/hats/economy", nil)
	var eco map[string]any
	json.Unmarshal(rec.Body.Bytes(), &eco)
	if eco["creator_share_pct"].(float64) != 88 || eco["burn_pct"].(float64) != 12 || eco["median"].(float64) != 100 {
		t.Errorf("economy = %v", eco)
	}
	for _, c := range []string{"r1", "r2", "r3"} {
		seedCharacterForInv(t, db, c)
		studioDo(t, db, "POST", "/api/v1/hats/"+hat+"/report", map[string]string{"character_id": c})
	}
	studioDo(t, db, "POST", "/api/v1/hats/"+hat+"/report", map[string]string{"character_id": "r3"}) // duplicate ignored
	var st string
	var n int
	db.QueryRow(`SELECT status, report_count FROM hats WHERE hat_id=?`, hat).Scan(&st, &n)
	if st != "review" || n != 3 {
		t.Errorf("status=%s reports=%d, want review/3", st, n)
	}
}

func TestHatStudio_DesignLimit(t *testing.T) {
	db := newInventoryDB(t)
	defer db.Close()
	seedCharacterForInv(t, db, "maker")
	for i := 0; i < 12; i++ {
		publish(t, db, "maker", 50)
	}
	rec := studioDo(t, db, "POST", "/api/v1/characters/maker/hats/design", map[string]any{
		"name": "One Too Many", "flow_cost": 50, "png_base64": studioPNG(t, 32, 100)})
	if rec.Code != 409 {
		t.Errorf("want 409, got %d", rec.Code)
	}
}

func TestHatStudio_LimitedEdition_SellsOutThenRefuses(t *testing.T) {
	db := newInventoryDB(t)
	defer db.Close()
	for _, id := range []string{"maker", "b1", "b2", "b3"} {
		seedCharacterForInv(t, db, id)
	}
	db.Exec(`UPDATE characters SET gold_balance=1000 WHERE character_id IN ('b1','b2','b3')`)
	rec := studioDo(t, db, "POST", "/api/v1/characters/maker/hats/design", map[string]any{
		"name": "Ltd Cone", "flow_cost": 100, "png_base64": studioPNG(t, 32, 100), "quantity_limit": 2,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("publish: %d %s", rec.Code, rec.Body.String())
	}
	var hat map[string]any
	json.Unmarshal(rec.Body.Bytes(), &hat)
	id := hat["hat_id"].(string)

	for _, b := range []string{"b1", "b2"} {
		if r := studioDo(t, db, "POST", "/api/v1/characters/"+b+"/hats/buy", map[string]string{"hat_id": id}); r.Code != http.StatusNoContent {
			t.Fatalf("%s buy: %d %s", b, r.Code, r.Body.String())
		}
	}
	// Sold out: refused, and the buyer is not charged.
	r := studioDo(t, db, "POST", "/api/v1/characters/b3/hats/buy", map[string]string{"hat_id": id})
	if r.Code != http.StatusGone {
		t.Fatalf("third buy = %d, want 410: %s", r.Code, r.Body.String())
	}
	if g := gold(db, "b3"); g != 1000 {
		t.Errorf("sold-out buyer was charged: balance %d", g)
	}
	// Catalog still lists it, flagged sold out with nothing remaining.
	cat := studioDo(t, db, "GET", "/api/v1/hats", nil)
	var list []map[string]any
	json.Unmarshal(cat.Body.Bytes(), &list)
	var found bool
	for _, h := range list {
		if h["hat_id"] == id {
			found = true
			if h["sold_out"] != true || h["remaining"].(float64) != 0 || h["quantity_limit"].(float64) != 2 {
				t.Errorf("catalog entry = %v", h)
			}
		}
	}
	if !found {
		t.Error("sold-out hat missing from catalog")
	}
}

func TestHatStudio_UnlimitedByDefault_AndLimitValidation(t *testing.T) {
	db := newInventoryDB(t)
	defer db.Close()
	seedCharacterForInv(t, db, "maker")
	id := publish(t, db, "maker", 100)
	var lim sql.NullInt64
	db.QueryRow(`SELECT quantity_limit FROM hats WHERE hat_id=?`, id).Scan(&lim)
	if lim.Valid {
		t.Errorf("default quantity_limit = %v, want NULL", lim.Int64)
	}
	for _, bad := range []int{0, -1, 1000001} {
		rec := studioDo(t, db, "POST", "/api/v1/characters/maker/hats/design", map[string]any{
			"name": "Bad", "flow_cost": 100, "png_base64": studioPNG(t, 32, 100), "quantity_limit": bad,
		})
		if rec.Code != http.StatusBadRequest {
			t.Errorf("limit %d = %d, want 400", bad, rec.Code)
		}
	}
}
