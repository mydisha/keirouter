# Deploy KeiRouter

KeiRouter can run as a local binary, a single Docker container with SQLite, or a
container plus Postgres for team/VPS deployments.

## Quick Start (Pre-built Docker Image)

Pull and run the latest public image from GitHub Container Registry:

```bash
docker pull ghcr.io/mydisha/keirouter:latest

# Run with docker compose (recommended)
docker compose up -d
```

Or run directly with Docker:

```bash
docker run -d \
  --name keirouter \
  -p 20180:20180 \
  -v keirouter-data:/data \
  ghcr.io/mydisha/keirouter:latest
```

Available tags:
- `ghcr.io/mydisha/keirouter:latest` — latest stable from `main`
- `ghcr.io/mydisha/keirouter:1.2.3` — specific version
- `ghcr.io/mydisha/keirouter:sha-abc1234` — pinned to a commit

## Local Development (One-Liner)

Run this single command — it clones, installs deps, and starts everything:

```bash
curl -fsSL https://raw.githubusercontent.com/mydisha/keirouter/main/scripts/quickstart.sh | bash
```

No `.env`, no config, no manual steps. It will:
- Check Go 1.24+ and Node.js 20+ are installed
- Clone the repo to `~/keirouter` (or use existing checkout)
- Install frontend dependencies (npm ci)
- Download Go modules
- Start backend (:20180) and dashboard (:5180) with hot reload

Dashboard: http://localhost:5180 (password: `keirouter`)

> **Already have the repo?** Just run `make setup` from the project root.

## Local Install (Binary)

Download the prebuilt binary + dashboard assets for this OS/CPU (macOS or Linux, amd64 or arm64) and install them system-wide. No Go or Node.js needed:

```bash
curl -fsSL https://raw.githubusercontent.com/mydisha/keirouter/main/scripts/install.sh | bash
keirouter start
```

Add `-s -- --service` on Linux to run it as a systemd user service that starts on boot, or `-s -- --source` to build from source instead (Go 1.26+, Node.js 20.19+).

If you prefer Docker:

```bash
curl -fsSL https://raw.githubusercontent.com/mydisha/keirouter/main/scripts/install.sh | bash -s -- --docker
```

## VPS Deployment Guide

### Option 1: VPS with Docker Compose (SQLite - Default)

This is the simplest way to get KeiRouter running on a clean VPS (Ubuntu/Debian).

**1. Clone the repository**
```bash
git clone https://github.com/mydisha/keirouter.git
cd keirouter
```

**2. Configure Environment Variables**
```bash
cp .env.example .env
```
Open `.env` with your favorite editor (e.g., `nano .env`). You **must** generate a secure 32-byte master key and set it as `KEIROUTER_MASTER_KEY`:
```bash
# Generate a key locally:
openssl rand -base64 32
```
Paste the generated key into your `.env` file.

**3. Start the Deployment**
```bash
docker compose up -d --build
```

**4. View Logs**
```bash
docker compose logs -f keirouter
```

**5. Access the Dashboard**
By default, KeiRouter will be available at `http://YOUR_VPS_IP:20180`. KeiRouter automatically trusts `X-Forwarded-Proto` from TLS-terminating reverse proxies by default, so cookies will be marked Secure only when you actually serve HTTPS through nginx/Caddy/Traefik. The dashboard session cookie remains usable over plain HTTP for local-only deployments (see issue #56).

It is highly recommended to put KeiRouter behind a reverse proxy like **Nginx**, **Caddy**, or **Traefik** to secure it with HTTPS and a custom domain.

### Option 2: VPS with Postgres

If you are deploying for a team or expecting high traffic, you can use Postgres instead of SQLite.

**1. Prepare `.env`**
Follow the steps above to create your `.env` file, but make sure you also set the database password:
```env
POSTGRES_PASSWORD=your_secure_postgres_password
```

**2. Start the Deployment**
Use the override compose file to start both KeiRouter and Postgres:
```bash
docker compose -f compose.yaml -f compose.postgres.yaml up -d --build
```
*Note: The app container still stores runtime secrets and generated files in a Docker volume mounted at `/data`, while request/account data is stored in the Postgres database.*

## Coolify Deployment Guide

Deploying KeiRouter on [Coolify](https://coolify.io/) is highly recommended as it automates SSL/TLS certificates and reverse proxy configuration.

### Deployment Steps

1. **Create a New Resource**: In your Coolify dashboard, create a new resource and select **Project** -> **Environment** -> **Add New Resource**.
2. **Select Source**: Choose **Git Repository** (Public) and enter:
   - **Repository URL**: `https://github.com/mydisha/keirouter`
   - **Branch**: `main`
3. **Build Pack**: Select **Docker Compose** as the build pack. Pastikan Anda mengatur **Docker Compose Location** (atau *Compose file path*) ke `compose.yaml` karena Coolify mungkin mencari `docker-compose.yaml` secara default.
4. **Configuration**:
   - **Domains**: Enter your custom domain (e.g., `https://keirouter.yourdomain.com`). Coolify will automatically map this to the exposed port.
   - **Port**: Make sure the container port is set to `20180` (this is the port KeiRouter listens on inside the container).
5. **Environment Variables**:
   Navigate to the Environment Variables tab in Coolify and add the following variables (Switch to Developer view to edit as text):
   ```env
   KEIROUTER_SERVER__HOST=0.0.0.0
   KEIROUTER_SERVER__PORT=20180
   KEIROUTER_SECURITY__BIND_LOOPBACK_ONLY=false
   # Coolify terminates TLS and forwards via X-Forwarded-Proto; trusting it
   # (the default) makes the dashboard session cookie Secure over HTTPS.
   KEIROUTER_SECURITY__TRUST_FORWARDED_HEADERS=true
   # Generate a 32-byte base64 key locally and paste it here:
   KEIROUTER_SECURITY__MASTER_KEY=<your_generated_master_key>
   KEIROUTER_LOG__FORMAT=json
   ```
6. **Persistent Storage**:
   KeiRouter needs persistent storage for its SQLite database and runtime secrets. In Coolify, go to the **Storages** tab and create a volume:
   - **Name**: `keirouter-data`
   - **Destination**: `/data`
7. **Deploy**: Click the **Deploy** button. Coolify will build the Docker image and start the container.

### Using External/Managed Postgres on Coolify

If you provisioned a Postgres database on Coolify or use an external managed DB, add these additional environment variables to your KeiRouter resource:

```env
KEIROUTER_DATABASE__DRIVER=postgres
KEIROUTER_DATABASE__DSN=postgres://USER:PASSWORD@HOST:5432/DB?sslmode=require
```
*(Replace `USER`, `PASSWORD`, `HOST`, and `DB` with your Postgres credentials).*

## Updates

```bash
git pull
docker compose up -d --build
```

For the local source installer, rerun the install command.

## Security Notes

- Keep `KEIROUTER_SECURITY__MASTER_KEY` stable and backed up.
- Use HTTPS when exposing the dashboard outside localhost.
- The dashboard has session auth, but production deployments should still sit
  behind a reverse proxy, firewall, or platform access control.
- The default dashboard password is `keirouter`; change it on first login.
