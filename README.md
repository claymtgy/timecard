# Crew Timesheets / PM / CRM

Clock-in, timesheets, project tasks, invoicing and payroll for a small crew — replacing
`Timesheet.xlsx` — with its own OpenID Connect sign-in service so future apps (CRM, etc.) share one login.

| Part | Path | What it is |
|---|---|---|
| Identity provider | `cmd/idp`, `internal/idp` | OIDC provider (authorization code + PKCE, refresh tokens, single sign-on, RP-initiated logout) built on the certified [`zitadel/oidc`](https://github.com/zitadel/oidc) library, argon2id passwords, Postgres storage |
| App API | `cmd/app`, `internal/app` | Go JSON API; signs users in through the IdP (server-side session, CSRF-protected); invoice PDFs and email |
| Web UI | `web/` | Vue 3 + TypeScript + Vite single-page app (mobile-friendly clock-in) |
| Shared | `internal/calc` | Pure time/money math (unit-tested against the spreadsheet's numbers) |
| Services | `docker-compose.yml` | Postgres 17 (IdP uses schema `idp`, the app schema `app`; migrations run on start-up) and Mailpit (catches outgoing email in development) |

## Running locally

Prerequisites: Go 1.26+, Node 22+, Docker.

```bash
cp .env.example .env
docker compose up -d                 # Postgres :5432, Mailpit :1025 (web UI http://localhost:8025)
go run ./cmd/idp                     # terminal 1 — IdP on http://localhost:8081
go run ./cmd/app                     # terminal 2 — API on http://localhost:8080
cd web && npm install && npm run dev # terminal 3 — UI on http://localhost:5173
```

Open http://localhost:5173 → **Sign in**. In development `IDP_ALLOW_SIGNUP=true`, so choose **Create one** on the
sign-in page. **The first person to sign in becomes the app's admin**; everyone after starts as a *worker*
until an admin changes their role (Users page). Link each person's login to their worker record there
(or give the worker the same email and it links automatically on first sign-in).

Accounts can also be managed from the command line (the password is read from stdin):

```bash
go run ./cmd/idp user add -email you@example.com -name "Your Name"
go run ./cmd/idp user passwd -email you@example.com
go run ./cmd/idp user disable -email someone@example.com
go run ./cmd/idp user list
```

## How it works

**Workers** sign in and land on **Clock in**:
1. *Project* — open projects, with the one from their previous clock-in preselected.
2. *Task* — that project's open tasks, with the previous task preselected. Tap **Clock in**.
3. While on the clock: a live timer, **Switch project / task** (closes the current block and starts the next),
   or **Clock out** (break minutes, prefilled from last time, and optional notes).
   Clocking out writes time entries in the company time zone (`APP_TIMEZONE`), split at midnight if needed.

Workers can also see their week (**My time**) and move their assigned tasks. They never see rates or money.

**Admins / managers** set things up:
* **Clients** — who gets invoiced.
* **Projects** — each belongs to a client and is billed **hourly** (bill rate × hours) or **by bid**
  (fixed amount; profit = bid − hours × labor-cost rate, with an optional profit share % paid to a worker).
  Optional pay-rate override (e.g. $25/h on spec homes).
* **Tasks** — added per project, or from a **task template**: pick a template when creating a project or use
  *Add from template* on a project, then add project-specific tasks. Templates are managed under
  *Task templates*; *Save as template* turns a project's task list into a new template.
* **Invoices** — per client: pick the period, tick the hourly projects (unbilled hours) and bid projects
  (full or partial/progress amount), add extra lines (materials) → draft. **View/Download PDF** or
  **Email to client** (PDF attached; marks it sent). Then mark paid, or void (hours become unbilled again).
* **Payroll**, **Time log** (all entries, CSV), **Project profit** (the old "Bid Jobs" sheet), **Dashboard**
  (who's clocked in, unbilled work, unpaid invoices and payroll).
* **Company & email** — name/address/phone/payment instructions printed on invoices; default due days.

### Email

Sending uses SMTP (`SMTP_HOST`, `SMTP_PORT`, `SMTP_USERNAME`, `SMTP_PASSWORD`, `SMTP_FROM`). Port 465 uses TLS;
other ports use STARTTLS when offered. In development the defaults point at Mailpit, which delivers nothing —
open http://localhost:8025 to see what would have been sent. For real delivery use your provider's SMTP
(Google Workspace, Microsoft 365, Postmark, SES, …).

### Demo data

To load a slice of the original spreadsheet (after `cmd/app` has started once, on an empty database):

```bash
docker compose exec -T postgres psql -U crm -d crm -v ON_ERROR_STOP=1 < scripts/demo_seed.sql
```

The header of `scripts/demo_seed.sql` lists the invoice and profit figures you should see; they match the
workbook (e.g. Lot 117: profit $2,215, 20% share $443, Jake's pay $1,088).

## How the spreadsheet maps to the app

| Spreadsheet | App |
|---|---|
| Per-person tabs: date, project, start, end, break, hours | **Clock in** (or manual entries on **My time**); week totals split into hourly vs. bid hours automatically |
| "Paid?" per person-week, Jake Pay formulas | **Payroll** — owed per worker, pay runs, pay items (bonuses, "extra", deductions, profit shares) |
| Others tab: wage, paycheck | Worker pay rate (snapshotted on each entry); per-project pay-rate override |
| **Overview**: invoice #, dates, project, hours per person, man-hours, rate, total, paid | **Invoices** — hours per worker shown; PDF + email; draft → sent → paid / void |
| **Bid Jobs**: lot, phase hours per person, bid, profit, share %, Jake pay | **Project profit** and each bid project's page (hours by task × worker); "Add to payroll" posts the share |
| Phases (dry in, shingling, finish carpentry) | Tasks on the project — reusable via templates |

## Deploying (timecard.clayton-montgomery.dev)

The production stack (`docker-compose.prod.yml`) runs Postgres, the sign-in service and the app behind the
shared Caddy proxy in `~/infra`. It publishes no ports: Caddy reaches `timecard-app:8080` and
`timecard-auth:8081` over the external `edge` network; Postgres is only on the stack's private network.

| URL | Service |
|---|---|
| https://timecard.clayton-montgomery.dev | App (UI + API) |
| https://auth.clayton-montgomery.dev | Sign-in (OIDC provider) |

1. **DNS** — A records for `timecard` and `auth` pointing at the droplet.
2. **Caddy** — copy [deploy/infra/Caddyfile](deploy/infra/Caddyfile) and [deploy/infra/docker-compose.yml](deploy/infra/docker-compose.yml) over the ones in `~/infra` (they are your current files plus the two Timecard blocks and env vars),
   add `TIMECARD_DOMAIN` / `TIMECARD_AUTH_DOMAIN` to `~/infra/.env`
   (see the snippet header), then run `docker compose up -d` in `~/infra`.
3. **Secrets** — in the cloned repo, `cp .env.production.example .env.production` and fill `POSTGRES_PASSWORD`,
   `IDP_CRYPTO_KEY` and `OIDC_CLIENT_SECRET` with values from `openssl rand -hex 32`.
4. **Start**:
   ```bash
   docker compose -f docker-compose.prod.yml --env-file .env.production up -d --build
   ```
   On a 1 GB droplet the Go/Node build can run out of memory; if it does, add swap and retry:
   ```bash
   fallocate -l 2G /swapfile && chmod 600 /swapfile && mkswap /swapfile && swapon /swapfile
   ```
5. **First admin** — sign-up is off in production, so create your account from the CLI (it asks for a password):
   ```bash
   docker compose -f docker-compose.prod.yml --env-file .env.production exec idp idp user add -email you@example.com -name "Your Name"
   ```
   Then open https://timecard.clayton-montgomery.dev and sign in — the first sign-in becomes the app admin.
   Add crew members the same way (or temporarily set `IDP_ALLOW_SIGNUP=true`).

**Updating:** `git pull`, then the same `up -d --build` command; migrations run automatically on start.
**Backups:** `docker compose -f docker-compose.prod.yml --env-file .env.production exec postgres pg_dump -U timecard timecard > backup.sql`

Behind the proxy the IdP takes client IPs from `X-Forwarded-For` (`IDP_TRUST_PROXY=true` — safe because Caddy
overwrites that header), and the app calls the IdP directly at `http://idp:8081` (`OIDC_INTERNAL_URL`) while tokens
still carry the public issuer `https://auth.clayton-montgomery.dev`.

## Production notes

* Serve both services over HTTPS. Set `IDP_ISSUER`, `APP_URL`, redirect URIs accordingly; cookies become `Secure`
  automatically for `https://` URLs.
* Generate real secrets: `IDP_CRYPTO_KEY` (32+ random chars) and `IDP_APP_CLIENT_SECRET` / `OIDC_CLIENT_SECRET` (must match).
* Set `IDP_ALLOW_SIGNUP=false` and create accounts with `idp user add`.
* Set `APP_TIMEZONE` to where the crew works, and real SMTP settings.
* Build the UI (`cd web && npm run build`) and point `APP_WEB_DIST` at `web/dist`; `cmd/app` then serves UI and API
  from one origin (use `APP_URL=https://your-host`).
* Signing keys are generated on first start and stored in `idp.signing_keys`; protect database backups accordingly.

## Development

```bash
go test ./...                  # unit tests (calc, IdP, invoice PDF, email encoding)
go vet ./...
cd web && npm run typecheck && npm run build
```

API routes are listed in `internal/app/server.go`; SQL lives next to the handlers in `internal/app/api_*.go`.
Money is integer cents and durations integer minutes everywhere; dates are `YYYY-MM-DD`.
