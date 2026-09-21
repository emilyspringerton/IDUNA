package main

import (
	"context"
	"database/sql"
	"encoding/hex"
	"fmt"
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
	"iduna/internal/brawlpit"
	"iduna/internal/deckstats"
	"iduna/internal/drive"
	"iduna/internal/http/handlers"
	"iduna/internal/http/middleware"
	"iduna/internal/mailinglist"
	"iduna/internal/nock"
	"iduna/internal/promptoverse"
	"iduna/internal/shankpit"
	"iduna/internal/statuspage"
	"iduna/internal/store"
	"iduna/internal/tenantprovision"
	"iduna/internal/tyler"
	"iduna/internal/userlog"
	"iduna/internal/util"
	"iduna/internal/vault"
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
	webCeremonyH := &handlers.WebCeremonyHandler{
		GoogleClientID:     googleClientID,
		GoogleClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
		RedirectURI:        getenv("CEREMONY_OAUTH_REDIRECT_URI", baseURL+"/"),
		Keys:               keys,
		Store:              iamStore,
		Issuer:             issuer,
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
	driveSlurpH := &handlers.DriveSlurpHandler{
		GoogleClientID:     googleClientID,
		GoogleClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
		RedirectURI:        getenv("DRIVE_SLURP_OAUTH_REDIRECT_URI", baseURL+"/admin/drive-slurp/oauth/callback"),
		IdunaRoot:          getenv("IDUNA_ROOT", "."),
	}
	openexecutiveH := handlers.NewOpenExecutiveHandler(iamStore, baseURL)
	adminH := &handlers.AdminHandler{Store: iamStore, DB: db, DriveSlurp: driveSlurpH, OpenExecutive: openexecutiveH}
	adminH.Init()
	adminLoginH := &handlers.AdminLoginHandler{Store: iamStore, Keys: keys, Issuer: issuer, CookieDomain: os.Getenv("IDUNA_ADMIN_COOKIE_DOMAIN")}
	applesH := &handlers.ApplesHandler{Store: iamStore, ApplesGitDir: os.Getenv("APPLES_GIT_DIR")}
	pushTokensH := &handlers.PushTokensHandler{Store: iamStore}
	intelligenceH := &handlers.IntelligenceHandler{Store: iamStore}
	heimdalH := &handlers.HeimdalHandler{Store: iamStore}

	// Subscriptions (Emily+ gate) — S23-04. StripeWebhookSecret wired explicitly here (was
	// previously left unset, silently falling back to stripeWebhook's own os.Getenv lookup at
	// call time -- real, harmless either way now that an EMPTY secret fails closed, but real
	// and explicit is better than implicit for a real payment-verification config value).
	subscriptionH := &handlers.SubscriptionHandler{Store: iamStore, StripeWebhookSecret: os.Getenv("GFD_STRIPE_WEBHOOK_SECRET")}

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
	adminH.Mailinglist = mailingListStore
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
			"stinkies":   os.Getenv("MAILCHIMP_STINKIES_LIST_ID"),
			"freehoodie": os.Getenv("MAILCHIMP_FREEHOODIE_LIST_ID"),
		},
	}
	// S245-01: opt-in config/file-key unlock mode, for a product tenant that
	// needs signups to keep working after an unattended redeploy/crash with
	// no operator standing by. Unset (the default, including EINHORN's own
	// instance) leaves the existing human-passphrase path exactly as before
	// — this never runs unless a deployer explicitly opts in.
	if keyFilePath := os.Getenv("MAILING_LIST_KEY_FILE"); keyFilePath != "" {
		if err := mailingListAutoUnlock(mailingListStore, mailingListVault, keyFilePath); err != nil {
			log.Fatalf("mailinglist: file-key auto-unlock failed: %v", err)
		}
		log.Printf("mailinglist: vault auto-unlocked from key file %s", keyFilePath)
	} else {
		log.Printf("mailinglist: vault locked — run cmd/mailing-list-unlock to accept signups")
	}

	// IDUNA Vault — founder-only password manager (S170-03b, VS0 per
	// docs/NORTHSTAR_PASSWORD_MANAGER.md). Same never-at-rest-unencrypted
	// posture as the mailing list, own SQLite file, own lock -- reuses
	// mailinglist.Vault's crypto primitive directly rather than duplicating
	// it (see internal/vault package doc for the reuse rationale). Starts
	// LOCKED; an operator runs `emily vault unlock` after every restart.
	vaultDBPath := os.Getenv("VAULT_DB_PATH")
	if vaultDBPath == "" {
		vaultDBPath = "./var/vault.db"
	}
	vaultStore, err := vault.Open(vaultDBPath)
	if err != nil {
		log.Fatalf("vault: failed to open store: %v", err)
	}
	vaultH := &handlers.VaultHandler{
		Store: vaultStore,
		Vault: mailinglist.NewVault(),
	}
	log.Printf("vault: locked — run `emily vault unlock` to access items")

	// DIS (Digital Immune System) proxy — first consumer of the collector
	// outside WordPress/EDIS. nginx on this box shares one access log across
	// every vhost, so the already-running edis-dis collector already sees
	// okemily.com's traffic; this just exposes it to okemily's static pages.
	disCollectorURL := os.Getenv("DIS_COLLECTOR_URL")
	if disCollectorURL == "" {
		disCollectorURL = "http://127.0.0.1:9099"
	}
	disH := &handlers.DISHandler{
		CollectorURL: disCollectorURL,
		AllowOrigin: []string{
			"https://okemily.com",
			"https://www.okemily.com",
		},
	}

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

	// TYLER reading room -- same own-SQLite-file/render-to-static shape as
	// the blog, but its own store/templates: TYLER episode scripts have
	// real headers/tables/checklists the blog's paragraph-only renderer
	// can't handle, and read better on a book-styled page than a dev-blog
	// theme. See internal/tyler's own package doc.
	tylerDBPath := os.Getenv("TYLER_DB_PATH")
	if tylerDBPath == "" {
		tylerDBPath = "./var/tyler.db"
	}
	tylerStore, err := tyler.Open(tylerDBPath)
	if err != nil {
		log.Fatalf("tyler: failed to open store: %v", err)
	}
	tylerOutputDir := os.Getenv("TYLER_OUTPUT_DIR")
	if tylerOutputDir == "" {
		tylerOutputDir = "/var/www/okemily/tyler"
	}
	tylerH := &handlers.TylerHandler{Store: tylerStore, Renderer: &tyler.Renderer{OutputDir: tylerOutputDir}}

	// Prompt-o-verse gallery -- same own-SQLite-file/render-to-static shape
	// as blog/tyler, but also writes a generated image file alongside each
	// page (the taxonomy data model is 3 pieces per node: top-level prompt,
	// generated image, labeled tags -- see internal/promptoverse's own
	// package doc and EMILY/docs/NORTHSTAR_PROMPT_O_VERSE.md).
	promptoverseDBPath := os.Getenv("PROMPTOVERSE_DB_PATH")
	if promptoverseDBPath == "" {
		promptoverseDBPath = "./var/promptoverse.db"
	}
	promptoverseStore, err := promptoverse.Open(promptoverseDBPath)
	if err != nil {
		log.Fatalf("promptoverse: failed to open store: %v", err)
	}
	promptoverseOutputDir := os.Getenv("PROMPTOVERSE_OUTPUT_DIR")
	if promptoverseOutputDir == "" {
		promptoverseOutputDir = "/var/www/okemily/prompt-o-verse"
	}
	promptoverseH := &handlers.PromptOVerseHandler{Store: promptoverseStore, Renderer: &promptoverse.Renderer{OutputDir: promptoverseOutputDir, GoogleClientID: googleClientID, Store: promptoverseStore}}

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
	statusHistoryH := &handlers.StatusHistoryHandler{Store: statusStore, Targets: statusTargets}

	mux := http.NewServeMux()

	// Existing device routes.
	deviceH.Register(mux)

	// VS0 web ceremony (app.js's actual contract — see docs/kikoryu/VS0_IDENTITY_GATE.md
	// "known divergence #2": these paths were called by the frontend since it was
	// written but never registered anywhere).
	webCeremonyH.Register(mux)
	mux.Handle("/me", middleware.RequireAuth(keys)(http.HandlerFunc(webCeremonyH.HandleMe)))
	mux.Handle("/honor-code/accept", middleware.RequireAuth(keys)(http.HandlerFunc(webCeremonyH.HandleHonorAccept)))
	mux.Handle("/me/handle", middleware.RequireAuth(keys)(http.HandlerFunc(webCeremonyH.HandleMeHandle)))

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

	// OpenExecutive API — M2M credential provisioning (admin only), service health checks
	mux.Handle("/api/v1/openexecutive/health", http.HandlerFunc(openexecutiveH.HealthCheck))
	openexecProvisionProtected := middleware.RequireAuth(keys)(http.HandlerFunc(openexecutiveH.Provision))
	mux.Handle("/api/v1/openexecutive/provision", openexecProvisionProtected)

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

	// Subscriptions API — auth required for /me and provision (subscriptions.admin checked
	// inside the handler); /stripe and /tiers are real, deliberately PUBLIC exceptions,
	// registered separately here (Go's own ServeMux picks the more specific pattern) --
	// a genuine, found-live bug fixed the same pass as the signature-forging one below: both
	// were previously caught by the blanket RequireAuth wrapper despite each having its own
	// real reason to be public. /stripe in particular is Stripe's OWN webhook callback -- it
	// carries no IDUNA JWT and never will, authenticating itself via a real Stripe-Signature
	// HMAC instead (see verifyStripeSignature) -- so wrapping it in RequireAuth meant the real
	// Stripe service could never reach this endpoint at all, and no real subscription could
	// ever activate via webhook in production. /tiers is a public pricing listing per this
	// handler's own doc comment, same real class of bug (comment said public, code required
	// auth).
	mux.Handle("/api/v1/subscriptions/stripe", subscriptionH)
	mux.Handle("/api/v1/subscriptions/tiers", subscriptionH)
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
	// S245-02: real export endpoint, gated behind a dedicated permission
	// (not iduna.admin — least-privilege, same reasoning as kanban.access)
	// since this is the one mailing-list route that returns real PII.
	mux.Handle("/api/v1/mailing-list/export",
		middleware.RequireAuth(keys)(middleware.RequirePermission("mailinglist.export")(http.HandlerFunc(mailingListH.Export))))
	// S245-03: per-instance, admin-settable Mailchimp config -- its own
	// dedicated permission, distinct from mailinglist.export (this one
	// writes/reads config, not subscriber PII).
	mailingListSettingsProtected := middleware.RequireAuth(keys)(middleware.RequirePermission("mailinglist.admin")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			mailingListH.GetMailchimpSettings(w, r)
		case http.MethodPut:
			mailingListH.PutMailchimpSettings(w, r)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})))
	mux.Handle("/api/v1/mailing-list/settings/mailchimp", mailingListSettingsProtected)
	disH.Register(mux)

	// CarePyre contact form — MOVED to IDUNA_PRO, founder real-time 2026-09-08: "move the
	// contact form to idunapro" (a PII-handling audit found this data belongs in the same
	// service/DB as CarePyre's own tiered RBAC and GDPR pipeline, not gated by IDUNA's much
	// broader, catch-all iduna.admin population). See IDUNA_PRO/internal/http/handlers/
	// carepyre_contact.go. The old table (migrations/truestore/202608100001_
	// carepyre_contact_submissions.sql) is left in place, frozen, as a read-only historical
	// backup — its rows were copied forward via IDUNA_PRO/scripts/
	// migrate-carepyre-contacts-from-iduna.sh, not deleted.

	// Vault — every endpoint loopback-gated inside the handler itself, same
	// convention as mailing-list unlock/init (see VaultHandler doc comment).
	vaultH.Register(mux)

	// Blog — posting (programmatic or manual, same endpoint) requires
	// blog.write; reading is public.
	blogCreateProtected := middleware.RequireAuth(keys)(middleware.RequirePermission("blog.write")(http.HandlerFunc(blogH.Create)))
	blogH.RegisterRoutes(mux, blogCreateProtected)

	// TYLER reading room -- posting requires tyler.write; reading is public.
	tylerCreateProtected := middleware.RequireAuth(keys)(middleware.RequirePermission("tyler.write")(http.HandlerFunc(tylerH.Create)))
	tylerH.RegisterRoutes(mux, tylerCreateProtected)

	// Prompt-o-verse gallery -- posting/adding-variants both require promptoverse.write; reading is public.
	promptoverseCreateProtected := middleware.RequireAuth(keys)(middleware.RequirePermission("promptoverse.write")(http.HandlerFunc(promptoverseH.Create)))
	promptoverseAddVariantProtected := middleware.RequireAuth(keys)(middleware.RequirePermission("promptoverse.write")(http.HandlerFunc(promptoverseH.AddVariant)))
	promptoverseMergeTagsProtected := middleware.RequireAuth(keys)(middleware.RequirePermission("promptoverse.write")(http.HandlerFunc(promptoverseH.MergeTags)))
	promptoverseH.RegisterRoutes(mux, promptoverseCreateProtected, promptoverseAddVariantProtected, promptoverseMergeTagsProtected)

	// Mashup nominations -- the social layer for Prompt-o-verse (S176-27,
	// "build out mashup nomination as a social tool"). Nominating requires
	// only a valid logged-in user (honor-code check happens inside the
	// handler, since it needs a store lookup middleware alone can't do);
	// reviewing (approve/reject) requires promptoverse.mashups.review.
	mashupNominationsH := &handlers.MashupNominationsHandler{Store: promptoverseStore, IAMStore: iamStore}
	mashupNominationsCreateProtected := middleware.RequireAuth(keys)(http.HandlerFunc(mashupNominationsH.Create))
	mashupNominationsReviewProtected := middleware.RequireAuth(keys)(middleware.RequirePermission("promptoverse.mashups.review")(http.HandlerFunc(mashupNominationsH.Review)))
	mashupNominationsH.RegisterRoutes(mux, mashupNominationsCreateProtected, mashupNominationsReviewProtected)

	// Prompt-o-verse discovery page data -- style registry + GPT-2-harvested
	// candidates + content-block dead-letter dataset, read-only, public.
	mux.HandleFunc("GET /api/v1/promptoverse/discovery", (&handlers.DiscoveryHandler{}).Get)

	mux.Handle("/api/v1/status", statusH)
	mux.Handle("/api/v1/status/history", statusHistoryH)

	// Admin login/logout — public (no auth required).
	mux.Handle("/admin/login", adminLoginH)
	mux.Handle("/admin/logout", adminLoginH)

	// Admin UI — requires iduna.admin permission; cookie auth for browser navigation.
	adminProtected := middleware.RequireCookieAuth(keys, iamStore, "/admin/login", handlers.AdminSessionTTL)(middleware.RequirePermission("iduna.admin")(adminH))
	mux.Handle("/admin", adminProtected)
	mux.Handle("/admin/", adminProtected)

	// Developer notebook portal — human Google SSO (not agent login), gated
	// by devportal.access, a new permission granted to nobody by default
	// (see migrations/truestore/202608250001_devportal_permissions.sql).
	// /portal/login is public; /portal itself requires a live cookie
	// session AND the permission grant. See internal/http/handlers/portal.go
	// for the full design rationale.
	// Proj/Keys/Issuer are wired in below, once userProj exists (it's
	// declared further down, at the "User event log + projector" block) --
	// portalH is a pointer, so setting its fields after registration is
	// fine, request handling only ever reads them at request time.
	portalH := &handlers.PortalHandler{GoogleClientID: googleClientID}
	mux.HandleFunc("GET /portal/login", portalH.Login)
	mux.HandleFunc("POST /portal/login", portalH.LocalLogin)
	mux.HandleFunc("GET /portal/logout", portalH.Logout)
	portalProtected := middleware.RequireCookieAuth(keys, iamStore, "/portal/login", handlers.AdminSessionTTL)(middleware.RequirePermission("devportal.access")(http.HandlerFunc(portalH.Home)))
	mux.Handle("/portal", portalProtected)
	// S227-01: the log query page requires BOTH devportal.access (the base portal gate) AND
	// logs.read (the same permission GET /services/search/jobs itself requires) -- a devportal
	// user isn't automatically entitled to read the security/audit log this page exposes.
	portalLogsProtected := middleware.RequireCookieAuth(keys, iamStore, "/portal/login", handlers.AdminSessionTTL)(
		middleware.RequirePermission("devportal.access")(middleware.RequirePermission("logs.read")(http.HandlerFunc(portalH.Logs))))
	mux.Handle("/portal/logs", portalLogsProtected)

	// Unified search (kanban card 1111, "IDUNA UNIFIED SEARCH INTERFACE") -- same real
	// double-permission-gate shape as /portal/logs above, requiring BOTH read permissions since
	// this page shows both corpora at once (apples.read gates the same content
	// GET /api/v1/apples itself requires).
	portalSearchProtected := middleware.RequireCookieAuth(keys, iamStore, "/portal/login", handlers.AdminSessionTTL)(
		middleware.RequirePermission("devportal.access")(middleware.RequirePermission("logs.read")(middleware.RequirePermission("apples.read")(http.HandlerFunc(portalH.Search)))))
	mux.Handle("/portal/search", portalSearchProtected)

	// Kanban prioritization layer (S200-04-adjacent tooling, 2026-08-26) --
	// see internal/http/handlers/kanban.go's own doc comment for the full
	// founder-quote chain. The browser board (/admin/kanban and its own
	// /admin/kanban/api/cards) reuses iduna.admin, same gate as every other
	// /admin/* page -- a human who can already reach the Back Office needs
	// no separate grant. The bearer-gated /api/v1/kanban/cards (CLI/agent
	// access -- "i can ask the ai agent to work from the priority or cruise
	// backlog") uses a NEW, narrower kanban.access permission instead of
	// iduna.admin -- principle of least privilege: an automated agent that
	// reads/writes the kanban queue has no business also holding full Back
	// Office admin rights (migrations/truestore/202608260002_kanban_access_permission.sql,
	// granted to EMILY-PRIME in config/agents.json). Two mounts of the SAME
	// KanbanHandler either way -- zero duplicated logic, just two
	// middleware chains (and two different permissions) in front of one
	// handler instance.
	// Real bridge from EMILY/BACKLOG.md to the kanban board (2026-09-02,
	// founder real-time: "get the backlog working with the kanban" -> "if
	// it gets added to backlog via the kanban interface it needs to wind
	// up in the golden backlog file in git and as we work it needs to all
	// stay in sync") -- see internal/backlog's own doc comment and
	// kanban.go/kanban_inbox.go's own headers. EMILY and IDUNA are sibling
	// checkouts under the same monorepo root by convention everywhere else
	// in this repo (every CLAUDE.md cross-links "EMILY/BACKLOG.md" as a
	// bare relative path); default here resolves that the same way,
	// overridable for a non-standard checkout layout. Shared by kanbanH
	// (create/list) and kanbanInboxH below -- one real path, not two that
	// could drift apart.
	backlogPath := getenv("EMILY_BACKLOG_PATH", "/home/fatbaby/EMILY/BACKLOG.md")
	kanbanH := &handlers.KanbanHandler{DB: db, BacklogPath: backlogPath, Store: iamStore, ApplesGitDir: os.Getenv("APPLES_GIT_DIR")}
	kanbanPageProtected := middleware.RequireCookieAuth(keys, iamStore, "/admin/login", handlers.AdminSessionTTL)(middleware.RequirePermission("iduna.admin")(&handlers.KanbanPageHandler{}))
	mux.Handle("/admin/kanban", kanbanPageProtected)
	kanbanAdminAPIProtected := middleware.RequireCookieAuth(keys, iamStore, "/admin/login", handlers.AdminSessionTTL)(middleware.RequirePermission("iduna.admin")(kanbanH))
	mux.Handle("/admin/kanban/api/cards", kanbanAdminAPIProtected)
	mux.Handle("/admin/kanban/api/cards/", kanbanAdminAPIProtected)
	kanbanAPIProtected := middleware.RequireAuth(keys)(middleware.RequirePermission("kanban.access")(kanbanH))
	mux.Handle("/api/v1/kanban/cards", kanbanAPIProtected)
	mux.Handle("/api/v1/kanban/cards/", kanbanAPIProtected)
	kanbanInboxH := &handlers.KanbanInboxHandler{DB: db, BacklogPath: backlogPath}
	mux.Handle("/admin/kanban/api/inbox", middleware.RequireCookieAuth(keys, iamStore, "/admin/login", handlers.AdminSessionTTL)(middleware.RequirePermission("iduna.admin")(kanbanInboxH)))

	// IDUNA_PRO tenant provisioning control plane (docs/EMILY_FOR_BUSINESS_NORTHSTAR.md's own
	// "control-plane model", founder real-time 2026-09-03/2026-09-07): internal IDUNA stays the
	// backbone and gains the real capability to spin up a live, separate IDUNA_PRO instance per
	// tenant. admin-only -- no public self-serve signup exists yet (console.okemily.com is
	// still unbuilt), see internal/tenantprovision's own header comment for the full mechanism.
	tenantsH := &handlers.TenantsHandler{DB: db, Cfg: tenantprovision.NewDefaultConfig(db)}
	tenantsProtected := middleware.RequireAuth(keys)(middleware.RequirePermission("iduna.admin")(tenantsH))
	mux.Handle("/api/v1/tenants", tenantsProtected)

	// IDUNA Notebook Phase 1 (IN-000/IN-001, docs/IDUNA_NOTEBOOK_NORTHSTAR.md): plain, owner-
	// scoped notes CRUD -- gated by ordinary RequireAuth only, no admin/kanban-style shared
	// permission, since ownership itself (the caller's own real JWT sub) is the real access
	// control for a personal, self-serve feature.
	notesH := &handlers.NotesHandler{DB: db}
	notesProtected := middleware.RequireAuth(keys)(notesH)
	mux.Handle("/api/v1/notes", notesProtected)
	mux.Handle("/api/v1/notes/", notesProtected)

	// GFD Item Builder (ITEM_BUILDER_NORTHSTAR.md Phase 2a) -- same direct-file-access precedent
	// as the kanban/BACKLOG.md bridge above: IDUNA and GoblinFoxDragon are sibling checkouts on
	// this box, so this reads/writes GoblinFoxDragon/data/items.json directly.
	gfdItemsJSONPath := getenv("GFD_ITEMS_JSON_PATH", "/home/fatbaby/GoblinFoxDragon/data/items.json")
	gfdItemsH := &handlers.GfdItemsHandler{ItemsJSONPath: gfdItemsJSONPath}
	gfdItemsAdminProtected := middleware.RequireCookieAuth(keys, iamStore, "/admin/login", handlers.AdminSessionTTL)(middleware.RequirePermission("iduna.admin")(gfdItemsH))
	mux.Handle("/admin/gfd-items/api/items", gfdItemsAdminProtected)
	mux.Handle("/admin/gfd-items/api/items/", gfdItemsAdminProtected)
	mux.Handle("/admin/gfd-items", middleware.RequireCookieAuth(keys, iamStore, "/admin/login", handlers.AdminSessionTTL)(middleware.RequirePermission("iduna.admin")(&handlers.GfdItemsPageHandler{})))
	// Batch-propose assistant (ITEM_BUILDER_NORTHSTAR.md Phase 2d) -- reuses gfdItemsH's own
	// real create-item logic on approval, see GfdItemProposalHandler's own header comment.
	gfdItemProposalsH := &handlers.GfdItemProposalHandler{DB: db, Items: gfdItemsH}
	gfdItemProposalsProtected := middleware.RequireCookieAuth(keys, iamStore, "/admin/login", handlers.AdminSessionTTL)(middleware.RequirePermission("iduna.admin")(gfdItemProposalsH))
	mux.Handle("/admin/gfd-items/api/proposals", gfdItemProposalsProtected)
	mux.Handle("/admin/gfd-items/api/proposals/", gfdItemProposalsProtected)

	// NOCK (founder real-time, 2026-09-12: "we are gonna need to build our own tools to create
	// the textures... lets yolo it into iduna"). Deliberately NOT under GFD's own naming/scope --
	// built for SHANKPIT's real texture needs first so it stays a genuinely reusable engine tool,
	// not a GFD-only feature (see internal/nock's own package doc for the full rationale). Every
	// real operation lives in internal/nock.Service, shared by this HTTP API and cmd/nock's CLI.
	nockDataDir := getenv("NOCK_DATA_DIR", "/home/fatbaby/IDUNA/var/nock-projects")
	nockSvc, err := nock.NewService(nockDataDir)
	if err != nil {
		log.Fatalf("nock: %v", err)
	}
	nockH := &handlers.NockHandler{Svc: nockSvc}
	nockAdminProtected := middleware.RequireCookieAuth(keys, iamStore, "/admin/login", handlers.AdminSessionTTL)(middleware.RequirePermission("iduna.admin")(nockH))
	mux.Handle("/admin/nock/api/", nockAdminProtected)
	nockAssetsH, err := handlers.NewNockAssetsHandler()
	if err != nil {
		log.Fatalf("nock assets: %v", err)
	}
	mux.Handle("/admin/nock/", middleware.RequireCookieAuth(keys, iamStore, "/admin/login", handlers.AdminSessionTTL)(middleware.RequirePermission("iduna.admin")(nockAssetsH)))

	// NOCK code-server (VS Code in the browser, github.com/coder/code-server) -- founder
	// real-time: "can we add VS code to the nock tools? ... have the admin work through IDUNA so
	// i can use my same login flow from back office into NOCK." No fork of code-server: it runs
	// unmodified, standalone, with its own auth disabled (ops/systemd/nock-code-server.service,
	// 127.0.0.1:8892) -- this route's own RequireCookieAuth+iduna.admin chain, identical to every
	// other /admin/nock/* route above, is the real, only gate.
	//
	// CORRECTED (2026-09-21, live click-through testing): mounting this under a /admin/nock/code
	// *subpath* does NOT work -- checked directly against the real running code-server instance,
	// not assumed. Its own server-side router only recognizes fixed ROOT-level paths (/, /login,
	// /_static/*, /stable-<hash>/static/*, /manifest.json, ...) with no base-path/prefix support
	// at all (confirmed: /admin/nock/code/ -> 404, /_static/... at root -> 200, the identical
	// nested path under the subpath prefix -> 404). This is the exact same class of bug this
	// file's own /news/ location comment already names for newssite ("root-relative links...
	// broke under this subpath proxy... moved to its own subdomain") -- same fix here: a
	// dedicated, root-mounted host (console.okemily.com, DNS/nginx/cert in
	// IDUNA/ops/sudo-queue-console-okemily-nginx.sh) so code-server sees every request at the
	// real root it expects. Go's http.ServeMux (1.22+) host-specific pattern below matches ONLY
	// requests with this exact Host header, and -- being host-specific -- takes priority over
	// every hostless "/..." pattern in this file for that host, so console.okemily.com serves
	// nothing but this proxy. NewNockCodeProxyHandler itself needed no change: it already forwards
	// the full incoming path unchanged, which is exactly correct once the mount point IS root.
	nockCodeProxy := handlers.NewNockCodeProxyHandler("http://127.0.0.1:8892")
	if codeServerHost := getenv("NOCK_CODE_SERVER_HOST", ""); codeServerHost != "" {
		// A relative "/admin/login" redirect target (correct for every other RequireCookieAuth
		// use in this file, where the login page IS reachable on the same host) causes an
		// infinite redirect loop here: console.okemily.com's only registered route is this very
		// catch-all, so an unauthenticated browser hitting "/" gets redirected to
		// "https://console.okemily.com/admin/login" (relative -> resolved against the CURRENT
		// host), which is itself caught by the same catch-all, which redirects again, forever.
		// Found live (2026-09-21, real browser -- curl's own default Accept header doesn't
		// include text/html, so it takes RequireCookieAuth's other, non-redirecting branch and
		// never hit this). Fix: an ABSOLUTE login URL pointing at the real host that actually
		// serves /admin/login.
		codeServerLoginURL := getenv("NOCK_CODE_SERVER_LOGIN_URL", "/admin/login")
		nockCodeProxyProtected := middleware.RequireCookieAuth(keys, iamStore, codeServerLoginURL, handlers.AdminSessionTTL)(middleware.RequirePermission("iduna.admin")(nockCodeProxy))
		mux.Handle(codeServerHost+"/", nockCodeProxyProtected)
	}

	// NOCK texture library (founder real-time, 2026-09-12: "we are making a texture generator
	// and manager so it needs to have CRUD and all that and also we are gonna want to save them
	// in sqlite"). A real re-scope from the file-backed Project/Layer engine above: the primary
	// managed entity going forward is a standalone Texture row, backed by the shared IDUNA
	// SQLite `db` handle every other real CRUD surface in this file already uses -- see
	// internal/nock/texture_store.go's own header comment.
	nockTexturesH := &handlers.NockTexturesHandler{Store: &nock.TextureStore{DB: db}}
	nockTexturesProtected := middleware.RequireCookieAuth(keys, iamStore, "/admin/login", handlers.AdminSessionTTL)(middleware.RequirePermission("iduna.admin")(nockTexturesH))
	mux.Handle("/admin/nock/api/textures", nockTexturesProtected)
	mux.Handle("/admin/nock/api/textures/", nockTexturesProtected)

	// NOCK animation repository (founder real-time, 2026-09-16: "need animation repository" /
	// "ok I need to import quaternion assets nock tools drag and drop" -- the storage/browse
	// half of "let's start iterating towards nock tools modeler (blender) and golden band").
	// Same real shape as the texture library above; see internal/nock/anim_store.go and
	// gltf_convert.go's own header comments.
	nockAnimationsH := &handlers.NockAnimationsHandler{Store: &nock.AnimStore{DB: db}}
	nockAnimationsProtected := middleware.RequireCookieAuth(keys, iamStore, "/admin/login", handlers.AdminSessionTTL)(middleware.RequirePermission("iduna.admin")(nockAnimationsH))
	mux.Handle("/admin/nock/api/animations", nockAnimationsProtected)
	mux.Handle("/admin/nock/api/animations/", nockAnimationsProtected)

	// NOCK door script repository (SHANKPIT Story System Phase 1, S459-81 -- founder real-time:
	// "you know what we are tryna do fill in the gaps", closing the "via the nock tools" gap
	// named at the very start of that whole design thread). Same real shape as the texture/
	// animation stores above; see internal/nock/door_script_store.go and
	// door_script_compile.go's own header comments. The download route is a SEPARATE, public,
	// unauthenticated handler (nock_door_scripts_public.go) -- SHANKPIT's own game server is the
	// real consumer and has no IDUNA login of its own, same posture shankpit-levels' own public
	// export route already established.
	doorScriptStore := &nock.DoorScriptStore{DB: db}
	nockDoorScriptsH := &handlers.NockDoorScriptsHandler{Store: doorScriptStore}
	nockDoorScriptsProtected := middleware.RequireCookieAuth(keys, iamStore, "/admin/login", handlers.AdminSessionTTL)(middleware.RequirePermission("iduna.admin")(nockDoorScriptsH))
	mux.Handle("/admin/nock/api/door-scripts", nockDoorScriptsProtected)
	mux.Handle("/admin/nock/api/door-scripts/", nockDoorScriptsProtected)
	nockDoorScriptsPublicH := &handlers.NockDoorScriptsPublicHandler{Store: doorScriptStore}
	mux.Handle("/api/v1/nock-door-scripts/", nockDoorScriptsPublicH)

	// BRAWLPIT online level editor (S415-02/03, founder real-time: "get the brawlpit level
	// editor online - web technologies - we already started building nock - can we finish
	// building out some of that interface so we can kind of parlay it into an online brawlpit
	// level editor?"). Served under NOCK's own /admin/nock/ surface -- same real React app shell,
	// a new tab -- per the founder's explicit "parlay [NOCK's] interface" framing, but backed by
	// its own real package/table (internal/brawlpit), not folded into internal/nock itself, same
	// design principle that already keeps NOCK un-coupled from any one specific game.
	brawlpitLevelStore := &brawlpit.LevelStore{DB: db}
	brawlpitLevelsH := &handlers.BrawlpitLevelsHandler{Store: brawlpitLevelStore}
	brawlpitLevelsProtected := middleware.RequireCookieAuth(keys, iamStore, "/admin/login", handlers.AdminSessionTTL)(middleware.RequirePermission("iduna.admin")(brawlpitLevelsH))
	mux.Handle("/admin/nock/api/brawlpit-levels", brawlpitLevelsProtected)
	mux.Handle("/admin/nock/api/brawlpit-levels/", brawlpitLevelsProtected)

	// S417-02, founder real-time: "brawlpit needs a level selection/browser interface it needs
	// to work over https or some secure channel" -- a real, PUBLIC (unauthenticated), read-only
	// mirror of the same store, for the native BRAWLPIT client to list/fetch community levels.
	// Deliberately unauthenticated (browsing is public discovery, not editing) and a genuinely
	// separate, minimal handler type with no write methods at all -- see
	// brawlpit_levels_public.go's own doc comment.
	brawlpitLevelsPublicH := &handlers.BrawlpitLevelsPublicHandler{Store: brawlpitLevelStore}
	mux.Handle("/api/v1/brawlpit-levels", brawlpitLevelsPublicH)
	mux.Handle("/api/v1/brawlpit-levels/", brawlpitLevelsPublicH)

	// SHANKPIT NOCK level editor v0 (EMILY/BACKLOG.md SECTION 459, founder real-time: "so v0 it
	// and start working dont worry about the current levels lets just go full level select
	// brawlpit repo exact model for now") -- deliberately the exact same shape as BRAWLPIT's own
	// level editor immediately above (admin-gated CRUD + a separate public read-only mirror), one
	// game later. Own real package/table (internal/shankpit), not folded into internal/nock,
	// same design principle that already keeps NOCK un-coupled from any one specific game.
	shankpitMaterialStore := &shankpit.MaterialStore{DB: db}
	// S482, founder real-time: "i dont want to make doors be levels please - make widget or
	// something they are both objects but widgets just dont show up in the levels menu" -- a
	// real, separate table/store from levels (see the migration's own doc comment for why),
	// wired into LevelStore so flattenObjects can resolve a widget-referencing object at export
	// time the same real way it already resolves a level-referencing one.
	shankpitWidgetStore := &shankpit.WidgetStore{DB: db}
	shankpitLevelStore := &shankpit.LevelStore{DB: db, Materials: shankpitMaterialStore, Widgets: shankpitWidgetStore}
	shankpitLevelsH := &handlers.ShankpitLevelsHandler{Store: shankpitLevelStore}
	shankpitLevelsProtected := middleware.RequireCookieAuth(keys, iamStore, "/admin/login", handlers.AdminSessionTTL)(middleware.RequirePermission("iduna.admin")(shankpitLevelsH))
	mux.Handle("/admin/nock/api/shankpit-levels", shankpitLevelsProtected)
	mux.Handle("/admin/nock/api/shankpit-levels/", shankpitLevelsProtected)

	shankpitLevelsPublicH := &handlers.ShankpitLevelsPublicHandler{Store: shankpitLevelStore}
	mux.Handle("/api/v1/shankpit-levels", shankpitLevelsPublicH)
	mux.Handle("/api/v1/shankpit-levels/", shankpitLevelsPublicH)

	// S459-16, founder real-time: "we will need the ability to add new materials and set their
	// textures" / "registries for everything" -- same real admin-CRUD-plus-public-registry split
	// as levels immediately above.
	shankpitMaterialsH := &handlers.ShankpitMaterialsHandler{Store: shankpitMaterialStore}
	shankpitMaterialsProtected := middleware.RequireCookieAuth(keys, iamStore, "/admin/login", handlers.AdminSessionTTL)(middleware.RequirePermission("iduna.admin")(shankpitMaterialsH))
	mux.Handle("/admin/nock/api/shankpit-materials", shankpitMaterialsProtected)
	mux.Handle("/admin/nock/api/shankpit-materials/", shankpitMaterialsProtected)

	shankpitMaterialsPublicH := &handlers.ShankpitMaterialsPublicHandler{Store: shankpitMaterialStore}
	mux.Handle("/api/v1/shankpit-materials", shankpitMaterialsPublicH)

	// S482, founder real-time: "make widget or something they are both objects but widgets just
	// dont show up in the levels menu" -- admin CRUD only, same gate every other NOCK editor
	// surface uses. No public /api/v1 registry endpoint: unlike levels/materials, a widget is
	// never independently fetched by the native client -- it only ever reaches the native loader
	// already-flattened into whichever level's own export placed it as an object.
	shankpitWidgetsH := &handlers.ShankpitWidgetsHandler{Store: shankpitWidgetStore}
	shankpitWidgetsProtected := middleware.RequireCookieAuth(keys, iamStore, "/admin/login", handlers.AdminSessionTTL)(middleware.RequirePermission("iduna.admin")(shankpitWidgetsH))
	mux.Handle("/admin/nock/api/shankpit-widgets", shankpitWidgetsProtected)
	mux.Handle("/admin/nock/api/shankpit-widgets/", shankpitWidgetsProtected)

	// S459-19, founder real-time: "can we implement sprays? ... export to spray goes to sprays
	// registry same treatment ... we need a nock sprays interface right now just to set the
	// default." Same real admin-CRUD-plus-public-registry split as levels/materials above.
	shankpitSprayStore := &shankpit.SprayStore{DB: db}
	shankpitSpraysH := &handlers.ShankpitSpraysHandler{Store: shankpitSprayStore}
	shankpitSpraysProtected := middleware.RequireCookieAuth(keys, iamStore, "/admin/login", handlers.AdminSessionTTL)(middleware.RequirePermission("iduna.admin")(shankpitSpraysH))
	mux.Handle("/admin/nock/api/shankpit-sprays", shankpitSpraysProtected)
	mux.Handle("/admin/nock/api/shankpit-sprays/", shankpitSpraysProtected)

	shankpitSpraysPublicH := &handlers.ShankpitSpraysPublicHandler{Store: shankpitSprayStore}
	mux.Handle("/api/v1/shankpit-sprays", shankpitSpraysPublicH)
	mux.Handle("/api/v1/shankpit-sprays/", shankpitSpraysPublicH)

	// S420, founder real-time: "lets make a checkpoint registry so we can train from multiple
	// locations and then we can add checkpoints from colab?" -- a real, remote, shared RL
	// checkpoint registry (see internal/brawlpit/checkpoint_store.go's own doc comment for why a
	// local scripts/rl_league.py directory alone can't serve multiple training machines/Colab
	// runtimes). List/download are public (same trust level GET /api/v1/brawlpit-levels already
	// established); upload is gated behind the real M2M brawlpit.checkpoints.write permission
	// (migrations/truestore/202609131400_brawlpit_rl_checkpoints.sql's own new BRAWLPIT-RL agent).
	brawlpitCheckpointsHInner := &handlers.BrawlpitCheckpointsHandler{Store: &brawlpit.CheckpointStore{DB: db, BlobDir: "./var/brawlpit-checkpoints"}}
	brawlpitCheckpointsH := middleware.RequireAuth(keys)(
		middleware.RequirePermission("brawlpit.checkpoints.write")(
			brawlpitCheckpointsHInner,
		),
	)
	// The auth+permission wrapper above would incorrectly gate the public list/download GETs too
	// (RequireAuth/RequirePermission apply to the WHOLE handler, not per-method) -- so list/
	// download get their own, separate, unauthenticated handler instance pointed at the same
	// real store, and only the POST upload route uses the gated one. Same real store, same
	// underlying data either way -- this is a routing split, not two different registries.
	brawlpitCheckpointsPublicH := &handlers.BrawlpitCheckpointsHandler{Store: &brawlpit.CheckpointStore{DB: db, BlobDir: "./var/brawlpit-checkpoints"}}
	mux.Handle("/api/v1/brawlpit-checkpoints", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			brawlpitCheckpointsH.ServeHTTP(w, r)
			return
		}
		brawlpitCheckpointsPublicH.ServeHTTP(w, r)
	}))
	// S421-04, founder real-time: "can we start recording the match results with the actual
	// outcomes?" -- POST .../match-result needs the same M2M brawlpit.checkpoints.write gate as
	// the upload route above, but it lives under the trailing-slash catch-all (a sub-path, not
	// the exact base path) alongside the public GET .../active|:id/download|:id/weights routes,
	// so the split has to happen on method+path together here, not just method.
	mux.Handle("/api/v1/brawlpit-checkpoints/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/match-result") {
			brawlpitCheckpointsH.ServeHTTP(w, r)
			return
		}
		brawlpitCheckpointsPublicH.ServeHTTP(w, r)
	}))

	// S421, founder real-time: "just like the level editor (or the skins interface) we should be
	// able to select a model for the opponent from the registry" -- the one write action a human
	// makes through the real selection UI, admin-gated the same way brawlpit-levels' own editing
	// surface already is (a human picking an opponent through NOCK's UI, not the training
	// pipeline's own M2M upload above).
	brawlpitCheckpointActivateHInner := &handlers.BrawlpitCheckpointActivateHandler{Store: &brawlpit.CheckpointStore{DB: db, BlobDir: "./var/brawlpit-checkpoints"}}
	// S428, founder real-time: "i want to reset training but not include certain models from the
	// registry - can you add a checkbox to the registry backend to disable those models from the
	// league?" -- the real checkbox backend, same admin-cookie gate as activate above. Both
	// actions share one route pattern (/admin/nock/api/brawlpit-checkpoints/:id/<verb>), so they
	// dispatch on path suffix inside one gated handler, same real split-by-suffix convention the
	// public API route above already established.
	brawlpitCheckpointDisableHInner := &handlers.BrawlpitCheckpointDisableHandler{Store: &brawlpit.CheckpointStore{DB: db, BlobDir: "./var/brawlpit-checkpoints"}}
	brawlpitCheckpointActivateH := middleware.RequireCookieAuth(keys, iamStore, "/admin/login", handlers.AdminSessionTTL)(
		middleware.RequirePermission("iduna.admin")(
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/disable") {
					brawlpitCheckpointDisableHInner.ServeHTTP(w, r)
					return
				}
				brawlpitCheckpointActivateHInner.ServeHTTP(w, r)
			}),
		),
	)
	mux.Handle("/admin/nock/api/brawlpit-checkpoints/", brawlpitCheckpointActivateH)

	// S459-49, founder real-time: "bring in the bot registry affordances on NOCK all the same -
	// ability to disable - hide disabled - set default (defer this put the button then put like a
	// daisy ui alert not implemented) - for shankpit" -- the real SHANKPIT checkpoint registry,
	// same routing shape as the BRAWLPIT block immediately above (see that block's own comments
	// for the full public-vs-gated split rationale; internal/shankpit/checkpoint_store.go's own
	// doc comment for what's deliberately scoped down from BRAWLPIT's final registry).
	shankpitCheckpointsHInner := &handlers.ShankpitCheckpointsHandler{Store: &shankpit.CheckpointStore{DB: db, BlobDir: "./var/shankpit-checkpoints"}}
	shankpitCheckpointsH := middleware.RequireAuth(keys)(
		middleware.RequirePermission("shankpit.checkpoints.write")(
			shankpitCheckpointsHInner,
		),
	)
	shankpitCheckpointsPublicH := &handlers.ShankpitCheckpointsHandler{Store: &shankpit.CheckpointStore{DB: db, BlobDir: "./var/shankpit-checkpoints"}}
	mux.Handle("/api/v1/shankpit-checkpoints", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			shankpitCheckpointsH.ServeHTTP(w, r)
			return
		}
		shankpitCheckpointsPublicH.ServeHTTP(w, r)
	}))
	mux.Handle("/api/v1/shankpit-checkpoints/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// S459-76: PATCH .../:id (real elo-update route) needs the SAME agent-auth gate as
		// POST above -- everything else under this prefix (download, active) stays public.
		if r.Method == http.MethodPatch {
			shankpitCheckpointsH.ServeHTTP(w, r)
			return
		}
		shankpitCheckpointsPublicH.ServeHTTP(w, r)
	}))

	shankpitCheckpointActivateHInner := &handlers.ShankpitCheckpointActivateHandler{Store: &shankpit.CheckpointStore{DB: db, BlobDir: "./var/shankpit-checkpoints"}}
	shankpitCheckpointDisableHInner := &handlers.ShankpitCheckpointDisableHandler{Store: &shankpit.CheckpointStore{DB: db, BlobDir: "./var/shankpit-checkpoints"}}
	shankpitCheckpointActivateH := middleware.RequireCookieAuth(keys, iamStore, "/admin/login", handlers.AdminSessionTTL)(
		middleware.RequirePermission("iduna.admin")(
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/disable") {
					shankpitCheckpointDisableHInner.ServeHTTP(w, r)
					return
				}
				shankpitCheckpointActivateHInner.ServeHTTP(w, r)
			}),
		),
	)
	mux.Handle("/admin/nock/api/shankpit-checkpoints/", shankpitCheckpointActivateH)

	// DEADWEIGHT (and any future games.Registry game): NOCK admin write actions on the game-scoped registry.
	mux.Handle("/admin/nock/api/game-checkpoints/", middleware.RequireCookieAuth(keys, iamStore, "/admin/login", handlers.AdminSessionTTL)(
		middleware.RequirePermission("iduna.admin")(
			&handlers.GameCheckpointsAdminRouter{DB: db},
		),
	))

	// GFD Mob Drops (kanban GFD-MD-001) -- same direct-file-access precedent as GFD Item
	// Builder above, applied to the newly data-driven data/mob_drops.json.
	gfdMobDropsJSONPath := getenv("GFD_MOB_DROPS_JSON_PATH", "/home/fatbaby/GoblinFoxDragon/data/mob_drops.json")
	gfdMobDropsH := &handlers.GfdMobDropsHandler{DropsJSONPath: gfdMobDropsJSONPath}
	gfdMobDropsAdminProtected := middleware.RequireCookieAuth(keys, iamStore, "/admin/login", handlers.AdminSessionTTL)(middleware.RequirePermission("iduna.admin")(gfdMobDropsH))
	mux.Handle("/admin/gfd-mob-drops/api/tables", gfdMobDropsAdminProtected)
	mux.Handle("/admin/gfd-mob-drops/api/tables/", gfdMobDropsAdminProtected)
	mux.Handle("/admin/gfd-mob-drops", middleware.RequireCookieAuth(keys, iamStore, "/admin/login", handlers.AdminSessionTTL)(middleware.RequirePermission("iduna.admin")(&handlers.GfdMobDropsPageHandler{})))

	// GFD Registration waitlist toggle (kanban GFD-UA-001, second half).
	gfdRegistrationH := &handlers.GfdRegistrationHandler{DB: db}
	gfdRegistrationAdminProtected := middleware.RequireCookieAuth(keys, iamStore, "/admin/login", handlers.AdminSessionTTL)(middleware.RequirePermission("iduna.admin")(gfdRegistrationH))
	mux.Handle("/admin/gfd-registration/api/mode", gfdRegistrationAdminProtected)
	mux.Handle("/admin/gfd-registration/api/waitlist", gfdRegistrationAdminProtected)
	mux.Handle("/admin/gfd-registration/api/waitlist/", gfdRegistrationAdminProtected)
	mux.Handle("/admin/gfd-registration", middleware.RequireCookieAuth(keys, iamStore, "/admin/login", handlers.AdminSessionTTL)(middleware.RequirePermission("iduna.admin")(&handlers.GfdRegistrationPageHandler{})))

	// DEADWEIGHT admin hub (S508: "give me iduna back office tools to craft a DEADWEIGHT Premium
	// key... simple form just like GFD tools"; S523 founder real-time: "clean up the menu so it
	// just says DEADWEIGHT and the sub pages have the 2 tabs" -- consolidated from a standalone
	// "Game Claim Codes" link into one DEADWEIGHT nav entry with Claim Codes / Players tabs).
	gameClaimCodesH := &handlers.GameClaimCodesHandler{DB: db}
	gameClaimCodesAdminProtected := middleware.RequireCookieAuth(keys, iamStore, "/admin/login", handlers.AdminSessionTTL)(middleware.RequirePermission("iduna.admin")(gameClaimCodesH))
	mux.Handle("/admin/deadweight/api/games", gameClaimCodesAdminProtected)
	mux.Handle("/admin/deadweight/api/generate", gameClaimCodesAdminProtected)
	mux.Handle("/admin/deadweight/api/codes", gameClaimCodesAdminProtected)
	mux.Handle("/admin/deadweight/api/players", gameClaimCodesAdminProtected)
	mux.Handle("/admin/deadweight", middleware.RequireCookieAuth(keys, iamStore, "/admin/login", handlers.AdminSessionTTL)(middleware.RequirePermission("iduna.admin")(&handlers.DeadweightAdminPageHandler{})))

	// Real, general per-user settings home (WOTAN-24412) + the first real setting, high
	// contrast (ACCESSABILITY-14441). Deliberately RequireCookieAuth ALONE, no
	// RequirePermission wrapper -- unlike every admin/devportal page above, this is for ANY
	// authenticated user, not an admin-gated tool.
	userSettingsH := &handlers.UserSettingsHandler{DB: db}
	mux.Handle("/api/v1/settings/me", middleware.RequireCookieAuth(keys, iamStore, "/admin/login", handlers.AdminSessionTTL)(userSettingsH))
	mux.Handle("/settings", middleware.RequireCookieAuth(keys, iamStore, "/admin/login", handlers.AdminSessionTTL)(&handlers.UserSettingsPageHandler{DB: db}))

	// GFD Mob Spawns (GFD-MOBSPAWN-001 Phase 3) -- same direct-file-access precedent as the
	// other two GFD admin pages, applied to the newly data-driven data/mob_spawns.json.
	gfdMobSpawnsJSONPath := getenv("GFD_MOB_SPAWNS_JSON_PATH", "/home/fatbaby/GoblinFoxDragon/data/mob_spawns.json")
	gfdMobSpawnsH := &handlers.GfdMobSpawnsHandler{RulesJSONPath: gfdMobSpawnsJSONPath}
	gfdMobSpawnsAdminProtected := middleware.RequireCookieAuth(keys, iamStore, "/admin/login", handlers.AdminSessionTTL)(middleware.RequirePermission("iduna.admin")(gfdMobSpawnsH))
	mux.Handle("/admin/gfd-mob-spawns/api/rules", gfdMobSpawnsAdminProtected)
	mux.Handle("/admin/gfd-mob-spawns/api/rules/", gfdMobSpawnsAdminProtected)
	mux.Handle("/admin/gfd-mob-spawns", middleware.RequireCookieAuth(keys, iamStore, "/admin/login", handlers.AdminSessionTTL)(middleware.RequirePermission("iduna.admin")(&handlers.GfdMobSpawnsPageHandler{})))

	// GFD Dungeon Roster (GFD-MOBSPAWN-001 Phase 5, the final phase) -- same direct-file-access
	// precedent as the other three GFD admin pages.
	gfdDungeonRosterJSONPath := getenv("GFD_DUNGEON_ROSTER_JSON_PATH", "/home/fatbaby/GoblinFoxDragon/data/dungeon_roster.json")
	gfdDungeonRosterH := &handlers.GfdDungeonRosterHandler{RosterJSONPath: gfdDungeonRosterJSONPath}
	gfdDungeonRosterAdminProtected := middleware.RequireCookieAuth(keys, iamStore, "/admin/login", handlers.AdminSessionTTL)(middleware.RequirePermission("iduna.admin")(gfdDungeonRosterH))
	mux.Handle("/admin/gfd-dungeon-roster/api/dungeons", gfdDungeonRosterAdminProtected)
	mux.Handle("/admin/gfd-dungeon-roster/api/dungeons/", gfdDungeonRosterAdminProtected)
	mux.Handle("/admin/gfd-dungeon-roster", middleware.RequireCookieAuth(keys, iamStore, "/admin/login", handlers.AdminSessionTTL)(middleware.RequirePermission("iduna.admin")(&handlers.GfdDungeonRosterPageHandler{})))

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
	// Portal art — Prompt-o-verse gallery images (fenrir-robot, fox-robot),
	// same "downsample + serve as a real static file" pattern OKEMILY's own
	// wotan art uses, reused here for the notebook portal's login/home pages.
	mux.HandleFunc("GET /portal/images/fenrir-robot.jpg", serveStatic("static/portal/fenrir-robot.jpg"))
	mux.HandleFunc("GET /portal/images/fox-robot.jpg", serveStatic("static/portal/fox-robot.jpg"))
	// Also used by the redesigned /admin/login page (Back Office) --
	// kept under the same /portal/images/ path rather than inventing a
	// second art route for one more image.
	mux.HandleFunc("GET /portal/images/eye-of-providence-robot.jpg", serveStatic("static/portal/eye-of-providence-robot.jpg"))

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

	// ── Unified logging backend (Splunk-shaped: POST /services/collector, GET
	// /services/search/jobs) — founder real-time, 2026-09-02: "create a unified logging
	// backend for IDUNA using the new tech... one place to jump to and grab the logs... use
	// whatever affordances and apis splunk uses". A real, SEPARATE event log from the user-event
	// one above (that one is scoped to IDUNA local users specifically), but reuses that SAME
	// real userlog.FileEventLog/Event machinery with its own root dir -- see
	// internal/http/handlers/logs.go's own header comment for the real, checked-not-assumed
	// reason this doesn't cross-import PRRJECT_FATBABY's own eventstore package instead (real
	// CI here checks out this repo standalone, no go.work/sibling-repo present). Real, honest
	// scope: ingest + search infrastructure only, not a retrofit of every existing handler to
	// emit events yet.
	unifiedLogDir := filepath.Join(idunaRootForLog, "var", "eventlog")
	unifiedLog, err := userlog.NewFileEventLog(unifiedLogDir)
	if err != nil {
		log.Fatalf("unified event log: %v", err)
	}
	defer unifiedLog.Close()
	logsH := &handlers.LogsHandler{Store: unifiedLog, HECToken: getenv("IDUNA_HEC_TOKEN", "")}
	handlers.RegisterLogsRoutes(mux, logsH, keys)

	// S226-02/S226-03: wire real auth + admin events into the unified log now that unifiedLog
	// exists (same real "construct early, wire the field once its own dependency exists" pattern
	// portalH.Proj below already uses). All of these were already constructed above and
	// registered into mux by pointer, so setting EventLog here reaches the exact same handler
	// instances actually serving requests. Covers every real "login to the IDUNA backend"
	// surface (Google/agent API auth, the Back Office cookie login, the developer portal cookie
	// login) plus admin suspend/unsuspend for both users and agents.
	googleAuthH.EventLog = unifiedLog
	agentAuthH.EventLog = unifiedLog
	adminLoginH.EventLog = unifiedLog
	adminH.EventLog = unifiedLog

	// S226-04: wire the remaining real code paths named as this section's own explicit follow-up
	// -- HEIMDAL sprint transitions and Apple postings, alongside admin.go's own newly-added
	// role-assign/revoke + agent-permission-grant/revoke + agent-secret-rotate emissions (same
	// adminH.EventLog wire above already covers those, no separate line needed here).
	heimdalH.EventLog = unifiedLog
	applesH.EventLog = unifiedLog

	// kanban card 3243242: kanban was the one real, load-bearing subsystem missing from this
	// list -- card create/move/complete now emit into the same unified log every other real
	// IDUNA code path already does.
	kanbanH.EventLog = unifiedLog

	// S453, founder real-time: "lets start a log streaming trail and iduna unified logging for
	// when the brawlpit AI is changed on the server" -- every real "the live AI population
	// changed" point (a new checkpoint pushed, which one is the live opponent, which are
	// eligible at all) now emits here too. brawlpitCheckpointsH specifically (not
	// brawlpitCheckpointsPublicH) -- only that instance's own upload route is a real write.
	brawlpitCheckpointsHInner.EventLog = unifiedLog
	brawlpitCheckpointActivateHInner.EventLog = unifiedLog
	brawlpitCheckpointDisableHInner.EventLog = unifiedLog
	shankpitCheckpointsHInner.EventLog = unifiedLog
	shankpitCheckpointActivateHInner.EventLog = unifiedLog
	shankpitCheckpointDisableHInner.EventLog = unifiedLog

	// Wire the developer portal's real IDUNA login now that userProj exists
	// (see the portalH declaration above, in the /portal route block, for
	// why this is set here rather than in the struct literal there).
	portalH.Proj = userProj
	portalH.Keys = keys
	portalH.Issuer = issuer
	portalH.EventLog = unifiedLog
	portalH.Store = iamStore
	portalH.BlogStore = blogStore

	localAuthH := &handlers.LocalAuthHandler{Keys: keys, Proj: userProj, Issuer: issuer, EventLog: unifiedLog}
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
	// S503-06: generic per-game online services (guest accounts, token verify, match results, stats) and the
	// game-scoped checkpoint registry. Guest register/login share the auth IP limiter's budget style but with
	// their own bucket so game traffic can't starve real logins. Config rows live in internal/games.
	// WOTAN's public, read-only DEADWEIGHT draft-deck browser (decks + win rates from dw_server's decks.ndjson). More specific
	// patterns than the generic /api/v1/games/ handler below, so they win.
	deckStatsH := &handlers.DeckStatsHandler{
		Store:   &deckstats.Store{Path: getenv("DEADWEIGHT_DECK_LOG", "/home/fatbaby/DEADWEIGHT/var/matches/decks.ndjson")},
		Limiter: middleware.NewIPRateLimiter(120),
	}
	mux.Handle("/api/v1/games/deadweight/decks", deckStatsH)
	mux.Handle("/api/v1/games/deadweight/decks/", deckStatsH)
	mux.Handle("/api/v1/games/deadweight/card-stats", deckStatsH)
	mux.Handle("/api/v1/games/", &handlers.GameOnlineHandler{DB: db, Keys: keys, Limiter: middleware.NewIPRateLimiter(30)})
	mux.Handle("/api/v1/game-checkpoints/", &handlers.GameCheckpointsRouter{DB: db, Keys: keys, EventLog: unifiedLog})

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
		Game:   "shankpit", // S241-01
	})
	mux.Handle("/api/v1/shankpit/ticket", shankpitTicketH)

	// WOTAN for REDGARDEN (NORTHSTAR §12 Phase A/F, S170-26/41): REDGARDEN
	// bots have no OAuth login, so unlike shankpit's ticket handler (which
	// mints for the caller's own player_id, from a real human's JWT), this
	// mints on behalf of a player_id in the request body, restricted to the
	// REDGARDEN-BOTS M2M agent via redgarden.ticket.mint — see
	// redgarden_ticket.go's doc comment for the full trust model.
	redgardenTicketH := middleware.RequireAuth(keys)(
		middleware.RequirePermission("redgarden.ticket.mint")(&handlers.RedgardenTicketHandler{
			DB:     db,
			Secret: []byte(os.Getenv("REDGARDEN_TICKET_SECRET")),
		}),
	)
	mux.Handle("/api/v1/redgarden/ticket", redgardenTicketH)

	// REDGARDEN_GUI_NORTHSTAR.md Milestone 3 (2026-07-31): the real, non-bot counterpart to the
	// handler just above — mints on behalf of a real DragonsNShit character's own player_id
	// (GoblinFoxDragon/apps2/mud's `battlegrounds` command), gated behind a separate permission
	// so redgarden.ticket.mint's own bot-only blast-radius guarantee is untouched. See
	// redgarden_player_ticket.go's own doc comment for the full trust model.
	redgardenPlayerTicketH := middleware.RequireAuth(keys)(
		middleware.RequirePermission("redgarden.player-ticket.mint")(&handlers.RedgardenPlayerTicketHandler{
			DB:     db,
			Secret: []byte(os.Getenv("REDGARDEN_TICKET_SECRET")),
		}),
	)
	mux.Handle("/api/v1/redgarden/player-ticket", redgardenPlayerTicketH)

	// REDGARDEN_GUI_NORTHSTAR.md's "no GUI login path" gap, closed: apps/arena's own login
	// screen calls /api/v1/auth/email/login for a player JWT, then this endpoint (same
	// "mint for the caller's own JWT subject" trust model as shankpitTicketH above) instead of
	// going through apps2/mud's telnet `battlegrounds` command.
	redgardenSelfTicketH := middleware.RequireAuth(keys)(&handlers.RedgardenSelfTicketHandler{
		DB:     db,
		Secret: []byte(os.Getenv("REDGARDEN_TICKET_SECRET")),
	})
	mux.Handle("/api/v1/redgarden/self-ticket", redgardenSelfTicketH)

	// Chat relay between GoblinFoxDragon's apps2/mud (telnet) and REDGARDEN's Battlegrounds GUI
	// client -- two separate processes/protocols with no channel of their own, IDUNA is the one
	// thing both already authenticate against. Any authenticated caller may post or poll (own
	// auth check inside the handler covers the no-claims case) -- founder: "can we start adding
	// affordances to the fork to surface the features of the MUD?" -> "In-match MUD chat."
	chatMessagesH := middleware.RequireAuth(keys)(&handlers.ChatMessagesHandler{DB: db})
	mux.Handle("/api/v1/chat/messages", chatMessagesH)

	redgardenResultH := middleware.RequireAuth(keys)(
		middleware.RequirePermission("redgarden.match.write")(&handlers.RedgardenGameResultHandler{DB: db}),
	)
	mux.Handle("/api/v1/redgarden/game-result", redgardenResultH)

	// Public leaderboard read — same trust level as GET /api/v1/players/{id}.
	mux.Handle("/api/v1/redgarden/leaderboard", &handlers.RedgardenLeaderboardHandler{DB: db})

	redgardenHeroResultH := middleware.RequireAuth(keys)(
		middleware.RequirePermission("redgarden.match.write")(&handlers.RedgardenHeroResultHandler{DB: db}),
	)
	mux.Handle("/api/v1/redgarden/hero-result", redgardenHeroResultH)

	// Public hero leaderboard read — "which heroes are strongest," same trust level as the
	// player leaderboard above.
	mux.Handle("/api/v1/redgarden/hero-leaderboard", &handlers.RedgardenHeroLeaderboardHandler{DB: db})

	// Live match spectator state (2026-07-30, founder: "i want to watch the match on my phone
	// web view") — same redgarden.match.write trust as the write handlers above; the GET side is
	// public, same trust level as the leaderboards.
	redgardenLiveMatchWriteH := middleware.RequireAuth(keys)(
		middleware.RequirePermission("redgarden.match.write")(&handlers.RedgardenLiveMatchHandler{}),
	)
	mux.Handle("/api/v1/redgarden/live-match", redgardenLiveMatchWriteH)
	mux.Handle("/api/v1/redgarden/live-match/latest", &handlers.RedgardenLiveMatchGetHandler{})

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

	// WEAKNIGHT_BEDROCK_RACERS connect-ticket + matchmaking queue (2026-08-28 racer-first pivot,
	// founder real-time: "build login from the beginning take it from GFD" / "after you login it
	// drops you into matchmaking queiueueue") -- direct instantiation of the exact same real,
	// proven SHANKPIT patterns just above (RacerTicketHandler is a straight port of
	// ShankpitTicketHandler; ShankpitQueue's own type is already generic enough to reuse as-is,
	// no new queue implementation needed, just a second instance with its own ServerAddr).
	racerTicketH := middleware.RequireAuth(keys)(&handlers.RacerTicketHandler{
		Secret: []byte(os.Getenv("RACER_TICKET_SECRET")),
		Game:   "racer", // S241-01
	})
	mux.Handle("/api/v1/racer/ticket", racerTicketH)

	racerServerAddr := os.Getenv("RACER_SERVER_ADDR")
	if racerServerAddr == "" {
		racerServerAddr = "127.0.0.1:7788" // matches apps/client/src/main.c's own default --server-port
	}
	racerQueue := handlers.NewShankpitQueue(racerServerAddr)
	// Bots already fill every non-human RC_MAX_VEHICLES slot server-side (apps/server/src/main.c)
	// -- one real human queuing is a real, playable match on its own, unlike SHANKPIT's own
	// human-vs-human bar. See ShankpitQueue.MinPlayers's own doc comment.
	racerQueue.MinPlayers = 1
	mux.Handle("/api/v1/racer/queue/join", middleware.RequireAuth(keys)(http.HandlerFunc(racerQueue.Join)))
	mux.Handle("/api/v1/racer/queue/leave", middleware.RequireAuth(keys)(http.HandlerFunc(racerQueue.Leave)))
	mux.Handle("/api/v1/racer/queue/status", middleware.RequireAuth(keys)(http.HandlerFunc(racerQueue.Status)))

	// PAPERCRAFT connect-ticket (2026-08-28, real Phase 0) -- ticket only, no queue: this is a
	// real single-node persistent world ("papercraft shouldnt have matches"), so a player mints a
	// ticket and connects straight to the one always-running server, unlike the racer's own
	// matchmaking flow just above.
	papercraftTicketH := middleware.RequireAuth(keys)(&handlers.PapercraftTicketHandler{
		Secret: []byte(os.Getenv("PAPERCRAFT_TICKET_SECRET")),
		Game:   "papercraft", // S241-01
	})
	mux.Handle("/api/v1/papercraft/ticket", papercraftTicketH)

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
	mux.Handle("/api/v1/hats", mmoH)
	mux.Handle("/api/v1/hats/", mmoH)
	// SSH key -> character fingerprint lookup (SSH_TRANSPORT_IDENTITY_SPEC.md §3, Stage 5) --
	// list/bind/revoke live under /api/v1/characters/:id/ssh-keys, already covered by the
	// /api/v1/characters/ registration above; this is only the top-level lookup-by-fingerprint
	// route. Found live: this registration was missing entirely on first pass, caught by testing
	// the real endpoint against a throwaway instance rather than trusting the handler's own
	// ServeHTTP switch statement alone -- a real 404 (net/http's own default, not
	// ssh_keys.go's) is what a missing mux.Handle looks like, and is easy to mistake for the
	// handler's own real "fingerprint not found" 404 if only the handler code is read.
	mux.Handle("/api/v1/ssh-keys", mmoH)

	// Supply chain API (S136-02/03) — auth required.
	supplyH := middleware.RequireAuth(keys)(&handlers.SupplyHandler{DB: db})
	mux.Handle("/api/v1/supply/", supplyH)

	// Research cache API (S137-03) — auth required.
	researchH := middleware.RequireAuth(keys)(&handlers.ResearchHandler{DB: db})
	mux.Handle("/api/v1/research/", researchH)

	// EINHORN INDEX knowledge graph proxy (S138-06) — auth required; proxies to KGRAPH_URL.
	kgraphH := middleware.RequireAuth(keys)(&handlers.KGraphHandler{})
	mux.Handle("/api/v1/kgraph/", kgraphH)

	// IDUNA_ADDR lets a throwaway instance (tests, live verification) bind a private port instead of
	// colliding with the real :8080. Default is unchanged.
	listenAddr := getenv("IDUNA_ADDR", ":8080")
	log.Printf("iduna listening on %s", listenAddr)
	log.Fatal(http.ListenAndServe(listenAddr, mux))
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// mailingListAutoUnlock implements S245-01's config/file-key vault mode: on
// first boot (vault not yet initialized) it generates a fresh key, writes it
// to keyFilePath (0600), and initializes the vault with it; on every
// subsequent boot it reads the existing key file back and unlocks with it.
// Either way the vault ends up unlocked in-process with no human involved —
// the whole point of this mode over the default passphrase path.
func mailingListAutoUnlock(store *mailinglist.Store, v *mailinglist.Vault, keyFilePath string) error {
	initialized, err := store.Initialized()
	if err != nil {
		return fmt.Errorf("check vault init state: %w", err)
	}
	if !initialized {
		key, err := mailinglist.NewFileKey()
		if err != nil {
			return err
		}
		canaryCT, canaryNonce, err := mailinglist.NewCanaryFromKey(key)
		if err != nil {
			return err
		}
		// Empty salt: file-key mode does no Argon2 derivation, so the
		// vault_meta salt column (NOT NULL, but not length-checked) goes
		// unused here — kept only so InitVault/VaultMeta's shared schema
		// doesn't need a passphrase-vs-keyfile branch.
		if err := store.InitVault([]byte{}, canaryCT, canaryNonce); err != nil {
			return fmt.Errorf("init vault: %w", err)
		}
		if err := os.WriteFile(keyFilePath, []byte(hex.EncodeToString(key)+"\n"), 0o600); err != nil {
			return fmt.Errorf("write key file: %w", err)
		}
	}

	_, canaryCT, canaryNonce, err := store.VaultMeta()
	if err != nil {
		return fmt.Errorf("read vault meta: %w", err)
	}
	return v.UnlockFromKeyFile(keyFilePath, canaryCT, canaryNonce)
}
