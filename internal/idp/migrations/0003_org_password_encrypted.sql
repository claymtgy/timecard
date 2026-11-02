-- Encrypted copy of the organization password, so an organization's admins can look it up.
-- password_hash is still what sign-up matches against. NULL for passwords set before this
-- migration: those can't be recovered, and `idp org passwd` replaces them.
ALTER TABLE organizations ADD COLUMN password_encrypted text;
