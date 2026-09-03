# Sub2API Deployment Files

This directory contains files for deploying Sub2API on Linux servers and Apple-silicon Macs.

## Deployment Methods

| Method | Best For | Setup Wizard |
|--------|----------|--------------|
| **Docker Compose** | Quick setup, all-in-one | Not needed (auto-setup) |
| **Apple container** | Native local stack on macOS 26 | Not needed (auto-setup) |
| **Binary Install** | Production servers, systemd | Web-based wizard |

## Explicit Production Releases (Dedicated Server)

Ordinary pushes and tags do not start GitHub Actions. CI, security scans,
artifact releases, and production deployment are all manually dispatched.
The production workflow checks out the exact fork `main` commit on a
GitHub-hosted runner, builds one `linux/amd64` Docker image, and streams a
zstd-compressed Docker archive through the restricted deploy SSH key.
The runner hashes the exact compressed archive before upload, and production
verifies that digest before loading it. Docker/containerd may assign different
local image and config IDs both during `docker save` and again during
`docker load`, so those daemon-local IDs are recorded for diagnostics but are
not treated as cross-host transport identities. Production separately verifies
the loaded image's platform plus source, revision, and version labels.

The production host does not check out source, download build dependencies, or
compile the application in the normal path. Its receiver enforces a compressed
upload size limit, verifies the archive digest and tag, the `linux/amd64`
platform, and OCI source/revision/version labels, then passes the verified local
image to the existing blue-green release helper. A failed identity check,
health check, or traffic switch never replaces the active color.

Official upstream changes are deliberately merged into fork `main` by a
maintainer first. The release server does not poll or merge the official
upstream itself.

Install the root-owned receiver, release service, and runtime recovery guard on
the production host:

```bash
sudo deploy/install-autodeploy.sh \
  --production-branch main \
  --production-repo https://github.com/Turtle-Li/sub2api.git

/opt/sub2api/scripts/sub2api-autodeploy.sh --check
journalctl -u sub2api-autodeploy.service -n 100 --no-pager
systemctl status sub2api-runtime-guard.timer --no-pager
```

Run the manual `CI` workflow first when GitHub-hosted verification is desired,
then explicitly dispatch `.github/workflows/sub2api-production-deploy.yml` from
`main`. The workflow refuses other refs and aborts if `main` advances while the
image is building. It requires the repository secrets
`SUB2API_DEPLOY_SSH_KEY`, `SUB2API_DEPLOY_HOST`,
`SUB2API_DEPLOY_PORT`, `SUB2API_DEPLOY_USER`, and
`SUB2API_DEPLOY_KNOWN_HOSTS`. The live configuration is
`/etc/sub2api-autodeploy.env`, and server logs are stored in
`/var/log/sub2api-release/`. Build cache remains on GitHub Actions; no image
registry credentials are needed because the archive is transferred directly.

For a DNS-only multi-origin API, install each node with
`--health-resolve api.turtleligpt.com:443:NODE_PUBLIC_IPV4`. This makes release
and runtime-recovery health checks verify the local origin with the production
hostname and certificate instead of following a DNS answer to a peer.

The production helper recognizes `sub2api-blue`, `sub2api-green`, and the
legacy `sub2api` application name. Long-lived Responses WebSocket connections
can keep an old color draining after a release, so the helper resolves the
active color from Caddy and selects only an absent or stopped target. A later
release fails closed while any inactive application container is still
running, because every application container also consumes shared background
queues even when Caddy sends it no HTTP traffic. Let the drain monitor stop the
old color, or verify that it has zero active connections before stopping it.
After the switch passes all rollback gates, the release helper launches that
monitor as an independent transient systemd unit. This is required because the
automatic release service is `Type=oneshot`: a plain `nohup` child is still
killed with the service cgroup when the release command exits. The unit name is
recorded in the release log directory as `drain-unit.name`, and its output is
written to `drain-monitor.log`.
`SUB2API_RELEASE_ALLOW_PREEXISTING_DRAINING_CONTAINER=true` is an emergency
override for deployments where background queues are disabled or the operator
has separately fenced their consumers; it should not be enabled on normal
Sub2API production hosts.

On a dual-node rollback or canary origin that must continue serving requests
without consuming shared background work, set
`SUB2API_RELEASE_BACKGROUND_MODE=preserve-standby` for the audited release
invocation. The helper requires the current generation to be
`traffic=accepting ... background=standby`, records that final state in the
durable local transaction, and keeps the selected generation standby on
commit, rollback, and crash recovery. The default `activate` mode retains the
normal single-owner transfer behavior. Before intentionally transferring
background ownership to this node, restore `activate` and use the coordinated
cross-node ownership runbook; do not leave `preserve-standby` as an accidental
permanent override.

For the fixed-egress cache migration only,
`SUB2API_RELEASE_FIXED_EGRESS_COMPATIBILITY_MODE` controls the application
environment written into the new blue-green generation. Valid values are
`preserve` (ordinary default), `true` (Phase A: old-writer-compatible, no
account retirement fences), and `false` (normal-final: strict fixed-egress
validation and permanent fences enabled). Prepared-target validation rejects a
container created for the opposite explicit mode or for a mode different from
the active source when `preserve` is used; an absent preserve source is rejected.
The Phase-A application also rejects proxy CAS at both service and repository
boundaries. Use the same immutable image for both stages and follow the ordered
inventory/fence/CAS gates in
`../docs/operations/SUB2_DUAL_NODE_RUNTIME_CERT_EGRESS.md`.

### GCP Taiwan Premium transport ingress

The retained GCP Taiwan Premium address now runs the approved HAProxy
transport-only ingress for isolated explicit-IP canaries. It is not an
application, database, worker, or OAuth-egress node, and production DNS remains
on the old origin. Its exact resource identity, three-carrier evidence,
versioned configuration, live validation, rollback, and remaining cutover
gates are recorded in
[`gcp-taiwan-line/README.md`](gcp-taiwan-line/README.md). Do not install another
line protocol, release the tested address, add runtime credentials, or change
traffic/DNS as part of an ordinary application release.

### Runtime recovery and historical fallback

`install-autodeploy.sh` enables `sub2api-runtime-guard.timer` by default. The
timer runs 30 seconds after boot and 30 seconds after each completed check. It
does not build, pull, or create application containers. Every run acquires the
same `/run/sub2api-maintenance/sub2api-maintenance.lock` used by production
releases, then:

1. starts and verifies PostgreSQL, Redis, and Caddy, restarting a dependency
   once if it remains unhealthy;
2. resolves the active application from the host Caddyfile and requires the
   host file, Caddy startup file, and live Admin API to agree;
3. starts or restarts that active container and verifies its internal and
   public health endpoints;
4. if the active slot cannot recover, stops it before starting a stopped
   historical slot, or promotes the single healthy old slot already draining;
5. delegates the traffic change to the audited blue-green helper, then verifies
   Docker health, the three Caddy views, and the public health endpoint.

The failed active and historical fallback are never intentionally kept running
as two healthy queue consumers. Ambiguous Caddy state, multiple running old
slots, a missing historical container, OOM/non-zero historical exits, or lock
contention all fail closed. Lock contention is a successful no-op because a
release or other planned maintenance owns the runtime at that moment.
The drain monitor also takes this lock around its final Caddy revalidation and
`docker stop`, so it cannot stop an old slot while the guard is promoting it.

A retained `.sub2api-blue-green-caddy-transaction.env` is intentionally not a
timer no-op: the runtime guard fails visibly on every scheduled run and makes
no lifecycle change until the blue-green helper recovers the transaction. Run
that helper once with the same root-owned release environment; a successful
recovery restores the host, container-startup, and live Caddy views, clears the
transaction, and exits non-zero to require a clean coordinator rerun. Never
delete the transaction file by hand or suppress the repeated service failure.

The certificate receiver and drain monitor are intentionally not Caddyfile
transaction writers. The receiver validates and reloads a rendered stream
without modifying the bound startup file; the drain monitor only revalidates
the selected generation and stops an inactive container under the maintenance
lock. Their lack of the three Caddy transaction fences is therefore an audited
exemption, not an alternate mutation path.

Use the guard for emergency recovery instead of guessing a Compose service or
container color:

```bash
sudo systemctl start sub2api-runtime-guard.service
sudo journalctl -u sub2api-runtime-guard.service -n 100 --no-pager
```

Any manual database cutover or application/Caddy lifecycle command must also
hold `/run/sub2api-maintenance/sub2api-maintenance.lock`. A production release
acquires it internally. Do not wrap the release command in a second `flock`
invocation.

### Shared maintenance-lock safety

The installer and every lock consumer create the `/run/sub2api-maintenance`
parent as `root:root` mode `0700`, then create the shared lock as a one-link
`root:root` mode `0600` regular file. Symlinks, non-regular files, unexpected
owners, permissive modes, and hard links fail closed before `flock`; a valid
already-held lock retains the caller's documented busy/no-op behavior. Shell
redirection has no native `O_NOFOLLOW`, so the implementation checks the
private parent and lock metadata both before and after opening, and confirms
the pathname matches the open descriptor inode. This prevents unprivileged
replacement races; root remains trusted. Existing configurations that still
name `/run/lock/sub2api-maintenance.lock` must be migrated with
`install-autodeploy.sh --replace-config`: before it writes configuration,
scripts, or systemd units, the installer non-blockingly holds both that exact
legacy inode and the canonical private lock through exit. A held legacy lock
therefore fails without changing deployment state. Production accepts no
`SUB2API_MAINTENANCE_LOCK_FILE` override; the caller-owned alternate-path
switch is restricted to hermetic deployment tests. A short-lived supervisor,
rather than `install` or `systemctl` descendants, owns the fence descriptors,
so a persistent child cannot keep either lock busy after installation exits.

GitHub workflow concurrency serializes production runs. The receiver also holds
an exclusive upload/release lock and fails closed if another release is in
progress. The archive defaults to a 1 GiB compressed size limit, configurable
with `SUB2API_GITHUB_IMAGE_MAX_BYTES`.

Install the dedicated forced-command account before adding the key to GitHub:

```bash
sudo deploy/install-github-deploy-trigger.sh \
  --public-key-file /path/to/sub2api-github-deploy.pub
```

The account has no interactive shell access. Its key accepts only the validated
`deploy-image COMMIT VERSION ARCHIVE_DIGEST` protocol and cannot start the legacy
source-build service or run arbitrary SSH commands.

`sub2api-autodeploy.timer` is disabled by default. It can be explicitly used
by a root operator as a source-build recovery fallback with
`sudo deploy/install-autodeploy.sh --enable-timer`, but it is not part of the
normal production path.

## Files

| File | Description |
|------|-------------|
| `docker-compose.yml` | Docker Compose configuration (named volumes) |
| `docker-compose.local.yml` | Docker Compose configuration (local directories, easy migration) |
| `docker-deploy.sh` | **One-click Docker deployment script (recommended)** |
| `install-github-deploy-trigger.sh` | Installs the restricted GitHub Actions deploy-key account |
| `sub2api-github-deploy-trigger.sh` | Forced SSH command that validates the deploy protocol |
| `sub2api-github-image-release.sh` | Validates and loads a GitHub-built image before blue-green release |
| `sub2api-server-release.sh` | Runs preflight, blue-green switch, verification, rollback, and draining |
| `sub2api-drain-monitor.sh` | Waits for a drained slot to become idle and stops it under the maintenance lock |
| `sub2api-maintenance-lock.sh` | Validates and opens the private shared maintenance lock for root-owned helpers |
| `sub2api-runtime-guard.sh` | Recovers dependencies/active slot and safely falls back to a historical slot |
| `sub2api-runtime-guard.service` | Root-owned one-shot runtime recovery service |
| `sub2api-runtime-guard.timer` | Runs the runtime guard every 30 seconds |
| `sub2api-autodeploy.sh` | Legacy source-preparation recovery controller |
| `apple-container.sh` | Native Apple `container` lifecycle script |
| `APPLE_CONTAINER.md` | Apple `container` deployment and operations guide |
| `.env.example` | Container environment variables template |
| `DOCKER.md` | Docker Hub documentation |
| `install.sh` | One-click binary installation script |
| `install-datamanagementd.sh` | datamanagementd 一键安装脚本 |
| `sub2api.service` | Systemd service unit file |
| `sub2api-datamanagementd.service` | datamanagementd systemd service unit file |
| `DATAMANAGEMENTD_CN.md` | datamanagementd 部署与联动说明（中文） |
| `config.example.yaml` | Example configuration file |
| `EDGE_SECURITY.md` | Reverse proxy, CDN/WAF, trusted proxy, and ingress hardening guide |

---

## Apple container Deployment

Apple-silicon Macs running macOS 26 can run the complete Sub2API, PostgreSQL, and Redis stack with Apple `container` 1.1.0 or newer:

```bash
./apple-container.sh init
./apple-container.sh up
./apple-container.sh status
./apple-container.sh logs app -f
```

The script uses Apple named volumes, starts dependencies in order, and performs live readiness checks. It does not provide a continuous restart supervisor; run `./apple-container.sh up` after a host reboot. Docker Compose remains the recommended production deployment path.

See [APPLE_CONTAINER.md](./APPLE_CONTAINER.md) for configuration, upgrades, persistence, networking behavior, and limitations.

---

## Docker Deployment (Recommended)

### Method 1: One-Click Deployment (Recommended)

Use the automated preparation script for the easiest setup:

```bash
# Download and run the preparation script
curl -sSL https://raw.githubusercontent.com/Wei-Shaw/sub2api/main/deploy/docker-deploy.sh | bash

# Or download first, then run
curl -sSL https://raw.githubusercontent.com/Wei-Shaw/sub2api/main/deploy/docker-deploy.sh -o docker-deploy.sh
chmod +x docker-deploy.sh
./docker-deploy.sh
```

**What the script does:**
- Downloads `docker-compose.local.yml` and `.env.example`
- Automatically generates secure secrets (JWT_SECRET, TOTP_ENCRYPTION_KEY, POSTGRES_PASSWORD)
- Creates `.env` file with generated secrets
- Creates necessary data directories (data/, postgres_data/, redis_data/)
- **Displays generated credentials** (POSTGRES_PASSWORD, JWT_SECRET, etc.)

**After running the script:**
```bash
# Start services
docker compose -f docker-compose.local.yml up -d

# View logs
docker compose -f docker-compose.local.yml logs -f sub2api

# If admin password was auto-generated, find it in logs:
docker compose -f docker-compose.local.yml logs sub2api | grep "admin password"

# Access Web UI
# http://localhost:8080
```

### Method 2: Manual Deployment

If you prefer manual control:

```bash
# Clone repository
git clone https://github.com/Wei-Shaw/sub2api.git
cd sub2api/deploy

# Configure environment
cp .env.example .env
chmod 600 .env
nano .env  # Set POSTGRES_PASSWORD and other required variables

# Generate secure secrets (recommended)
JWT_SECRET=$(openssl rand -hex 32)
TOTP_ENCRYPTION_KEY=$(openssl rand -hex 32)
echo "JWT_SECRET=${JWT_SECRET}" >> .env
echo "TOTP_ENCRYPTION_KEY=${TOTP_ENCRYPTION_KEY}" >> .env

# Create data directories
mkdir -p data postgres_data redis_data

# Start all services using local directory version
docker compose -f docker-compose.local.yml up -d

# View logs (check for auto-generated admin password)
docker compose -f docker-compose.local.yml logs -f sub2api

# Access Web UI
# http://localhost:8080
```

### Deployment Version Comparison

| Version | Data Storage | Migration | Best For |
|---------|-------------|-----------|----------|
| **docker-compose.local.yml** | Local directories (./data, ./postgres_data, ./redis_data) | ✅ Easy (tar entire directory) | Production, need frequent backups/migration |
| **docker-compose.yml** | Named volumes (/var/lib/docker/volumes/) | ⚠️ Requires docker commands | Simple setup, don't need migration |

**Recommendation:** Use `docker-compose.local.yml` (deployed by `docker-deploy.sh`) for easier data management and migration.

### How Auto-Setup Works

When using Docker Compose with `AUTO_SETUP=true`:

1. On first run, the system automatically:
   - Connects to PostgreSQL and Redis
   - Applies database migrations (SQL files in `backend/migrations/*.sql`) and records them in `schema_migrations`
   - Generates JWT secret (if not provided)
   - Creates admin account (password auto-generated if not provided)
   - Writes config.yaml

2. No manual Setup Wizard needed - just configure `.env` and start

3. If `ADMIN_PASSWORD` is not set, check logs for the generated password:
   ```bash
   docker compose logs sub2api | grep "admin password"
   ```

### Startup and Database Recovery

Sub2API applies database migrations during application startup. PostgreSQL can
remain in its recovery/startup phase briefly after a host or Docker daemon
restart. The application retries transient PostgreSQL startup and connection
errors with bounded exponential backoff, then starts automatically when the
database becomes ready. Authentication errors, migration checksum mismatches,
SQL errors, and other permanent configuration or data errors fail immediately.

The Compose example also uses a PostgreSQL health check that verifies both
server readiness and a simple SQL query. `depends_on: condition: service_healthy`
controls dependency ordering for a fresh Compose start, but it is not a
replacement for application-level retries when Docker restores existing
containers after a host restart.

For systemd deployments, keep `Restart=always` and `RestartSec` configured in
`sub2api.service`; the application retry covers transient database startup,
while systemd remains the supervisor for permanent process exits. For
Kubernetes, use a PostgreSQL readiness probe and retain the Sub2API startup
retry behavior; configure the application liveness probe separately so a
database recovery period is not treated as a permanent process failure.

### Database Migration Notes (PostgreSQL)

- Migrations are applied in lexicographic order (e.g. `001_...sql`, `002_...sql`).
- `schema_migrations` tracks applied migrations (filename + checksum).
- Migrations are forward-only; rollback requires a DB backup restore or a manual compensating SQL script.

**Verify `users.allowed_groups` → `user_allowed_groups` backfill**

During the incremental GORM→Ent migration, `users.allowed_groups` (legacy `BIGINT[]`) is being replaced by a normalized join table `user_allowed_groups(user_id, group_id)`.

Run this query to compare the legacy data vs the join table:

```sql
WITH old_pairs AS (
  SELECT DISTINCT u.id AS user_id, x.group_id
  FROM users u
  CROSS JOIN LATERAL unnest(u.allowed_groups) AS x(group_id)
  WHERE u.allowed_groups IS NOT NULL
)
SELECT
  (SELECT COUNT(*) FROM old_pairs)           AS old_pair_count,
  (SELECT COUNT(*) FROM user_allowed_groups) AS new_pair_count;
```

### datamanagementd（数据管理）联动

如需启用管理后台“数据管理”功能，请额外部署宿主机 `datamanagementd`：

- 主进程固定探测 `/tmp/sub2api-datamanagement.sock`
- Docker 场景下需把宿主机 Socket 挂载到容器内同路径
- 详细步骤见：`deploy/DATAMANAGEMENTD_CN.md`

### Commands

For **local directory version** (docker-compose.local.yml):

```bash
# Start services
docker compose -f docker-compose.local.yml up -d

# Stop services
docker compose -f docker-compose.local.yml down

# View logs
docker compose -f docker-compose.local.yml logs -f sub2api

# Restart Sub2API only
docker compose -f docker-compose.local.yml restart sub2api

# Update to latest version
docker compose -f docker-compose.local.yml pull
docker compose -f docker-compose.local.yml up -d

# Remove all data (caution!)
docker compose -f docker-compose.local.yml down
rm -rf data/ postgres_data/ redis_data/
```

For **named volumes version** (docker-compose.yml):

```bash
# Start services
docker compose up -d

# Stop services
docker compose down

# View logs
docker compose logs -f sub2api

# Restart Sub2API only
docker compose restart sub2api

# Update to latest version
docker compose pull
docker compose up -d

# Remove all data (caution!)
docker compose down -v
```

### Environment Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `POSTGRES_PASSWORD` | **Yes** | - | PostgreSQL password |
| `JWT_SECRET` | **Recommended** | *(auto-generated)* | JWT secret (fixed for persistent sessions) |
| `TOTP_ENCRYPTION_KEY` | **Recommended** | *(auto-generated)* | TOTP encryption key (fixed for persistent 2FA) |
| `SERVER_PORT` | No | `8080` | Server port |
| `ADMIN_EMAIL` | No | `admin@sub2api.local` | Admin email |
| `ADMIN_PASSWORD` | No | *(auto-generated)* | Admin password |
| `TZ` | No | `Asia/Shanghai` | Timezone |
| `UPDATE_GITHUB_TOKEN` | No | *(empty)* | Token for `api.github.com` release checks only; asset downloads remain anonymous. |
| `GEMINI_OAUTH_CLIENT_ID` | No | *(builtin)* | Google OAuth client ID (Gemini OAuth). Leave empty to use the built-in Gemini CLI client. |
| `GEMINI_OAUTH_CLIENT_SECRET` | No | *(builtin)* | Google OAuth client secret (Gemini OAuth). Leave empty to use the built-in Gemini CLI client. |
| `GEMINI_OAUTH_SCOPES` | No | *(default)* | OAuth scopes (Gemini OAuth) |
| `GEMINI_QUOTA_POLICY` | No | *(empty)* | JSON overrides for Gemini local quota simulation (Code Assist only). |

See `.env.example` for all available options.

> **Note:** The `docker-deploy.sh` script automatically generates `JWT_SECRET`, `TOTP_ENCRYPTION_KEY`, and `POSTGRES_PASSWORD` for you.

### Easy Migration (Local Directory Version)

When using `docker-compose.local.yml`, all data is stored in local directories, making migration simple:

```bash
# On source server: Stop services and create archive
cd /path/to/deployment
docker compose -f docker-compose.local.yml down
cd ..
tar czf sub2api-complete.tar.gz deployment/

# Transfer to new server
scp sub2api-complete.tar.gz user@new-server:/path/to/destination/

# On new server: Extract and start
tar xzf sub2api-complete.tar.gz
cd deployment/
docker compose -f docker-compose.local.yml up -d
```

Your entire deployment (configuration + data) is migrated!

---

## Gemini OAuth Configuration

Sub2API supports three methods to connect to Gemini:

### Method 1: Code Assist OAuth (Recommended for GCP Users)

**No configuration needed** - always uses the built-in Gemini CLI OAuth client (public).

1. Leave `GEMINI_OAUTH_CLIENT_ID` and `GEMINI_OAUTH_CLIENT_SECRET` empty
2. In the Admin UI, create a Gemini OAuth account and select **"Code Assist"** type
3. Complete the OAuth flow in your browser

> Note: Even if you configure `GEMINI_OAUTH_CLIENT_ID` / `GEMINI_OAUTH_CLIENT_SECRET` for AI Studio OAuth,
> Code Assist OAuth will still use the built-in Gemini CLI client.

**Requirements:**
- Google account with access to Google Cloud Platform
- A GCP project (auto-detected or manually specified)

**How to get Project ID (if auto-detection fails):**
1. Go to [Google Cloud Console](https://console.cloud.google.com/)
2. Click the project dropdown at the top of the page
3. Copy the Project ID (not the project name) from the list
4. Common formats: `my-project-123456` or `cloud-ai-companion-xxxxx`

### Method 2: AI Studio OAuth (For Regular Google Accounts)

Requires your own OAuth client credentials.

**Step 1: Create OAuth Client in Google Cloud Console**

1. Go to [Google Cloud Console - Credentials](https://console.cloud.google.com/apis/credentials)
2. Create a new project or select an existing one
3. **Enable the Generative Language API:**
   - Go to "APIs & Services" → "Library"
   - Search for "Generative Language API"
   - Click "Enable"
4. **Configure OAuth Consent Screen** (if not done):
   - Go to "APIs & Services" → "OAuth consent screen"
   - Choose "External" user type
   - Fill in app name, user support email, developer contact
   - Add scopes: `https://www.googleapis.com/auth/generative-language.retriever` (and optionally `https://www.googleapis.com/auth/cloud-platform`)
   - Add test users (your Google account email)
5. **Create OAuth 2.0 credentials:**
   - Go to "APIs & Services" → "Credentials"
   - Click "Create Credentials" → "OAuth client ID"
   - Application type: **Web application** (or **Desktop app**)
   - Name: e.g., "Sub2API Gemini"
   - Authorized redirect URIs: Add `http://localhost:1455/auth/callback`
6. Copy the **Client ID** and **Client Secret**
7. **⚠️ Publish to Production (IMPORTANT):**
   - Go to "APIs & Services" → "OAuth consent screen"
   - Click "PUBLISH APP" to move from Testing to Production
   - **Testing mode limitations:**
     - Only manually added test users can authenticate (max 100 users)
     - Refresh tokens expire after 7 days
     - Users must be re-added periodically
   - **Production mode:** Any Google user can authenticate, tokens don't expire
   - Note: For sensitive scopes, Google may require verification (demo video, privacy policy)

**Step 2: Configure Environment Variables**

```bash
GEMINI_OAUTH_CLIENT_ID=your-client-id.apps.googleusercontent.com
GEMINI_OAUTH_CLIENT_SECRET=GOCSPX-your-client-secret

# 可选：如需使用 Gemini CLI 内置 OAuth Client（Code Assist / Google One）
# 安全说明：本仓库不会内置该 client_secret，请在运行环境通过环境变量注入。
# GEMINI_CLI_OAUTH_CLIENT_SECRET=GOCSPX-your-built-in-secret
```

**Step 3: Create Account in Admin UI**

1. Create a Gemini OAuth account and select **"AI Studio"** type
2. Complete the OAuth flow
   - After consent, your browser will be redirected to `http://localhost:1455/auth/callback?code=...&state=...`
   - Copy the full callback URL (recommended) or just the `code` and paste it back into the Admin UI

### Method 3: API Key (Simplest)

1. Go to [Google AI Studio](https://aistudio.google.com/app/apikey)
2. Click "Create API key"
3. In Admin UI, create a Gemini **API Key** account
4. Paste your API key (starts with `AIza...`)

### Comparison Table

| Feature | Code Assist OAuth | AI Studio OAuth | API Key |
|---------|-------------------|-----------------|---------|
| Setup Complexity | Easy (no config) | Medium (OAuth client) | Easy |
| GCP Project Required | Yes | No | No |
| Custom OAuth Client | No (built-in) | Yes (required) | N/A |
| Rate Limits | GCP quota | Standard | Standard |
| Best For | GCP developers | Regular users needing OAuth | Quick testing |

---

## Binary Installation

For production servers using systemd.

### One-Line Installation

```bash
curl -sSL https://raw.githubusercontent.com/Wei-Shaw/sub2api/main/deploy/install.sh | sudo bash
```

### Manual Installation

1. Download the latest release from [GitHub Releases](https://github.com/Wei-Shaw/sub2api/releases)
2. Extract and copy the binary to `/opt/sub2api/`
3. Copy `sub2api.service` to `/etc/systemd/system/`
4. Run:
   ```bash
   sudo systemctl daemon-reload
   sudo systemctl enable sub2api
   sudo systemctl start sub2api
   ```
5. Open the Setup Wizard in your browser to complete configuration

### Commands

```bash
# Install
sudo ./install.sh

# Upgrade
sudo ./install.sh upgrade

# Uninstall
sudo ./install.sh uninstall
```

### Service Management

```bash
# Start the service
sudo systemctl start sub2api

# Stop the service
sudo systemctl stop sub2api

# Restart the service
sudo systemctl restart sub2api

# Check status
sudo systemctl status sub2api

# View logs
sudo journalctl -u sub2api -f

# Enable auto-start on boot
sudo systemctl enable sub2api
```

### Configuration

#### Server Address and Port

During installation, you will be prompted to configure the server listen address and port. These settings are stored in the systemd service file as environment variables.

To change after installation:

1. Edit the systemd service:
   ```bash
   sudo systemctl edit sub2api
   ```

2. Add or modify:
   ```ini
   [Service]
   Environment=SERVER_HOST=0.0.0.0
   Environment=SERVER_PORT=3000
   ```

3. Reload and restart:
   ```bash
   sudo systemctl daemon-reload
   sudo systemctl restart sub2api
   ```

#### Gemini OAuth Configuration

If you need to use AI Studio OAuth for Gemini accounts, add the OAuth client credentials to the systemd service file:

1. Edit the service file:
   ```bash
   sudo nano /etc/systemd/system/sub2api.service
   ```

2. Add your OAuth credentials in the `[Service]` section (after the existing `Environment=` lines):
   ```ini
   Environment=GEMINI_OAUTH_CLIENT_ID=your-client-id.apps.googleusercontent.com
   Environment=GEMINI_OAUTH_CLIENT_SECRET=GOCSPX-your-client-secret
   ```

   如需使用“内置 Gemini CLI OAuth Client”（Code Assist / Google One），还需要注入：
   ```ini
   Environment=GEMINI_CLI_OAUTH_CLIENT_SECRET=GOCSPX-your-built-in-secret
   ```

3. Reload and restart:
   ```bash
   sudo systemctl daemon-reload
   sudo systemctl restart sub2api
   ```

> **Note:** Code Assist OAuth does not require any configuration - it uses the built-in Gemini CLI client.
> See the [Gemini OAuth Configuration](#gemini-oauth-configuration) section above for detailed setup instructions.

#### Application Configuration

The main config file is at `/etc/sub2api/config.yaml` (created by Setup Wizard).

### Prerequisites

- Linux server (Ubuntu 20.04+, Debian 11+, CentOS 8+, etc.)
- PostgreSQL 14+
- Redis 6+
- systemd

### Directory Structure

```
/opt/sub2api/
├── sub2api              # Main binary
├── sub2api.backup       # Backup (after upgrade)
└── data/                # Runtime data

/etc/sub2api/
└── config.yaml          # Configuration file
```

---

## Troubleshooting

### Docker

For **local directory version**:

```bash
# Check container status
docker compose -f docker-compose.local.yml ps

# View detailed logs
docker compose -f docker-compose.local.yml logs --tail=100 sub2api

# Check database connection
docker compose -f docker-compose.local.yml exec postgres pg_isready

# Check Redis connection
docker compose -f docker-compose.local.yml exec redis redis-cli ping

# Restart all services
docker compose -f docker-compose.local.yml restart

# Check data directories
ls -la data/ postgres_data/ redis_data/
```

For **named volumes version**:

```bash
# Check container status
docker compose ps

# View detailed logs
docker compose logs --tail=100 sub2api

# Check database connection
docker compose exec postgres pg_isready

# Check Redis connection
docker compose exec redis redis-cli ping

# Restart all services
docker compose restart
```

### Binary Install

```bash
# Check service status
sudo systemctl status sub2api

# View recent logs
sudo journalctl -u sub2api -n 50

# Check config file
sudo cat /etc/sub2api/config.yaml

# Check PostgreSQL
sudo systemctl status postgresql

# Check Redis
sudo systemctl status redis
```

### Common Issues

1. **Port already in use**: Change `SERVER_PORT` in `.env` or systemd config
2. **Database connection failed**: Check PostgreSQL is running and credentials are correct
3. **Redis connection failed**: Check Redis is running and password is correct
4. **Permission denied**: Ensure proper file ownership for binary install

---

## TLS Fingerprint Configuration

Sub2API supports TLS fingerprint simulation to make requests appear as if they come from the official Claude CLI (Node.js client).

> **💡 Tip:** Visit **[tls.sub2api.org](https://tls.sub2api.org/)** to get TLS fingerprint information for different devices and browsers.

### Default Behavior

- Built-in `claude_cli_v2` profile simulates Node.js 20.x + OpenSSL 3.x
- JA3 Hash: `1a28e69016765d92e3b381168d68922c`
- JA4: `t13d5911h1_a33745022dd6_1f22a2ca17c4`
- Profile selection: `accountID % profileCount`

### Configuration

```yaml
gateway:
  tls_fingerprint:
    enabled: true  # Global switch
    profiles:
      # Simple profile (uses default cipher suites)
      profile_1:
        name: "Profile 1"

      # Profile with custom cipher suites (use compact array format)
      profile_2:
        name: "Profile 2"
        cipher_suites: [4866, 4867, 4865, 49199, 49195, 49200, 49196]
        curves: [29, 23, 24]
        point_formats: 0

      # Another custom profile
      profile_3:
        name: "Profile 3"
        cipher_suites: [4865, 4866, 4867, 49199, 49200]
        curves: [29, 23, 24, 25]
```

### Profile Fields

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Display name (required) |
| `cipher_suites` | []uint16 | Cipher suites in decimal. Empty = default |
| `curves` | []uint16 | Elliptic curves in decimal. Empty = default |
| `point_formats` | []uint8 | EC point formats. Empty = default |

### Common Values Reference

**Cipher Suites (TLS 1.3):** `4865` (AES_128_GCM), `4866` (AES_256_GCM), `4867` (CHACHA20)

**Cipher Suites (TLS 1.2):** `49195`, `49196`, `49199`, `49200` (ECDHE variants)

**Curves:** `29` (X25519), `23` (P-256), `24` (P-384), `25` (P-521)
