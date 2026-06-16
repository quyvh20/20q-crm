// Phase-2 audit probe: login, fetch webhook token+secret, dump an existing workflow's JSON shape.
const http = require('http');
function req(path, { method='GET', body, token } = {}) {
  return new Promise((resolve, reject) => {
    const data = body ? JSON.stringify(body) : null;
    const u = new URL(path, 'http://localhost:8080');
    const opts = { hostname: u.hostname, port: u.port, path: u.pathname + u.search, method,
      headers: { 'Content-Type': 'application/json', ...(data?{'Content-Length':Buffer.byteLength(data)}:{}), ...(token?{Authorization:'Bearer '+token}:{}) } };
    const r = http.request(opts, x => { let c=[]; x.on('data',b=>c.push(b)); x.on('end',()=>{ const raw=Buffer.concat(c).toString(); let j; try{j=JSON.parse(raw)}catch{}; resolve({status:x.statusCode, json:j, raw}); }); });
    r.on('error', reject); if (data) r.write(data); r.end();
  });
}
(async () => {
  const login = await req('/api/auth/login', { method:'POST', body:{ email:'local_admin@20q.com', password:'password123' } });
  const token = login.json?.data?.access_token;
  console.log('login', login.status, 'token?', !!token);
  const tk = await req('/api/webhooks/token', { token });
  console.log('webhook token:', JSON.stringify(tk.json?.data));
  const sec = await req('/api/webhooks/reveal-secret', { method:'POST', token });
  console.log('secret:', sec.json?.data?.secret);
  const list = await req('/api/workflows?page=1&size=20', { token });
  const wfs = list.json?.data?.workflows || [];
  console.log('workflows:', wfs.map(w=>({id:w.id,name:w.name,active:w.is_active})));
  if (wfs[0]) {
    const wf = await req('/api/workflows/'+wfs[0].id, { token });
    console.log('WF DETAIL KEYS:', Object.keys(wf.json?.data||{}));
    console.log('WF DETAIL:', JSON.stringify(wf.json?.data));
  }
})().catch(e=>{ console.error('ERR', e); process.exit(1); });
