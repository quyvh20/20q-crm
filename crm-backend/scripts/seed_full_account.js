const http = require('http');

const BASE = 'http://localhost:8081';

function post(path, body, token) {
  return new Promise((resolve, reject) => {
    const data = JSON.stringify(body);
    const url = new URL(path, BASE);
    const opts = {
      hostname: url.hostname, port: url.port, path: url.pathname,
      method: 'POST', headers: {
        'Content-Type': 'application/json',
        'Content-Length': Buffer.byteLength(data),
        ...(token ? { 'Authorization': `Bearer ${token}` } : {}),
      },
    };
    const req = http.request(opts, res => {
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

function put(path, body, token) {
  return new Promise((resolve, reject) => {
    const data = JSON.stringify(body);
    const url = new URL(path, BASE);
    const opts = {
      hostname: url.hostname, port: url.port, path: url.pathname,
      method: 'PUT', headers: {
        'Content-Type': 'application/json',
        'Content-Length': Buffer.byteLength(data),
        ...(token ? { 'Authorization': `Bearer ${token}` } : {}),
      },
    };
    const req = http.request(opts, res => {
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

async function main() {
  const EMAIL = 'test_admin@20q.com';
  const PASSWORD = 'password123';

  console.log('1. Registering super test org...');
  const reg = await post('/api/auth/register', {
    email: EMAIL,
    password: PASSWORD,
    first_name: 'Test',
    last_name: 'Admin',
    org_name: 'Super Test Corp',
  });
  
  if (reg.status === 409) {
    console.log('  ⚠️ User already exists. Make sure to login to get token instead, but for this script we assume clean slate or we restart DB.');
    // Try to login
    const login = await post('/api/auth/login', { email: EMAIL, password: PASSWORD });
    token = login.body?.data?.access_token;
  } else {
    token = reg.body?.data?.access_token;
  }

  if (!token) {
    console.log('  ❌ Auth failed:', reg.raw?.substring(0, 200));
    return;
  }
  console.log('  ✅ Auth Success');

  // Stages
  console.log('2. Creating Pipeline Stages...');
  const stageNames = ['Discovery', 'Proposal Made', 'Negotiation', 'Won', 'Lost'];
  const stageIDs = {};
  for (let i=0; i<stageNames.length; i++) {
    const s = await post('/api/pipeline/stages', { name: stageNames[i], position: i, color: '#4F46E5' }, token);
    stageIDs[stageNames[i]] = s.body?.data?.id;
  }
  console.log('  ✅ Stages Created');

  // Tags
  console.log('3. Creating Tags...');
  const tagNames = ['VIP', 'Technology', 'SaaS', 'Hot Lead', 'Enterprise'];
  const tagIDs = {};
  for (const t of tagNames) {
    const tr = await post('/api/tags', { name: t, color: '#FCD34D' }, token);
    tagIDs[t] = tr.body?.data?.id;
  }
  console.log('  ✅ Tags Created');

  // Companies
  console.log('4. Creating Companies...');
  const comp1 = await post('/api/companies', { name: 'Acme Corp', industry: 'Manufacturing', website: 'https://acme.corp' }, token);
  const comp2 = await post('/api/companies', { name: 'Stark Industries', industry: 'Defense', website: 'https://stark.industries' }, token);
  const comp3 = await post('/api/companies', { name: 'Wayne Enterprises', industry: 'Conglomerate', website: 'https://wayne.enterprises' }, token);
  
  const cID1 = comp1.body?.data?.id;
  const cID2 = comp2.body?.data?.id;
  const cID3 = comp3.body?.data?.id;
  console.log('  ✅ Companies Created');

  // Contacts
  console.log('5. Creating Contacts...');
  const contacts = [
    { first_name: 'John', last_name: 'Doe', email: 'john@acme.corp', phone: '+1234567890', company_id: cID1, tag_ids: [tagIDs['VIP'], tagIDs['Enterprise']] },
    { first_name: 'Tony', last_name: 'Stark', email: 'tony@stark.industries', phone: '+1987654321', company_id: cID2, tag_ids: [tagIDs['Technology'], tagIDs['VIP']] },
    { first_name: 'Bruce', last_name: 'Wayne', email: 'bruce@wayne.enterprises', phone: '+1029384756', company_id: cID3, tag_ids: [tagIDs['Enterprise']] },
    { first_name: 'Pepper', last_name: 'Potts', email: 'pepper@stark.industries', company_id: cID2, tag_ids: [tagIDs['Hot Lead']] },
  ];

  const contactRes = [];
  for (const c of contacts) {
    const r = await post('/api/contacts', c, token);
    contactRes.push(r.body?.data?.id);
  }
  console.log('  ✅ Contacts Created');

  // Deals
  console.log('6. Creating Deals...');
  const deals = [
    { title: 'Acme Software License', value: 50000, stage_id: stageIDs['Discovery'], contact_id: contactRes[0], company_id: cID1, probability: 20 },
    { title: 'Stark Cloud Migration', value: 150000, stage_id: stageIDs['Proposal Made'], contact_id: contactRes[1], company_id: cID2, probability: 50 },
    { title: 'Wayne Infrastructure Upgrade', value: 250000, stage_id: stageIDs['Negotiation'], contact_id: contactRes[2], company_id: cID3, probability: 80 },
    { title: 'Stark Small Contract', value: 10000, stage_id: stageIDs['Discovery'], contact_id: contactRes[3], company_id: cID2, probability: 10 },
  ];

  const dealRes = [];
  for (const d of deals) {
    const r = await post('/api/deals', d, token);
    dealRes.push(r.body?.data?.id);
  }
  console.log('  ✅ Deals Created');

  // Knowledge Base
  console.log('7. Seeding Knowledge Base...');
  await put('/api/knowledge-base/company', { content: "Super Test Corp is a leading software provider established in 2026. We pride ourselves on creating high quality SaaS solutions." }, token);
  await put('/api/knowledge-base/playbook', { content: "Always maintain a professional but friendly tone. Focus on long-term value over short-term sales. For pricing questions, emphasize our transparent flat-rate model." }, token);
  await put('/api/knowledge-base/products', { content: "Product A: Premium CRM at $99/mo.\nProduct B: Enterprise Analytics at $499/mo." }, token);
  console.log('  ✅ KB Seeded');

  // Activities
  console.log('8. Seeding Activities...');
  if (dealRes[0]) {
    await post('/api/activities', { type: 'note', title: 'Initial Call Notes', description: 'John is very interested in our software. Needs a demo next week.', contact_id: contactRes[0], deal_id: dealRes[0] }, token);
  }
  if (dealRes[1]) {
    await post('/api/activities', { type: 'meeting', title: 'Consultation with Tony', description: 'Discussed cloud scaling requirements. They expect 10x growth next year.', contact_id: contactRes[1], deal_id: dealRes[1] }, token);
  }
  console.log('  ✅ Activities Seeded');

  console.log('\n\n✅ ALL SEEDING COMPLETE');
  console.log('----------------------------------------------------');
  console.log(`Email:    ${EMAIL}`);
  console.log(`Password: ${PASSWORD}`);
  console.log('----------------------------------------------------');
}

main().catch(console.error);
