package main

import (
	"database/sql"
	"log"
	"net/http"
	"os"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"iduna/internal/auth/device"
	authjwt "iduna/internal/auth/jwt"
	"iduna/internal/http/handlers"
	"iduna/internal/http/middleware"
	"iduna/internal/store"
	"iduna/internal/util"
)

func main() {
	dsn := os.Getenv("MYSQL_DSN")
	if dsn == "" {
		log.Fatal("MYSQL_DSN is required")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		log.Fatal(err)
	}

	// Device flow (existing).
	svc := device.NewService(device.NewMySQLStore(db))
	deviceH := &handlers.DeviceHandler{
		Svc:            svc,
		StartLimiter:   util.NewWindowRateLimiter(10, time.Minute),
		ConfirmLimiter: util.NewWindowRateLimiter(20, time.Minute),
		JWTSecret:      []byte(os.Getenv("JWT_SECRET")),
		BaseURL:        getenv("BASE_URL", "http://localhost:8080"),
	}

	// ES256 key management.
	keyFile := getenv("KEY_FILE", "./iduna-key.json")
	keys, err := authjwt.LoadOrGenerateKeys(keyFile)
	if err != nil {
		log.Fatalf("loading ES256 keys: %v", err)
	}

	// IAM store.
	iamStore := store.NewMySQLStore(db)

	issuer := getenv("JWT_ISSUER", "https://iam.farthq.internal")
	baseURL := getenv("BASE_URL", "http://localhost:8080")
	googleClientID := os.Getenv("GOOGLE_CLIENT_ID")

	// Handlers.
	googleAuthH := &handlers.GoogleAuthHandler{
		GoogleClientID: googleClientID,
		Keys:           keys,
		Store:          iamStore,
		Issuer:         issuer,
	}
	meH := &handlers.MeHandler{
		Store:     iamStore,
		Authority: baseURL,
	}
	jwksH := &handlers.JWKSHandler{Keys: keys}
	healthH := &handlers.HealthHandler{}
	adminH := &handlers.AdminHandler{Store: iamStore}
	adminH.Init()

	mux := http.NewServeMux()

	// Existing device routes.
	deviceH.Register(mux)

	// New IAM routes.
	mux.Handle("/api/v1/auth/google", googleAuthH)
	mux.Handle("/api/v1/identities/me",
		middleware.RequireAuth(keys)(
			middleware.RequirePermission("iduna.me.read")(meH),
		),
	)
	mux.Handle("/.well-known/jwks.json", jwksH)
	mux.Handle("/health", healthH)

	// Admin UI — requires iduna.admin permission.
	adminProtected := middleware.RequireAuth(keys)(middleware.RequirePermission("iduna.admin")(adminH))
	mux.Handle("/admin", adminProtected)
	mux.Handle("/admin/", adminProtected)

	log.Println("iduna listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", mux))
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
