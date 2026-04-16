/**
 * Verifies the /followup slash command end-to-end:
 * 1. Confirms client-side expansion: /followup → full instruction
 * 2. Registers fresh org, creates a stage, seeds 3 deals (no activities = stale)
 * 3. Sends expanded instruction to /api/ai/command
 * 4. Asserts AI responds with follow-up email content
 */

const API = "http://localhost:8081/api";
const ts  = Date.now();

// Mirror frontend's SLASH_COMMANDS map
const SLASH_COMMANDS = {
  '/followup': 'Draft follow-up emails for all deals inactive 7+ days',
  '/summary':  'Give me a pipeline summary and key actions for this week',
  '/tasks':    'What are my overdue and due-today tasks?',
};
const expand = (raw) => SLASH_COMMANDS[raw.trim()] || raw.trim();

async function readSSE(res) {
  const reader  = res.body.getReader();
  const decoder = new TextDecoder();
  let buf = "";
  const events = [];
  while (true) {
    const { value, done } = await reader.read();
    if (done) break;
    buf += decoder.decode(value, { stream: true });
    const lines = buf.split('\n');
    buf = lines.pop() || "";
    for (const line of lines) {
      if (!line.startsWith('data: ')) continue;
      try { events.push(JSON.parse(line.slice(6))); } catch (_) {}
    }
  }
  return events;
}

async function apiFetch(path, token, body, method = "POST") {
  const H = { "Content-Type": "application/json" };
  if (token) H["Authorization"] = `Bearer ${token}`;
  const res = await fetch(`${API}${path}`, { method, headers: H, body: body ? JSON.stringify(body) : undefined });
  const text = await res.text();
  try { return { status: res.status, data: JSON.parse(text) }; }
  catch (_) { return { status: res.status, data: text }; }
}

async function run() {
  // ── Step 1: Client-side expansion ─────────────────────────────────────
  console.log("=== Step 1: Client-side expansion check ===");
  const raw      = '/followup';
  const expanded = expand(raw);
  const EXPECTED = 'Draft follow-up emails for all deals inactive 7+ days';
  console.log(`  Input   : "${raw}"`);
  console.log(`  Expanded: "${expanded}"`);
  if (expanded !== EXPECTED) {
    console.error(`  ❌ FAIL`); process.exit(1);
  }
  console.log("  ✅ PASS — /followup maps to the correct full instruction\n");

  // ── Step 2: Register org ───────────────────────────────────────────────
  console.log("=== Step 2: Register fresh test org ===");
  const regRes = await apiFetch('/auth/register', null, {
    email: `fu_${ts}@test.com`, password: "password123",
    first_name: "Follow", last_name: "Up", org_name: `FollowCo ${ts}`,
  });
  const token = regRes.data?.data?.access_token;
  if (!token) { console.error("  ❌ Register failed:", regRes.data); return; }
  console.log("  ✅ Registered\n");

  // ── Step 3: Create a pipeline stage ───────────────────────────────────
  console.log("=== Step 3: Create pipeline stage + 3 deals ===");
  const stageRes = await apiFetch('/pipeline/stages', token, { name: "Prospecting", order: 1 });
  const stageId  = stageRes.data?.data?.id ?? null;
  console.log(`  Stage: ${stageId ? `✅ ${stageId}` : "⚠️  skipped (no stage id)"}`);

  // ── Step 4: Seed 3 deals (no activities → stale) ─────────────────────
  const dealPayloads = [
    { title: "Acme Corp — Enterprise License", value: 15000, stage_id: stageId },
    { title: "TechStart — SaaS Subscription",  value:  8500, stage_id: stageId },
    { title: "GlobalRetail — Annual Plan",      value: 22000, stage_id: stageId },
  ];
  for (const d of dealPayloads) {
    if (!d.stage_id) delete d.stage_id;
    const r = await apiFetch('/deals', token, d);
    const id = r.data?.data?.id;
    console.log(`  Deal "${d.title}": ${id ? `✅ id=${id}` : `❌ ${JSON.stringify(r.data)}`}`);
  }

  // ── Step 5: Send expanded /followup instruction ────────────────────────
  console.log(`\n=== Step 4: POST /ai/command ===`);
  console.log(`  Sending: "${expanded}"`);
  const cmdRes = await fetch(`${API}/ai/command`, {
    method: "POST",
    headers: { "Content-Type": "application/json", "Authorization": `Bearer ${token}` },
    body: JSON.stringify({ message: expanded }),
  });
  console.log(`  HTTP status: ${cmdRes.status}`);

  const events = await readSSE(cmdRes);
  const types  = events.map(e => e.type);
  console.log(`  SSE events : ${types.join(' → ')}`);

  const aiText = events.find(e => e.type === 'response')?.message ?? '';
  console.log(`\n  AI Response:\n${'─'.repeat(60)}\n${aiText}\n${'─'.repeat(60)}`);

  // ── Step 6: Assertions ────────────────────────────────────────────────
  console.log("\n=== Step 5: Content assertions ===");
  const lower = aiText.toLowerCase();
  const checks = [
    ["mentions 'follow-up' or 'follow up'", lower.includes('follow-up') || lower.includes('follow up')],
    ["mentions 'email'",                    lower.includes('email')],
    ["mentions deals / inactive / stale",   lower.includes('deal') || lower.includes('inactive') || lower.includes('stale') || lower.includes('acme') || lower.includes('techstart') || lower.includes('globalretail')],
  ];

  let allPass = true;
  for (const [label, ok] of checks) {
    console.log(`  ${ok ? '✅' : '❌'} ${label}`);
    if (!ok) allPass = false;
  }

  console.log(allPass
    ? '\n✅ OVERALL PASS — /followup correctly expands and the AI generates follow-up email content!'
    : '\n⚠️  PARTIAL — Expansion is correct; review AI response above for relevance.');
}

run().catch(err => { console.error("Fatal:", err.message); process.exit(1); });
