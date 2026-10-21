-- Timesheet / project management schema. search_path is set to "app" by the migration runner.
-- Money is stored in integer cents, durations in integer minutes.

CREATE TABLE workers (
  id             bigserial PRIMARY KEY,
  name           text NOT NULL,
  email          text NOT NULL DEFAULT '',
  phone          text NOT NULL DEFAULT '',
  pay_rate_cents integer NOT NULL DEFAULT 0 CHECK (pay_rate_cents >= 0), -- 0 = not on hourly payroll (e.g. owners)
  active         boolean NOT NULL DEFAULT true,
  notes          text NOT NULL DEFAULT '',
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now()
);

-- People who sign in through the IdP. subject = OIDC "sub".
CREATE TABLE users (
  id            bigserial PRIMARY KEY,
  subject       text NOT NULL UNIQUE,
  email         text NOT NULL DEFAULT '',
  name          text NOT NULL DEFAULT '',
  role          text NOT NULL DEFAULT 'worker' CHECK (role IN ('admin', 'manager', 'worker')),
  worker_id     bigint UNIQUE REFERENCES workers(id) ON DELETE SET NULL,
  created_at    timestamptz NOT NULL DEFAULT now(),
  last_login_at timestamptz
);

CREATE TABLE sessions (
  id         text PRIMARY KEY, -- SHA-256 of the cookie value
  user_id    bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  csrf_token text NOT NULL,
  id_token   text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz NOT NULL
);
CREATE INDEX sessions_user_idx ON sessions (user_id);

-- Pending OIDC logins (state -> nonce + PKCE verifier).
CREATE TABLE login_states (
  state      text PRIMARY KEY,
  nonce      text NOT NULL,
  verifier   text NOT NULL,
  return_to  text NOT NULL DEFAULT '/',
  expires_at timestamptz NOT NULL
);

-- Customers (the start of the CRM).
CREATE TABLE clients (
  id           bigserial PRIMARY KEY,
  name         text NOT NULL,
  contact_name text NOT NULL DEFAULT '',
  email        text NOT NULL DEFAULT '',
  phone        text NOT NULL DEFAULT '',
  address      text NOT NULL DEFAULT '',
  notes        text NOT NULL DEFAULT '',
  active       boolean NOT NULL DEFAULT true,
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now()
);

-- A project groups related jobs, e.g. a development ("Smithfield") for one client.
CREATE TABLE projects (
  id          bigserial PRIMARY KEY,
  client_id   bigint REFERENCES clients(id) ON DELETE SET NULL,
  name        text NOT NULL,
  description text NOT NULL DEFAULT '',
  status      text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'on_hold', 'completed', 'archived')),
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now()
);

-- A job is the unit that is billed: hourly (bill rate x hours) or fixed bid.
CREATE TABLE jobs (
  id                      bigserial PRIMARY KEY,
  project_id              bigint NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name                    text NOT NULL,
  description             text NOT NULL DEFAULT '',
  billing_type            text NOT NULL CHECK (billing_type IN ('hourly', 'fixed')),
  bill_rate_cents         integer NOT NULL DEFAULT 0 CHECK (bill_rate_cents >= 0),   -- hourly jobs
  bid_amount_cents        bigint  NOT NULL DEFAULT 0 CHECK (bid_amount_cents >= 0),  -- fixed jobs
  labor_cost_rate_cents   integer NOT NULL DEFAULT 0 CHECK (labor_cost_rate_cents >= 0), -- $/hr used for profit
  pay_rate_override_cents integer CHECK (pay_rate_override_cents >= 0),              -- null = worker's own rate
  profit_share_bps        integer NOT NULL DEFAULT 0 CHECK (profit_share_bps BETWEEN 0 AND 10000),
  profit_share_worker_id  bigint REFERENCES workers(id) ON DELETE SET NULL,
  status                  text NOT NULL DEFAULT 'active' CHECK (status IN ('planned', 'active', 'done', 'cancelled')),
  start_date              date,
  completed_on            date,
  created_at              timestamptz NOT NULL DEFAULT now(),
  updated_at              timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX jobs_project_idx ON jobs (project_id);

-- Tasks are both project-management items and job phases (e.g. "Dry in", "Shingling").
CREATE TABLE tasks (
  id                 bigserial PRIMARY KEY,
  job_id             bigint NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
  title              text NOT NULL,
  description        text NOT NULL DEFAULT '',
  status             text NOT NULL DEFAULT 'todo' CHECK (status IN ('todo', 'in_progress', 'blocked', 'done')),
  assignee_worker_id bigint REFERENCES workers(id) ON DELETE SET NULL,
  due_date           date,
  estimate_minutes   integer CHECK (estimate_minutes >= 0),
  position           integer NOT NULL DEFAULT 0,
  completed_at       timestamptz,
  created_at         timestamptz NOT NULL DEFAULT now(),
  updated_at         timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX tasks_job_idx ON tasks (job_id, position);
CREATE INDEX tasks_assignee_idx ON tasks (assignee_worker_id) WHERE status <> 'done';

CREATE TABLE invoices (
  id           bigserial PRIMARY KEY,
  number       integer NOT NULL UNIQUE,
  client_id    bigint REFERENCES clients(id) ON DELETE RESTRICT,
  project_id   bigint REFERENCES projects(id) ON DELETE SET NULL,
  period_start date,
  period_end   date,
  issue_date   date NOT NULL DEFAULT CURRENT_DATE,
  due_date     date,
  status       text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'sent', 'paid', 'void')),
  paid_date    date,
  notes        text NOT NULL DEFAULT '',
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE invoice_lines (
  id               bigserial PRIMARY KEY,
  invoice_id       bigint NOT NULL REFERENCES invoices(id) ON DELETE CASCADE,
  job_id           bigint REFERENCES jobs(id) ON DELETE SET NULL,
  kind             text NOT NULL CHECK (kind IN ('hours', 'fixed', 'other')),
  description      text NOT NULL,
  minutes          integer,                 -- hours lines: billed minutes
  quantity         double precision NOT NULL DEFAULT 1,
  unit_price_cents bigint NOT NULL,
  amount_cents     bigint NOT NULL,
  position         integer NOT NULL DEFAULT 0
);
CREATE INDEX invoice_lines_invoice_idx ON invoice_lines (invoice_id);
CREATE INDEX invoice_lines_job_idx ON invoice_lines (job_id);

CREATE TABLE pay_runs (
  id           bigserial PRIMARY KEY,
  worker_id    bigint NOT NULL REFERENCES workers(id) ON DELETE RESTRICT,
  period_start date NOT NULL,
  period_end   date NOT NULL CHECK (period_end >= period_start),
  status       text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'paid')),
  paid_date    date,
  notes        text NOT NULL DEFAULT '',
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX pay_runs_worker_idx ON pay_runs (worker_id, period_end);

-- One-off pay items: profit shares on bid jobs, bonuses, reimbursements, deductions (negative).
CREATE TABLE pay_adjustments (
  id           bigserial PRIMARY KEY,
  worker_id    bigint NOT NULL REFERENCES workers(id) ON DELETE CASCADE,
  job_id       bigint REFERENCES jobs(id) ON DELETE SET NULL,
  kind         text NOT NULL CHECK (kind IN ('profit_share', 'bonus', 'reimbursement', 'deduction', 'other')),
  description  text NOT NULL DEFAULT '',
  amount_cents bigint NOT NULL,
  adj_date     date NOT NULL,
  pay_run_id   bigint REFERENCES pay_runs(id) ON DELETE SET NULL,
  created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX pay_adjustments_profit_share_key ON pay_adjustments (job_id, worker_id) WHERE kind = 'profit_share';
CREATE INDEX pay_adjustments_unpaid_idx ON pay_adjustments (worker_id) WHERE pay_run_id IS NULL;

CREATE TABLE time_entries (
  id             bigserial PRIMARY KEY,
  worker_id      bigint NOT NULL REFERENCES workers(id) ON DELETE RESTRICT,
  job_id         bigint NOT NULL REFERENCES jobs(id) ON DELETE RESTRICT,
  task_id        bigint REFERENCES tasks(id) ON DELETE SET NULL,
  work_date      date NOT NULL,
  start_minute   integer CHECK (start_minute BETWEEN 0 AND 1440), -- minutes after midnight
  end_minute     integer CHECK (end_minute BETWEEN 0 AND 1440),
  break_minutes  integer NOT NULL DEFAULT 0 CHECK (break_minutes >= 0),
  minutes        integer NOT NULL CHECK (minutes BETWEEN 0 AND 1440),
  pay_rate_cents integer NOT NULL DEFAULT 0 CHECK (pay_rate_cents >= 0), -- snapshot at entry time
  billable       boolean NOT NULL DEFAULT true,
  notes          text NOT NULL DEFAULT '',
  invoice_id     bigint REFERENCES invoices(id) ON DELETE SET NULL,
  pay_run_id     bigint REFERENCES pay_runs(id) ON DELETE SET NULL,
  created_by     bigint REFERENCES users(id) ON DELETE SET NULL,
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now(),
  CHECK ((start_minute IS NULL AND end_minute IS NULL)
      OR (start_minute IS NOT NULL AND end_minute IS NOT NULL AND end_minute > start_minute))
);
CREATE INDEX time_entries_worker_date_idx ON time_entries (worker_id, work_date);
CREATE INDEX time_entries_job_date_idx ON time_entries (job_id, work_date);
CREATE INDEX time_entries_invoice_idx ON time_entries (invoice_id);
CREATE INDEX time_entries_pay_run_idx ON time_entries (pay_run_id);
