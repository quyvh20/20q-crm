// Phase-2 audit harness. Usage: node scripts/p2_tests.js <webhook|compat|nested|loop>
const http = require('http');
const crypto = require('crypto');

function req(path, { method='GET', body, token, rawBody, headers={} } = {}) {
  return new Promise((resolve, reject) => {
    const data = rawBody !== undefined ? rawBody : (body ? JSON.stringify(body) : null);
    const u = new URL(path, 'http://localhost:8080');
    const opts = { hostname:u.hostname, port:u.port, path:u.pathname+u.search, method,
      headers: { 'Content-Type':'application/json', ...(data!=null?{'Content-Length':Buffer.byteLength(data)}:{}), ...(token?{Authorization:'Bearer '+token}:{}), ...headers } };
    const r = http.request(opts, x => { let c=[]; x.on('data',b=>c.push(b)); x.on('end',()=>{ const raw=Buffer.concat(c).toString(); let j; try{j=JSON.parse(raw)}catch{}; resolve({status:x.statusCode, json:j, raw}); }); });
    r.on('error', reject); if (data!=null) r.write(data); r.end();
  });
}
const sleep = ms => new Promise(r=>setTimeout(r,ms));
async function login() {
  const l = await req('/api/auth/login', { method:'POST', body:{ email:'local_admin@20q.com', password:'password123' } });
  return l.json.data.access_token;
}
async function pollRuns(token, wfId, { want='completed', tries=20, gap=700 } = {}) {
  for (let i=0;i<tries;i++) {
    const r = await req(`/api/workflows/${wfId}/runs?page=1&size=10`, { token });
    const runs = r.json?.data?.runs || [];
    const hit = runs.find(x => x.status === want);
    if (hit) return { run: hit, allStatuses: runs.map(x=>x.status) };
    if (runs.length && runs.every(x=>['failed','skipped','completed'].includes(x.status)) && want!=='failed') {
      // terminal but not the wanted status
      if (i>3) return { run:null, allStatuses: runs.map(x=>x.status), terminal:true };
    }
    await sleep(gap);
  }
  const r = await req(`/api/workflows/${wfId}/runs?page=1&size=10`, { token });
  return { run:null, allStatuses:(r.json?.data?.runs||[]).map(x=>x.status), timedOut:true };
}

async function testWebhook() {
  const token = await login();
  const tk = (await req('/api/webhooks/token', { token })).json.data;
  const secret = (await req('/api/webhooks/reveal-secret', { method:'POST', token })).json.data.secret;
  console.log('webhook url:', tk.url);

  // 1. Create an ACTIVE contact_created workflow with a create_task action (no external deps)
  const create = await req('/api/workflows', { method:'POST', token, body: {
    name: 'P2 Webhook → Task',
    description: 'audit #6',
    is_active: true,
    trigger: { type: 'contact_created', params: {} },
    conditions: null,
    steps: [ { id:'s1', type:'action', action:{ id:'s1', type:'create_task', params:{ title:'New lead: {{contact.email}}', priority:'high', due_in_days:2 } } } ],
    actions: [ { id:'s1', type:'create_task', params:{ title:'New lead: {{contact.email}}', priority:'high', due_in_days:2 } } ],
  }});
  const wfId = create.json?.data?.id;
  console.log('create wf:', create.status, wfId, 'active=', create.json?.data?.is_active);
  // ensure active
  if (create.json?.data?.is_active === false) {
    const t = await req(`/api/workflows/${wfId}/toggle`, { method:'POST', token });
    console.log('toggled active:', t.status, t.json?.data?.is_active);
  }

  // 2. POST signed inbound webhook with a fresh email
  const email = `lead_${Date.now()}@example.com`;
  const payloadObj = { email, first_name:'Web', last_name:'Hook', company:'Curl Co' };
  const rawBody = JSON.stringify(payloadObj);
  const sig = 'sha256=' + crypto.createHmac('sha256', secret).update(rawBody).digest('hex');
  console.log('--- bad signature attempt ---');
  const bad = await req(`/api/webhooks/inbound/${tk.token}`, { method:'POST', rawBody, headers:{ 'X-Signature':'sha256=deadbeef' } });
  console.log('bad sig status:', bad.status, bad.json?.error?.code);
  console.log('--- valid signature ---');
  const resp = await req(`/api/webhooks/inbound/${tk.token}`, { method:'POST', rawBody, headers:{ 'X-Signature':sig } });
  console.log('inbound status:', resp.status, JSON.stringify(resp.json));

  // 3. Poll the workflow runs for a completed run
  const { run, allStatuses } = await pollRuns(token, wfId, { want:'completed' });
  console.log('run statuses:', allStatuses);
  console.log('completed run:', run ? { id:run.id, status:run.status, ctxEmail: run.trigger_context?.contact?.email } : null);
  console.log(run && run.status==='completed' ? 'WEBHOOK_RESULT: PASS' : 'WEBHOOK_RESULT: FAIL');
}

(async () => {
  const cmd = process.argv[2];
  if (cmd === 'webhook') await testWebhook();
  else console.log('unknown cmd', cmd);
})().catch(e=>{ console.error('ERR', e); process.exit(1); });
