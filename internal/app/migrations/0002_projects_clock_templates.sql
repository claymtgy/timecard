-- 1) Flatten projects/jobs: the billable unit is now the project (hourly or fixed bid),
--    owned by a client, with tasks underneath. Every existing job becomes a project.
ALTER TABLE projects RENAME TO legacy_projects;
ALTER TABLE jobs RENAME TO projects;
ALTER TABLE projects ADD COLUMN client_id bigint REFERENCES clients(id) ON DELETE SET NULL;
UPDATE projects p
SET client_id = l.client_id,
    name = CASE WHEN position(lower(l.name) IN lower(p.name)) > 0 THEN p.name ELSE l.name || ' – ' || p.name END
FROM legacy_projects l
WHERE l.id = p.project_id;
ALTER TABLE projects DROP COLUMN project_id;
CREATE INDEX projects_client_idx ON projects (client_id);

ALTER TABLE tasks RENAME COLUMN job_id TO project_id;
ALTER TABLE time_entries RENAME COLUMN job_id TO project_id;
ALTER TABLE invoice_lines RENAME COLUMN job_id TO project_id;
ALTER TABLE pay_adjustments RENAME COLUMN job_id TO project_id;
ALTER TABLE invoices DROP COLUMN project_id;
DROP TABLE legacy_projects; -- also drops its sequence, freeing the name below
ALTER SEQUENCE jobs_id_seq RENAME TO projects_id_seq;

-- 2) Invoice delivery tracking.
ALTER TABLE invoices ADD COLUMN sent_at timestamptz, ADD COLUMN sent_to text NOT NULL DEFAULT '';

-- 3) Task templates an admin can apply to a project (then add to).
CREATE TABLE task_templates (
  id          bigserial PRIMARY KEY,
  name        text NOT NULL,
  description text NOT NULL DEFAULT '',
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE task_template_items (
  id               bigserial PRIMARY KEY,
  template_id      bigint NOT NULL REFERENCES task_templates(id) ON DELETE CASCADE,
  title            text NOT NULL,
  description      text NOT NULL DEFAULT '',
  estimate_minutes integer CHECK (estimate_minutes >= 0),
  position         integer NOT NULL DEFAULT 0
);
CREATE INDEX task_template_items_template_idx ON task_template_items (template_id, position);

-- 4) Clock in / out. One open session per worker; clocking out turns it into time entries.
CREATE TABLE clock_sessions (
  worker_id  bigint PRIMARY KEY REFERENCES workers(id) ON DELETE CASCADE,
  project_id bigint NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  task_id    bigint REFERENCES tasks(id) ON DELETE SET NULL,
  started_at timestamptz NOT NULL,
  notes      text NOT NULL DEFAULT '',
  created_by bigint REFERENCES users(id) ON DELETE SET NULL
);

-- 5) Company details printed on invoices (single row).
CREATE TABLE company_settings (
  id               integer PRIMARY KEY DEFAULT 1 CHECK (id = 1),
  name             text NOT NULL DEFAULT '',
  address          text NOT NULL DEFAULT '',
  phone            text NOT NULL DEFAULT '',
  email            text NOT NULL DEFAULT '',
  invoice_footer   text NOT NULL DEFAULT '',
  default_due_days integer NOT NULL DEFAULT 30 CHECK (default_due_days BETWEEN 0 AND 365),
  updated_at       timestamptz NOT NULL DEFAULT now()
);
INSERT INTO company_settings (id) VALUES (1);
