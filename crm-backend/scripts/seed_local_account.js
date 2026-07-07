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
const get = (path, token) => request(path, {}, token, 'GET');
const patch = (path, body, token) => request(path, body, token, 'PATCH');

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

  // ── P6 (dynamic-role UX) test scaffolding ──────────────────────────────────
  // A custom role, a second user holding it, a pending invitation, and a second
  // org with a cross-membership so the role dropdowns, delete-with-reassign,
  // duplicate & customize, invitations panel, and the workspace chooser are all
  // locally exercisable. These are best-effort: invites hit RequireVerifiedEmail,
  // so if the local admin's email isn't verified some steps log and are skipped —
  // the custom role and business data above still seed regardless.
  const SECOND_EMAIL = 'john@20q.com';
  const THIRD_EMAIL = 'jane@20q.com';
  const DEVOWNER_EMAIL = 'devowner@20q.com';

  console.log('7. Custom role (Sales Manager, cloned from manager)...');
  let salesManagerRoleId, viewerRoleId;
  try {
    const roles = (await get('/api/roles', token)).body?.data || [];
    const managerId = roles.find(r => r.name === 'manager')?.id;
    viewerRoleId = roles.find(r => r.name === 'viewer')?.id;
    if (!roles.some(r => r.name === 'Sales Manager')) {
      const rr = await post('/api/roles', {
        name: 'Sales Manager',
        description: 'Manager-level access focused on the sales pipeline.',
        clone_from_id: managerId,
      }, token);
      salesManagerRoleId = rr.body?.data?.id;
      console.log('  created Sales Manager:', salesManagerRoleId || rr.raw?.substring(0, 200));
    } else {
      salesManagerRoleId = roles.find(r => r.name === 'Sales Manager')?.id;
      console.log('  Sales Manager already exists:', salesManagerRoleId);
    }
  } catch (e) { console.log('  role step failed:', e.message); }

  console.log('8. Second user (john@20q.com) invited as Sales Manager + accepted...');
  try {
    const inv = await post('/api/workspaces/invites', { email: SECOND_EMAIL, role_id: salesManagerRoleId || viewerRoleId }, token);
    const dtok = inv.body?.data?.debug_token;
    if (dtok) {
      const acc = await post('/api/auth/accept-invite', { token: dtok, password: PASSWORD, first_name: 'John', last_name: 'Rep' });
      console.log('  john joined:', acc.status);
    } else {
      console.log('  invite returned no debug_token (status ' + inv.status + ') — skipping accept:', inv.raw?.substring(0, 160));
    }
  } catch (e) { console.log('  second-user step failed:', e.message); }

  console.log('9. Pending invitation (jane@20q.com as viewer, left pending)...');
  try {
    const inv = await post('/api/workspaces/invites', { email: THIRD_EMAIL, role_id: viewerRoleId }, token);
    console.log('  pending invite status:', inv.status);
  } catch (e) { console.log('  pending-invite step failed:', e.message); }

  console.log('10. Second org (Acme DevCorp) + local_admin cross-membership...');
  try {
    const reg2 = await post('/api/auth/register', {
      email: DEVOWNER_EMAIL, password: PASSWORD, first_name: 'Dev', last_name: 'Owner', org_name: 'Acme DevCorp',
    });
    let devToken = reg2.body?.data?.access_token;
    if (reg2.status === 409) {
      const login2 = await post('/api/auth/login', { email: DEVOWNER_EMAIL, password: PASSWORD });
      devToken = login2.body?.data?.access_token;
    }
    if (devToken) {
      const devRoles = (await get('/api/roles', devToken)).body?.data || [];
      const devAdminId = devRoles.find(r => r.name === 'admin')?.id;
      const inv = await post('/api/workspaces/invites', { email: EMAIL, role_id: devAdminId }, devToken);
      const dtok = inv.body?.data?.debug_token;
      if (dtok) {
        // local_admin already exists → accept adds membership (no password needed).
        const acc = await post('/api/auth/accept-invite', { token: dtok });
        console.log('  local_admin added to Acme DevCorp as admin:', acc.status);
      } else {
        console.log('  cross-org invite returned no debug_token (status ' + inv.status + '):', inv.raw?.substring(0, 160));
      }
    }
  } catch (e) { console.log('  second-org step failed:', e.message); }

  console.log('\nSEED COMPLETE');
  console.log('-------------------------------');
  console.log(`Admin:    ${EMAIL} / ${PASSWORD}  (owns Local Test Corp; admin in Acme DevCorp)`);
  console.log(`Member:   ${SECOND_EMAIL} / ${PASSWORD}  (Sales Manager custom role, if invites are enabled)`);
  console.log(`Dev org:  ${DEVOWNER_EMAIL} / ${PASSWORD}  (owns Acme DevCorp)`);
  console.log('-------------------------------');
}
main().catch(console.error);
