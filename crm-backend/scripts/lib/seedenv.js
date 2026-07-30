'use strict';

// Shared configuration for the scripts in crm-backend/scripts.
//
// FAIL-CLOSED BY DESIGN: there is no fallback credential here, not even a weak
// one for localhost. A hardcoded default is exactly what put a working
// production admin login (live_admin@20q.com / password123, pointed at the
// Railway prod URL) into the tip of a PUBLIC repo for 105 days. Any default
// string becomes a de-facto shared secret: it lands in docs, in shell history,
// and eventually back in the repo. Exiting with an actionable message costs one
// env var and cannot leak.
//
// The remote guard is a SECOND, independent switch rather than something folded
// into SEED_BASE, because the failure being prevented is not "no configuration"
// — it is "configuration that silently points at production", which is precisely
// what the deleted seed_live_account.js was. Localhost by default plus an
// explicit opt-in means no invocation reaches prod by accident.

const LOCAL_DEFAULT = 'http://localhost:8080';
const LOCAL_HOSTS = ['localhost', '127.0.0.1', '::1', '[::1]'];

function die(msg) {
  console.error('\n' + msg + '\n');
  process.exit(1);
}

function baseURL() {
  const base = process.env.SEED_BASE || LOCAL_DEFAULT;
  let u;
  try {
    u = new URL(base);
  } catch {
    die(`SEED_BASE is not a valid URL: ${base}`);
  }
  if (!LOCAL_HOSTS.includes(u.hostname) && process.env.SEED_ALLOW_REMOTE !== '1') {
    die(
      `Refusing to target remote host ${u.hostname}.\n` +
        `These scripts WRITE data (they register orgs, seed records and fire AI requests).\n` +
        `To target ${base} deliberately, also set SEED_ALLOW_REMOTE=1.`
    );
  }
  return base.replace(/\/+$/, '');
}

function credentials() {
  const email = process.env.SEED_EMAIL;
  const password = process.env.SEED_PASSWORD;
  if (!email || !password) {
    die(
      'SEED_EMAIL and SEED_PASSWORD must both be set. There is deliberately no default.\n\n' +
        'Note that SEED_PASSWORD must satisfy the password policy in\n' +
        'internal/usecase/password_policy.go — the invite-acceptance path enforces it,\n' +
        'so a blocklisted password makes the seed fail partway through.\n\n' +
        'PowerShell:\n' +
        '  $env:SEED_EMAIL="local_admin@20q.com"; $env:SEED_PASSWORD="<pw>"; node scripts/seed_local_account.js\n\n' +
        'bash:\n' +
        '  SEED_EMAIL=local_admin@20q.com SEED_PASSWORD="<pw>" node scripts/seed_local_account.js'
    );
  }
  return { email, password };
}

module.exports = { baseURL, credentials, die };
