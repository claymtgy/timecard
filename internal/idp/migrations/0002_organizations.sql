-- Organizations (companies). Created with `idp org add`; every user belongs to exactly one.
-- People join by entering the organization password at sign-up, which alone identifies the
-- organization, so it is stored as a keyed hash that can be looked up (see orgs.go).
CREATE TABLE organizations (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name          text NOT NULL,
  password_hash text UNIQUE, -- NULL = nobody can join until `idp org passwd` sets one
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX organizations_name_key ON organizations (lower(name));

-- Everything that existed before multi-tenancy belongs to this organization.
-- The app schema's migration uses the same id. Rename it with `idp org rename`.
INSERT INTO organizations (id, name) VALUES ('00000000-0000-0000-0000-000000000001', 'Default organization');

ALTER TABLE users ADD COLUMN org_id uuid REFERENCES organizations(id) ON DELETE RESTRICT;
UPDATE users SET org_id = '00000000-0000-0000-0000-000000000001';
ALTER TABLE users ALTER COLUMN org_id SET NOT NULL;
CREATE INDEX users_org_idx ON users (org_id);
