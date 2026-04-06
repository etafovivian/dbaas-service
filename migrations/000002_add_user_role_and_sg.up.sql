-- User role and database access
ALTER TABLE users ADD COLUMN role TEXT NOT NULL DEFAULT 'readonly'
    CHECK (role IN ('owner','readwrite','readonly'));
ALTER TABLE users ADD COLUMN databases TEXT[] NOT NULL DEFAULT '{}';

-- Security group tracking on instances
ALTER TABLE instances ADD COLUMN openstack_secgroup_id TEXT;

-- Firewall rules for per-instance security group management
CREATE TABLE IF NOT EXISTS firewall_rules (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    instance_id         UUID        NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
    tenant_id           UUID        NOT NULL,
    direction           TEXT        NOT NULL DEFAULT 'ingress'
                        CHECK (direction IN ('ingress','egress')),
    protocol            TEXT        NOT NULL DEFAULT 'tcp'
                        CHECK (protocol IN ('tcp','udp','icmp')),
    port_range_min      INT,
    port_range_max      INT,
    remote_ip_prefix    TEXT        NOT NULL,
    description         TEXT        NOT NULL DEFAULT '',
    openstack_rule_id   TEXT,
    status              TEXT        NOT NULL DEFAULT 'active'
                        CHECK (status IN ('creating','active','deleting','deleted','error')),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS firewall_rules_instance_id ON firewall_rules (instance_id);
