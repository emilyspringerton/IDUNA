package main

import (
	"bytes"
	_ "embed"
	"strings"
	"text/template"
)

// logsyncSh is EMILY/gitops/images/dwlogs/logsync.sh, copied in (IDUNA's CI checks this repo out alone).
//
//go:embed logsync.sh
var logsyncSh string

// The first-boot script (cloud-init runs a #! user-data script once, as root). It carries NO secrets: unlike
// wotan-vultr's static box, this one runs IDUNA (signing keys, sessions, NOCK compiling user code), so the origin key
// and the webmaster seed are copied in over SSH afterwards (main.go pushSecrets) and only then does iduna-up start
// the stack. Ubuntu only (docker.io + docker-compose-v2 come from its own archive); plan checks the family before
// anything is bought. Every interpolated value is validated against a strict pattern in config.go.
//
// The box (one service per box, founder 2026-11-26; GKE's pod spec EMILY/gitops/specs/iduna.pod is the reference):
//
//	host nginx :80/:443   Cloudflare-proxied iam.<zone>, <zone>, www.<zone> with a Cloudflare Origin CA cert;
//	                      routes are the GKE Gateway's (92-iduna-routes.yaml, 95-okemily-apex.yaml)
//	compose  iduna        IDUNA_IMAGE (GHCR), 127.0.0.1:8080, state in /srv/iduna/var (= /app/var)
//	         web          nginx serving /srv/iduna/var/www (okemily.com: OKEMILY rsync + IDUNA's rendered dirs), :8090
//	         sc-tunnel    PARENA secure-channel server, public :8443 -> iduna:8080 (built here from EDGE.GAME)
//	         logsync      mirrors DEADWEIGHT's match/deck logs into var/dwlogs for WOTAN (errors until DEADWEIGHT is up)
const userDataTmpl = `#!/bin/bash
set -euo pipefail
exec > >(tee -a /var/log/iduna-provision.log) 2>&1
echo "iduna-vultr first boot $(date -u +%FT%TZ)"
export DEBIAN_FRONTEND=noninteractive
apt-get update -y
apt-get install -y --no-install-recommends nginx git rsync ufw ca-certificates curl docker.io docker-compose-v2
systemctl enable --now docker

install -d -m 755 /opt/iduna /srv/iduna/var /srv/iduna/var/www /srv/iduna/var/dwlogs
install -d -m 700 /etc/iduna /etc/ssl/iduna

cat > /etc/iduna/box.env <<'EOF'
IDUNA_IMAGE={{.Image}}
EDGE_GIT_URL={{.EdgeGitURL}}
EDGE_GIT_REF={{.EdgeGitRef}}
OKEMILY_GIT_URL={{.OkemilyGitURL}}
OKEMILY_GIT_REF={{.OkemilyGitRef}}
EOF

cat > /opt/iduna/compose.yml <<'EOF'
services:
  iduna:
    image: {{.Image}}
    restart: unless-stopped
    working_dir: /app
    env_file: [/etc/iduna/iduna.env]
    environment:
      SQLITE_PATH: /app/var/iduna.db
      BASE_URL: https://iam.{{.Zone}}
      SSO_ALLOWED_REDIRECT_HOSTS: "wotan.{{.Zone}},iam.{{.Zone}},{{.Zone}},www.{{.Zone}},localhost,127.0.0.1"
      # Defaults point at /home/fatbaby/IDUNA/var/..., outside the volume: data there would die with the container.
      NOCK_DATA_DIR: /app/var/nock-projects
      NOCK_VIDEO_DIR: /app/var/nock-videos
      NOCK_ROBOTS_GIT_DIR: "off"
      BLOG_OUTPUT_DIR: /app/var/www/blog
      TYLER_OUTPUT_DIR: /app/var/www/tyler
      PROMPTOVERSE_OUTPUT_DIR: /app/var/www/prompt-o-verse
      DEADWEIGHT_MATCH_LOG: /app/var/dwlogs/matches.ndjson
      DEADWEIGHT_DECK_LOG: /app/var/dwlogs/decks.ndjson
    volumes: ["/srv/iduna/var:/app/var"]
    ports: ["127.0.0.1:8080:8080"]
    healthcheck:
      test: ["CMD", "curl", "-fsS", "http://127.0.0.1:8080/health"]
      interval: 15s
      timeout: 5s
      retries: 5
      start_period: 30s
  web:
    image: nginx:1.27-alpine
    restart: unless-stopped
    volumes:
      - /opt/iduna/web.conf:/etc/nginx/conf.d/default.conf:ro
      - /srv/iduna/var:/app/var:ro
    ports: ["127.0.0.1:8090:80"]
  sc-tunnel:
    image: sc-tunnel:local
    restart: unless-stopped
    user: "0:0"   # the key lives on the root-owned var bind mount (GKE got this from fsGroup)
    command: ["server", "8443", "iduna", "8080", "/app/var/sc-tunnel.key"]
    volumes: ["/srv/iduna/var:/app/var"]
    ports: ["8443:8443"]
    depends_on: [iduna]
  logsync:
    image: curlimages/curl:8.11.1
    restart: unless-stopped
    user: "0:0"
    entrypoint: ["/bin/sh", "/logsync.sh"]
    environment:
      SRC: {{.DWLogsSrc}}
      DEST: /app/var/dwlogs
    volumes:
      - /opt/iduna/logsync.sh:/logsync.sh:ro
      - /srv/iduna/var:/app/var
EOF

# The okemily-web sidecar's own config (EMILY/gitops/images/okemily-web/default.conf).
cat > /opt/iduna/web.conf <<'EOF'
server {
    listen 80;
    root /app/var/www;
    index index.html;
    location / { try_files $uri $uri/ =404; }
    location /prompt-o-verse/ { add_header Cache-Control "no-cache"; try_files $uri $uri/ =404; }
}
EOF

# EMILY/gitops/images/dwlogs/logsync.sh, verbatim.
cat > /opt/iduna/logsync.sh <<'EOF'
{{.LogsyncSh}}EOF

cat > /usr/local/bin/okemily-deploy <<'EOF'
#!/bin/bash
# The monorepo's okemily-deploy.sh, pointed at this box: OKEMILY -> /srv/iduna/var/www. The excluded dirs are
# rendered by IDUNA itself (blog/tyler/prompt-o-verse, blog-manifest.txt) or built elsewhere (battlegrounds);
# a --delete sync without them wipes them (happened for real 2026-07-19). Do not remove the exclusions.
set -euo pipefail
. /etc/iduna/box.env
if [ ! -d /opt/okemily-src/.git ]; then
  git clone --depth 1 --branch "$OKEMILY_GIT_REF" "$OKEMILY_GIT_URL" /opt/okemily-src
else
  git -C /opt/okemily-src fetch --depth 1 origin "$OKEMILY_GIT_REF" && git -C /opt/okemily-src reset --hard FETCH_HEAD
fi
rsync -a --delete --chmod=D755,F644 /opt/okemily-src/ /srv/iduna/var/www/ --exclude='.git' --exclude='blog' \
  --exclude='tyler' --exclude='blog-manifest.txt' --exclude='prompt-o-verse' --exclude='battlegrounds'
echo "okemily deployed $(git -C /opt/okemily-src rev-parse --short HEAD)"
EOF
chmod 755 /usr/local/bin/okemily-deploy

cat > /usr/local/bin/iduna-up <<'EOF'
#!/bin/bash
# Start (or update) the IDUNA stack. Refuses until iduna-vultr has pushed the secrets in over SSH.
set -euo pipefail
. /etc/iduna/box.env
for f in /etc/ssl/iduna/origin.pem /etc/ssl/iduna/origin.key; do
  [ -s "$f" ] || { echo "iduna-up: $f missing (iduna-vultr apply pushes it over SSH)" >&2; exit 1; }
done
touch /etc/iduna/iduna.env && chmod 600 /etc/iduna/iduna.env
if [ ! -d /opt/edge-src/.git ]; then
  git clone --depth 1 --branch "$EDGE_GIT_REF" "$EDGE_GIT_URL" /opt/edge-src
else
  git -C /opt/edge-src fetch --depth 1 origin "$EDGE_GIT_REF" && git -C /opt/edge-src reset --hard FETCH_HEAD
fi
docker build -q -t sc-tunnel:local -f /opt/edge-src/tools/sc_tunnel.Dockerfile /opt/edge-src
/usr/local/bin/okemily-deploy
cd /opt/iduna
docker compose pull -q iduna web logsync
docker compose up -d
for i in $(seq 1 60); do
  curl -fsS -o /dev/null http://127.0.0.1:8080/health && break
  [ "$i" = 60 ] && { docker compose logs --tail 50 iduna; echo "iduna-up: IDUNA never became healthy" >&2; exit 1; }
  sleep 5
done
# IDUNA reads var/webmaster.json at every start and seeds uid 0 only if absent. Once it is up the password has
# done its job; it does not stay on disk.
if [ -f /srv/iduna/var/webmaster.json ]; then
  docker compose logs iduna | grep -i 'webmaster' | tail -3 || true
  shred -u /srv/iduna/var/webmaster.json
fi
rm -f /etc/nginx/sites-enabled/iduna-boot
ln -sf /etc/nginx/sites-available/iduna /etc/nginx/sites-enabled/iduna
nginx -t && systemctl reload nginx
echo "sc-tunnel: $(docker compose logs sc-tunnel | grep -o 'pin [0-9a-f]*' | tail -1)"
echo "iduna-up: healthy"
EOF
chmod 755 /usr/local/bin/iduna-up

cat > /etc/nginx/snippets/iduna-proxy.conf <<'EOF'
proxy_http_version 1.1;
proxy_set_header Upgrade $http_upgrade;
proxy_set_header Connection $connection_upgrade;
proxy_set_header Host $host;
proxy_set_header X-Real-IP $remote_addr;
proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
proxy_set_header X-Forwarded-Proto https;
proxy_read_timeout 3600s;   # GKE GCPBackendPolicy timeoutSec 3600 (WebSockets)
proxy_send_timeout 3600s;
EOF

cat > /etc/nginx/sites-available/iduna <<'EOF'
map $http_upgrade $connection_upgrade { default upgrade; '' close; }
server {
    listen 80;
    listen [::]:80;
    server_name iam.{{.Zone}} {{.Zone}} www.{{.Zone}};
    return 301 https://$host$request_uri;
}
# iam.<zone>: all of IDUNA; "/" goes to the SSO login (92-iduna-routes.yaml).
server {
    listen 443 ssl http2;
    listen [::]:443 ssl http2;
    server_name iam.{{.Zone}};
    ssl_certificate     /etc/ssl/iduna/origin.pem;
    ssl_certificate_key /etc/ssl/iduna/origin.key;
    ssl_protocols TLSv1.2 TLSv1.3;
    client_max_body_size 4096m;   # NOCK_VIDEO_MAX_MB default
    location = / { return 302 /api/v1/auth/sso/login$is_args$args; }
    location / { proxy_pass http://127.0.0.1:8080; include snippets/iduna-proxy.conf; }
}
# <zone> + www: IDUNA's paths to IDUNA, everything else to the site (95-okemily-apex.yaml).
server {
    listen 443 ssl http2;
    listen [::]:443 ssl http2;
    server_name {{.Zone}} www.{{.Zone}};
    ssl_certificate     /etc/ssl/iduna/origin.pem;
    ssl_certificate_key /etc/ssl/iduna/origin.key;
    ssl_protocols TLSv1.2 TLSv1.3;
    client_max_body_size 4096m;
    location = /.well-known/jwks.json { proxy_pass http://127.0.0.1:8080; include snippets/iduna-proxy.conf; }
    location ~ ^/(api|services|admin|portal|q|nock)(/|$) { proxy_pass http://127.0.0.1:8080; include snippets/iduna-proxy.conf; }
    location /gfd-ws { return 503; }   # gfd-wsrelay is not up on Vultr yet (games come next)
    location /news { return 301 https://news.{{.Zone}}/; }
    location / { proxy_pass http://127.0.0.1:8090; include snippets/iduna-proxy.conf; }
}
EOF
# No cert until iduna-vultr pushes it, and nginx refuses a 443 server without one: until iduna-up enables the real
# site, port 80 only redirects (which is also what apply polls to know first boot is done).
cat > /etc/nginx/sites-available/iduna-boot <<'EOF'
server { listen 80 default_server; listen [::]:80 default_server; return 301 https://$host$request_uri; }
EOF
rm -f /etc/nginx/sites-enabled/default
ln -sf /etc/nginx/sites-available/iduna-boot /etc/nginx/sites-enabled/iduna-boot
nginx -t && systemctl enable --now nginx && systemctl reload nginx

ufw allow OpenSSH
ufw allow 80/tcp
ufw allow 443/tcp
ufw allow 8443/tcp   # sc-tunnel (note: Docker publishes ports past ufw anyway; listed so the intent is on record)
ufw --force enable
echo "iduna-vultr first boot done $(date -u +%FT%TZ)"
`

type udVars struct {
	Zone, Image                  string
	EdgeGitURL, EdgeGitRef       string
	OkemilyGitURL, OkemilyGitRef string
	DWLogsSrc                    string
	LogsyncSh                    string
}

// dwlogsPlaceholder is what logsync polls when IDUNA_DWLOGS_SRC is unset: an address that never resolves (.invalid
// is reserved, RFC 6761), so the sidecar logs one curl error per file every 30 s until DEADWEIGHT exists.
const dwlogsPlaceholder = "http://deadweight.invalid:8081"

func renderUserData(c config) (string, error) {
	t, err := template.New("ud").Parse(userDataTmpl)
	if err != nil {
		return "", err
	}
	src := c.DWLogsSrc
	if src == "" {
		src = dwlogsPlaceholder
	}
	sh := logsyncSh
	if !strings.HasSuffix(sh, "\n") {
		sh += "\n"
	}
	var b bytes.Buffer
	err = t.Execute(&b, udVars{
		Zone: c.Zone, Image: c.Image,
		EdgeGitURL: c.EdgeGitURL, EdgeGitRef: c.EdgeGitRef,
		OkemilyGitURL: c.OkemilyGitURL, OkemilyGitRef: c.OkemilyGitRef,
		DWLogsSrc: src, LogsyncSh: sh,
	})
	return b.String(), err
}
