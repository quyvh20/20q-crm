const https = require('https');

const BASE_URL = 'https://20q-crm-production.up.railway.app';

function request(path, body, token, method = 'POST') {
  return new Promise((resolve, reject) => {
    const url = new URL(path, BASE_URL);
    const opts = {
      hostname: url.hostname, port: 443, path: url.pathname,
      method: method, headers: {
        'Content-Type': 'application/json',
        ...(token ? { 'Authorization': `Bearer ${token}` } : {}),
      },
    };
    
    let data;
    if (body) {
      data = JSON.stringify(body);
      opts.headers['Content-Length'] = Buffer.byteLength(data);
    }

    const req = https.request(opts, res => {
      let chunks = [];
      res.on('data', c => chunks.push(c));
      res.on('end', () => {
        const raw = Buffer.concat(chunks).toString();
        try { resolve({ status: res.statusCode, body: JSON.parse(raw), raw }); }
        catch { resolve({ status: res.statusCode, raw }); }
      });
    });
    req.on('error', reject);
    if (data) req.write(data);
    req.end();
  });
}

async function run() {
  console.log("1. Logging in as live_admin@20q.com...");
  const login = await request('/api/auth/login', { email: 'live_admin@20q.com', password: 'password123' }, null, 'POST');
  const token = login.body?.data?.access_token;
  if (!token) {
    console.error("Login failed!", login.raw);
    return;
  }
  console.log("   ✅ Success.");

  console.log("\n2. Triggering an AI Command (to ensure usage exists in DB)...");
  // using fetch for streaming SSE simply to trigger the request correctly without hanging on https.request stream parsing
  const fetchRes = await fetch(`${BASE_URL}/api/ai/command`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${token}` },
    body: JSON.stringify({ message: "Give me a detailed summary of my pipeline" })
  });
  const text = await fetchRes.text();
  console.log("   ✅ Command triggered (Response preview:", text.substring(0, 50).replace(/\n/g, '') + "... )");

  console.log("\n3. Waiting 2 seconds for DB write...");
  await new Promise(r => setTimeout(r, 2000));

  console.log("\n4. Fetching Top 10 Usages...");
  const usages = await request('/api/ai/usage/top?sort=recent', null, token, 'GET');
  if (usages.status !== 200) {
    console.error("   ❌ Failed to fetch:", usages.raw);
    return;
  }

  const ds = usages.body?.data || [];
  console.log(`\n=== TOP ${ds.length} MOST RECENT REQUESTS (Last 24h) ===`);
  console.table(ds.map(d => ({
    endpoint: d.feature,
    input_tokens: d.input_tokens,
    output_tokens: d.output_tokens,
    cache_hit: d.cache_hit,
    cost_usd: d.cost_usd
  })));
}

run().catch(console.error);
