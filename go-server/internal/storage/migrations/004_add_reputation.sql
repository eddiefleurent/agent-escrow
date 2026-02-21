CREATE TABLE IF NOT EXISTS reputation (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    address TEXT NOT NULL,
    role TEXT NOT NULL CHECK(role IN ('worker', 'buyer')),
    completed INTEGER NOT NULL DEFAULT 0,
    disputed INTEGER NOT NULL DEFAULT 0,
    failed INTEGER NOT NULL DEFAULT 0,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_reputation_address_role ON reputation(address, role);
