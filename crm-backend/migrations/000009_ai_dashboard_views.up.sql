-- Migration 000009: Pre-built AI Dashboard Analytics Views
-- Defines saved SQL Views to instantly answer high-value business questions.

-- 1. Index to speed up temporal queries
CREATE INDEX IF NOT EXISTS idx_ai_token_usages_created_at ON ai_token_usages(created_at);

-- 2. "Top 10 most expensive requests yesterday"
CREATE OR REPLACE VIEW v_ai_top_expensive_requests_yesterday AS
SELECT 
    id, org_id, user_id, feature, model, provider, 
    input_tokens, output_tokens, cached_input_tokens, 
    cost_usd, latency_ms, cache_hit, stop_reason, created_at
FROM ai_token_usages
WHERE created_at >= CURRENT_DATE - INTERVAL '1 day' 
  AND created_at < CURRENT_DATE
ORDER BY cost_usd DESC
LIMIT 10;

-- 3. "Top 10 most expensive users this week"
CREATE OR REPLACE VIEW v_ai_top_expensive_users_this_week AS
SELECT 
    u.id AS user_id,
    u.email,
    u.first_name,
    u.last_name,
    SUM(a.cost_usd) AS total_cost_usd,
    COUNT(a.id) AS total_requests
FROM ai_token_usages a
JOIN users u ON a.user_id = u.id
WHERE a.created_at >= date_trunc('week', CURRENT_DATE)
GROUP BY u.id, u.email, u.first_name, u.last_name
ORDER BY total_cost_usd DESC
LIMIT 10;

-- 4. "Cost per endpoint this month"
CREATE OR REPLACE VIEW v_ai_cost_per_endpoint_this_month AS
SELECT 
    feature AS endpoint,
    SUM(cost_usd) AS total_cost_usd,
    SUM(input_tokens) AS total_input_tokens,
    SUM(output_tokens) AS total_output_tokens,
    SUM(cached_input_tokens) AS total_cached_input_tokens,
    COUNT(id) AS total_requests
FROM ai_token_usages
WHERE created_at >= date_trunc('month', CURRENT_DATE)
GROUP BY feature
ORDER BY total_cost_usd DESC;

-- 5. "Cost per user tier" (Organizational Billing Tier mapping)
CREATE OR REPLACE VIEW v_ai_cost_per_org_tier AS
SELECT 
    o.plan_tier,
    SUM(a.cost_usd) AS total_cost_usd,
    COUNT(DISTINCT a.org_id) AS active_orgs,
    COUNT(a.id) AS total_requests
FROM ai_token_usages a
JOIN organizations o ON a.org_id = o.id
GROUP BY o.plan_tier
ORDER BY total_cost_usd DESC;

-- 6. "Empirical Average Cost Baseline per Endpoint"
CREATE OR REPLACE VIEW v_ai_endpoint_cost_baselines AS
SELECT 
    feature AS endpoint,
    COUNT(id) AS sample_size,
    AVG(cost_usd) AS avg_cost_usd_baseline,
    AVG(input_tokens) AS avg_input_tokens,
    AVG(output_tokens) AS avg_output_tokens,
    AVG(cached_input_tokens) AS avg_cached_input_tokens
FROM ai_token_usages
GROUP BY feature
HAVING COUNT(id) >= 5;
