const https = require('https');
const BASE_URL = 'https://20q-crm-production.up.railway.app';
function requestRaw(path, body, token) {
  return new Promise((r, j) => {
    const data = body ? JSON.stringify(body) : '';
    const req = https.request({
      hostname: new URL(BASE_URL).hostname, path, method: 'POST',
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
  const q = await requestRaw('/api/ai/command', {message: 'Just reply OK directly.'}, t);
  console.log("Status:", q.status);
  console.log("Body snippet:", q.body.substring(0, 500));
}
run();
