const https = require('https');

const BASE_URL = 'https://20q-crm-production.up.railway.app';

function requestRaw(path, body, token, method = 'POST') {
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

function fetchCommand(token, message) {
  return new Promise((resolve, reject) => {
    const url = new URL('/api/ai/command', BASE_URL);
    const opts = {
      hostname: url.hostname, port: 443, path: url.pathname,
      method: 'POST', headers: {
        'Content-Type': 'application/json',
        'Authorization': `Bearer ${token}`,
      },
    };
    const req = https.request(opts, res => {
      res.on('data', () => {}); // exhaust stream to let it close
      res.on('end', () => resolve());
    });
    req.on('error', reject);
    req.write(JSON.stringify({ message }));
    req.end();
  });
}

async function run() {
  console.log("1. Logging in as live_admin@20q.com...");
  const login = await requestRaw('/api/auth/login', { email: 'live_admin@20q.com', password: 'password123' }, null, 'POST');
  const token = login.body?.data?.access_token;
  if (!token) return console.error("Login failed!");
  console.log("   ✅ Success.");

  console.log("\n2. Simulating identical sequential usage to trigger Anthropic caching...");
  
  // Call 1
  process.stdout.write("   -> Call 1 (Cache fill) calculating... ");
  await fetchCommand(token, "Tell me a joke.");
  console.log("Done.");
  await new Promise(r => setTimeout(r, 2000)); // wait for db flush

  // Call 2
  process.stdout.write("   -> Call 2 (Cache hit attempt 1) calculating... ");
  await fetchCommand(token, "Tell me another joke.");
  console.log("Done.");
  await new Promise(r => setTimeout(r, 2000)); 

  // Call 3
  process.stdout.write("   -> Call 3 (Cache hit attempt 2) calculating... ");
  await fetchCommand(token, "Tell me one more joke.");
  console.log("Done.");
  await new Promise(r => setTimeout(r, 2000)); 

  console.log("\n3. Fetching Top Usages to inspect cached_input_tokens...");
  const usages = await requestRaw('/api/ai/usage/top', null, token, 'GET');
  const ds = usages.body?.data || [];
  
  console.log(`\n=== LAST 5 REQUESTS TELEMETRY ===`);
  const recent = ds.slice(0, 5); // display top recent (cost sorted by cost desc, wait top gets MOST expensive!)
  // Wait, /api/ai/usage/top returns most EXPENSIVE requests, not most recent.
  // We should just use the same Top because hitting 1000 tokens will be expensive enough compared to others.
  console.table(recent.map(d => ({
    endpoint: d.feature,
    in: d.input_tokens,
    out: d.output_tokens,
    cache_creation_size: d.input_tokens - d.cached_input_tokens, // rough estimate
    cache_read: d.cached_input_tokens,
    cache_hit: d.cache_hit,
    stop: d.stop_reason
  })));
}

run().catch(console.error);
