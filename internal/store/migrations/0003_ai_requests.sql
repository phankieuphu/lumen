CREATE TABLE ai_requests (
    id INTEGER PRIMARY KEY,
    created_at TEXT NOT NULL,
    provider TEXT NOT NULL,
    task TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('ok', 'error', 'cancelled', 'cached')),
    duration_ms INTEGER NOT NULL,
    input_tokens INTEGER NOT NULL,
    output_tokens INTEGER NOT NULL
);
