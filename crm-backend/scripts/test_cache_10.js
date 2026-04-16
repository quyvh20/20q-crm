const https = require('https');
const BASE_URL = 'https://20q-crm-production.up.railway.app';
function requestRaw(path, body, token) {
  return new Promise((r, j) => {
    const data = body ? JSON.stringify(body) : '';
    const req = https.request({
      hostname: new URL(BASE_URL).hostname, path, method: 'POST',
      headers: { 'Content-Type': 'application/json', ...(token ? {'Authorization': 'Bearer '+token}:{})}
    }, res => {
      let b = ''; res.on('data', c => b+=c); res.on('end', () => r(JSON.parse(b)));
    });
    if (data) req.write(data); req.end();
  });
}
async function run() {
  const login = await requestRaw('/api/auth/login', {email:'live_admin@20q.com',password:'password123'});
  const t = login.data.access_token;
  for(let i=0;i<6;i++) {
    await new Promise((r,j) => {
      const q = https.request({
        hostname: new URL(BASE_URL).hostname, path: '/api/ai/command', method: 'POST',
        headers: { 'Content-Type': 'application/json', 'Authorization': 'Bearer '+t}
      }, res => { res.on('data', ()=>{}); res.on('end', r); });
      q.write(JSON.stringify({message: 'Just reply OK directly.'})); q.end();
    });
    await new Promise(r => setTimeout(r, 1000));
  }
}
run();
