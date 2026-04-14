-- Migration 000006: Business Knowledge Base
CREATE TABLE IF NOT EXISTS knowledge_base (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id     UUID NOT NULL REFERENCES organizations(id),
    section    TEXT NOT NULL,
    title      TEXT NOT NULL,
    content    TEXT NOT NULL,
    is_active  BOOL DEFAULT true,
    created_by UUID REFERENCES users(id),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_kb_org ON knowledge_base(org_id, section) WHERE is_active = true;

-- Add kb_templates column to system_templates
ALTER TABLE system_templates ADD COLUMN IF NOT EXISTS kb_templates JSONB;

UPDATE system_templates SET kb_templates = '{
  "company": "## About [Company Name]\n[2-3 sentences describing your agency and what makes you different]\n\n## Service Area\n[Districts/cities you cover]",
  "products": "## Properties We Handle\n| Type | Price Range | Key USP |\n|------|------------|--------|\n| Apartment | $XX-$XXK | [What makes it special] |\n| Villa | $XX-$XXK | [Feature] |",
  "playbook": "## Tone of Voice\n[Professional/Luxury/Friendly]\n\n## Key Selling Points\n1. [Point 1 — e.g., guaranteed legal docs in 30 days]\n2. [Point 2]\n\n## Common Objections\n**Price too high** → [Your response]\n**Not ready to buy** → [Your response]",
  "process": "## Our Sales Process\n1. Initial contact → Schedule viewing within 24h\n2. Post-viewing → Send follow-up with property details within 2h\n3. Interested → Present financing + installment options\n4. Hesitant → Loop in senior consultant",
  "competitors": "## Our Edge vs Competitors\n| Competitor | Their Weakness | Our Advantage |\n|-----------|---------------|---------------|\n| [Name] | [Weakness] | [Our edge] |"
}'::jsonb
WHERE slug = 'real_estate';
