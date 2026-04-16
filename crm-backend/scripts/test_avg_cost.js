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
  
  // Since we don't have a direct endpoint for average cost, let's fetch the top 100 recent queries and average them client-side.
  const usages = await requestRaw('/api/ai/usage/top?sort=recent', null, t, 'GET');
  const ds = JSON.parse(usages.body).data || [];
  
  let totals = {};
  for (const d of ds) {
    if (!totals[d.feature]) totals[d.feature] = {cost: 0, count: 0};
    totals[d.feature].cost += d.cost_usd;
    totals[d.feature].count += 1;
  }
  
  console.log("\n=== MEASURED EMPIRICAL BASELINES ===\n");
  for (const [feature, data] of Object.entries(totals)) {
     const avg = data.cost / data.count;
     console.log(`Endpoint: ${feature.padEnd(20)} | Avg Cost: $${avg.toFixed(6)} | Computed from ${data.count} live requests`);
  }
  
}
run();
