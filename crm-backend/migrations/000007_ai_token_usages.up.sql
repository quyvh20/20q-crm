CREATE TABLE IF NOT EXISTS ai_token_usages (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    model VARCHAR(255) NOT NULL,
    provider VARCHAR(255) NOT NULL,
    feature VARCHAR(255) NOT NULL,
    input_tokens INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    cached_input_tokens INTEGER NOT NULL DEFAULT 0,
    latency_ms INTEGER NOT NULL DEFAULT 0,
    stop_reason VARCHAR(100) NOT NULL DEFAULT '',
    cache_hit BOOLEAN NOT NULL DEFAULT false,
    cost_usd NUMERIC(10, 6) NOT NULL DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_ai_token_usages_org_id ON ai_token_usages(org_id);
CREATE INDEX IF NOT EXISTS idx_ai_token_usages_user_id ON ai_token_usages(user_id);
