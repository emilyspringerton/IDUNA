package handlers

// hs_live.go -- real-time Hearthstone deck tracking for WOTAN, private per player.
//
// Flow: the player's PC streams two log files' new lines to POST /live/lines using a short-lived,
// upload-scoped token minted for their own IDUNA identity (POST /live/token); the server keeps ONE
// session per IDUNA sub in memory, runs the HRIP PARENA tracker over the current game's lines, joins
// the queued deck (Decks.log "Finding Game With Deck" -> deck code -> hs_cards) and serves the result
// to that same sub only (GET /live/state). Nothing here is public: state is keyed by the verified
// token subject, so one player can never read or write another's session.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"iduna/internal/auth/jwt"
	"iduna/internal/hsdeck"
	"iduna/internal/hstracker"
	"iduna/internal/http/middleware"
)

const (
	hsLiveUploadAud  = "hs-live-upload"
	hsLiveTokenTTL   = 12 * time.Hour
	hsLiveMaxBody    = 4 << 20
	hsLiveMaxPower   = 120000 // lines kept for the current game
	hsLiveMaxDecks   = 800
	hsLiveMaxLineLen = 4000
)

var (
	hsLogPrefixRe = regexp.MustCompile(`^[A-Z] \d{2}:\d{2}:\d{2}\.\d+ `)
	hsDeckCodeRe  = regexp.MustCompile(`^AAE[A-Za-z0-9+/=]{8,}$`)
	hsPowerLineRe = regexp.MustCompile(`^D \d{2}:\d{2}:\d{2}\.\d+ GameState\.DebugPrint(Power|Game)\(\) - `)
)

type liveSession struct {
	mu       sync.Mutex
	decks    []string
	power    []string
	dirty    bool
	computed time.Time
	state    liveState
	updated  time.Time
}

type liveCard struct {
	CardID string `json:"card_id"`
	Name   string `json:"name"`
	Cost   int    `json:"cost"`
	Count  int    `json:"count"`
}

type liveDeck struct {
	Name  string `json:"name"`
	ID    string `json:"id"`
	Class string `json:"class"`
	Size  int    `json:"size"`
}

type liveState struct {
	Connected  bool       `json:"connected"` // has this player's uplink ever posted
	InGame     bool       `json:"in_game"`
	Turn       int        `json:"turn"`
	Deck       *liveDeck  `json:"deck,omitempty"`
	Left       []liveCard `json:"left"`
	LeftTotal  int        `json:"left_total"`
	Drawn      []liveCard `json:"drawn"`
	DrawnTotal int        `json:"drawn_total"`
	Opponent   struct {
		Name  string     `json:"name"`
		Cards []liveCard `json:"cards"`
	} `json:"opponent"`
	Note      string `json:"note,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

func (h *HSHandler) session(sub string, create bool) *liveSession {
	h.liveMu.Lock()
	defer h.liveMu.Unlock()
	if h.liveSess == nil {
		h.liveSess = map[string]*liveSession{}
	}
	s := h.liveSess[sub]
	if s == nil && create {
		s = &liveSession{}
		h.liveSess[sub] = s
	}
	return s
}

// liveToken mints an upload-scoped token for the caller's own identity. It can only POST /live/lines.
func (h *HSHandler) liveToken(w http.ResponseWriter, v hsViewer) {
	if v.sub == "" {
		hsErr(w, http.StatusUnauthorized, "sign in with IDUNA first")
		return
	}
	now := time.Now().UTC()
	tok, err := jwt.Sign(h.Keys, map[string]any{
		"sub": v.sub, "aud": hsLiveUploadAud, "iss": "iduna",
		"iat": now.Unix(), "exp": now.Add(hsLiveTokenTTL).Unix(),
		"permissions": []string{"hs.live.upload"},
	})
	if err != nil {
		hsErr(w, http.StatusInternalServerError, "could not mint token")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": tok, "expires_in": int(hsLiveTokenTTL.Seconds())})
}

type liveLinesReq struct {
	Reset bool     `json:"reset"`
	Decks []string `json:"decks"`
	Power []string `json:"power"`
}

// liveLines accepts new log lines. Auth: an upload-scoped token or a normal IDUNA token; either way the
// session is the token's own subject.
func (h *HSHandler) liveLines(w http.ResponseWriter, r *http.Request) {
	ah := r.Header.Get("Authorization")
	if !strings.HasPrefix(ah, "Bearer ") || h.Keys == nil {
		hsErr(w, http.StatusUnauthorized, "missing upload token")
		return
	}
	claims, err := jwt.Verify(h.Keys, strings.TrimPrefix(ah, "Bearer "))
	sub, _ := claims["sub"].(string)
	if err != nil || sub == "" {
		hsErr(w, http.StatusUnauthorized, "invalid or expired upload token")
		return
	}
	// The uplink posts about once a second while a game is on, so it must not share the 40/min
	// deck-write limiter (that starved it after ~1 minute and dropped batches). Separate, generous
	// per-subject bucket; still bounded (and the body is capped) so it cannot be abused.
	if h.WriteLimiter != nil {
		h.liveMu.Lock()
		if h.liveLim == nil {
			h.liveLim = middleware.NewIPRateLimiter(900)
		}
		lim := h.liveLim
		h.liveMu.Unlock()
		if !lim.Allow("hslive:" + sub) {
			w.Header().Set("Retry-After", "5")
			hsErr(w, http.StatusTooManyRequests, "slow down")
			return
		}
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, hsLiveMaxBody))
	if err != nil {
		hsErr(w, http.StatusRequestEntityTooLarge, "batch too large")
		return
	}
	var req liveLinesReq
	if err := json.Unmarshal(body, &req); err != nil {
		hsErr(w, http.StatusUnprocessableEntity, "bad json")
		return
	}
	s := h.session(sub, true)
	s.mu.Lock()
	defer s.mu.Unlock()
	if req.Reset {
		s.power = s.power[:0]
	}
	for _, ln := range req.Decks {
		ln = strings.TrimRight(ln, "\r\n")
		if len(ln) > hsLiveMaxLineLen || !hsLogPrefixRe.MatchString(ln) && !strings.HasPrefix(ln, "###") && !strings.HasPrefix(ln, "# Deck ID") {
			continue
		}
		s.decks = append(s.decks, ln)
	}
	if len(s.decks) > hsLiveMaxDecks {
		s.decks = append([]string(nil), s.decks[len(s.decks)-hsLiveMaxDecks:]...)
	}
	for _, ln := range req.Power {
		ln = strings.TrimRight(ln, "\r\n")
		if len(ln) > hsLiveMaxLineLen || !hsPowerLineRe.MatchString(ln) {
			continue
		}
		if strings.HasSuffix(strings.TrimSpace(ln), "GameState.DebugPrintPower() - CREATE_GAME") {
			s.power = s.power[:0] // a new game: only the current one is tracked live
		}
		s.power = append(s.power, ln)
	}
	if len(s.power) > hsLiveMaxPower {
		s.power = append([]string(nil), s.power[len(s.power)-hsLiveMaxPower:]...)
	}
	s.dirty = true
	s.updated = time.Now()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "power_lines": len(s.power)})
}

// liveState returns the caller's own tracker state.
func (h *HSHandler) liveState(w http.ResponseWriter, r *http.Request, v hsViewer) {
	if v.sub == "" {
		hsErr(w, http.StatusUnauthorized, "sign in with IDUNA first")
		return
	}
	s := h.session(v.sub, false)
	if s == nil {
		writeJSON(w, http.StatusOK, liveState{Left: []liveCard{}, Drawn: []liveCard{}, Note: "Waiting for your PC. Start the uplink from this page."})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dirty && time.Since(s.computed) > 700*time.Millisecond {
		s.state = h.computeLive(r.Context(), s)
		s.computed = time.Now()
		s.dirty = false
	}
	st := s.state
	st.Connected = true
	if st.Left == nil {
		st.Left = []liveCard{}
	}
	if st.Drawn == nil {
		st.Drawn = []liveCard{}
	}
	st.UpdatedAt = s.updated.UTC().Format(time.RFC3339)
	writeJSON(w, http.StatusOK, st)
}

type queuedDeck struct{ name, id, code string }

// lastQueuedDeck finds the most recent "Finding Game With Deck:" block in Decks.log lines.
func lastQueuedDeck(lines []string) (queuedDeck, bool) {
	var cur, last queuedDeck
	var found, in bool
	for _, raw := range lines {
		c := strings.TrimSpace(hsLogPrefixRe.ReplaceAllString(raw, ""))
		switch {
		case c == "Finding Game With Deck:":
			cur, in = queuedDeck{}, true
		case in && strings.HasPrefix(c, "### "):
			cur.name = strings.TrimPrefix(c, "### ")
		case in && strings.HasPrefix(c, "# Deck ID:"):
			cur.id = strings.TrimSpace(strings.TrimPrefix(c, "# Deck ID:"))
		case in && hsDeckCodeRe.MatchString(c):
			cur.code = c
			last, found, in = cur, true, false
		}
	}
	return last, found
}

type hsCardRow struct {
	name   string
	cost   int
	cardID string
}

func (h *HSHandler) computeLive(ctx context.Context, s *liveSession) liveState {
	st := liveState{Connected: true}
	dq, ok := lastQueuedDeck(s.decks)
	if !ok {
		st.Note = "No queued deck seen yet. Queue a game in Hearthstone."
		return st
	}
	deck, err := hsdeck.Decode(dq.code)
	if err != nil {
		st.Note = "Could not read the queued deck code."
		return st
	}
	// dbf -> card (needs hs_cards.card_id loaded)
	byDBF := map[int]hsCardRow{}
	for _, c := range deck.DBFCards {
		var row hsCardRow
		if err := h.DB.QueryRow(`SELECT name,cost,card_id FROM hs_cards WHERE dbf_id=?`, c.DBF).Scan(&row.name, &row.cost, &row.cardID); err != nil || row.cardID == "" {
			st.Note = "Card table not loaded yet (dbf " + strconv.Itoa(c.DBF) + ")."
			return st
		}
		byDBF[c.DBF] = row
	}
	deckCount := map[string]int{}
	card := map[string]hsCardRow{}
	size := 0
	for _, c := range deck.DBFCards {
		r := byDBF[c.DBF]
		deckCount[r.cardID] += c.Count
		card[r.cardID] = r
		size += c.Count
	}
	st.Deck = &liveDeck{Name: dq.name, ID: dq.id, Class: deck.Class, Size: size}

	rt := h.Tracker
	if rt == nil {
		h.liveMu.Lock()
		if h.Tracker == nil {
			h.Tracker = &hstracker.Runner{}
		}
		rt = h.Tracker
		h.liveMu.Unlock()
	}
	var rec *hstracker.Record
	if len(s.power) > 0 {
		recs, err := rt.Run(ctx, s.power)
		if err != nil {
			st.Note = "Tracker unavailable: " + err.Error()
			return st
		}
		if len(recs) > 0 {
			rec = &recs[len(recs)-1]
		}
	}
	if rec == nil {
		st.Note = "Deck ready. Waiting for the game to start."
		st.Left = cardsFrom(deckCount, card)
		st.LeftTotal = size
		return st
	}
	st.InGame = rec.Complete == 0
	st.Turn = rec.Turns
	me := rec.Me
	if me != 1 && me != 2 {
		st.Left = cardsFrom(deckCount, card)
		st.LeftTotal = size
		st.Note = "Game found; waiting for your opening hand to identify you."
		return st
	}
	zone := map[int]string{}
	for _, raw := range rec.Timeline {
		var ev []json.RawMessage
		if json.Unmarshal(raw, &ev) != nil || len(ev) < 6 {
			continue
		}
		var kind string
		var id int
		var z string
		if json.Unmarshal(ev[0], &kind) != nil || kind != "z" || json.Unmarshal(ev[2], &id) != nil || json.Unmarshal(ev[5], &z) != nil {
			continue
		}
		zone[id] = z
	}
	drawn := map[string]int{}
	oppSeen := map[string]int{}
	for _, e := range rec.Entities {
		if e.Card == "" || strings.HasPrefix(e.Card, "HERO_") || strings.HasPrefix(e.Card, "TIME_EVENT") {
			continue
		}
		if e.Player == me {
			if e.Zone0 == "DECK" {
				if z, ok := zone[e.ID]; ok && z != "DECK" {
					drawn[e.Card]++
				}
			}
		} else {
			oppSeen[e.Card]++
		}
	}
	left := map[string]int{}
	for id, n := range deckCount {
		if d := n - drawn[id]; d > 0 {
			left[id] = d
		}
	}
	for _, n := range left {
		st.LeftTotal += n
	}
	for _, n := range drawn {
		st.DrawnTotal += n
	}
	st.Left = cardsFrom(left, card)
	st.Drawn = h.cardsByID(drawn, card)
	st.Opponent.Name = rec.Player1
	if me == 1 {
		st.Opponent.Name = rec.Player2
	}
	st.Opponent.Cards = h.cardsByID(oppSeen, card)
	return st
}

func cardsFrom(counts map[string]int, known map[string]hsCardRow) []liveCard {
	out := make([]liveCard, 0, len(counts))
	for id, n := range counts {
		r := known[id]
		name := r.name
		if name == "" {
			name = id
		}
		out = append(out, liveCard{CardID: id, Name: name, Cost: r.cost, Count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Cost != out[j].Cost {
			return out[i].Cost < out[j].Cost
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// cardsByID is cardsFrom plus a DB lookup for cards not in the deck (opponent / generated).
func (h *HSHandler) cardsByID(counts map[string]int, known map[string]hsCardRow) []liveCard {
	for id := range counts {
		if _, ok := known[id]; ok {
			continue
		}
		var r hsCardRow
		if err := h.DB.QueryRow(`SELECT name,cost,card_id FROM hs_cards WHERE card_id=? LIMIT 1`, id).Scan(&r.name, &r.cost, &r.cardID); err == nil {
			known[id] = r
		}
	}
	return cardsFrom(counts, known)
}

// liveUplink serves the PowerShell uplink. It contains no secret: the token is passed on the command line.
func (h *HSHandler) liveUplink(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, hsUplinkScript)
}

const hsUplinkScript = `# WOTAN Hearthstone deck tracker uplink. Read-only: it tails your Hearthstone log files and sends the new
# lines to WOTAN with a token only valid for uploading your own tracker data. Close this window to stop.
param(
  [Parameter(Mandatory=$true)][string]$Token,
  [string]$Base = 'https://wotan.okemily.com',
  [string]$LogsDir = ''
)
$ErrorActionPreference = 'Stop'
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
$url = $Base.TrimEnd('/') + '/api/v1/hs/live/lines'
if (-not $LogsDir) {
  foreach ($c in @("${env:ProgramFiles(x86)}\Hearthstone\Logs", "$env:ProgramFiles\Hearthstone\Logs", 'C:\Program Files (x86)\Hearthstone\Logs')) {
    if (Test-Path $c) { $LogsDir = $c; break }
  }
}
if (-not $LogsDir -or -not (Test-Path $LogsDir)) { Write-Host 'Could not find Hearthstone\Logs. Pass -LogsDir "<path>".'; exit 1 }
Write-Host "Watching $LogsDir"

# Compiled helper: PowerShell's pipeline is far too slow for tens of thousands of log lines.
Add-Type -TypeDefinition @'
using System; using System.IO; using System.Text; using System.Collections.Generic; using System.Text.RegularExpressions;
public static class HsTail {
  public static string[] ReadNew(string path, ref long offset, string pattern) {
    if (!File.Exists(path)) return new string[0];
    byte[] buf; int got = 0;
    using (FileStream fs = new FileStream(path, FileMode.Open, FileAccess.Read, FileShare.ReadWrite)) {
      if (fs.Length < offset) offset = 0;
      long n = fs.Length - offset;
      if (n <= 0) return new string[0];
      fs.Seek(offset, SeekOrigin.Begin);
      buf = new byte[n];
      while (got < n) { int r = fs.Read(buf, got, (int)n - got); if (r <= 0) break; got += r; }
    }
    int last = Array.LastIndexOf(buf, (byte)10, got - 1);
    if (last < 0) return new string[0];
    offset += last + 1;
    string text = Encoding.UTF8.GetString(buf, 0, last);
    Regex re = pattern == null ? null : new Regex(pattern);
    List<string> res = new List<string>();
    foreach (string raw in text.Split('\n')) { string ln = raw.TrimEnd('\r'); if (re == null || re.IsMatch(ln)) res.Add(ln); }
    return res.ToArray();
  }
  public static string JsonArr(string[] a, int from, int count) {
    StringBuilder sb = new StringBuilder("[");
    for (int i = 0; i < count; i++) {
      if (i > 0) sb.Append(',');
      sb.Append('"');
      foreach (char c in a[from + i]) {
        if (c == '\\') sb.Append("\\\\"); else if (c == '"') sb.Append("\\\""); else if (c < ' ') sb.Append(' '); else sb.Append(c);
      }
      sb.Append('"');
    }
    return sb.Append(']').ToString();
  }
}
'@

# Pending buffers: a batch is only dropped after the server accepted it, so a 429/network blip never
# loses log lines (the tracker needs every line of the game).
$pendD = New-Object 'System.Collections.Generic.List[string]'
$pendP = New-Object 'System.Collections.Generic.List[string]'
$script:pendReset = $false

function Flush-Pending {
  # only the current game matters: start at the last CREATE_GAME still pending
  for ($k = $pendP.Count - 1; $k -ge 1; $k--) {
    if ($pendP[$k].EndsWith('GameState.DebugPrintPower() - CREATE_GAME')) { $pendP.RemoveRange(0, $k); $script:pendReset = $true; break }
  }
  do {
    $cnt = [Math]::Min(4000, $pendP.Count)
    $arr = $pendP.GetRange(0, $cnt).ToArray()
    $dArr = $pendD.ToArray()
    $body = '{"reset":' + $(if ($script:pendReset) { 'true' } else { 'false' }) + ',"decks":' + [HsTail]::JsonArr($dArr, 0, $dArr.Length) + ',"power":' + [HsTail]::JsonArr($arr, 0, $arr.Length) + '}'
    try {
      Invoke-RestMethod -Uri $url -Method Post -Headers @{ Authorization = "Bearer $Token" } -ContentType 'application/json; charset=utf-8' -Body ([Text.Encoding]::UTF8.GetBytes($body)) -TimeoutSec 30 | Out-Null
    } catch {
      $code = 0; try { $code = [int]$_.Exception.Response.StatusCode } catch {}
      if ($code -eq 401) { Write-Host 'Token expired. Get a new command from the tracker page.'; exit 2 }
      Write-Host "Upload failed (will retry): $($_.Exception.Message)"
      return
    }
    $pendP.RemoveRange(0, $cnt); $pendD.Clear(); $script:pendReset = $false
    if ($cnt -gt 0) { Write-Host ("sent {0} lines" -f $cnt) }
  } while ($pendP.Count -gt 0)
}

$ErrorActionPreference = 'Continue'   # a transient file/IO error must not kill the tracker
$folder = ''; [long]$pOff = 0; [long]$dOff = 0
$pat = 'GameState\.DebugPrint(Power|Game)\(\) - '
while ($true) {
  try {
    $newest = Get-ChildItem $LogsDir -Directory -Filter 'Hearthstone_*' -ErrorAction Stop | Sort-Object Name -Descending | Select-Object -First 1
    if ($newest) {
      if ($newest.FullName -ne $folder) {
        $folder = $newest.FullName; $pOff = 0; $dOff = 0; $script:pendReset = $true
        $pendD.Clear(); $pendP.Clear()
        Write-Host "Session: $($newest.Name)"
      }
      $d = [HsTail]::ReadNew((Join-Path $folder 'Decks.log'), [ref]$dOff, $null)
      $p = [HsTail]::ReadNew((Join-Path $folder 'Power.log'), [ref]$pOff, $pat)
      if ($d.Length -gt 0) { $pendD.AddRange($d) }
      if ($p.Length -gt 0) { $pendP.AddRange($p) }
      if ($pendD.Count -gt 0 -or $pendP.Count -gt 0 -or $script:pendReset) { Flush-Pending }
    }
  } catch {
    Write-Host "Error (continuing): $($_.Exception.Message)"
  }
  Start-Sleep -Milliseconds 1000
}
`
