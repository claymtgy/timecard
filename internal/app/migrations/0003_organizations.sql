-- Multi-tenancy: all business data belongs to an organization, enforced by the database.
--
-- API requests run inside a transaction with `SET LOCAL ROLE timecard_tenant` and the
-- setting app.org_id (internal/app/tenant.go). Row-level security then limits that role to the
-- organization's rows, so a query that forgets to filter returns nothing rather than another
-- company's data. New rows get org_id from the same setting via the column default.
-- The connecting role owns the tables and bypasses RLS; it is used only for pre-tenant
-- work (session lookup, the login upsert, cleanup).

-- Organizations are owned by the IdP; this is a mirror, updated at each sign-in.
CREATE TABLE organizations (
  id         uuid PRIMARY KEY,
  name       text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);
-- Same id as the IdP's migration: existing data belongs to this organization.
INSERT INTO organizations (id, name) VALUES ('00000000-0000-0000-0000-000000000001', 'Default organization');

-- NULL (fail closed) when no organization is set.
CREATE FUNCTION current_org() RETURNS uuid LANGUAGE sql STABLE AS
$$ SELECT nullif(current_setting('app.org_id', true), '')::uuid $$;

-- org_id on every tenant table, existing rows going to the default organization.
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['users', 'workers', 'clients', 'projects', 'tasks', 'task_templates', 'task_template_items',
                           'time_entries', 'invoices', 'invoice_lines', 'pay_runs', 'pay_adjustments', 'clock_sessions']
  LOOP
    EXECUTE format('ALTER TABLE %I ADD COLUMN org_id uuid NOT NULL DEFAULT ''00000000-0000-0000-0000-000000000001'' REFERENCES organizations(id) ON DELETE RESTRICT', t);
    EXECUTE format('ALTER TABLE %I ALTER COLUMN org_id SET DEFAULT current_org()', t);
    EXECUTE format('CREATE INDEX %I ON %I (org_id)', t || '_org_idx', t);
  END LOOP;
END $$;

-- One company_settings row per organization (created at first sign-in).
ALTER TABLE company_settings ADD COLUMN org_id uuid REFERENCES organizations(id) ON DELETE RESTRICT;
UPDATE company_settings SET org_id = '00000000-0000-0000-0000-000000000001';
ALTER TABLE company_settings DROP COLUMN id;
ALTER TABLE company_settings ALTER COLUMN org_id SET NOT NULL;
ALTER TABLE company_settings ALTER COLUMN org_id SET DEFAULT current_org();
ALTER TABLE company_settings ADD PRIMARY KEY (org_id);

-- Invoice numbers restart per organization.
ALTER TABLE invoices DROP CONSTRAINT invoices_number_key;
ALTER TABLE invoices ADD CONSTRAINT invoices_org_number_key UNIQUE (org_id, number);

-- Foreign keys ignore RLS, so make them include org_id: a row can never reference another
-- organization's record, whatever id a client sends.
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['workers', 'clients', 'projects', 'tasks', 'task_templates', 'invoices', 'pay_runs']
  LOOP
    EXECUTE format('ALTER TABLE %I ADD CONSTRAINT %I UNIQUE (org_id, id)', t, t || '_org_id_key');
  END LOOP;
END $$;

CREATE FUNCTION pg_temp.retarget_fk(child text, col text, parent text, action text) RETURNS void LANGUAGE plpgsql AS $$
DECLARE old_name text;
BEGIN
  SELECT c.conname INTO STRICT old_name
  FROM pg_constraint c
  JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = c.conkey[1]
  WHERE c.conrelid = child::regclass AND c.contype = 'f' AND array_length(c.conkey, 1) = 1 AND a.attname = col;
  EXECUTE format('ALTER TABLE %I DROP CONSTRAINT %I', child, old_name);
  EXECUTE format('ALTER TABLE %I ADD FOREIGN KEY (org_id, %I) REFERENCES %I (org_id, id) ON DELETE %s',
    child, col, parent, CASE action WHEN 'SET NULL' THEN format('SET NULL (%I)', col) ELSE action END);
END $$;

SELECT pg_temp.retarget_fk(child, col, parent, action) FROM (VALUES
  ('users',               'worker_id',              'workers',        'SET NULL'),
  ('projects',            'client_id',              'clients',        'SET NULL'),
  ('projects',            'profit_share_worker_id', 'workers',        'SET NULL'),
  ('tasks',               'project_id',             'projects',       'CASCADE'),
  ('tasks',               'assignee_worker_id',     'workers',        'SET NULL'),
  ('task_template_items', 'template_id',            'task_templates', 'CASCADE'),
  ('invoices',            'client_id',              'clients',        'RESTRICT'),
  ('invoice_lines',       'invoice_id',             'invoices',       'CASCADE'),
  ('invoice_lines',       'project_id',             'projects',       'SET NULL'),
  ('pay_runs',            'worker_id',              'workers',        'RESTRICT'),
  ('pay_adjustments',     'worker_id',              'workers',        'CASCADE'),
  ('pay_adjustments',     'project_id',             'projects',       'SET NULL'),
  ('pay_adjustments',     'pay_run_id',             'pay_runs',       'SET NULL'),
  ('time_entries',        'worker_id',              'workers',        'RESTRICT'),
  ('time_entries',        'project_id',             'projects',       'RESTRICT'),
  ('time_entries',        'task_id',                'tasks',          'SET NULL'),
  ('time_entries',        'invoice_id',             'invoices',       'SET NULL'),
  ('time_entries',        'pay_run_id',             'pay_runs',       'SET NULL'),
  ('clock_sessions',      'worker_id',              'workers',        'CASCADE'),
  ('clock_sessions',      'project_id',             'projects',       'CASCADE'),
  ('clock_sessions',      'task_id',                'tasks',          'SET NULL')
) AS v(child, col, parent, action);

-- Restricted role for request handling. Roles are cluster-wide, hence the existence check.
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'timecard_tenant') THEN
    CREATE ROLE timecard_tenant NOLOGIN;
  END IF;
  EXECUTE format('GRANT timecard_tenant TO %I', current_user);
END $$;
GRANT USAGE ON SCHEMA app TO timecard_tenant;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA app TO timecard_tenant;
GRANT SELECT ON organizations TO timecard_tenant;
ALTER TABLE organizations ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON organizations USING (id = current_org());

-- sessions and login_states get no grants: the tenant role can't touch them at all.
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['users', 'workers', 'clients', 'projects', 'tasks', 'task_templates', 'task_template_items',
                           'time_entries', 'invoices', 'invoice_lines', 'pay_runs', 'pay_adjustments', 'clock_sessions',
                           'company_settings']
  LOOP
    EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON %I TO timecard_tenant', t);
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY tenant_isolation ON %I USING (org_id = current_org()) WITH CHECK (org_id = current_org())', t);
  END LOOP;
END $$;
