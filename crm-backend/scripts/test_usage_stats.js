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
async function run() {
  const login = await requestRaw('/api/auth/login', {email:'live_admin@20q.com',password:'password123'});
  const t = JSON.parse(login.body).data.access_token;
  
  console.log("Fetching AI endpoint stats from live DB...");
  const res = await requestRaw('/api/ai/usage/stats', null, t, 'GET');
  if(res.status !== 200) {
    console.error("Failed!", res.body);
    return;
  }
  
  const stats = JSON.parse(res.body).data;
  console.log("\n=== STOP REASON: 'MAX_TOKENS' PERCENTAGE ===\n");
  for(const [feature, data] of Object.entries(stats)) {
     console.log(`Endpoint: ${feature.padEnd(20)} | Total: ${data.total.toString().padEnd(4)} | Max Token Hits: ${data.max_tokens.toString().padEnd(4)} | Rate: ${data.percent.toFixed(2)}%`);
  }
  
}
run();
