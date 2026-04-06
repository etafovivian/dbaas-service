-- ========== INSTANCES ==========
CREATE TABLE IF NOT EXISTS instances (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id         UUID        NOT NULL,
    environment_id    UUID        NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001',
    name              TEXT        NOT NULL,
    engine            TEXT        NOT NULL DEFAULT 'postgres'
                      CHECK (engine IN ('postgres')),
    engine_version    TEXT        NOT NULL DEFAULT '18',
    tier              TEXT        NOT NULL DEFAULT 'single'
                      CHECK (tier IN ('single', 'ha')),
    flavor_id         TEXT        NOT NULL,
    disk_size_gb      INT         NOT NULL,
    status            TEXT        NOT NULL DEFAULT 'creating'
                      CHECK (status IN ('creating','available','modifying','deleting','deleted','error','backing-up','restoring')),
    host              TEXT,
    port              INT         NOT NULL DEFAULT 5432,
    admin_user        TEXT        NOT NULL DEFAULT 'etafovivian_admin',
    admin_password    TEXT        NOT NULL,
    dbaas_project_id  TEXT,
    backup_bucket     TEXT,
    metadata          JSONB       NOT NULL DEFAULT '{}',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at        TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS instances_tenant_env_name_active
    ON instances (tenant_id, environment_id, name) WHERE status != 'deleted';
CREATE INDEX IF NOT EXISTS instances_tenant_id ON instances (tenant_id);

-- ========== INSTANCE NODES ==========
CREATE TABLE IF NOT EXISTS instance_nodes (
    id                   UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    instance_id          UUID        NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
    node_index           INT         NOT NULL,
    role                 TEXT        NOT NULL DEFAULT 'pending'
                         CHECK (role IN ('pending','primary','replica','unknown')),
    openstack_server_id  TEXT,
    openstack_volume_id  TEXT,
    private_ip           TEXT,
    mgmt_ip              TEXT,
    status               TEXT        NOT NULL DEFAULT 'creating'
                         CHECK (status IN ('creating','ready','error','deleting','deleted')),
    agent_connected      BOOLEAN     NOT NULL DEFAULT FALSE,
    last_heartbeat       TIMESTAMPTZ,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS instance_nodes_instance_id ON instance_nodes (instance_id);
CREATE UNIQUE INDEX IF NOT EXISTS instance_nodes_instance_index ON instance_nodes (instance_id, node_index);

-- ========== DATABASES ==========
CREATE TABLE IF NOT EXISTS databases (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    instance_id       UUID        NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
    tenant_id         UUID        NOT NULL,
    name              TEXT        NOT NULL,
    charset           TEXT        NOT NULL DEFAULT 'UTF8',
    collation         TEXT        NOT NULL DEFAULT 'en_US.UTF-8',
    status            TEXT        NOT NULL DEFAULT 'creating'
                      CHECK (status IN ('creating','active','deleting','deleted','error')),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at        TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS databases_instance_name_active
    ON databases (instance_id, name) WHERE status != 'deleted';

-- ========== USERS ==========
CREATE TABLE IF NOT EXISTS users (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    instance_id       UUID        NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
    tenant_id         UUID        NOT NULL,
    username          TEXT        NOT NULL,
    status            TEXT        NOT NULL DEFAULT 'creating'
                      CHECK (status IN ('creating','active','deleting','deleted','error')),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at        TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS users_instance_username_active
    ON users (instance_id, username) WHERE status != 'deleted';

-- ========== BACKUPS ==========
CREATE TABLE IF NOT EXISTS backups (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    instance_id       UUID        NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
    tenant_id         UUID        NOT NULL,
    name              TEXT        NOT NULL,
    description       TEXT        NOT NULL DEFAULT '',
    backup_type       TEXT        NOT NULL DEFAULT 'full'
                      CHECK (backup_type IN ('full','diff','incr')),
    size_bytes        BIGINT,
    status            TEXT        NOT NULL DEFAULT 'creating'
                      CHECK (status IN ('creating','completed','failed','restoring','deleted')),
    started_at        TIMESTAMPTZ,
    completed_at      TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at        TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS backups_instance_id ON backups (instance_id);

-- ========== CONFIGURATIONS ==========
CREATE TABLE IF NOT EXISTS configurations (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    instance_id       UUID        NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
    parameter         TEXT        NOT NULL,
    value             TEXT        NOT NULL,
    pending_restart   BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS configurations_instance_param
    ON configurations (instance_id, parameter);
