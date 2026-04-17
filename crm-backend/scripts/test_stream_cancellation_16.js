const https = require('https');
const BASE_URL = 'https://20q-crm-production.up.railway.app';
function requestRaw(path, body, token, method='POST') {
  return new Promise((r, j) => {
    const data = body ? JSON.stringify(body) : '';
    const req = https.request({
      hostname: new URL(BASE_URL).hostname, path, method,
      headers: { 'Content-Type': 'application/json', ...(token ? {'Authorization': 'Bearer '+token}:{})}
    }, res => {
      let b = ''; res.on('data', c => b+=c); res.on('end', () => r({status: res.statusCode, body: b}));
    });
    if (data) req.write(data); req.end();
  });
}

function streamRaw(path, body, token) {
  return new Promise((resolve, reject) => {
    const req = https.request({
      hostname: new URL(BASE_URL).hostname,
      path,
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'Authorization': 'Bearer '+token }
    }, res => {
      let textOutput = '';
      res.on('data', chunk => {
        textOutput += chunk;
        console.log(`[Stream Chunk] => ${chunk.toString().substring(0, 100).replace(/\n/g, '\\n')}`);
        if(textOutput.length > 50) {
            console.log("\n[!] Forcefully aborting connection mid-stream over 50 chars!");
            req.destroy();
            resolve("aborted");
        }
      });
      res.on('end', () => resolve("finished normally"));
    });
    req.write(JSON.stringify(body));
    req.end();
  });
}

async function run() {
  const login = await requestRaw('/api/auth/login', {email:'live_admin@20q.com',password:'password123'});
  const t = JSON.parse(login.body).data.access_token;
  
  console.log("Starting a massive streaming request...");
  await streamRaw('/api/ai/chat', {message: 'Write me an extremely long 1000-word essay about the history of artificial intelligence and its detailed implications on society. Do not use summarizing.'}, t);
  
  console.log("\nWaiting 2 seconds for server to parse aborted telemetry...");
  await new Promise(r => setTimeout(r, 2000));
  
  console.log("\nFetching recent top usages...");
  const usages = await requestRaw('/api/ai/usage/top?sort=recent', null, t, 'GET');
  const ds = JSON.parse(usages.body).data || [];
  
  const mostRecent = ds[0];
  console.log("\n=== MOST RECENT TELEMETRY RECORD ===");
  console.log(`Endpoint: ${mostRecent.feature}`);
  console.log(`Output Tokens (Estimated from Early Term): ${mostRecent.output_tokens}`);
  console.log(`Latency Ms (Reflects early close): ${mostRecent.latency_ms} ms`);
  console.log(`Stop Reason: ${mostRecent.stop_reason}`);
  if (mostRecent.stop_reason === 'client_aborted' && mostRecent.latency_ms < 2000) {
      console.log("\n✅ SUCCESS: Connection was accurately aborted gracefully! Upstream was terminated. You are not paying for background token inflation.");
  } else {
      console.log("\n❌ FAIL: Did not properly catch abort!");
  }
}
run();
