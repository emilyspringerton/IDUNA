package main

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"iduna/internal/auth/device"
	authjwt "iduna/internal/auth/jwt"
	"iduna/internal/blog"
	"iduna/internal/drive"
	"iduna/internal/http/handlers"
	"iduna/internal/http/middleware"
	"iduna/internal/mailinglist"
	"iduna/internal/statuspage"
	"iduna/internal/store"
	"iduna/internal/userlog"
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
	applesH := &handlers.ApplesHandler{Store: iamStore, ApplesGitDir: os.Getenv("APPLES_GIT_DIR")}
	pushTokensH := &handlers.PushTokensHandler{Store: iamStore}
	intelligenceH := &handlers.IntelligenceHandler{Store: iamStore}
	heimdalH := &handlers.HeimdalHandler{Store: iamStore}

	// Subscriptions (Emily+ gate) — S23-04.
	subscriptionH := &handlers.SubscriptionHandler{Store: iamStore}

	// Check-in monitors — alerting backend.
	monitorsH := &handlers.MonitorsHandler{Store: iamStore}

	// Drive API — configured via GOOGLE_DRIVE_SERVICE_ACCOUNT_JSON + GOOGLE_DRIVE_FOLDER_ID.
	// Starts in degraded mode (503) if env var not set; no startup failure.
	driveH := &handlers.DriveHandler{}
	if saJSON := os.Getenv("GOOGLE_DRIVE_SERVICE_ACCOUNT_JSON"); saJSON != "" {
		folderID := os.Getenv("GOOGLE_DRIVE_FOLDER_ID")
		dc, err := drive.New(saJSON, folderID)
		if err != nil {
			log.Printf("drive: failed to initialize client: %v (drive API disabled)", err)
		} else {
			driveH.Client = dc
			log.Printf("drive: initialized (folder=%q)", folderID)
		}
	} else {
		log.Printf("drive: GOOGLE_DRIVE_SERVICE_ACCOUNT_JSON not set — drive API in degraded mode")
	}

	// Mailing-list vault — okemily.com signups, never-at-rest-unencrypted per
	// explicit founder direction. Starts LOCKED on every process start; an
	// operator must run cmd/mailing-list-unlock (interactive passphrase,
	// never a flag/env var) before new signups are accepted. See
	// internal/mailinglist package doc for the full threat-model writeup.
	mailingListDBPath := os.Getenv("MAILING_LIST_DB_PATH")
	if mailingListDBPath == "" {
		mailingListDBPath = "./var/mailinglist.db"
	}
	mailingListStore, err := mailinglist.Open(mailingListDBPath)
	if err != nil {
		log.Fatalf("mailinglist: failed to open store: %v", err)
	}
	mailingListVault := mailinglist.NewVault()
	var mailchimpClient *mailinglist.MailchimpClient
	if mcKey := os.Getenv("MAILCHIMP_API_KEY"); mcKey != "" {
		mailchimpClient = mailinglist.NewMailchimpClient(mcKey, os.Getenv("MAILCHIMP_LIST_ID"))
		log.Printf("mailinglist: mailchimp sync configured")
	} else {
		log.Printf("mailinglist: MAILCHIMP_API_KEY not set — subscribers recorded in IDUNA only, no mailchimp sync")
	}
	mailingListH := &handlers.MailingListHandler{
		Store:     mailingListStore,
		Vault:     mailingListVault,
		Mailchimp: mailchimpClient,
		AllowOrigin: []string{
			"https://okemily.com",
			"https://www.okemily.com",
		},
		// Dedicated per-product Mailchimp audiences, kept off the general
		// okemily.com list. Empty until a founder creates the audience in
		// Mailchimp's dashboard and sets the env var — signups still record
		// fine in IDUNA's own store either way (see mailinglist.go).
		MailchimpLists: map[string]string{
			"stinkies": os.Getenv("MAILCHIMP_STINKIES_LIST_ID"),
		},
	}
	log.Printf("mailinglist: vault locked — run cmd/mailing-list-unlock to accept signups")

	// Blog — static HTML, no PHP/MySQL (this box had ~400MB free RAM and a
	// nearly-full swap when this was built; a second WordPress+MySQL stack
	// risked the exact OOM-kill incident SECTION 152 fixed). Own SQLite file,
	// rendered directly to /var/www/okemily/blog on every publish.
	blogDBPath := os.Getenv("BLOG_DB_PATH")
	if blogDBPath == "" {
		blogDBPath = "./var/blog.db"
	}
	blogStore, err := blog.Open(blogDBPath)
	if err != nil {
		log.Fatalf("blog: failed to open store: %v", err)
	}
	blogOutputDir := os.Getenv("BLOG_OUTPUT_DIR")
	if blogOutputDir == "" {
		blogOutputDir = "/var/www/okemily/blog"
	}
	blogH := &handlers.BlogHandler{Store: blogStore, Renderer: &blog.Renderer{OutputDir: blogOutputDir}}

	// Status page — real health checks against the services that actually
	// have a reachable public endpoint (see statuspage.DefaultTargets doc
	// for why emily-agent/SHANKPIT are deliberately excluded, not shown as
	// permanently "down"). Own SQLite file; background checker polls every
	// 60s starting immediately at startup.
	statusDBPath := os.Getenv("STATUS_DB_PATH")
	if statusDBPath == "" {
		statusDBPath = "./var/statuspage.db"
	}
	statusStore, err := statuspage.Open(statusDBPath)
	if err != nil {
		log.Fatalf("statuspage: failed to open store: %v", err)
	}
	statusTargets := statuspage.DefaultTargets()
	statusChecker := statuspage.NewChecker(statusStore, statusTargets)
	go statusChecker.Run(context.Background(), 60*time.Second, func(err error) {
		log.Printf("[statuspage] %v", err)
	})
	statusH := &handlers.StatusPageHandler{Store: statusStore, Targets: statusTargets}

	mux := http.NewServeMux()

	// Existing device routes.
	deviceH.Register(mux)

	// New IAM routes.
	mux.Handle("/api/v1/auth/google", googleAuthH)
	mux.Handle("/api/v1/auth/agent", agentAuthH) // M2M credential exchange (HQ-SPEC-IAM-095 §3.1)
	mux.Handle("/api/v1/auth/refresh", &handlers.RefreshHandler{Keys: keys, Issuer: issuer})
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

	// Subscriptions API — auth required; provision requires subscriptions.admin (inside handler).
	subsProtected := middleware.RequireAuth(keys)(subscriptionH)
	mux.Handle("/api/v1/subscriptions", subsProtected)
	mux.Handle("/api/v1/subscriptions/", subsProtected)

	// Monitors API — check-in is public; CRUD requires auth (permission checks inside handler).
	// Public check-in path does not go through RequireAuth middleware.
	mux.Handle("/api/v1/monitors/checkin/", monitorsH)
	monitorsProtected := middleware.RequireAuth(keys)(monitorsH)
	mux.Handle("/api/v1/monitors", monitorsProtected)
	mux.Handle("/api/v1/monitors/", monitorsProtected)

	// Drive API — auth required; permission checks (drive.write / drive.read) inside handler.
	driveProtected := middleware.RequireAuth(keys)(driveH)
	mux.Handle("/api/v1/drive/upload", driveProtected)
	mux.Handle("/api/v1/drive/files", driveProtected)
	mux.Handle("/api/v1/drive/files/", driveProtected)

	// Mailing-list — subscribe is public but rate-limited (5/min/IP, generous
	// for a real signup form, tight against scripted abuse, enforced inside
	// the handler itself — see MailingListHandler.Limiter); unlock/init are
	// loopback-gated inside the handler, not by auth middleware, since
	// there's no human JWT session for an operator running a CLI on the box.
	mailingListH.Limiter = middleware.NewIPRateLimiter(5)
	mailingListH.Register(mux)

	// Blog — posting (programmatic or manual, same endpoint) requires
	// blog.write; reading is public.
	blogCreateProtected := middleware.RequireAuth(keys)(middleware.RequirePermission("blog.write")(http.HandlerFunc(blogH.Create)))
	blogH.RegisterRoutes(mux, blogCreateProtected)

	mux.Handle("/api/v1/status", statusH)

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

	// ── User event log + projector ─────────────────────────────────────────────
	idunaRootForLog := getenv("IDUNA_ROOT", ".")
	userEventLogDir := filepath.Join(idunaRootForLog, "var", "user-events")
	uel, err := userlog.NewFileEventLog(userEventLogDir)
	if err != nil {
		log.Fatalf("user event log: %v", err)
	}
	defer uel.Close()

	var userProj userlog.UserProjector
	if dsn != "" {
		userProj = userlog.NewMySQLProjector(db)
	} else {
		userProj = userlog.NewSQLiteProjector(db)
	}

	// Replay unapplied events on startup, then seed webmaster from var/webmaster.json.
	webmasterCredPath := filepath.Join(idunaRootForLog, "var", "webmaster.json")
	if err := userlog.SeedWebmaster(context.Background(), webmasterCredPath, uel, userProj); err != nil {
		log.Printf("webmaster seed: %v (continuing — webmaster may already exist or file is absent)", err)
	} else {
		log.Println("webmaster: uid=0 ready")
	}

	localAuthH := &handlers.LocalAuthHandler{Keys: keys, Proj: userProj, Issuer: issuer}
	registerH := &handlers.RegisterHandler{Keys: keys, Log: uel, Proj: userProj, Store: iamStore, Issuer: issuer}
	usersH := &handlers.UsersHandler{Log: uel, Proj: userProj}

	// OpenAPI spec — public.
	mux.Handle("/api/v1/openapi.json", &handlers.OpenAPIHandler{})

	// Per-IP rate limiter: 10 req/min on auth endpoints (S126-09).
	authLimiter := middleware.NewIPRateLimiter(10)
	authRateLimit := middleware.AuthRateLimit(authLimiter)

	// Local (password) auth + open registration — public, rate-limited.
	mux.Handle("/api/v1/auth/local", authRateLimit(localAuthH))
	mux.Handle("/api/v1/auth/register", authRateLimit(registerH))

	// User CRUD — requires JWT.
	usersProtected := middleware.RequireAuth(keys)(usersH)
	mux.Handle("/api/v1/users", usersProtected)
	mux.Handle("/api/v1/users/", usersProtected)

	// Agents API — requires JWT; listing emily_cluster agents for distributed Emily.
	agentsH := &handlers.AgentsHandler{Store: iamStore}
	agentsProtected := middleware.RequireAuth(keys)(agentsH)
	mux.Handle("/api/v1/agents", agentsProtected)
	mux.Handle("/api/v1/agents/", agentsProtected)

	// User-event SSE stream — Colab notebooks subscribe here for real-time user events.
	streamH := middleware.RequireAuth(keys)(&handlers.UserEventStreamHandler{Log: uel})
	mux.Handle("/api/v1/stream/user-events", streamH)

	// SHANKPIT player registry — register + profile.
	// S126-10: /profile sub-path is public; all other player routes require auth.
	profileH := &handlers.PlayerProfileHandler{DB: db, Store: iamStore}
	rawPlayersH := &handlers.PlayersHandler{DB: db}
	playerDispatch := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/profile") {
			profileH.ServeHTTP(w, r)
			return
		}
		middleware.RequireAuth(keys)(rawPlayersH).ServeHTTP(w, r)
	})
	mux.Handle("/api/v1/players/register", middleware.RequireAuth(keys)(rawPlayersH))
	mux.Handle("/api/v1/players/", playerDispatch)

	// SHANKPIT email+password auth — public (creates/validates player credentials).
	playerEmailAuthH := &handlers.PlayerEmailAuthHandler{DB: db, Keys: keys, Issuer: issuer}
	mux.Handle("/api/v1/auth/email/register", playerEmailAuthH)
	mux.Handle("/api/v1/auth/email/login", playerEmailAuthH)

	// SHANKPIT Google OAuth browser flow — public (no prior auth needed).
	shankpitAuthH := &handlers.ShankpitAuthHandler{
		GoogleClientID:     googleClientID,
		GoogleClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
		RedirectURI:        os.Getenv("SHANKPIT_OAUTH_REDIRECT_URI"),
		Keys:               keys,
		Store:              iamStore,
		DB:                 db,
		Issuer:             issuer,
		BaseURL:            baseURL,
	}
	mux.Handle("/api/v1/auth/google/shankpit", shankpitAuthH)
	mux.Handle("/api/v1/auth/google/shankpit/callback", shankpitAuthH)

	// SHANKPIT connect-ticket minting — requires an existing IDUNA JWT
	// (from the OAuth/email flows above); the game server verifies the
	// resulting ticket itself via HMAC (S156-02).
	shankpitTicketH := middleware.RequireAuth(keys)(&handlers.ShankpitTicketHandler{
		Secret: []byte(os.Getenv("SHANKPIT_TICKET_SECRET")),
	})
	mux.Handle("/api/v1/shankpit/ticket", shankpitTicketH)

	// SHANKPIT-460 v0 matchmaking queue (S156-03) — in-memory, ephemeral by
	// design (see handlers.ShankpitQueue doc comment). ServerAddr is the one
	// persistent game server instance (no per-match instances in v0); no
	// public DNS name exists yet for it (play.farthq.com is reserved but
	// deliberately not created until SHANKPIT ships externally, per
	// HQ-SPEC-INFRA-105), so this defaults to loopback for same-box testing.
	shankpitServerAddr := os.Getenv("SHANKPIT_SERVER_ADDR")
	if shankpitServerAddr == "" {
		shankpitServerAddr = "127.0.0.1:6969"
	}
	shankpitQueue := handlers.NewShankpitQueue(shankpitServerAddr)
	mux.Handle("/api/v1/shankpit/queue/join", middleware.RequireAuth(keys)(http.HandlerFunc(shankpitQueue.Join)))
	mux.Handle("/api/v1/shankpit/queue/leave", middleware.RequireAuth(keys)(http.HandlerFunc(shankpitQueue.Leave)))
	mux.Handle("/api/v1/shankpit/queue/status", middleware.RequireAuth(keys)(http.HandlerFunc(shankpitQueue.Status)))

	// DragonsNShit MMO API (S75-02/03/04/05) — auth required.
	mmoH := middleware.RequireAuth(keys)(&handlers.MMOHandler{DB: db})
	mux.Handle("/api/v1/characters", mmoH)
	mux.Handle("/api/v1/characters/", mmoH)
	mux.Handle("/api/v1/items", mmoH)
	mux.Handle("/api/v1/items/", mmoH)
	mux.Handle("/api/v1/guilds", mmoH)
	mux.Handle("/api/v1/guilds/", mmoH)
	mux.Handle("/api/v1/world-events", mmoH)
	mux.Handle("/api/v1/world-events/", mmoH)
	// Field office district overlay (S127-05) — same M2M auth as MMO routes.
	mux.Handle("/api/v1/fieldoffices", mmoH)
	mux.Handle("/api/v1/fieldoffices/", mmoH)

	// Supply chain API (S136-02/03) — auth required.
	supplyH := middleware.RequireAuth(keys)(&handlers.SupplyHandler{DB: db})
	mux.Handle("/api/v1/supply/", supplyH)

	// Research cache API (S137-03) — auth required.
	researchH := middleware.RequireAuth(keys)(&handlers.ResearchHandler{DB: db})
	mux.Handle("/api/v1/research/", researchH)

	// EINHORN INDEX knowledge graph proxy (S138-06) — auth required; proxies to KGRAPH_URL.
	kgraphH := middleware.RequireAuth(keys)(&handlers.KGraphHandler{})
	mux.Handle("/api/v1/kgraph/", kgraphH)

	log.Println("iduna listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", mux))
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
