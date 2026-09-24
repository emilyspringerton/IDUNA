package device

import (
	"context"
	"testing"
	"time"

	"iduna/internal/store"
	"iduna/internal/util"
)

// Real, found-live bug (2026-09-24, BIG_O's own new IDUNA device-auth phone app -- see
// store_sqlite.go's own header comment for the full root-cause account): service_test.go's own
// fakeStore is a plain in-memory Go struct, never exercising SQLiteStore's real SQL against a
// real modernc.org/sqlite connection -- so a systemic, 100%-reproducing time.Time round-trip bug
// in every real device-auth poll went completely uncaught. This file closes that real gap: a real
// SQLiteStore, backed by a real, migrated (translated MySQL->SQLite, same real path main.go's own
// bootstrap uses) :memory: database, driving the full real Start->Poll->Exchange flow through
// actual SQL, not a mock.
func newTestSQLiteDeviceStore(t *testing.T) *SQLiteStore {
	t.Helper()
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.RunSQLiteMigrations(db, "../../../migrations/truestore"); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	return NewSQLiteDeviceStore(db)
}

func TestSQLiteStore_DeviceRequestRoundTrip(t *testing.T) {
	st := newTestSQLiteDeviceStore(t)
	ctx := context.Background()

	deviceCode := "real-test-device-code-12345"
	hash := util.SHA256Bytes([]byte(deviceCode))
	expires := time.Now().UTC().Add(15 * time.Minute)

	req := &Request{
		DeviceCodeHash:  hash,
		UserCodeNorm:    "TESTCODE",
		UserCodeDisplay: "TEST-CODE",
		Status:          "pending",
		ExpiresAt:       expires,
		PollIntervalMS:  2000,
		StreamID:        "test-stream-1",
	}
	if err := st.InsertDeviceRequest(ctx, req); err != nil {
		t.Fatalf("InsertDeviceRequest: %v", err)
	}
	if req.ID == 0 {
		t.Fatalf("InsertDeviceRequest did not assign a real ID")
	}

	// The real, central regression case: a freshly-inserted, definitely-valid, definitely-not-
	// expired request must be found and its real time.Time fields must actually parse back --
	// this is the exact real call chain that previously always failed with a Scan error, silently
	// surfaced to callers as ErrInvalidOrExpired.
	got, err := st.GetDeviceRequestByDeviceHash(ctx, hash)
	if err != nil {
		t.Fatalf("GetDeviceRequestByDeviceHash on a real, just-inserted, valid request: %v", err)
	}
	if got.Status != "pending" {
		t.Errorf("Status = %q, want pending", got.Status)
	}
	if got.ExpiresAt.IsZero() {
		t.Errorf("ExpiresAt did not parse back from storage -- got zero time")
	}
	if !got.ExpiresAt.After(time.Now().UTC()) {
		t.Errorf("ExpiresAt = %v, want a real time still in the future (matches what was inserted)", got.ExpiresAt)
	}
	if time.Now().UTC().After(got.ExpiresAt) {
		t.Errorf("a freshly-inserted, 15-minutes-out request incorrectly parsed as already expired")
	}
	if got.LastPollAt.Valid {
		t.Errorf("LastPollAt.Valid = true on a request that has never been polled")
	}

	// UpdatePollState -> a real last_poll_at round-trips correctly too.
	pollTime := time.Now().UTC()
	if err := st.UpdatePollState(ctx, req.ID, pollTime); err != nil {
		t.Fatalf("UpdatePollState: %v", err)
	}
	got2, err := st.GetDeviceRequestByDeviceHash(ctx, hash)
	if err != nil {
		t.Fatalf("GetDeviceRequestByDeviceHash after UpdatePollState: %v", err)
	}
	if !got2.LastPollAt.Valid {
		t.Fatalf("LastPollAt.Valid = false after a real UpdatePollState call")
	}
	if got2.LastPollAt.Time.IsZero() {
		t.Errorf("LastPollAt.Time did not parse back from storage -- got zero time")
	}

	// GetDeviceRequestByUserCode -- the real second lookup path (used by /device/confirm).
	got3, err := st.GetDeviceRequestByUserCode(ctx, "TESTCODE")
	if err != nil {
		t.Fatalf("GetDeviceRequestByUserCode: %v", err)
	}
	if got3.ExpiresAt.IsZero() {
		t.Errorf("GetDeviceRequestByUserCode's own ExpiresAt did not parse back from storage")
	}
}

func TestSQLiteStore_AuthorizeAndExchangeRoundTrip(t *testing.T) {
	st := newTestSQLiteDeviceStore(t)
	ctx := context.Background()

	deviceCode := "real-test-device-code-67890"
	hash := util.SHA256Bytes([]byte(deviceCode))
	req := &Request{
		DeviceCodeHash: hash, UserCodeNorm: "AUTHCODE", UserCodeDisplay: "AUTH-CODE",
		Status: "pending", ExpiresAt: time.Now().UTC().Add(15 * time.Minute),
		PollIntervalMS: 2000, StreamID: "test-stream-2",
	}
	if err := st.InsertDeviceRequest(ctx, req); err != nil {
		t.Fatalf("InsertDeviceRequest: %v", err)
	}

	var userID [16]byte
	userID[0] = 42
	ipHash := util.SHA256Bytes([]byte("1.2.3.4"))
	uaHash := util.SHA256Bytes([]byte("test-agent"))
	if err := st.AuthorizeRequest(ctx, req.ID, userID[:], ipHash, uaHash, time.Now().UTC()); err != nil {
		t.Fatalf("AuthorizeRequest: %v", err)
	}

	authorized, err := st.GetDeviceRequestByDeviceHash(ctx, hash)
	if err != nil {
		t.Fatalf("GetDeviceRequestByDeviceHash after AuthorizeRequest: %v", err)
	}
	if authorized.Status != "authorized" {
		t.Fatalf("Status = %q, want authorized", authorized.Status)
	}

	exchangePlain := "real-test-exchange-code"
	exchangeHash := util.SHA256Bytes([]byte(exchangePlain))
	exchangeExpires := time.Now().UTC().Add(60 * time.Second)
	ex := &Exchange{
		ExchangeHash: exchangeHash, ExchangePlain: exchangePlain, UserID: userID[:],
		DeviceRequest: req.ID, ExpiresAt: exchangeExpires,
	}
	if err := st.UpsertExchangeForRequest(ctx, authorized, ex); err != nil {
		t.Fatalf("UpsertExchangeForRequest: %v", err)
	}

	// The real, central regression case for the exchange side: ExpiresAt must parse back non-zero
	// and still be in the future, exactly the same class of bug as the device-request side above.
	gotEx, err := st.GetExchangeByPlainOrHash(ctx, exchangePlain, exchangeHash)
	if err != nil {
		t.Fatalf("GetExchangeByPlainOrHash: %v", err)
	}
	if gotEx.ExpiresAt.IsZero() {
		t.Errorf("Exchange.ExpiresAt did not parse back from storage -- got zero time")
	}
	if !gotEx.ExpiresAt.After(time.Now().UTC()) {
		t.Errorf("Exchange.ExpiresAt = %v, want a real time still in the future", gotEx.ExpiresAt)
	}
	if gotEx.ConsumedAt.Valid {
		t.Errorf("ConsumedAt.Valid = true on a freshly-created, unconsumed exchange")
	}

	if err := st.ConsumeExchange(ctx, gotEx.ID, req.ID, time.Now().UTC()); err != nil {
		t.Fatalf("ConsumeExchange: %v", err)
	}
	// A second consume of the same exchange must fail (single-use), matching the real, existing
	// Service-level contract this store method already promises.
	if err := st.ConsumeExchange(ctx, gotEx.ID, req.ID, time.Now().UTC()); err == nil {
		t.Errorf("a second ConsumeExchange on an already-consumed exchange should fail, got nil error")
	}
}

// TestSQLiteStore_FullDeviceServiceFlow drives the real Service (service.go, unmodified) against
// this real SQLiteStore end to end -- Start -> Confirm -> Poll -> Exchange -- the exact real
// sequence a live BIG_O/IDUNA.GAME client actually performs, through real SQL at every step, not
// service_test.go's own in-memory fakeStore.
func TestSQLiteStore_FullDeviceServiceFlow(t *testing.T) {
	st := newTestSQLiteDeviceStore(t)
	svc := NewService(st)
	ctx := context.Background()

	start, err := svc.Start(ctx, "http://localhost/device")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if start.DeviceCode == "" || start.UserCode == "" {
		t.Fatalf("Start returned an empty device_code/user_code")
	}

	// Real, central regression check: polling a freshly-started, unauthorized request must
	// report "pending", never an error -- this is the exact real call that always failed before
	// the fix (DEVICE_CODE_INVALID_OR_EXPIRED for a definitely-valid, fresh request).
	poll1, err := svc.Poll(ctx, start.DeviceCode)
	if err != nil {
		t.Fatalf("Poll on a fresh, unauthorized request returned an error (this is the real bug this test guards against): %v", err)
	}
	if poll1.Status != "pending" {
		t.Fatalf("Poll status = %q, want pending", poll1.Status)
	}

	var uid [16]byte
	uid[0] = 9
	if err := svc.Confirm(ctx, start.UserCode, uid, util.SHA256Bytes([]byte("1.2.3.4")), util.SHA256Bytes([]byte("test-ua"))); err != nil {
		t.Fatalf("Confirm: %v", err)
	}

	// Real, honest wait for the real poll_interval_ms this request was actually created with
	// (Start's own real, fixed 2000ms) -- poll1 above already advanced last_poll_at, and polling
	// again inside that same real window is correctly POLLING_TOO_FAST (a different, legitimate
	// guard, not the bug this test suite exists to catch).
	time.Sleep(2100 * time.Millisecond)

	poll2, err := svc.Poll(ctx, start.DeviceCode)
	if err != nil {
		t.Fatalf("Poll after Confirm: %v", err)
	}
	if poll2.Status != "authorized" || poll2.ExchangeCode == "" {
		t.Fatalf("Poll after Confirm = %+v, want authorized with a real exchange_code", poll2)
	}
}
