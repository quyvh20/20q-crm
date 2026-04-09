-- Migration 000004: Industry Template Seeds
-- Insert 4 system_templates for common CRM verticals

INSERT INTO system_templates (slug, name, pipeline_stages, custom_field_defs, ai_context) VALUES
(
    'real_estate',
    'Real Estate',
    '["New Lead","Viewing Scheduled","Viewed","Deposit Paid","Notarized"]',
    '[{"key":"area","type":"number","label":"Area (m²)"},{"key":"budget","type":"currency","label":"Budget"},{"key":"direction","type":"select","label":"Direction","options":["East","West","South","North"]}]',
    'You are a CRM assistant for a real estate agency. Clients are looking to buy or rent property.'
),
(
    'education',
    'Education',
    '["Consultation","Placement Test","Awaiting Payment","Enrolled","Graduated"]',
    '[{"key":"level","type":"select","label":"Level","options":["Beginner","Intermediate","Advanced"]},{"key":"goal","type":"text","label":"Learning Goal"},{"key":"schedule","type":"text","label":"Preferred Schedule"}]',
    'You are a CRM assistant for an education center. Students are looking to enroll in courses.'
),
(
    'agency',
    'Agency / Services',
    '["Brief Received","Proposal","Negotiation","Signed","In Progress","Delivered"]',
    '[{"key":"budget","type":"currency","label":"Project Budget"},{"key":"channels","type":"multiselect","label":"Channels","options":["Facebook","Google","TikTok","Email","SEO"]},{"key":"deadline","type":"date","label":"Deadline"}]',
    'You are a CRM assistant for a marketing/creative agency managing client projects.'
),
(
    'retail',
    'Retail / E-commerce',
    '["Initial Contact","Demo","Proposal","Negotiation","Order Placed"]',
    '[{"key":"product_interest","type":"text","label":"Product Interest"},{"key":"quantity","type":"number","label":"Quantity"},{"key":"region","type":"select","label":"Region","options":["North","Central","South"]}]',
    'You are a CRM assistant for a retail or e-commerce business managing B2B orders.'
);
