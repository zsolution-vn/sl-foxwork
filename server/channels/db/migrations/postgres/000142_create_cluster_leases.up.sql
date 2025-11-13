CREATE TABLE IF NOT EXISTS cluster_leases (
    leaseid VARCHAR(64) PRIMARY KEY,
    holderid VARCHAR(64) NOT NULL,
    renewedat BIGINT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_cluster_leases_renewedat ON cluster_leases (renewedat);


