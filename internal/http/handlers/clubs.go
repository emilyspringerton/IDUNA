package handlers

// clubs.go -- WOTAN Phase 2 Clubs (WOTAN/CLUBS_NORTHSTAR.md): clubs (teams), roles, privacy tiers, invites,
// join requests, a members-only strategy feed, featured decks, audit and a club-context token.
//
// Security model (sections 3, 4): the JWT only proves WHO the caller is. What they may do in a club is
// re-read from club_memberships on every request through the single guard() choke point, and club_id is only
// ever taken from the path (never from a body or query) so a tampered body cannot cross tenants. Secret clubs
// answer 404 to non-members everywhere; Private/Social answer 403. Public views are built from an explicit
// allow-list of columns/fields so a future column cannot leak by default.
//
// Same conventions as hs_social.go: Cache-Control no-store, {total,limit,offset,items} lists, {error,code}
// errors, per-IP read limiter and per-subject write limiter, all user text bounded by hsText.
//
// Telemetry/analytics endpoints (sync-target, matchups, contributors) are chunk P2-B and are not here.

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"iduna/internal/auth/jwt"
	"iduna/internal/clubs"
	"iduna/internal/http/middleware"
	"iduna/internal/userlog"
)

const (
	clubTokenAud      = "wotan-club"
	clubTokenTTL      = 15 * time.Minute
	clubInviteURLBase = "https://wotan.okemily.com/clubs/join.html#"
	clubSQLTime       = "2006-01-02 15:04:05"

	clubDefClubsPerUser   = 5
	clubDefMembersPerClub = 250
	clubDefInviteTTLMax   = 168 // hours
	clubDefPendingInvites = 50
	clubFeaturedCap       = 12
)

// ClubsHandler serves /api/v1/clubs/ (plus /api/v1/me/clubs). EventLog is optional (nil = no mirror).
type ClubsHandler struct {
	DB           *sql.DB
	Keys         *jwt.Keys
	ReadLimiter  *middleware.IPRateLimiter // per IP, nil = unlimited (tests)
	WriteLimiter *middleware.IPRateLimiter // per subject, nil = unlimited (tests)
	EventLog     userlog.EventLog
}

var (
	clubSlugRe      = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,28}[a-z0-9]$`)
	clubAccentRe    = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	clubReservedSlg = map[string]bool{"me": true, "users": true, "admin": true, "by-slug": true, "invites": true,
		"join": true, "new": true, "create": true, "settings": true, "clubs": true, "api": true, "wotan": true,
		"iduna": true, "emily": true, "manage": true, "workspace": true}
)

// clubReadNodes are the nodes still usable while a club is suspended (workspace is read-only then).
var clubReadNodes = map[clubs.Node]bool{
	clubs.NodeView: true, clubs.NodeRosterView: true, clubs.NodeFeedRead: true, clubs.NodeDecksRead: true,
	clubs.NodeAnalyticsView: true, clubs.NodeAuditView: true,
}

// ---- plumbing -----------------------------------------------------------------------------------------

type dbq interface {
	QueryRow(query string, args ...any) *sql.Row
	Query(query string, args ...any) (*sql.Rows, error)
	Exec(query string, args ...any) (sql.Result, error)
}

type clubViewer struct {
	sub   string
	perms map[string]bool
}

func (h *ClubsHandler) viewer(r *http.Request) clubViewer {
	ah := r.Header.Get("Authorization")
	if !strings.HasPrefix(ah, "Bearer ") || h.Keys == nil {
		return clubViewer{}
	}
	claims, err := jwt.Verify(h.Keys, strings.TrimPrefix(ah, "Bearer "))
	if err != nil {
		return clubViewer{}
	}
	// Upload tokens and club-context tokens are for external verifiers, never bearer identity here (R3).
	if aud, _ := claims["aud"].(string); aud == hsLiveUploadAud || aud == clubTokenAud || aud == oauthStateAud {
		return clubViewer{}
	}
	sub, _ := claims["sub"].(string)
	v := clubViewer{sub: sub, perms: map[string]bool{}}
	if perms, ok := claims["permissions"].([]any); ok {
		for _, p := range perms {
			if s, _ := p.(string); s != "" {
				v.perms[s] = true
			}
		}
	}
	return v
}

func clubErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{"error": msg, "code": code})
}

func clubNow() string { return time.Now().UTC().Format(clubSQLTime) }

func clubTier(t int) string {
	switch t {
	case 1:
		return "secret"
	case 2:
		return "private"
	case 3:
		return "social"
	}
	return ""
}

func clubHash(token string) string {
	s := sha256.Sum256([]byte(token))
	return hex.EncodeToString(s[:])
}

func clubDecodeOpt(r *http.Request, dst any) error {
	err := hsDecode(r, dst)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

type clubRow struct {
	ID, Slug, Name, Tagline, Description string
	Tier                                 int
	PrevTier                             sql.NullInt64
	Exclusive                            bool
	JoinPolicy                           string
	ShowContrib                          bool
	OwnerSub, BrandJSON                  string
	Suspended                            bool
	SuspendedReason                      string
	Deleted                              bool
	CreatedAt, UpdatedAt                 string
}

const clubCols = `id, slug, name, tagline, description, privacy_tier, prev_tier, is_exclusive, join_policy,
 show_contributors, owner_sub, brand_json, suspended, suspended_reason, deleted, created_at, updated_at`

func loadClub(q dbq, col string, arg any) (clubRow, error) {
	var c clubRow
	var ex, sc, sus, del int
	err := q.QueryRow(`SELECT `+clubCols+` FROM clubs WHERE `+col+`=?`, arg).Scan(&c.ID, &c.Slug, &c.Name, &c.Tagline,
		&c.Description, &c.Tier, &c.PrevTier, &ex, &c.JoinPolicy, &sc, &c.OwnerSub, &c.BrandJSON, &sus,
		&c.SuspendedReason, &del, &c.CreatedAt, &c.UpdatedAt)
	c.Exclusive, c.ShowContrib, c.Suspended, c.Deleted = ex == 1, sc == 1, sus == 1, del == 1
	return c, err
}

type clubBrand struct {
	Accent     string `json:"accent"`
	BannerText string `json:"banner_text"`
}

// brandOf re-encodes through the struct so only the two allowed keys can ever be emitted.
func brandOf(raw string) clubBrand {
	var b clubBrand
	_ = json.Unmarshal([]byte(raw), &b)
	if !clubAccentRe.MatchString(b.Accent) {
		b.Accent = ""
	}
	return b
}

func (h *ClubsHandler) memberRole(q dbq, clubID, sub string) (clubs.Role, bool) {
	var role string
	if err := q.QueryRow(`SELECT role FROM club_memberships WHERE club_id=? AND sub=?`, clubID, sub).Scan(&role); err != nil {
		return "", false
	}
	return clubs.Role(role), true
}

func (h *ClubsHandler) managerCount(q dbq, clubID string) int {
	var n int
	_ = q.QueryRow(`SELECT COUNT(*) FROM club_memberships WHERE club_id=? AND role='manager'`, clubID).Scan(&n)
	return n
}

func (h *ClubsHandler) handleOf(q dbq, sub string) string {
	var hd string
	_ = q.QueryRow(`SELECT handle FROM hs_profiles WHERE sub=?`, sub).Scan(&hd)
	return hd
}

func (h *ClubsHandler) subOfHandle(q dbq, handle string) string {
	var s string
	_ = q.QueryRow(`SELECT sub FROM hs_profiles WHERE handle=?`, strings.ToLower(strings.TrimSpace(handle))).Scan(&s)
	return s
}

// setting reads a platform cap from club_settings, falling back to the in-code default.
func (h *ClubsHandler) setting(q dbq, key string, def int) int {
	var v string
	if err := q.QueryRow(`SELECT value FROM club_settings WHERE key=?`, key).Scan(&v); err != nil {
		return def
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 1 {
		return def
	}
	return n
}

// ---- transaction with audit + event mirror --------------------------------------------------------------

type clubEvent struct {
	typ  string
	data map[string]any
}

type clubTx struct {
	h    *ClubsHandler
	tx   *sql.Tx
	r    *http.Request
	evs  []clubEvent
	err  error
	done bool
}

func (h *ClubsHandler) begin(r *http.Request) (*clubTx, error) {
	tx, err := h.DB.Begin()
	if err != nil {
		return nil, err
	}
	return &clubTx{h: h, tx: tx, r: r}, nil
}

// audit appends an append-only club_audit_log row inside the transaction and queues the unified-log mirror,
// which only fires if the transaction commits. The first failure poisons the transaction.
func (t *clubTx) audit(clubID, actor, action, target string, meta map[string]any, platform bool) {
	if t.err != nil {
		return
	}
	if meta == nil {
		meta = map[string]any{}
	}
	raw, _ := json.Marshal(meta)
	pa := 0
	if platform {
		pa = 1
	}
	_, t.err = t.tx.Exec(`INSERT INTO club_audit_log (club_id, actor_sub, action, target_sub, meta_json, platform_admin, at)
		VALUES (?,?,?,?,?,?,?)`, clubID, actor, action, target, string(raw), pa, clubNow())
	t.evs = append(t.evs, clubEvent{"iduna:wotan.club." + action, map[string]any{
		"club_id": clubID, "actor_sub": actor, "target_sub": target, "meta": meta, "platform_admin": platform}})
}

func (t *clubTx) commit() error {
	if t.done {
		return nil
	}
	t.done = true
	if t.err != nil {
		_ = t.tx.Rollback()
		return t.err
	}
	if err := t.tx.Commit(); err != nil {
		return err
	}
	for _, e := range t.evs {
		emitAuthEvent(t.r.Context(), t.h.EventLog, e.typ, "iduna-wotan-clubs", e.data)
	}
	return nil
}

func (t *clubTx) rollback() {
	if !t.done {
		t.done = true
		_ = t.tx.Rollback()
	}
}

// ---- routing --------------------------------------------------------------------------------------------

func (h *ClubsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.ReadLimiter != nil && !h.ReadLimiter.Allow(clientIP(r)) {
		hsErr(w, http.StatusTooManyRequests, "slow down")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	v := h.viewer(r)
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/"), "/")
	var p []string
	if rest != "" {
		p = strings.Split(rest, "/")
	}
	switch {
	case len(p) >= 1 && p[0] == "clubs":
		p = p[1:]
	case len(p) == 2 && p[0] == "me" && p[1] == "clubs":
		// /api/v1/me/clubs -- same handler as /api/v1/clubs/me/clubs
	default:
		hsErr(w, http.StatusNotFound, "not found")
		return
	}
	m := r.Method
	isWrite := m != http.MethodGet && m != http.MethodHead
	anonOK := m == http.MethodPost && len(p) == 2 && p[0] == "invites" && p[1] == "peek"
	if isWrite && !anonOK {
		if v.sub == "" {
			hsErr(w, http.StatusUnauthorized, "sign in with IDUNA first")
			return
		}
		if h.WriteLimiter != nil && !h.WriteLimiter.Allow("clubs:"+v.sub) {
			hsErr(w, http.StatusTooManyRequests, "you're doing that too fast")
			return
		}
	}
	n := len(p)
	switch {
	case n == 0 && m == http.MethodGet:
		h.directory(w, r)
	case n == 0 && m == http.MethodPost:
		h.create(w, r, v)
	case n == 2 && p[0] == "me" && p[1] == "clubs" && m == http.MethodGet:
		h.myClubs(w, v)
	case n == 3 && p[0] == "users" && p[2] == "clubs" && m == http.MethodGet:
		h.userClubs(w, v, p[1])
	case n >= 1 && p[0] == "admin":
		h.admin(w, r, v, p[1:])
	case n >= 2 && p[0] == "by-slug":
		h.bySlug(w, r, v, p[1:])
	case n == 2 && p[0] == "invites" && p[1] == "peek" && m == http.MethodPost:
		h.peekInvite(w, r, v)
	case n == 1 && p[0] == "join" && m == http.MethodPost:
		h.join(w, r, v)
	case n >= 1 && p[0] != "me" && p[0] != "users" && p[0] != "invites" && p[0] != "join":
		h.clubRoutes(w, r, v, p[0], p[1:])
	default:
		hsErr(w, http.StatusNotFound, "not found")
	}
}

func (h *ClubsHandler) clubRoutes(w http.ResponseWriter, r *http.Request, v clubViewer, id string, p []string) {
	m, n := r.Method, len(p)
	switch {
	case n == 0 && m == http.MethodPatch:
		h.patchSettings(w, r, v, id)
	case n == 0 && m == http.MethodDelete:
		h.deleteClub(w, r, v, id)
	case n == 1 && p[0] == "me" && m == http.MethodGet:
		h.clubMe(w, v, id)
	case n == 1 && p[0] == "token" && m == http.MethodPost:
		h.clubToken(w, v, id)
	case n == 1 && p[0] == "visibility" && m == http.MethodPut:
		h.putVisibility(w, r, v, id)
	case n == 1 && p[0] == "transfer" && m == http.MethodPost:
		h.transfer(w, r, v, id)
	case n == 1 && p[0] == "leave" && m == http.MethodPost:
		h.leave(w, r, v, id)
	case n == 1 && p[0] == "members" && m == http.MethodGet:
		h.listMembers(w, r, v, id)
	case n == 2 && p[0] == "members" && p[1] == "me" && m == http.MethodPatch:
		h.patchMyPrefs(w, r, v, id)
	case n == 2 && p[0] == "members" && m == http.MethodPatch:
		h.changeRole(w, r, v, id, p[1])
	case n == 2 && p[0] == "members" && m == http.MethodDelete:
		h.removeMember(w, r, v, id, p[1])
	case n == 1 && p[0] == "invites" && m == http.MethodPost:
		h.createInvite(w, r, v, id)
	case n == 1 && p[0] == "invites" && m == http.MethodGet:
		h.listInvites(w, v, id)
	case n == 2 && p[0] == "invites" && m == http.MethodDelete:
		h.revokeInvite(w, r, v, id, p[1])
	case n == 1 && p[0] == "requests" && m == http.MethodGet:
		h.listRequests(w, v, id)
	case n == 3 && p[0] == "requests" && p[2] == "decision" && m == http.MethodPost:
		h.decideRequest(w, r, v, id, p[1])
	case n == 1 && p[0] == "feed" && m == http.MethodGet:
		h.listFeed(w, r, v, id)
	case n == 1 && p[0] == "feed" && m == http.MethodPost:
		h.createPost(w, r, v, id)
	case n == 2 && p[0] == "feed" && m == http.MethodDelete:
		h.deletePost(w, r, v, id, p[1])
	case n == 3 && p[0] == "feed" && p[2] == "pin" && m == http.MethodPut:
		h.pinPost(w, r, v, id, p[1])
	case n == 3 && p[0] == "feed" && p[2] == "comments" && m == http.MethodGet:
		h.listComments(w, r, v, id, p[1])
	case n == 3 && p[0] == "feed" && p[2] == "comments" && m == http.MethodPost:
		h.addComment(w, r, v, id, p[1])
	case n == 2 && p[0] == "featured-decks" && (m == http.MethodPut || m == http.MethodDelete):
		h.featureDeck(w, r, v, id, p[1], m == http.MethodPut)
	case n == 1 && p[0] == "audit" && m == http.MethodGet:
		h.clubAudit(w, r, v, id)
	case n == 2 && p[0] == "analytics" && p[1] == "matchups" && m == http.MethodGet:
		h.analyticsMatchups(w, r, v, id)
	case n == 2 && p[0] == "analytics" && p[1] == "contributors" && m == http.MethodGet:
		h.analyticsContributors(w, r, v, id)
	default:
		hsErr(w, http.StatusNotFound, "not found")
	}
}

// ---- guard (section 4.1) --------------------------------------------------------------------------------

type clubCtx struct {
	c       clubRow
	role    clubs.Role
	isOwner bool
}

// guard is the single choke point for every club-scoped endpoint. clubID must come from the URL path only.
// node == "" means "any member" (leave, own presentation prefs). A suspended club is read-only for members
// and invisible (404) to everyone else.
func (h *ClubsHandler) guard(w http.ResponseWriter, v clubViewer, clubID string, node clubs.Node) *clubCtx {
	if v.sub == "" {
		hsErr(w, http.StatusUnauthorized, "sign in with IDUNA first")
		return nil
	}
	c, err := loadClub(h.DB, "id", clubID)
	if err != nil || c.Deleted {
		hsErr(w, http.StatusNotFound, "not found")
		return nil
	}
	role, member := h.memberRole(h.DB, c.ID, v.sub)
	if !member {
		if c.Tier == 1 || c.Suspended {
			hsErr(w, http.StatusNotFound, "not found")
		} else {
			clubErr(w, http.StatusForbidden, "forbidden", "you are not a member of this club")
		}
		return nil
	}
	g := &clubCtx{c: c, role: role, isOwner: c.OwnerSub == v.sub}
	if node != "" {
		if !clubs.CanOwner(role, g.isOwner, node) {
			clubErr(w, http.StatusForbidden, "forbidden", "your role cannot do that")
			return nil
		}
		if c.Suspended && !clubReadNodes[node] {
			clubErr(w, http.StatusForbidden, "club_suspended", "this club is suspended and read-only")
			return nil
		}
	}
	return g
}

// denial maps a clubs.Denial onto an HTTP response.
func clubDenied(w http.ResponseWriter, d *clubs.Denial) {
	status := http.StatusForbidden
	switch d.Code {
	case clubs.CodeLastManager, clubs.CodeNoChange:
		status = http.StatusConflict
	case clubs.CodeBadRole:
		status = http.StatusUnprocessableEntity
	}
	clubErr(w, status, d.Code, d.Msg)
}

// joinChecks runs the membership-adding preconditions inside tx: not already a member, exclusivity in both
// directions (section 3.4; Secret clubs count), and the per-club member cap. target.ID may be "" (create).
func (h *ClubsHandler) joinChecks(q dbq, sub string, target clubRow) (status int, code, msg string) {
	if target.ID != "" {
		if _, ok := h.memberRole(q, target.ID, sub); ok {
			return http.StatusConflict, "already_member", "you are already a member of this club"
		}
	}
	var others, exclusiveOthers int
	_ = q.QueryRow(`SELECT COUNT(*), COALESCE(SUM(c.is_exclusive),0) FROM club_memberships m JOIN clubs c ON c.id=m.club_id
		WHERE m.sub=? AND c.deleted=0 AND m.club_id<>?`, sub, target.ID).Scan(&others, &exclusiveOthers)
	if (target.Exclusive && others > 0) || exclusiveOthers > 0 {
		return http.StatusConflict, "exclusive_conflict", "an exclusive club and any other club cannot both hold the same account"
	}
	if target.ID != "" {
		var members int
		_ = q.QueryRow(`SELECT COUNT(*) FROM club_memberships WHERE club_id=?`, target.ID).Scan(&members)
		if members >= h.setting(q, "members_per_club", clubDefMembersPerClub) {
			return http.StatusConflict, "club_full", "this club is full"
		}
	}
	return 0, "", ""
}

// ---- create / directory / mine ----------------------------------------------------------------------------

func (h *ClubsHandler) create(w http.ResponseWriter, r *http.Request, v clubViewer) {
	var in struct {
		Name        string `json:"name"`
		Slug        string `json:"slug"`
		Tagline     string `json:"tagline"`
		PrivacyTier *int   `json:"privacy_tier"`
		IsExclusive bool   `json:"is_exclusive"`
	}
	if hsDecode(r, &in) != nil {
		hsErr(w, http.StatusBadRequest, "bad json")
		return
	}
	// wotan.club.create is implicit for any authenticated subject that owns a handle (player accounts carry
	// no role-based permissions); the handle is the public name everywhere.
	if h.handleOf(h.DB, v.sub) == "" {
		clubErr(w, http.StatusConflict, "handle_required", "claim a handle first")
		return
	}
	// Identity gate (IDENTITY_NORTHSTAR section 5/7.1): a linked Battle.net identity, or the 30-day grace
	// window, or wotan.identity.bypass. Clubs carry competitive claims.
	if g := identityGate(h.DB, v.sub, v.perms); !g.Open() {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error": "link a Battle.net account to create a club", "code": "battlenet_required", "gate": gateJSON(g)})
		return
	}
	name, ok := hsText(in.Name, 3, 40)
	if !ok {
		hsErr(w, http.StatusUnprocessableEntity, "name must be 3-40 characters")
		return
	}
	tagline, ok := hsText(in.Tagline, 0, 100)
	if !ok {
		hsErr(w, http.StatusUnprocessableEntity, "tagline must be at most 100 characters")
		return
	}
	slug := strings.ToLower(strings.TrimSpace(in.Slug))
	if !clubSlugRe.MatchString(slug) || strings.Contains(slug, "--") {
		hsErr(w, http.StatusUnprocessableEntity, "slug must be 3-30 characters of a-z, 0-9 and hyphens")
		return
	}
	if clubReservedSlg[slug] {
		clubErr(w, http.StatusUnprocessableEntity, "slug_reserved", "that slug is reserved")
		return
	}
	tier := 1
	if in.PrivacyTier != nil {
		tier = *in.PrivacyTier
	}
	if tier < 1 || tier > 3 {
		clubErr(w, http.StatusUnprocessableEntity, "tier_forbidden", "privacy_tier must be 1 (secret), 2 (private) or 3 (social)")
		return
	}
	tx, err := h.begin(r)
	if err != nil {
		hsErr(w, 500, "database error")
		return
	}
	defer tx.rollback()
	var owned int
	_ = tx.tx.QueryRow(`SELECT COUNT(*) FROM clubs WHERE owner_sub=? AND deleted=0`, v.sub).Scan(&owned)
	if owned >= h.setting(tx.tx, "clubs_per_user", clubDefClubsPerUser) {
		clubErr(w, http.StatusForbidden, "club_limit", "you have reached the limit of clubs you can create")
		return
	}
	if st, code, msg := h.joinChecks(tx.tx, v.sub, clubRow{Exclusive: in.IsExclusive}); st != 0 {
		clubErr(w, st, code, msg)
		return
	}
	var taken int
	_ = tx.tx.QueryRow(`SELECT COUNT(*) FROM clubs WHERE slug=?`, slug).Scan(&taken)
	if taken > 0 {
		clubErr(w, http.StatusConflict, "slug_taken", "that slug is taken")
		return
	}
	id := uuid.NewString()
	now := clubNow()
	ex := 0
	if in.IsExclusive {
		ex = 1
	}
	if _, err := tx.tx.Exec(`INSERT INTO clubs (id, slug, name, tagline, privacy_tier, is_exclusive, owner_sub, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?)`, id, slug, name, tagline, tier, ex, v.sub, now, now); err != nil {
		hsErr(w, 500, "database error")
		return
	}
	if _, err := tx.tx.Exec(`INSERT INTO club_memberships (club_id, sub, role, joined_at) VALUES (?,?, 'manager', ?)`, id, v.sub, now); err != nil {
		hsErr(w, 500, "database error")
		return
	}
	tx.audit(id, v.sub, "create", "", map[string]any{"slug": slug, "privacy_tier": tier, "is_exclusive": in.IsExclusive}, false)
	if err := tx.commit(); err != nil {
		hsErr(w, 500, "database error")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "slug": slug})
}

func (h *ClubsHandler) directory(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, offset := hsPage(r, 24)
	where := []string{"c.deleted=0", "c.suspended=0", "c.privacy_tier IN (2,3)"} // never Secret
	args := []any{}
	if s := strings.TrimSpace(q.Get("q")); s != "" {
		if len([]rune(s)) > 60 {
			s = string([]rune(s)[:60])
		}
		where = append(where, `(c.name LIKE ? ESCAPE '\' OR c.slug LIKE ? ESCAPE '\' OR c.tagline LIKE ? ESCAPE '\')`)
		args = append(args, hsLike(s), hsLike(s), hsLike(s))
	}
	order := "prestige DESC, members DESC, c.created_at DESC"
	switch q.Get("sort") {
	case "new":
		order = "c.created_at DESC, c.rowid DESC"
	case "members":
		order = "members DESC, c.created_at DESC"
	}
	from := ` FROM clubs c WHERE ` + strings.Join(where, " AND ")
	var total int
	if err := h.DB.QueryRow(`SELECT COUNT(*)`+from, args...).Scan(&total); err != nil {
		hsErr(w, 500, "database error")
		return
	}
	rows, err := h.DB.Query(`SELECT c.id, c.slug, c.name, c.tagline, c.privacy_tier, c.is_exclusive, c.join_policy, c.brand_json, c.created_at,
		(SELECT COUNT(*) FROM club_memberships m WHERE m.club_id=c.id) AS members,
		(SELECT COUNT(*) FROM club_featured_decks f JOIN hs_decks d ON d.id=f.deck_id WHERE f.club_id=c.id AND d.deleted=0 AND d.private=0)
		 + COALESCE((SELECT SUM(d.likes) FROM club_featured_decks f JOIN hs_decks d ON d.id=f.deck_id WHERE f.club_id=c.id AND d.deleted=0 AND d.private=0),0) AS prestige`+
		from+` ORDER BY `+order+` LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	if err != nil {
		hsErr(w, 500, "database error")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, slug, name, tagline, jp, brand, created string
		var tier, ex, members, prestige int
		if rows.Scan(&id, &slug, &name, &tagline, &tier, &ex, &jp, &brand, &created, &members, &prestige) != nil {
			continue
		}
		it := map[string]any{"id": id, "slug": slug, "name": name, "tagline": tagline, "privacy_tier": tier, "tier": clubTier(tier),
			"is_exclusive": ex == 1, "brand": brandOf(brand), "created_at": hsTime(created), "member_count": members, "prestige": prestige}
		if tier == 3 {
			it["join_policy"] = jp
		}
		items = append(items, it)
	}
	hsList(w, total, limit, offset, items)
}

func (h *ClubsHandler) myClubs(w http.ResponseWriter, v clubViewer) {
	if v.sub == "" {
		hsErr(w, http.StatusUnauthorized, "sign in with IDUNA first")
		return
	}
	rows, err := h.DB.Query(`SELECT c.id, c.slug, c.name, c.privacy_tier, c.is_exclusive, c.suspended, m.role, m.public_visible,
		m.show_badge, m.joined_at, c.owner_sub, c.brand_json,
		(SELECT COUNT(*) FROM club_memberships x WHERE x.club_id=c.id)
		FROM club_memberships m JOIN clubs c ON c.id=m.club_id WHERE m.sub=? AND c.deleted=0 ORDER BY m.joined_at, c.slug`, v.sub)
	if err != nil {
		hsErr(w, 500, "database error")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, slug, name, role, joined, owner, brand string
		var tier, ex, sus, pv, sb, cnt int
		if rows.Scan(&id, &slug, &name, &tier, &ex, &sus, &role, &pv, &sb, &joined, &owner, &brand, &cnt) != nil {
			continue
		}
		items = append(items, map[string]any{"id": id, "slug": slug, "name": name, "privacy_tier": tier, "tier": clubTier(tier),
			"is_exclusive": ex == 1, "suspended": sus == 1, "role": role, "is_owner": owner == v.sub, "public_visible": pv == 1,
			"show_badge": sb == 1, "joined_at": hsTime(joined), "member_count": cnt, "brand": brandOf(brand)})
	}
	hsList(w, len(items), len(items), 0, items)
}

// userClubs: public badges (Private/Social, member-hideable) plus -- for a signed-in viewer -- clubs the
// viewer shares with the target (mutual workspaces, including Secret ones, since co-members already see
// each other on the roster wall).
func (h *ClubsHandler) userClubs(w http.ResponseWriter, v clubViewer, handle string) {
	target := h.subOfHandle(h.DB, handle)
	if target == "" {
		hsErr(w, http.StatusNotFound, "not found")
		return
	}
	rows, err := h.DB.Query(`SELECT c.id, c.slug, c.name, c.privacy_tier, m.role, m.show_badge, c.owner_sub, c.brand_json,
		EXISTS(SELECT 1 FROM club_memberships me WHERE me.club_id=c.id AND me.sub=?)
		FROM club_memberships m JOIN clubs c ON c.id=m.club_id
		WHERE m.sub=? AND c.deleted=0 AND c.suspended=0 ORDER BY c.name`, v.sub, target)
	if err != nil {
		hsErr(w, 500, "database error")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, slug, name, role, owner, brand string
		var tier, badge, mutual int
		if rows.Scan(&id, &slug, &name, &tier, &role, &badge, &owner, &brand, &mutual) != nil {
			continue
		}
		pub := tier >= 2 && badge == 1
		if !pub && !(v.sub != "" && mutual == 1) {
			continue
		}
		items = append(items, map[string]any{"id": id, "slug": slug, "name": name, "privacy_tier": tier, "tier": clubTier(tier),
			"role": role, "is_owner": owner == target, "brand": brandOf(brand), "mutual": v.sub != "" && mutual == 1})
	}
	hsList(w, len(items), len(items), 0, items)
}

// ---- public, tier-shaped views (allow-list projection) ----------------------------------------------------

// publicClubRef resolves a slug for a public view: Secret/suspended/deleted are 404 to non-members. The SELECT
// lists only allow-listed columns (plus the filter columns); internal columns are never fetched.
type clubPublic struct {
	id, slug, name, tagline, description, joinPolicy, brand, created string
	tier                                                             int
	exclusive, showContrib, suspended                                bool
	role                                                             clubs.Role
	member                                                           bool
	ownerSub                                                         string
}

func (h *ClubsHandler) resolvePublic(w http.ResponseWriter, v clubViewer, slug string) *clubPublic {
	var c clubPublic
	var ex, sc, sus int
	err := h.DB.QueryRow(`SELECT id, slug, name, tagline, description, privacy_tier, join_policy, show_contributors,
		is_exclusive, brand_json, created_at, suspended, owner_sub FROM clubs WHERE slug=? AND deleted=0`, strings.ToLower(slug)).
		Scan(&c.id, &c.slug, &c.name, &c.tagline, &c.description, &c.tier, &c.joinPolicy, &sc, &ex, &c.brand, &c.created, &sus, &c.ownerSub)
	if err != nil {
		hsErr(w, http.StatusNotFound, "not found")
		return nil
	}
	c.exclusive, c.showContrib, c.suspended = ex == 1, sc == 1, sus == 1
	if v.sub != "" {
		c.role, c.member = h.memberRole(h.DB, c.id, v.sub)
	}
	if !c.member && (c.tier == 1 || c.suspended) {
		hsErr(w, http.StatusNotFound, "not found")
		return nil
	}
	return &c
}

func (h *ClubsHandler) bySlug(w http.ResponseWriter, r *http.Request, v clubViewer, p []string) {
	slug := p[0]
	m := r.Method
	switch {
	case len(p) == 1 && m == http.MethodGet:
		h.publicView(w, v, slug)
	case len(p) == 2 && p[1] == "roster" && m == http.MethodGet:
		h.publicRoster(w, r, v, slug)
	case len(p) == 2 && p[1] == "decks" && m == http.MethodGet:
		h.publicDecks(w, v, slug)
	case len(p) == 2 && p[1] == "contributors" && m == http.MethodGet:
		h.publicContributors(w, r, v, slug)
	case len(p) == 2 && p[1] == "requests" && m == http.MethodPost:
		h.createRequest(w, r, v, slug)
	default:
		hsErr(w, http.StatusNotFound, "not found")
	}
}

func (h *ClubsHandler) publicView(w http.ResponseWriter, v clubViewer, slug string) {
	c := h.resolvePublic(w, v, slug)
	if c == nil {
		return
	}
	var members int
	_ = h.DB.QueryRow(`SELECT COUNT(*) FROM club_memberships WHERE club_id=?`, c.id).Scan(&members)
	out := map[string]any{
		"id": c.id, "slug": c.slug, "name": c.name, "tagline": c.tagline, "description": c.description,
		"privacy_tier": c.tier, "tier": clubTier(c.tier), "is_exclusive": c.exclusive, "brand": brandOf(c.brand),
		"created_at": hsTime(c.created), "member_count": members,
	}
	if c.tier == 3 || c.member {
		out["join_policy"] = c.joinPolicy
		out["show_contributors"] = c.showContrib
	}
	if c.tier == 3 || c.member {
		out["featured_decks"] = h.featuredDecks(c.id)
	}
	view := map[string]any{"member": c.member, "role": string(c.role), "is_owner": c.member && c.ownerSub == v.sub, "perms": []string{}}
	if c.member {
		view["perms"] = clubs.Perms(c.role, c.ownerSub == v.sub)
		out["suspended"] = c.suspended
		if c.role == clubs.RoleManager {
			var pt sql.NullInt64
			_ = h.DB.QueryRow(`SELECT prev_tier FROM clubs WHERE id=?`, c.id).Scan(&pt)
			if pt.Valid {
				out["prev_tier"] = pt.Int64
			}
		}
	} else if v.sub != "" {
		var pending int
		_ = h.DB.QueryRow(`SELECT COUNT(*) FROM club_join_requests WHERE club_id=? AND sub=? AND status='pending'`, c.id, v.sub).Scan(&pending)
		view["pending_request"] = pending > 0
	}
	out["viewer"] = view
	writeJSON(w, http.StatusOK, out)
}

func (h *ClubsHandler) publicRoster(w http.ResponseWriter, r *http.Request, v clubViewer, slug string) {
	c := h.resolvePublic(w, v, slug)
	if c == nil {
		return
	}
	limit, offset := hsPage(r, 100)
	var total int
	_ = h.DB.QueryRow(`SELECT COUNT(*) FROM club_memberships m JOIN hs_profiles p ON p.sub=m.sub WHERE m.club_id=? AND m.public_visible=1`, c.id).Scan(&total)
	rows, err := h.DB.Query(`SELECT p.handle, m.role, m.sub=?, m.joined_at, COALESCE(k.legend_rank,0), COALESCE(k.rank_verified,0), COALESCE(k.region,'')
		FROM club_memberships m JOIN hs_profiles p ON p.sub=m.sub LEFT JOIN hs_player_rank k ON k.sub=m.sub
		WHERE m.club_id=? AND m.public_visible=1
		ORDER BY CASE m.role WHEN 'manager' THEN 0 WHEN 'coach' THEN 1 ELSE 2 END, p.handle LIMIT ? OFFSET ?`, c.ownerSub, c.id, limit, offset)
	if err != nil {
		hsErr(w, 500, "database error")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var handle, role, joined, region string
		var owner bool
		var lr, rv int
		if rows.Scan(&handle, &role, &owner, &joined, &lr, &rv, &region) != nil {
			continue
		}
		it := map[string]any{"handle": handle, "role": role, "is_owner": owner, "joined_at": hsTime(joined)}
		if lr > 0 {
			it["rank"] = map[string]any{"legend_rank": lr, "verified": rv == 1, "rank_verified": rv == 1, "region": region}
		}
		items = append(items, it)
	}
	hsList(w, total, limit, offset, items)
}

func (h *ClubsHandler) publicDecks(w http.ResponseWriter, v clubViewer, slug string) {
	c := h.resolvePublic(w, v, slug)
	if c == nil {
		return
	}
	if c.tier != 3 && !c.member {
		clubErr(w, http.StatusForbidden, "tier_forbidden", "this club does not share decks publicly")
		return
	}
	items := h.featuredDecks(c.id)
	hsList(w, len(items), len(items), 0, items)
}

// featuredDecks lists a club's featured decks; only public, live decks are ever returned. claimed_by is the
// first publisher of that canonical deckstring (R8): "first to publish", not proof of invention.
func (h *ClubsHandler) featuredDecks(clubID string) []map[string]any {
	items := []map[string]any{}
	rows, err := h.DB.Query(`SELECT d.id, d.title, d.class, d.format, d.deckstring, d.card_count, COALESCE(p.handle,''), d.likes,
		COALESCE((SELECT p2.handle FROM hs_decks d2 JOIN hs_profiles p2 ON p2.sub=d2.owner_sub
		          WHERE d2.deckstring=d.deckstring AND d2.deleted=0 AND d2.private=0 ORDER BY d2.created_at ASC, d2.id ASC LIMIT 1),'')
		FROM club_featured_decks f JOIN hs_decks d ON d.id=f.deck_id LEFT JOIN hs_profiles p ON p.sub=d.owner_sub
		WHERE f.club_id=? AND d.deleted=0 AND d.private=0 AND d.merged_into=0 ORDER BY f.created_at DESC, d.id DESC`, clubID)
	if err != nil {
		return items
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var title, class, format, code, author, claimed string
		var cc, likes int
		if rows.Scan(&id, &title, &class, &format, &code, &cc, &author, &likes, &claimed) != nil {
			continue
		}
		items = append(items, map[string]any{"id": id, "title": title, "class": class, "format": format, "deckstring": code,
			"card_count": cc, "author": map[string]string{"handle": author}, "likes": likes, "claimed_by": claimed})
	}
	return items
}

// ---- club basics: me, token, settings, visibility, transfer, delete --------------------------------------

func (h *ClubsHandler) clubMe(w http.ResponseWriter, v clubViewer, id string) {
	g := h.guard(w, v, id, clubs.NodeView)
	if g == nil {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"role": string(g.role), "is_owner": g.isOwner, "perms": clubs.Perms(g.role, g.isOwner),
		"club": map[string]any{"id": g.c.ID, "slug": g.c.Slug, "name": g.c.Name, "privacy_tier": g.c.Tier,
			"tier": clubTier(g.c.Tier), "suspended": g.c.Suspended},
	})
}

// clubToken mints the short-lived club-context JWT for external verifiers (section 4.3). WOTAN's own API
// never accepts it as identity (viewer() rejects aud wotan-club); membership is always re-read.
func (h *ClubsHandler) clubToken(w http.ResponseWriter, v clubViewer, id string) {
	g := h.guard(w, v, id, clubs.NodeView)
	if g == nil {
		return
	}
	now := time.Now().UTC()
	perms := clubs.Perms(g.role, g.isOwner)
	tok, err := jwt.Sign(h.Keys, map[string]any{
		"sub": v.sub, "aud": clubTokenAud, "iss": "iduna", "iat": now.Unix(), "exp": now.Add(clubTokenTTL).Unix(),
		"club_id": g.c.ID, "club_role": string(g.role), "club_owner": g.isOwner, "permissions": perms,
	})
	if err != nil {
		hsErr(w, http.StatusInternalServerError, "could not mint token")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": tok, "expires_in": int(clubTokenTTL.Seconds()),
		"club_id": g.c.ID, "club_role": string(g.role), "permissions": perms})
}

func (h *ClubsHandler) patchSettings(w http.ResponseWriter, r *http.Request, v clubViewer, id string) {
	g := h.guard(w, v, id, clubs.NodeSettingsEdit)
	if g == nil {
		return
	}
	var in struct {
		Name        *string `json:"name"`
		Tagline     *string `json:"tagline"`
		Description *string `json:"description"`
		Brand       *struct {
			Accent     *string `json:"accent"`
			BannerText *string `json:"banner_text"`
		} `json:"brand"`
	}
	if hsDecode(r, &in) != nil {
		hsErr(w, http.StatusBadRequest, "bad json")
		return
	}
	name, tagline, desc := g.c.Name, g.c.Tagline, g.c.Description
	brand := brandOf(g.c.BrandJSON)
	var ok bool
	changed := []string{}
	if in.Name != nil {
		if name, ok = hsText(*in.Name, 3, 40); !ok {
			hsErr(w, http.StatusUnprocessableEntity, "name must be 3-40 characters")
			return
		}
		changed = append(changed, "name")
	}
	if in.Tagline != nil {
		if tagline, ok = hsText(*in.Tagline, 0, 100); !ok {
			hsErr(w, http.StatusUnprocessableEntity, "tagline must be at most 100 characters")
			return
		}
		changed = append(changed, "tagline")
	}
	if in.Description != nil {
		if desc, ok = hsText(*in.Description, 0, 2000); !ok {
			hsErr(w, http.StatusUnprocessableEntity, "description must be at most 2000 characters")
			return
		}
		changed = append(changed, "description")
	}
	if in.Brand != nil {
		if in.Brand.Accent != nil {
			if *in.Brand.Accent != "" && !clubAccentRe.MatchString(*in.Brand.Accent) {
				hsErr(w, http.StatusUnprocessableEntity, "accent must be a #rrggbb color")
				return
			}
			brand.Accent = *in.Brand.Accent
		}
		if in.Brand.BannerText != nil {
			if brand.BannerText, ok = hsText(*in.Brand.BannerText, 0, 80); !ok {
				hsErr(w, http.StatusUnprocessableEntity, "banner_text must be at most 80 characters")
				return
			}
		}
		changed = append(changed, "brand")
	}
	rawBrand, _ := json.Marshal(brand)
	tx, err := h.begin(r)
	if err != nil {
		hsErr(w, 500, "database error")
		return
	}
	defer tx.rollback()
	if role, member := h.memberRole(tx.tx, g.c.ID, v.sub); !member || !clubs.Can(role, clubs.NodeSettingsEdit) {
		clubErr(w, http.StatusForbidden, "forbidden", "your role cannot do that")
		return
	}
	if _, err := tx.tx.Exec(`UPDATE clubs SET name=?, tagline=?, description=?, brand_json=?, updated_at=? WHERE id=?`,
		name, tagline, desc, string(rawBrand), clubNow(), g.c.ID); err != nil {
		hsErr(w, 500, "database error")
		return
	}
	tx.audit(g.c.ID, v.sub, "settings.edit", "", map[string]any{"fields": changed}, false)
	if err := tx.commit(); err != nil {
		hsErr(w, 500, "database error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": g.c.ID, "slug": g.c.Slug, "name": name, "tagline": tagline,
		"description": desc, "brand": brand})
}

func (h *ClubsHandler) putVisibility(w http.ResponseWriter, r *http.Request, v clubViewer, id string) {
	g := h.guard(w, v, id, clubs.NodeVisibilitySet)
	if g == nil {
		return
	}
	var in struct {
		PrivacyTier      *int    `json:"privacy_tier"`
		IsExclusive      *bool   `json:"is_exclusive"`
		JoinPolicy       *string `json:"join_policy"`
		ShowContributors *bool   `json:"show_contributors"`
		Reveal           bool    `json:"reveal"` // restore prev_tier in one click (section 3.2)
	}
	if hsDecode(r, &in) != nil {
		hsErr(w, http.StatusBadRequest, "bad json")
		return
	}
	tx, err := h.begin(r)
	if err != nil {
		hsErr(w, 500, "database error")
		return
	}
	defer tx.rollback()
	role, member := h.memberRole(tx.tx, g.c.ID, v.sub)
	if !member || !clubs.Can(role, clubs.NodeVisibilitySet) {
		clubErr(w, http.StatusForbidden, "forbidden", "your role cannot do that")
		return
	}
	c, err := loadClub(tx.tx, "id", g.c.ID) // re-read inside the transaction
	if err != nil || c.Deleted {
		hsErr(w, http.StatusNotFound, "not found")
		return
	}
	meta := map[string]any{}
	tier, prev, excl, jp, sc := c.Tier, c.PrevTier, c.Exclusive, c.JoinPolicy, c.ShowContrib
	newTier := tier
	if in.Reveal {
		if tier == 1 {
			newTier = 2
			if prev.Valid && (prev.Int64 == 2 || prev.Int64 == 3) {
				newTier = int(prev.Int64)
			}
		}
	} else if in.PrivacyTier != nil {
		newTier = *in.PrivacyTier
		if newTier < 1 || newTier > 3 {
			clubErr(w, http.StatusUnprocessableEntity, "tier_forbidden", "privacy_tier must be 1, 2 or 3")
			return
		}
	}
	if newTier != tier {
		if newTier == 1 {
			prev = sql.NullInt64{Int64: int64(tier), Valid: true} // remember where to Reveal back to
		}
		meta["privacy_tier"] = map[string]any{"from": tier, "to": newTier}
		tier = newTier
	}
	if in.JoinPolicy != nil {
		if *in.JoinPolicy != "invite" && *in.JoinPolicy != "request" {
			hsErr(w, http.StatusUnprocessableEntity, "join_policy must be invite or request")
			return
		}
		if *in.JoinPolicy != jp {
			meta["join_policy"] = map[string]any{"from": jp, "to": *in.JoinPolicy}
		}
		jp = *in.JoinPolicy
	}
	if jp == "request" && tier != 3 {
		if in.JoinPolicy != nil && *in.JoinPolicy == "request" {
			clubErr(w, http.StatusUnprocessableEntity, "tier_forbidden", "only Social clubs can take join requests")
			return
		}
		jp = "invite" // tier moved away from Social: requests no longer apply
		meta["join_policy"] = map[string]any{"from": "request", "to": "invite"}
	}
	if in.ShowContributors != nil && *in.ShowContributors != sc {
		meta["show_contributors"] = map[string]any{"from": sc, "to": *in.ShowContributors}
		sc = *in.ShowContributors
	}
	if in.IsExclusive != nil && *in.IsExclusive != excl {
		if *in.IsExclusive {
			conflicts, ok := h.exclusiveConflicts(tx.tx, c.ID)
			if !ok {
				hsErr(w, 500, "database error")
				return
			}
			if len(conflicts) > 0 {
				writeJSON(w, http.StatusConflict, map[string]any{"error": "some members also belong to other clubs",
					"code": "exclusive_conflict", "members": conflicts})
				return
			}
		}
		meta["is_exclusive"] = map[string]any{"from": excl, "to": *in.IsExclusive}
		excl = *in.IsExclusive
	}
	if len(meta) > 0 {
		b2i := func(b bool) int {
			if b {
				return 1
			}
			return 0
		}
		if _, err := tx.tx.Exec(`UPDATE clubs SET privacy_tier=?, prev_tier=?, is_exclusive=?, join_policy=?, show_contributors=?, updated_at=? WHERE id=?`,
			tier, prev, b2i(excl), jp, b2i(sc), clubNow(), c.ID); err != nil {
			hsErr(w, 500, "database error")
			return
		}
		if jp != "request" {
			_, _ = tx.tx.Exec(`UPDATE club_join_requests SET status='cancelled', decided_at=? WHERE club_id=? AND status='pending'`, clubNow(), c.ID)
		}
		tx.audit(c.ID, v.sub, "visibility.set", "", meta, false)
	}
	if err := tx.commit(); err != nil {
		hsErr(w, 500, "database error")
		return
	}
	out := map[string]any{"privacy_tier": tier, "tier": clubTier(tier), "is_exclusive": excl, "join_policy": jp, "show_contributors": sc}
	if prev.Valid {
		out["prev_tier"] = prev.Int64
	}
	writeJSON(w, http.StatusOK, out)
}

// exclusiveConflicts lists this club's members who also belong to another live club (section 3.4).
func (h *ClubsHandler) exclusiveConflicts(q dbq, clubID string) ([]map[string]string, bool) {
	rows, err := q.Query(`SELECT m.sub, COALESCE(p.handle,'') FROM club_memberships m LEFT JOIN hs_profiles p ON p.sub=m.sub
		WHERE m.club_id=? AND EXISTS(SELECT 1 FROM club_memberships m2 JOIN clubs c2 ON c2.id=m2.club_id
		  WHERE m2.sub=m.sub AND m2.club_id<>m.club_id AND c2.deleted=0) ORDER BY p.handle`, clubID)
	if err != nil {
		return nil, false
	}
	defer rows.Close()
	out := []map[string]string{}
	for rows.Next() {
		var sub, handle string
		if rows.Scan(&sub, &handle) == nil {
			out = append(out, map[string]string{"sub": sub, "handle": handle})
		}
	}
	return out, true
}

func (h *ClubsHandler) transfer(w http.ResponseWriter, r *http.Request, v clubViewer, id string) {
	g := h.guard(w, v, id, clubs.NodeOwnerTransfer)
	if g == nil {
		return
	}
	var in struct {
		ToSub string `json:"to_sub"`
	}
	if hsDecode(r, &in) != nil || in.ToSub == "" {
		hsErr(w, http.StatusBadRequest, "to_sub required")
		return
	}
	if in.ToSub == v.sub {
		clubErr(w, http.StatusUnprocessableEntity, "self_change", "you already own this club")
		return
	}
	tx, err := h.begin(r)
	if err != nil {
		hsErr(w, 500, "database error")
		return
	}
	defer tx.rollback()
	c, err := loadClub(tx.tx, "id", g.c.ID)
	if err != nil || c.Deleted || c.OwnerSub != v.sub {
		clubErr(w, http.StatusForbidden, "forbidden", "only the owner can transfer ownership")
		return
	}
	trole, tmember := h.memberRole(tx.tx, c.ID, in.ToSub)
	if !tmember {
		hsErr(w, http.StatusNotFound, "that person is not a member of this club")
		return
	}
	if trole != clubs.RoleManager {
		clubErr(w, http.StatusUnprocessableEntity, "target_not_manager", "ownership can only go to another manager")
		return
	}
	if _, err := tx.tx.Exec(`UPDATE clubs SET owner_sub=?, updated_at=? WHERE id=?`, in.ToSub, clubNow(), c.ID); err != nil {
		hsErr(w, 500, "database error")
		return
	}
	tx.audit(c.ID, v.sub, "owner.transfer", in.ToSub, map[string]any{"from": v.sub, "to": in.ToSub}, false)
	if err := tx.commit(); err != nil {
		hsErr(w, 500, "database error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "owner_sub": in.ToSub})
}

// softDeleteClub is shared by the owner delete and the admin delete: soft-deletes the club and kills its
// pending invites and join requests. Memberships are kept for the audit trail; every membership lookup that
// matters joins clubs with deleted=0.
func softDeleteClub(t *clubTx, clubID string) {
	if t.err != nil {
		return
	}
	now := clubNow()
	_, t.err = t.tx.Exec(`UPDATE clubs SET deleted=1, updated_at=? WHERE id=?`, now, clubID)
	if t.err == nil {
		_, t.err = t.tx.Exec(`UPDATE club_invites SET revoked_at=? WHERE club_id=? AND used_at IS NULL AND revoked_at IS NULL`, now, clubID)
	}
	if t.err == nil {
		_, t.err = t.tx.Exec(`UPDATE club_join_requests SET status='cancelled', decided_at=? WHERE club_id=? AND status='pending'`, now, clubID)
	}
}

func (h *ClubsHandler) deleteClub(w http.ResponseWriter, r *http.Request, v clubViewer, id string) {
	g := h.guard(w, v, id, clubs.NodeDelete)
	if g == nil {
		return
	}
	tx, err := h.begin(r)
	if err != nil {
		hsErr(w, 500, "database error")
		return
	}
	defer tx.rollback()
	c, err := loadClub(tx.tx, "id", g.c.ID)
	if err != nil || c.Deleted || c.OwnerSub != v.sub {
		clubErr(w, http.StatusForbidden, "forbidden", "only the owner can delete the club")
		return
	}
	softDeleteClub(tx, c.ID)
	tx.audit(c.ID, v.sub, "delete", "", map[string]any{"slug": c.Slug}, false)
	if err := tx.commit(); err != nil {
		hsErr(w, 500, "database error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---- members ----------------------------------------------------------------------------------------------

func (h *ClubsHandler) listMembers(w http.ResponseWriter, r *http.Request, v clubViewer, id string) {
	g := h.guard(w, v, id, clubs.NodeRosterView)
	if g == nil {
		return
	}
	limit, offset := hsPage(r, 100)
	var total int
	_ = h.DB.QueryRow(`SELECT COUNT(*) FROM club_memberships WHERE club_id=?`, g.c.ID).Scan(&total)
	mgrs := h.managerCount(h.DB, g.c.ID)
	rows, err := h.DB.Query(`SELECT m.sub, COALESCE(p.handle,''), m.role, m.public_visible, m.show_badge, m.joined_at,
		COALESCE(k.battletag,''), COALESCE(k.battletag_verified,0), COALESCE(k.legend_rank,0), COALESCE(k.rank_verified,0), COALESCE(k.region,'')
		FROM club_memberships m LEFT JOIN hs_profiles p ON p.sub=m.sub LEFT JOIN hs_player_rank k ON k.sub=m.sub
		WHERE m.club_id=? ORDER BY CASE m.role WHEN 'manager' THEN 0 WHEN 'coach' THEN 1 ELSE 2 END, p.handle LIMIT ? OFFSET ?`,
		g.c.ID, limit, offset)
	if err != nil {
		hsErr(w, 500, "database error")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var sub, handle, role, joined, bt, region string
		var pv, sb, btv, lr, rv int
		if rows.Scan(&sub, &handle, &role, &pv, &sb, &joined, &bt, &btv, &lr, &rv, &region) != nil {
			continue
		}
		trole, tOwner, self := clubs.Role(role), sub == g.c.OwnerSub, sub == v.sub
		canRemove := clubs.CanActOn(g.role, g.isOwner, trole, tOwner,
			clubs.Action{Kind: clubs.KindRemove, Self: self, ManagerCount: mgrs}) == nil && !self
		setRoles := []string{}
		for _, nr := range []clubs.Role{clubs.RolePlayer, clubs.RoleCoach, clubs.RoleManager} {
			if clubs.CanActOn(g.role, g.isOwner, trole, tOwner,
				clubs.Action{Kind: clubs.KindChangeRole, NewRole: nr, Self: self, ManagerCount: mgrs}) == nil {
				setRoles = append(setRoles, string(nr))
			}
		}
		it := map[string]any{"sub": sub, "handle": handle, "role": role, "is_owner": tOwner, "is_me": self,
			"public_visible": pv == 1, "show_badge": sb == 1, "joined_at": hsTime(joined),
			"can_remove": canRemove, "can_set_roles": setRoles}
		if bt != "" {
			it["battletag"] = bt
			it["battletag_verified"] = btv == 1
		}
		if lr > 0 {
			// the web client reads battletag/flags from this object whenever it exists, so repeat them here
			it["rank"] = map[string]any{"legend_rank": lr, "verified": rv == 1, "rank_verified": rv == 1, "region": region,
				"battletag": bt, "battletag_verified": btv == 1}
		}
		items = append(items, it)
	}
	hsList(w, total, limit, offset, items)
}

func (h *ClubsHandler) changeRole(w http.ResponseWriter, r *http.Request, v clubViewer, id, target string) {
	g := h.guard(w, v, id, "")
	if g == nil {
		return
	}
	var in struct {
		Role string `json:"role"`
	}
	if hsDecode(r, &in) != nil {
		hsErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if g.c.Suspended {
		clubErr(w, http.StatusForbidden, "club_suspended", "this club is suspended and read-only")
		return
	}
	tx, err := h.begin(r)
	if err != nil {
		hsErr(w, 500, "database error")
		return
	}
	defer tx.rollback()
	c, err := loadClub(tx.tx, "id", g.c.ID)
	if err != nil || c.Deleted {
		hsErr(w, http.StatusNotFound, "not found")
		return
	}
	arole, amember := h.memberRole(tx.tx, c.ID, v.sub) // re-read the actor's CURRENT role
	if !amember {
		clubErr(w, http.StatusForbidden, "forbidden", "you are not a member of this club")
		return
	}
	trole, tmember := h.memberRole(tx.tx, c.ID, target)
	if !tmember {
		hsErr(w, http.StatusNotFound, "not found")
		return
	}
	d := clubs.CanActOn(arole, c.OwnerSub == v.sub, trole, c.OwnerSub == target, clubs.Action{
		Kind: clubs.KindChangeRole, NewRole: clubs.Role(in.Role), Self: target == v.sub, ManagerCount: h.managerCount(tx.tx, c.ID)})
	if d != nil {
		clubDenied(w, d)
		return
	}
	if _, err := tx.tx.Exec(`UPDATE club_memberships SET role=? WHERE club_id=? AND sub=?`, in.Role, c.ID, target); err != nil {
		hsErr(w, 500, "database error")
		return
	}
	tx.audit(c.ID, v.sub, "member.role", target, map[string]any{"from": string(trole), "to": in.Role}, false)
	if err := tx.commit(); err != nil {
		hsErr(w, 500, "database error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sub": target, "role": in.Role})
}

func (h *ClubsHandler) removeMember(w http.ResponseWriter, r *http.Request, v clubViewer, id, target string) {
	g := h.guard(w, v, id, "")
	if g == nil {
		return
	}
	if g.c.Suspended && target != v.sub {
		clubErr(w, http.StatusForbidden, "club_suspended", "this club is suspended and read-only")
		return
	}
	h.removeCore(w, r, v, g.c.ID, target)
}

func (h *ClubsHandler) leave(w http.ResponseWriter, r *http.Request, v clubViewer, id string) {
	g := h.guard(w, v, id, "")
	if g == nil {
		return
	}
	h.removeCore(w, r, v, g.c.ID, v.sub)
}

// removeCore removes target from the club inside one transaction that re-reads the actor's current role.
func (h *ClubsHandler) removeCore(w http.ResponseWriter, r *http.Request, v clubViewer, clubID, target string) {
	tx, err := h.begin(r)
	if err != nil {
		hsErr(w, 500, "database error")
		return
	}
	defer tx.rollback()
	c, err := loadClub(tx.tx, "id", clubID)
	if err != nil || c.Deleted {
		hsErr(w, http.StatusNotFound, "not found")
		return
	}
	arole, amember := h.memberRole(tx.tx, c.ID, v.sub)
	if !amember {
		clubErr(w, http.StatusForbidden, "forbidden", "you are not a member of this club")
		return
	}
	trole, tmember := h.memberRole(tx.tx, c.ID, target)
	if !tmember {
		hsErr(w, http.StatusNotFound, "not found")
		return
	}
	self := target == v.sub
	act := clubs.Action{Kind: clubs.KindRemove, Self: self, ManagerCount: h.managerCount(tx.tx, c.ID)}
	if self {
		act.Kind = clubs.KindLeave
	}
	if d := clubs.CanActOn(arole, c.OwnerSub == v.sub, trole, c.OwnerSub == target, act); d != nil {
		clubDenied(w, d)
		return
	}
	if _, err := tx.tx.Exec(`DELETE FROM club_memberships WHERE club_id=? AND sub=?`, c.ID, target); err != nil {
		hsErr(w, 500, "database error")
		return
	}
	// Invites this person minted die with their membership; their own pending request is moot.
	_, _ = tx.tx.Exec(`UPDATE club_invites SET revoked_at=? WHERE club_id=? AND created_by=? AND used_at IS NULL AND revoked_at IS NULL`, clubNow(), c.ID, target)
	action := "member.remove"
	if self {
		action = "member.leave"
	}
	tx.audit(c.ID, v.sub, action, target, map[string]any{"role": string(trole)}, false)
	if err := tx.commit(); err != nil {
		hsErr(w, 500, "database error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *ClubsHandler) patchMyPrefs(w http.ResponseWriter, r *http.Request, v clubViewer, id string) {
	g := h.guard(w, v, id, "")
	if g == nil {
		return
	}
	var in struct {
		PublicVisible *bool `json:"public_visible"`
		ShowBadge     *bool `json:"show_badge"`
	}
	if hsDecode(r, &in) != nil {
		hsErr(w, http.StatusBadRequest, "bad json")
		return
	}
	b2i := func(b bool) int {
		if b {
			return 1
		}
		return 0
	}
	tx, err := h.begin(r)
	if err != nil {
		hsErr(w, 500, "database error")
		return
	}
	defer tx.rollback()
	if _, ok := h.memberRole(tx.tx, g.c.ID, v.sub); !ok {
		clubErr(w, http.StatusForbidden, "forbidden", "you are not a member of this club")
		return
	}
	meta := map[string]any{}
	if in.PublicVisible != nil {
		_, tx.err = tx.tx.Exec(`UPDATE club_memberships SET public_visible=? WHERE club_id=? AND sub=?`, b2i(*in.PublicVisible), g.c.ID, v.sub)
		meta["public_visible"] = *in.PublicVisible
	}
	if in.ShowBadge != nil && tx.err == nil {
		_, tx.err = tx.tx.Exec(`UPDATE club_memberships SET show_badge=? WHERE club_id=? AND sub=?`, b2i(*in.ShowBadge), g.c.ID, v.sub)
		meta["show_badge"] = *in.ShowBadge
	}
	if len(meta) > 0 {
		tx.audit(g.c.ID, v.sub, "member.prefs", v.sub, meta, false)
	}
	if err := tx.commit(); err != nil {
		hsErr(w, 500, "database error")
		return
	}
	var pv, sb int
	_ = h.DB.QueryRow(`SELECT public_visible, show_badge FROM club_memberships WHERE club_id=? AND sub=?`, g.c.ID, v.sub).Scan(&pv, &sb)
	writeJSON(w, http.StatusOK, map[string]any{"public_visible": pv == 1, "show_badge": sb == 1})
}

// ---- invites / join ---------------------------------------------------------------------------------------

func clubToken() (token, hash string, err error) {
	b := make([]byte, 32) // 256 bits
	if _, err = rand.Read(b); err != nil {
		return
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, clubHash(token), nil
}

func (h *ClubsHandler) createInvite(w http.ResponseWriter, r *http.Request, v clubViewer, id string) {
	g := h.guard(w, v, id, "")
	if g == nil {
		return
	}
	var in struct {
		Role        string `json:"role"`
		TTLHours    int    `json:"ttl_hours"`
		BoundHandle string `json:"bound_handle"`
	}
	if clubDecodeOpt(r, &in) != nil {
		hsErr(w, http.StatusBadRequest, "bad json")
		return
	}
	if g.c.Suspended {
		clubErr(w, http.StatusForbidden, "club_suspended", "this club is suspended and read-only")
		return
	}
	role := clubs.Role(in.Role)
	if in.Role == "" {
		role = clubs.RolePlayer
	}
	tx, err := h.begin(r)
	if err != nil {
		hsErr(w, 500, "database error")
		return
	}
	defer tx.rollback()
	arole, amember := h.memberRole(tx.tx, g.c.ID, v.sub)
	if !amember {
		clubErr(w, http.StatusForbidden, "forbidden", "you are not a member of this club")
		return
	}
	if d := clubs.CanActOn(arole, g.c.OwnerSub == v.sub, "", false, clubs.Action{Kind: clubs.KindInvite, NewRole: role}); d != nil {
		clubDenied(w, d)
		return
	}
	ceil := h.setting(tx.tx, "invite_ttl_max_hours", clubDefInviteTTLMax)
	if ceil > clubDefInviteTTLMax {
		ceil = clubDefInviteTTLMax
	}
	ttl := in.TTLHours
	if ttl <= 0 {
		ttl = 48
	}
	if ttl > ceil {
		ttl = ceil
	}
	var pending int
	_ = tx.tx.QueryRow(`SELECT COUNT(*) FROM club_invites WHERE club_id=? AND used_at IS NULL AND revoked_at IS NULL AND expires_at>?`,
		g.c.ID, clubNow()).Scan(&pending)
	if pending >= h.setting(tx.tx, "pending_invites_per_club", clubDefPendingInvites) {
		clubErr(w, http.StatusConflict, "invite_cap", "too many pending invites; revoke some first")
		return
	}
	bound := ""
	if in.BoundHandle != "" {
		if bound = h.subOfHandle(tx.tx, in.BoundHandle); bound == "" {
			clubErr(w, http.StatusUnprocessableEntity, "unknown_handle", "no player has that handle")
			return
		}
	}
	token, hash, err := clubToken()
	if err != nil {
		hsErr(w, 500, "could not mint invite")
		return
	}
	iid := uuid.NewString()
	exp := time.Now().UTC().Add(time.Duration(ttl) * time.Hour).Format(clubSQLTime)
	if _, err := tx.tx.Exec(`INSERT INTO club_invites (id, club_id, token_hash, role, created_by, bound_sub, expires_at, created_at)
		VALUES (?,?,?,?,?,?,?,?)`, iid, g.c.ID, hash, string(role), v.sub, bound, exp, clubNow()); err != nil {
		hsErr(w, 500, "database error")
		return
	}
	tx.audit(g.c.ID, v.sub, "invite.create", bound, map[string]any{"invite_id": iid, "role": string(role), "ttl_hours": ttl}, false)
	if err := tx.commit(); err != nil {
		hsErr(w, 500, "database error")
		return
	}
	// The plaintext token exists only in this response; only its sha256 is stored.
	writeJSON(w, http.StatusCreated, map[string]any{"id": iid, "url": clubInviteURLBase + token, "role": string(role),
		"expires_at": hsTime(exp), "bound_handle": strings.ToLower(strings.TrimSpace(in.BoundHandle))})
}

func (h *ClubsHandler) listInvites(w http.ResponseWriter, v clubViewer, id string) {
	g := h.guard(w, v, id, clubs.NodeMembersInvite)
	if g == nil {
		return
	}
	rows, err := h.DB.Query(`SELECT i.id, i.role, COALESCE(c.handle,''), COALESCE(b.handle,''), i.expires_at, COALESCE(u.handle,''),
		i.used_at, i.revoked_at, i.created_at FROM club_invites i
		LEFT JOIN hs_profiles c ON c.sub=i.created_by LEFT JOIN hs_profiles b ON b.sub=i.bound_sub AND i.bound_sub<>''
		LEFT JOIN hs_profiles u ON u.sub=i.used_by AND i.used_by<>''
		WHERE i.club_id=? ORDER BY i.created_at DESC, i.rowid DESC LIMIT 200`, g.c.ID)
	if err != nil {
		hsErr(w, 500, "database error")
		return
	}
	defer rows.Close()
	now := time.Now().UTC()
	items := []map[string]any{}
	for rows.Next() {
		var iid, role, creator, bound, exp, usedBy, created string
		var usedAt, revokedAt sql.NullString
		if rows.Scan(&iid, &role, &creator, &bound, &exp, &usedBy, &usedAt, &revokedAt, &created) != nil {
			continue
		}
		status := "active"
		if t, err := time.ParseInLocation(clubSQLTime, exp, time.UTC); err == nil && !t.After(now) {
			status = "expired"
		}
		if revokedAt.Valid {
			status = "revoked"
		}
		if usedAt.Valid {
			status = "used"
		}
		items = append(items, map[string]any{"id": iid, "role": role, "created_by": creator, "bound_handle": bound,
			"expires_at": hsTime(exp), "used_by": usedBy, "used_at": hsTime(usedAt.String), "status": status, "created_at": hsTime(created)})
	}
	hsList(w, len(items), len(items), 0, items)
}

func (h *ClubsHandler) revokeInvite(w http.ResponseWriter, r *http.Request, v clubViewer, id, iid string) {
	g := h.guard(w, v, id, clubs.NodeMembersInvite)
	if g == nil {
		return
	}
	tx, err := h.begin(r)
	if err != nil {
		hsErr(w, 500, "database error")
		return
	}
	defer tx.rollback()
	res, err := tx.tx.Exec(`UPDATE club_invites SET revoked_at=? WHERE id=? AND club_id=? AND used_at IS NULL AND revoked_at IS NULL`,
		clubNow(), iid, g.c.ID)
	if err != nil {
		hsErr(w, 500, "database error")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		hsErr(w, http.StatusNotFound, "not found")
		return
	}
	tx.audit(g.c.ID, v.sub, "invite.revoke", "", map[string]any{"invite_id": iid}, false)
	if err := tx.commit(); err != nil {
		hsErr(w, 500, "database error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type clubInvite struct {
	id, clubID, role, createdBy, boundSub, expiresAt, usedBy string
	usedAt, revokedAt                                        sql.NullString
}

func (h *ClubsHandler) inviteByToken(q dbq, token string) (clubInvite, bool) {
	var i clubInvite
	err := q.QueryRow(`SELECT id, club_id, role, created_by, bound_sub, expires_at, used_by, used_at, revoked_at
		FROM club_invites WHERE token_hash=?`, clubHash(strings.TrimSpace(token))).
		Scan(&i.id, &i.clubID, &i.role, &i.createdBy, &i.boundSub, &i.expiresAt, &i.usedBy, &i.usedAt, &i.revokedAt)
	return i, err == nil
}

// inviteState classifies an invite: "" if redeemable, else the machine code (status) to report.
func (h *ClubsHandler) inviteState(q dbq, i clubInvite, c clubRow) (status int, code, msg string) {
	switch {
	case i.revokedAt.Valid || c.Deleted || c.Suspended:
		return http.StatusNotFound, "invite_invalid", "this invite is not valid"
	case i.usedAt.Valid:
		return http.StatusGone, "invite_used", "this invite has already been used"
	}
	if t, err := time.ParseInLocation(clubSQLTime, i.expiresAt, time.UTC); err != nil || !t.After(time.Now().UTC()) {
		return http.StatusGone, "invite_expired", "this invite has expired"
	}
	// The minting member must still hold the right to invite at this role; a removed or demoted inviter's
	// outstanding links die with their standing.
	crole, ok := h.memberRole(q, c.ID, i.createdBy)
	role := clubs.Role(i.role)
	if !ok || !clubs.Can(crole, clubs.NodeMembersInvite) || (role != clubs.RolePlayer && !clubs.Can(crole, clubs.NodeMembersInviteElev)) {
		return http.StatusNotFound, "invite_invalid", "this invite is not valid"
	}
	return 0, "", ""
}

func (h *ClubsHandler) peekInvite(w http.ResponseWriter, r *http.Request, v clubViewer) {
	var in struct {
		Token string `json:"token"`
	}
	if hsDecode(r, &in) != nil || in.Token == "" {
		hsErr(w, http.StatusBadRequest, "token required")
		return
	}
	i, ok := h.inviteByToken(h.DB, in.Token)
	if !ok {
		clubErr(w, http.StatusNotFound, "invite_invalid", "this invite is not valid")
		return
	}
	c, err := loadClub(h.DB, "id", i.clubID)
	if err != nil {
		clubErr(w, http.StatusNotFound, "invite_invalid", "this invite is not valid")
		return
	}
	if st, code, msg := h.inviteState(h.DB, i, c); st != 0 {
		clubErr(w, st, code, msg)
		return
	}
	out := map[string]any{"club": map[string]any{"id": c.ID, "slug": c.Slug, "name": c.Name, "tagline": c.Tagline,
		"privacy_tier": c.Tier, "tier": clubTier(c.Tier), "is_exclusive": c.Exclusive, "brand": brandOf(c.BrandJSON)},
		"role": i.role, "expires_at": hsTime(i.expiresAt), "bound": i.boundSub != ""}
	if v.sub != "" {
		out["handle_required"] = h.handleOf(h.DB, v.sub) == ""
		if st, code, _ := h.joinChecks(h.DB, v.sub, c); st != 0 {
			out["blocked"] = code
		}
		if i.boundSub != "" && i.boundSub != v.sub {
			out["blocked"] = "invite_bound"
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *ClubsHandler) join(w http.ResponseWriter, r *http.Request, v clubViewer) {
	var in struct {
		Token string `json:"token"`
	}
	if hsDecode(r, &in) != nil || in.Token == "" {
		hsErr(w, http.StatusBadRequest, "token required")
		return
	}
	if h.handleOf(h.DB, v.sub) == "" {
		clubErr(w, http.StatusConflict, "handle_required", "claim a handle first")
		return
	}
	tx, err := h.begin(r)
	if err != nil {
		hsErr(w, 500, "database error")
		return
	}
	defer tx.rollback()
	i, ok := h.inviteByToken(tx.tx, in.Token)
	if !ok {
		clubErr(w, http.StatusNotFound, "invite_invalid", "this invite is not valid")
		return
	}
	c, err := loadClub(tx.tx, "id", i.clubID)
	if err != nil {
		clubErr(w, http.StatusNotFound, "invite_invalid", "this invite is not valid")
		return
	}
	if st, code, msg := h.inviteState(tx.tx, i, c); st != 0 {
		clubErr(w, st, code, msg)
		return
	}
	if i.boundSub != "" && i.boundSub != v.sub {
		clubErr(w, http.StatusForbidden, "invite_bound", "this invite was issued to someone else")
		return
	}
	if st, code, msg := h.joinChecks(tx.tx, v.sub, c); st != 0 {
		clubErr(w, st, code, msg)
		return
	}
	// Single use, atomically: the UPDATE only wins once.
	res, err := tx.tx.Exec(`UPDATE club_invites SET used_by=?, used_at=? WHERE id=? AND used_at IS NULL AND revoked_at IS NULL`,
		v.sub, clubNow(), i.id)
	if err != nil {
		hsErr(w, 500, "database error")
		return
	}
	if n, _ := res.RowsAffected(); n != 1 {
		clubErr(w, http.StatusGone, "invite_used", "this invite has already been used")
		return
	}
	if _, err := tx.tx.Exec(`INSERT INTO club_memberships (club_id, sub, role, joined_at, invited_by) VALUES (?,?,?,?,?)`,
		c.ID, v.sub, i.role, clubNow(), i.createdBy); err != nil {
		hsErr(w, 500, "database error")
		return
	}
	tx.audit(c.ID, v.sub, "invite.redeem", i.createdBy, map[string]any{"invite_id": i.id, "role": i.role}, false)
	tx.audit(c.ID, v.sub, "member.join", v.sub, map[string]any{"via": "invite", "role": i.role}, false)
	if err := tx.commit(); err != nil {
		hsErr(w, 500, "database error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"club_id": c.ID, "slug": c.Slug, "name": c.Name, "role": i.role})
}

// ---- join requests (Social clubs, join_policy=request) -----------------------------------------------------

func (h *ClubsHandler) createRequest(w http.ResponseWriter, r *http.Request, v clubViewer, slug string) {
	if v.sub == "" {
		hsErr(w, http.StatusUnauthorized, "sign in with IDUNA first")
		return
	}
	var in struct {
		Message string `json:"message"`
	}
	if clubDecodeOpt(r, &in) != nil {
		hsErr(w, http.StatusBadRequest, "bad json")
		return
	}
	msg, ok := hsText(in.Message, 0, 300)
	if !ok {
		hsErr(w, http.StatusUnprocessableEntity, "message must be at most 300 characters")
		return
	}
	if h.handleOf(h.DB, v.sub) == "" {
		clubErr(w, http.StatusConflict, "handle_required", "claim a handle first")
		return
	}
	pub := h.resolvePublic(w, v, slug) // Secret / suspended / deleted => 404
	if pub == nil {
		return
	}
	if pub.tier != 3 || pub.joinPolicy != "request" {
		clubErr(w, http.StatusForbidden, "tier_forbidden", "this club does not take join requests")
		return
	}
	tx, err := h.begin(r)
	if err != nil {
		hsErr(w, 500, "database error")
		return
	}
	defer tx.rollback()
	c, err := loadClub(tx.tx, "id", pub.id)
	if err != nil || c.Deleted || c.Suspended {
		hsErr(w, http.StatusNotFound, "not found")
		return
	}
	if st, code, m := h.joinChecks(tx.tx, v.sub, c); st != 0 && code != "club_full" {
		clubErr(w, st, code, m)
		return
	}
	rid := uuid.NewString()
	if _, err := tx.tx.Exec(`INSERT INTO club_join_requests (id, club_id, sub, message, created_at) VALUES (?,?,?,?,?)`,
		rid, c.ID, v.sub, msg, clubNow()); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			clubErr(w, http.StatusConflict, "request_pending", "you already have a pending request")
			return
		}
		hsErr(w, 500, "database error")
		return
	}
	tx.audit(c.ID, v.sub, "request.create", v.sub, map[string]any{"request_id": rid}, false)
	if err := tx.commit(); err != nil {
		hsErr(w, 500, "database error")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": rid, "status": "pending"})
}

func (h *ClubsHandler) listRequests(w http.ResponseWriter, v clubViewer, id string) {
	g := h.guard(w, v, id, clubs.NodeMembersRequests)
	if g == nil {
		return
	}
	rows, err := h.DB.Query(`SELECT q.id, q.sub, COALESCE(p.handle,''), q.message, q.created_at
		FROM club_join_requests q LEFT JOIN hs_profiles p ON p.sub=q.sub
		WHERE q.club_id=? AND q.status='pending' ORDER BY q.created_at, q.rowid`, g.c.ID)
	if err != nil {
		hsErr(w, 500, "database error")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var rid, sub, handle, msg, created string
		if rows.Scan(&rid, &sub, &handle, &msg, &created) == nil {
			items = append(items, map[string]any{"id": rid, "sub": sub, "handle": handle, "message": msg, "created_at": hsTime(created)})
		}
	}
	hsList(w, len(items), len(items), 0, items)
}

func (h *ClubsHandler) decideRequest(w http.ResponseWriter, r *http.Request, v clubViewer, id, rid string) {
	g := h.guard(w, v, id, clubs.NodeMembersRequests)
	if g == nil {
		return
	}
	var in struct {
		Accept bool `json:"accept"`
	}
	if hsDecode(r, &in) != nil {
		hsErr(w, http.StatusBadRequest, "bad json")
		return
	}
	tx, err := h.begin(r)
	if err != nil {
		hsErr(w, 500, "database error")
		return
	}
	defer tx.rollback()
	if role, ok := h.memberRole(tx.tx, g.c.ID, v.sub); !ok || !clubs.Can(role, clubs.NodeMembersRequests) {
		clubErr(w, http.StatusForbidden, "forbidden", "your role cannot do that")
		return
	}
	var sub string
	if err := tx.tx.QueryRow(`SELECT sub FROM club_join_requests WHERE id=? AND club_id=? AND status='pending'`, rid, g.c.ID).Scan(&sub); err != nil {
		hsErr(w, http.StatusNotFound, "not found")
		return
	}
	status := "rejected"
	if in.Accept {
		c, err := loadClub(tx.tx, "id", g.c.ID)
		if err != nil || c.Deleted || c.Suspended {
			hsErr(w, http.StatusNotFound, "not found")
			return
		}
		if st, code, msg := h.joinChecks(tx.tx, sub, c); st != 0 {
			clubErr(w, st, code, msg) // request stays pending so a manager can resolve the conflict
			return
		}
		if _, err := tx.tx.Exec(`INSERT INTO club_memberships (club_id, sub, role, joined_at, invited_by) VALUES (?,?, 'player', ?, ?)`,
			c.ID, sub, clubNow(), v.sub); err != nil {
			hsErr(w, 500, "database error")
			return
		}
		status = "accepted"
	}
	if _, err := tx.tx.Exec(`UPDATE club_join_requests SET status=?, decided_by=?, decided_at=? WHERE id=?`, status, v.sub, clubNow(), rid); err != nil {
		hsErr(w, 500, "database error")
		return
	}
	tx.audit(g.c.ID, v.sub, "request."+strings.TrimSuffix(status, "ed"), sub, map[string]any{"request_id": rid}, false)
	if in.Accept {
		tx.audit(g.c.ID, v.sub, "member.join", sub, map[string]any{"via": "request", "role": "player"}, false)
	}
	if err := tx.commit(); err != nil {
		hsErr(w, 500, "database error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": rid, "status": status})
}

// ---- feed -------------------------------------------------------------------------------------------------

func (h *ClubsHandler) listFeed(w http.ResponseWriter, r *http.Request, v clubViewer, id string) {
	g := h.guard(w, v, id, clubs.NodeFeedRead)
	if g == nil {
		return
	}
	limit, offset := hsPage(r, 30)
	var total int
	_ = h.DB.QueryRow(`SELECT COUNT(*) FROM club_posts WHERE club_id=? AND deleted=0`, g.c.ID).Scan(&total)
	rows, err := h.DB.Query(`SELECT po.id, po.author_sub, COALESCE(p.handle,''), po.body, po.pinned, po.pinned_deck_id, po.created_at,
		(SELECT COUNT(*) FROM club_post_comments c WHERE c.post_id=po.id AND c.club_id=po.club_id AND c.deleted=0),
		COALESCE((SELECT d.title FROM hs_decks d WHERE d.id=po.pinned_deck_id AND d.private=0 AND d.deleted=0),''),
		COALESCE((SELECT d.class FROM hs_decks d WHERE d.id=po.pinned_deck_id AND d.private=0 AND d.deleted=0),'')
		FROM club_posts po LEFT JOIN hs_profiles p ON p.sub=po.author_sub
		WHERE po.club_id=? AND po.deleted=0 ORDER BY po.pinned DESC, po.id DESC LIMIT ? OFFSET ?`, g.c.ID, limit, offset)
	if err != nil {
		hsErr(w, 500, "database error")
		return
	}
	defer rows.Close()
	canMod := clubs.Can(g.role, clubs.NodeFeedModerate)
	items := []map[string]any{}
	for rows.Next() {
		var pid, deckID int64
		var author, handle, body, created, dTitle, dClass string
		var pinned, cc int
		if rows.Scan(&pid, &author, &handle, &body, &pinned, &deckID, &created, &cc, &dTitle, &dClass) != nil {
			continue
		}
		it := map[string]any{"id": pid, "author": map[string]string{"handle": handle}, "body": body, "pinned": pinned == 1,
			"comments": cc, "created_at": hsTime(created), "mine": author == v.sub, "can_delete": author == v.sub || canMod}
		if deckID > 0 && dTitle != "" {
			it["pinned_deck"] = map[string]any{"id": deckID, "title": dTitle, "class": dClass}
		}
		items = append(items, it)
	}
	hsList(w, total, limit, offset, items)
}

func (h *ClubsHandler) createPost(w http.ResponseWriter, r *http.Request, v clubViewer, id string) {
	g := h.guard(w, v, id, clubs.NodeFeedPost)
	if g == nil {
		return
	}
	var in struct {
		Body         string `json:"body"`
		PinnedDeckID int64  `json:"pinned_deck_id"`
	}
	if hsDecode(r, &in) != nil {
		hsErr(w, http.StatusBadRequest, "bad json")
		return
	}
	body, ok := hsText(in.Body, 1, 2000)
	if !ok {
		hsErr(w, http.StatusUnprocessableEntity, "post must be 1-2000 characters")
		return
	}
	if in.PinnedDeckID != 0 {
		var one int
		if h.DB.QueryRow(`SELECT 1 FROM hs_decks WHERE id=? AND private=0 AND deleted=0 AND merged_into=0`, in.PinnedDeckID).Scan(&one) != nil {
			hsErr(w, http.StatusUnprocessableEntity, "that deck is not available to pin")
			return
		}
	}
	tx, err := h.begin(r)
	if err != nil {
		hsErr(w, 500, "database error")
		return
	}
	defer tx.rollback()
	if role, ok := h.memberRole(tx.tx, g.c.ID, v.sub); !ok || !clubs.Can(role, clubs.NodeFeedPost) {
		clubErr(w, http.StatusForbidden, "forbidden", "your role cannot do that")
		return
	}
	res, err := tx.tx.Exec(`INSERT INTO club_posts (club_id, author_sub, body, pinned_deck_id, created_at) VALUES (?,?,?,?,?)`,
		g.c.ID, v.sub, body, in.PinnedDeckID, clubNow()) // club_id from the guard, never from the body
	if err != nil {
		hsErr(w, 500, "database error")
		return
	}
	pid, _ := res.LastInsertId()
	tx.audit(g.c.ID, v.sub, "post.create", "", map[string]any{"post_id": pid}, false)
	if err := tx.commit(); err != nil {
		hsErr(w, 500, "database error")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": pid})
}

func (h *ClubsHandler) postInClub(q dbq, clubID, pidStr string) (pid int64, author string, ok bool) {
	pid, err := strconv.ParseInt(pidStr, 10, 64)
	if err != nil {
		return 0, "", false
	}
	err = q.QueryRow(`SELECT author_sub FROM club_posts WHERE id=? AND club_id=? AND deleted=0`, pid, clubID).Scan(&author)
	return pid, author, err == nil
}

func (h *ClubsHandler) deletePost(w http.ResponseWriter, r *http.Request, v clubViewer, id, pidStr string) {
	g := h.guard(w, v, id, clubs.NodeFeedRead)
	if g == nil {
		return
	}
	if g.c.Suspended {
		clubErr(w, http.StatusForbidden, "club_suspended", "this club is suspended and read-only")
		return
	}
	tx, err := h.begin(r)
	if err != nil {
		hsErr(w, 500, "database error")
		return
	}
	defer tx.rollback()
	role, ok := h.memberRole(tx.tx, g.c.ID, v.sub)
	if !ok {
		clubErr(w, http.StatusForbidden, "forbidden", "you are not a member of this club")
		return
	}
	pid, author, found := h.postInClub(tx.tx, g.c.ID, pidStr)
	if !found {
		hsErr(w, http.StatusNotFound, "not found")
		return
	}
	if author != v.sub && !clubs.Can(role, clubs.NodeFeedModerate) {
		clubErr(w, http.StatusForbidden, "forbidden", "your role cannot do that")
		return
	}
	if _, err := tx.tx.Exec(`UPDATE club_posts SET deleted=1, pinned=0 WHERE id=? AND club_id=?`, pid, g.c.ID); err != nil {
		hsErr(w, 500, "database error")
		return
	}
	tx.audit(g.c.ID, v.sub, "post.delete", author, map[string]any{"post_id": pid, "moderated": author != v.sub}, false)
	if err := tx.commit(); err != nil {
		hsErr(w, 500, "database error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *ClubsHandler) pinPost(w http.ResponseWriter, r *http.Request, v clubViewer, id, pidStr string) {
	g := h.guard(w, v, id, clubs.NodeFeedModerate)
	if g == nil {
		return
	}
	var in struct {
		Pinned *bool `json:"pinned"`
	}
	if clubDecodeOpt(r, &in) != nil {
		hsErr(w, http.StatusBadRequest, "bad json")
		return
	}
	pin := in.Pinned == nil || *in.Pinned
	tx, err := h.begin(r)
	if err != nil {
		hsErr(w, 500, "database error")
		return
	}
	defer tx.rollback()
	if role, ok := h.memberRole(tx.tx, g.c.ID, v.sub); !ok || !clubs.Can(role, clubs.NodeFeedModerate) {
		clubErr(w, http.StatusForbidden, "forbidden", "your role cannot do that")
		return
	}
	pid, _, found := h.postInClub(tx.tx, g.c.ID, pidStr)
	if !found {
		hsErr(w, http.StatusNotFound, "not found")
		return
	}
	b := 0
	if pin {
		b = 1
	}
	if _, err := tx.tx.Exec(`UPDATE club_posts SET pinned=? WHERE id=? AND club_id=?`, b, pid, g.c.ID); err != nil {
		hsErr(w, 500, "database error")
		return
	}
	tx.audit(g.c.ID, v.sub, "post.pin", "", map[string]any{"post_id": pid, "pinned": pin}, false)
	if err := tx.commit(); err != nil {
		hsErr(w, 500, "database error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": pid, "pinned": pin})
}

func (h *ClubsHandler) listComments(w http.ResponseWriter, r *http.Request, v clubViewer, id, pidStr string) {
	g := h.guard(w, v, id, clubs.NodeFeedRead)
	if g == nil {
		return
	}
	pid, _, ok := h.postInClub(h.DB, g.c.ID, pidStr)
	if !ok {
		hsErr(w, http.StatusNotFound, "not found")
		return
	}
	limit, offset := hsPage(r, 50)
	var total int
	_ = h.DB.QueryRow(`SELECT COUNT(*) FROM club_post_comments WHERE post_id=? AND club_id=? AND deleted=0`, pid, g.c.ID).Scan(&total)
	rows, err := h.DB.Query(`SELECT c.id, c.author_sub, COALESCE(p.handle,''), c.body, c.created_at
		FROM club_post_comments c LEFT JOIN hs_profiles p ON p.sub=c.author_sub
		WHERE c.post_id=? AND c.club_id=? AND c.deleted=0 ORDER BY c.id LIMIT ? OFFSET ?`, pid, g.c.ID, limit, offset)
	if err != nil {
		hsErr(w, 500, "database error")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var cid int64
		var author, handle, body, created string
		if rows.Scan(&cid, &author, &handle, &body, &created) == nil {
			items = append(items, map[string]any{"id": cid, "author": map[string]string{"handle": handle}, "body": body,
				"created_at": hsTime(created), "mine": author == v.sub})
		}
	}
	hsList(w, total, limit, offset, items)
}

func (h *ClubsHandler) addComment(w http.ResponseWriter, r *http.Request, v clubViewer, id, pidStr string) {
	g := h.guard(w, v, id, clubs.NodeFeedPost)
	if g == nil {
		return
	}
	var in struct {
		Body string `json:"body"`
	}
	if hsDecode(r, &in) != nil {
		hsErr(w, http.StatusBadRequest, "bad json")
		return
	}
	body, ok := hsText(in.Body, 1, 1000)
	if !ok {
		hsErr(w, http.StatusUnprocessableEntity, "comment must be 1-1000 characters")
		return
	}
	tx, err := h.begin(r)
	if err != nil {
		hsErr(w, 500, "database error")
		return
	}
	defer tx.rollback()
	if role, ok := h.memberRole(tx.tx, g.c.ID, v.sub); !ok || !clubs.Can(role, clubs.NodeFeedPost) {
		clubErr(w, http.StatusForbidden, "forbidden", "your role cannot do that")
		return
	}
	pid, _, found := h.postInClub(tx.tx, g.c.ID, pidStr)
	if !found {
		hsErr(w, http.StatusNotFound, "not found")
		return
	}
	res, err := tx.tx.Exec(`INSERT INTO club_post_comments (post_id, club_id, author_sub, body, created_at) VALUES (?,?,?,?,?)`,
		pid, g.c.ID, v.sub, body, clubNow())
	if err != nil {
		hsErr(w, 500, "database error")
		return
	}
	cid, _ := res.LastInsertId()
	tx.audit(g.c.ID, v.sub, "comment.create", "", map[string]any{"post_id": pid, "comment_id": cid}, false)
	if err := tx.commit(); err != nil {
		hsErr(w, 500, "database error")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": cid})
}

// ---- featured decks ---------------------------------------------------------------------------------------

func (h *ClubsHandler) featureDeck(w http.ResponseWriter, r *http.Request, v clubViewer, id, deckStr string, add bool) {
	g := h.guard(w, v, id, clubs.NodeDecksFeature)
	if g == nil {
		return
	}
	deckID, err := strconv.ParseInt(deckStr, 10, 64)
	if err != nil || deckID <= 0 {
		hsErr(w, http.StatusNotFound, "not found")
		return
	}
	tx, err := h.begin(r)
	if err != nil {
		hsErr(w, 500, "database error")
		return
	}
	defer tx.rollback()
	if role, ok := h.memberRole(tx.tx, g.c.ID, v.sub); !ok || !clubs.Can(role, clubs.NodeDecksFeature) {
		clubErr(w, http.StatusForbidden, "forbidden", "your role cannot do that")
		return
	}
	if add {
		var one int
		if tx.tx.QueryRow(`SELECT 1 FROM hs_decks WHERE id=? AND private=0 AND deleted=0 AND merged_into=0`, deckID).Scan(&one) != nil {
			hsErr(w, http.StatusUnprocessableEntity, "only public decks can be featured")
			return
		}
		var have, already int
		_ = tx.tx.QueryRow(`SELECT COUNT(*) FROM club_featured_decks WHERE club_id=?`, g.c.ID).Scan(&have)
		_ = tx.tx.QueryRow(`SELECT COUNT(*) FROM club_featured_decks WHERE club_id=? AND deck_id=?`, g.c.ID, deckID).Scan(&already)
		if already == 0 && have >= h.setting(tx.tx, "featured_decks_cap", clubFeaturedCap) {
			clubErr(w, http.StatusConflict, "featured_cap", "unfeature a deck first")
			return
		}
		if already == 0 {
			if _, err := tx.tx.Exec(`INSERT INTO club_featured_decks (club_id, deck_id, featured_by, created_at) VALUES (?,?,?,?)`,
				g.c.ID, deckID, v.sub, clubNow()); err != nil {
				hsErr(w, 500, "database error")
				return
			}
			tx.audit(g.c.ID, v.sub, "deck.feature", "", map[string]any{"deck_id": deckID}, false)
		}
	} else {
		res, err := tx.tx.Exec(`DELETE FROM club_featured_decks WHERE club_id=? AND deck_id=?`, g.c.ID, deckID)
		if err != nil {
			hsErr(w, 500, "database error")
			return
		}
		if n, _ := res.RowsAffected(); n > 0 {
			tx.audit(g.c.ID, v.sub, "deck.unfeature", "", map[string]any{"deck_id": deckID}, false)
		}
	}
	if err := tx.commit(); err != nil {
		hsErr(w, 500, "database error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---- audit ------------------------------------------------------------------------------------------------

func (h *ClubsHandler) auditRows(clubID string, limit int, before int64) ([]map[string]any, int64, bool) {
	args := []any{clubID}
	cond := ""
	if before > 0 {
		cond = " AND a.id<?"
		args = append(args, before)
	}
	rows, err := h.DB.Query(`SELECT a.id, a.actor_sub, COALESCE(pa.handle,''), a.action, a.target_sub, COALESCE(pt.handle,''),
		a.meta_json, a.platform_admin, a.at FROM club_audit_log a
		LEFT JOIN hs_profiles pa ON pa.sub=a.actor_sub LEFT JOIN hs_profiles pt ON pt.sub=a.target_sub AND a.target_sub<>''
		WHERE a.club_id=?`+cond+` ORDER BY a.id DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, 0, false
	}
	defer rows.Close()
	items := []map[string]any{}
	var last int64
	for rows.Next() {
		var aid int64
		var actor, ah, action, target, th, meta, at string
		var pa int
		if rows.Scan(&aid, &actor, &ah, &action, &target, &th, &meta, &pa, &at) != nil {
			continue
		}
		var m map[string]any
		_ = json.Unmarshal([]byte(meta), &m)
		it := map[string]any{"id": aid, "action": action, "target_sub": target, "target_handle": th, "meta": m,
			"platform_admin": pa == 1, "at": hsTime(at)}
		if pa == 1 { // operators are shown as such, not by identity
			it["actor_handle"] = "IDUNA operator"
		} else {
			it["actor_sub"], it["actor_handle"] = actor, ah
		}
		items = append(items, it)
		last = aid
	}
	return items, last, true
}

func (h *ClubsHandler) clubAudit(w http.ResponseWriter, r *http.Request, v clubViewer, id string) {
	g := h.guard(w, v, id, clubs.NodeAuditView)
	if g == nil {
		return
	}
	limit := atoiDefault(r.URL.Query().Get("limit"), 50)
	if limit < 1 {
		limit = 1
	}
	if limit > 200 {
		limit = 200
	}
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	items, last, ok := h.auditRows(g.c.ID, limit, before)
	if !ok {
		hsErr(w, 500, "database error")
		return
	}
	var total int
	_ = h.DB.QueryRow(`SELECT COUNT(*) FROM club_audit_log WHERE club_id=?`, g.c.ID).Scan(&total)
	out := map[string]any{"total": total, "limit": limit, "offset": 0, "items": items}
	if len(items) == limit {
		out["next_before"] = last
	}
	writeJSON(w, http.StatusOK, out)
}

// clubsPerms is the effective node list for a role string (admin detail view).
func clubsPerms(role string, isOwner bool) []string { return clubs.Perms(clubs.Role(role), isOwner) }
