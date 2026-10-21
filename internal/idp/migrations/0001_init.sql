-- Identity provider schema. search_path is set to "idp" by the migration runner.

CREATE TABLE users (
  id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  email          text NOT NULL,
  name           text NOT NULL DEFAULT '',
  given_name     text NOT NULL DEFAULT '',
  family_name    text NOT NULL DEFAULT '',
  password_hash  text NOT NULL,
  email_verified boolean NOT NULL DEFAULT false,
  disabled       boolean NOT NULL DEFAULT false,
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX users_email_key ON users (lower(email));

-- Registered relying parties. Secrets are stored as SHA-256 hex (they are high-entropy).
CREATE TABLE clients (
  id                        text PRIMARY KEY,
  name                      text NOT NULL DEFAULT '',
  secret_hash               text,
  redirect_uris             text[] NOT NULL DEFAULT '{}',
  post_logout_redirect_uris text[] NOT NULL DEFAULT '{}',
  application_type          smallint NOT NULL DEFAULT 0, -- 0 web, 1 user agent, 2 native
  auth_method               text NOT NULL DEFAULT 'client_secret_basic',
  grant_types               text[] NOT NULL DEFAULT '{authorization_code,refresh_token}',
  dev_mode                  boolean NOT NULL DEFAULT false,
  created_at                timestamptz NOT NULL DEFAULT now(),
  updated_at                timestamptz NOT NULL DEFAULT now()
);

-- In-flight /authorize requests; the authorization code is attached once login succeeds.
CREATE TABLE auth_requests (
  id         uuid PRIMARY KEY,
  client_id  text NOT NULL REFERENCES clients(id) ON DELETE CASCADE,
  data       jsonb NOT NULL,
  user_id    uuid REFERENCES users(id) ON DELETE CASCADE,
  auth_time  timestamptz,
  done       boolean NOT NULL DEFAULT false,
  code       text UNIQUE,
  created_at timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz NOT NULL
);
CREATE INDEX auth_requests_expires_idx ON auth_requests (expires_at);

CREATE TABLE refresh_tokens (
  id          uuid PRIMARY KEY,
  token_hash  text NOT NULL UNIQUE,
  client_id   text NOT NULL REFERENCES clients(id) ON DELETE CASCADE,
  user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  audience    text[] NOT NULL DEFAULT '{}',
  scopes      text[] NOT NULL DEFAULT '{}',
  amr         text[] NOT NULL DEFAULT '{}',
  auth_time   timestamptz NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  expires_at  timestamptz NOT NULL
);

CREATE TABLE access_tokens (
  id               uuid PRIMARY KEY,
  client_id        text NOT NULL REFERENCES clients(id) ON DELETE CASCADE,
  user_id          uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  refresh_token_id uuid REFERENCES refresh_tokens(id) ON DELETE CASCADE,
  audience         text[] NOT NULL DEFAULT '{}',
  scopes           text[] NOT NULL DEFAULT '{}',
  created_at       timestamptz NOT NULL DEFAULT now(),
  expires_at       timestamptz NOT NULL
);
CREATE INDEX access_tokens_user_client_idx ON access_tokens (user_id, client_id);

-- RSA signing keys. The newest active key signs; all active keys are published in JWKS.
CREATE TABLE signing_keys (
  id          text PRIMARY KEY,
  algorithm   text NOT NULL,
  private_pem text NOT NULL,
  active      boolean NOT NULL DEFAULT true,
  created_at  timestamptz NOT NULL DEFAULT now()
);

-- Browser sessions at the IdP (single sign-on across apps). id = SHA-256 of the cookie value.
CREATE TABLE sessions (
  id          text PRIMARY KEY,
  user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  auth_time   timestamptz NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  expires_at  timestamptz NOT NULL,
  user_agent  text NOT NULL DEFAULT '',
  ip          text NOT NULL DEFAULT ''
);
CREATE INDEX sessions_user_idx ON sessions (user_id);
