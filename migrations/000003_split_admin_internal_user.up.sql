-- Split admin credentials into tenant-facing and internal (agent) credentials.
-- The internal user (dbaas_internal) is the PG superuser used by the agent.
-- The admin user (dbadmin) is tenant-facing with full privileges but not superuser.

ALTER TABLE instances ADD COLUMN internal_user TEXT NOT NULL DEFAULT 'dbaas_internal';
ALTER TABLE instances ADD COLUMN internal_password TEXT NOT NULL DEFAULT '';
