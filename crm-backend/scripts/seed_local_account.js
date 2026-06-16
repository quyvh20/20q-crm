// Local seed — adapted from seed_live_account.js to target the local dev stack.
// Registers an admin, creates pipeline stages (incl. "Won"), tags (incl. "VIP"),
// companies, contacts (John Doe = VIP), and deals so the workflow builder demo
// has real data to pick from and run against.
const http = require('http');
const https = require('https');

const BASE = process.env.SEED_BASE || 'http://localhost:8080';

function request(path, body, token, method = 'POST') {
  return new Promise((resolve, reject) => {
    const data = JSON.stringify(body);
    const url = new URL(path, BASE);
    const opts = {
      hostname: url.hostname, port: url.port || (url.protocol === 'https:' ? 443 : 80), path: url.pathname,
      method, headers: {
        'Content-Type': 'application/json',
        'Content-Length': Buffer.byteLength(data),
        ...(token ? { 'Authorization': `Bearer ${token}` } : {}),
      },
    };
    const client = url.protocol === 'https:' ? https : http;
    const req = client.request(opts, res => {
      let chunks = [];
      res.on('data', c => chunks.push(c));
      res.on('end', () => {
        const raw = Buffer.concat(chunks).toString();
        try { resolve({ status: res.statusCode, body: JSON.parse(raw), raw }); }
        catch { resolve({ status: res.statusCode, raw }); }
      });
    });
    req.on('error', reject);
    req.write(data);
    req.end();
  });
}
const post = (path, body, token) => request(path, body, token, 'POST');
const put = (path, body, token) => request(path, body, token, 'PUT');

async function main() {
  const EMAIL = 'local_admin@20q.com';
  const PASSWORD = 'password123';

  console.log(`Target: ${BASE}`);
  console.log('1. Registering org...');
  const reg = await post('/api/auth/register', {
    email: EMAIL, password: PASSWORD,
    first_name: 'Local', last_name: 'Admin', org_name: 'Local Test Corp',
  });

  let token;
  if (reg.status === 409) {
    console.log('  user exists; logging in...');
    const login = await post('/api/auth/login', { email: EMAIL, password: PASSWORD });
    token = login.body?.data?.access_token;
  } else {
    token = reg.body?.data?.access_token;
  }
  if (!token) { console.log('  AUTH FAILED:', reg.status, reg.raw?.substring(0, 300)); return; }
  console.log('  auth OK');

  console.log('2. Stages...');
  const stageNames = ['Discovery', 'Proposal Made', 'Negotiation', 'Won', 'Lost'];
  const stageIDs = {};
  for (let i = 0; i < stageNames.length; i++) {
    const s = await post('/api/pipeline/stages', { name: stageNames[i], position: i, color: '#4F46E5' }, token);
    stageIDs[stageNames[i]] = s.body?.data?.id;
  }
  console.log('  stages:', stageIDs);

  console.log('3. Tags...');
  const tagNames = ['VIP', 'Technology', 'SaaS', 'Hot Lead', 'Enterprise'];
  const tagIDs = {};
  for (const t of tagNames) {
    const tr = await post('/api/tags', { name: t, color: '#FCD34D' }, token);
    tagIDs[t] = tr.body?.data?.id;
  }
  console.log('  tags:', tagIDs);

  console.log('4. Companies...');
  const comp1 = await post('/api/companies', { name: 'Acme Corp', industry: 'Manufacturing', website: 'https://acme.corp' }, token);
  const comp2 = await post('/api/companies', { name: 'Stark Industries', industry: 'Defense', website: 'https://stark.industries' }, token);
  const cID1 = comp1.body?.data?.id;
  const cID2 = comp2.body?.data?.id;

  console.log('5. Contacts...');
  const contacts = [
    { first_name: 'John', last_name: 'Doe', email: 'john@acme.corp', phone: '+1234567890', company_id: cID1, tag_ids: [tagIDs['VIP'], tagIDs['Enterprise']] },
    { first_name: 'Tony', last_name: 'Stark', email: 'tony@stark.industries', phone: '+1987654321', company_id: cID2, tag_ids: [tagIDs['Technology'], tagIDs['VIP']] },
    { first_name: 'Pepper', last_name: 'Potts', email: 'pepper@stark.industries', company_id: cID2, tag_ids: [tagIDs['Hot Lead']] },
  ];
  const contactRes = [];
  for (const c of contacts) {
    const r = await post('/api/contacts', c, token);
    contactRes.push(r.body?.data?.id);
  }
  console.log('  contacts:', contactRes);

  console.log('6. Deals...');
  const deals = [
    { title: 'Acme Software License', value: 50000, stage_id: stageIDs['Discovery'], contact_id: contactRes[0], company_id: cID1, probability: 20 },
    { title: 'Stark Cloud Migration', value: 150000, stage_id: stageIDs['Proposal Made'], contact_id: contactRes[1], company_id: cID2, probability: 50 },
  ];
  const dealRes = [];
  for (const d of deals) {
    const r = await post('/api/deals', d, token);
    dealRes.push(r.body?.data?.id);
  }
  console.log('  deals:', dealRes);

  console.log('\nSEED COMPLETE');
  console.log('-------------------------------');
  console.log(`Email:    ${EMAIL}`);
  console.log(`Password: ${PASSWORD}`);
  console.log('-------------------------------');
}
main().catch(console.error);
