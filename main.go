package main

import (
	"database/sql"
	"log"
	"net/http"
	"os"
	"path/filepath"
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
	var db *sql.DB
	var iamStore store.IAMStore
	var deviceStore device.Store

	dsn := os.Getenv("MYSQL_DSN")
	if dsn != "" {
		// MySQL mode: external database required.
		var err error
		db, err = sql.Open("mysql", dsn)
		if err != nil {
			log.Fatal(err)
		}
		defer db.Close()
		if err := db.Ping(); err != nil {
			log.Fatal(err)
		}
		iamStore = store.NewMySQLStore(db)
		deviceStore = device.NewMySQLStore(db)
		log.Println("store: MySQL")
	} else {
		// Embedded mode: SQLite, zero external dependencies.
		// DB file lives next to the binary in var/iduna.db.
		dbPath := getenv("SQLITE_PATH", filepath.Join("var", "iduna.db"))
		var err error
		db, err = store.OpenSQLite(dbPath)
		if err != nil {
			log.Fatalf("open sqlite: %v", err)
		}
		defer db.Close()

		idunaRoot := getenv("IDUNA_ROOT", ".")
		migrationsDir := filepath.Join(idunaRoot, "migrations", "truestore")
		if err := store.RunSQLiteMigrations(db, migrationsDir); err != nil {
			log.Fatalf("sqlite migrations: %v", err)
		}

		sq := store.NewSQLiteStore(db)
		iamStore = sq
		deviceStore = device.NewSQLiteDeviceStore(db)
		log.Printf("store: SQLite (embedded) at %s", dbPath)
	}

	// Device flow.
	svc := device.NewService(deviceStore)
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
	agentAuthH := &handlers.AgentAuthHandler{
		Keys:   keys,
		Store:  iamStore,
		Issuer: issuer,
	}
	meH := &handlers.MeHandler{
		Store:     iamStore,
		Authority: baseURL,
	}
	jwksH := &handlers.JWKSHandler{Keys: keys}
	healthH := &handlers.HealthHandler{}
	adminH := &handlers.AdminHandler{Store: iamStore}
	adminH.Init()
	adminLoginH := &handlers.AdminLoginHandler{Store: iamStore, Keys: keys, Issuer: issuer}
	applesH := &handlers.ApplesHandler{Store: iamStore}
	pushTokensH := &handlers.PushTokensHandler{Store: iamStore}
	intelligenceH := &handlers.IntelligenceHandler{Store: iamStore}
	heimdalH := &handlers.HeimdalHandler{Store: iamStore}

	mux := http.NewServeMux()

	// Existing device routes.
	deviceH.Register(mux)

	// New IAM routes.
	mux.Handle("/api/v1/auth/google", googleAuthH)
	mux.Handle("/api/v1/auth/agent", agentAuthH) // M2M credential exchange (HQ-SPEC-IAM-095 §3.1)
	mux.Handle("/api/v1/identities/me",
		middleware.RequireAuth(keys)(
			middleware.RequirePermission("iduna.me.read")(meH),
		),
	)
	mux.Handle("/.well-known/jwks.json", jwksH)
	mux.Handle("/api/v1/jwks", jwksH) // also serve JWKS on the path idunaauth expects
	mux.Handle("/health", healthH)

	// Apples API — auth required; permission checks handled inside the handler.
	applesProtected := middleware.RequireAuth(keys)(applesH)
	mux.Handle("/api/v1/apples", applesProtected)
	mux.Handle("/api/v1/apples/", applesProtected)

	// Push tokens API (MJOLNIR FCM) — auth required; permission checks inside handler.
	pushTokensProtected := middleware.RequireAuth(keys)(pushTokensH)
	mux.Handle("/api/v1/push-tokens", pushTokensProtected)
	mux.Handle("/api/v1/push-tokens/", pushTokensProtected)

	// Intelligence API (MJOLNIR camera → Emily Prime vision) — auth required; permission checks inside.
	intelligenceProtected := middleware.RequireAuth(keys)(intelligenceH)
	mux.Handle("/api/v1/intelligence/observe", intelligenceProtected)
	mux.Handle("/api/v1/intelligence/observations", intelligenceProtected)
	mux.Handle("/api/v1/intelligence/observations/", intelligenceProtected)

	// HEIMDAL sprint planning API — auth required; permission checks inside.
	heimdalProtected := middleware.RequireAuth(keys)(heimdalH)
	mux.Handle("/api/v1/heimdal/sprints", heimdalProtected)
	mux.Handle("/api/v1/heimdal/sprints/", heimdalProtected)

	// Admin login/logout — public (no auth required).
	mux.Handle("/admin/login", adminLoginH)
	mux.Handle("/admin/logout", adminLoginH)

	// Admin UI — requires iduna.admin permission; cookie auth for browser navigation.
	adminProtected := middleware.RequireCookieAuth(keys, "/admin/login")(middleware.RequirePermission("iduna.admin")(adminH))
	mux.Handle("/admin", adminProtected)
	mux.Handle("/admin/", adminProtected)

	// Static files (registration SPA + event stream).
	idunaRoot := getenv("IDUNA_ROOT", ".")
	serveStatic := func(name string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			http.ServeFile(w, r, filepath.Join(idunaRoot, name))
		}
	}
	mux.HandleFunc("GET /{$}", serveStatic("index.html"))
	mux.HandleFunc("GET /app.js", serveStatic("app.js"))
	mux.HandleFunc("GET /styles.css", serveStatic("styles.css"))
	mux.HandleFunc("GET /event-stream/{$}", serveStatic("event-stream/index.html"))

	log.Println("iduna listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", mux))
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
