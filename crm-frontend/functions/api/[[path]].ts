// Cloudflare Pages Function — reverse-proxies every /api/* request to the
// backend so the browser only ever talks to the Pages origin.
//
// Why this exists: the SPA (pages.dev) and the API (railway.app) are different
// sites, so the auth session cookie was a THIRD-PARTY cookie. Modern Chrome
// (and Incognito always) blocks third-party cookies, so /api/auth/refresh could
// never send the cookie → every reload bounced the user back to the login page.
//
// Routing all API calls through pages.dev/api makes the cookie FIRST-PARTY (it
// binds to the Pages domain), which third-party blocking does not touch. It also
// removes the need for CORS entirely, since the browser request is same-origin.
//
// The backend origin is read from the BACKEND_URL Pages env var, defaulting to
// the Railway production host so this works with zero dashboard config.

interface Env {
  BACKEND_URL?: string;
}

const DEFAULT_BACKEND = "https://20q-crm-production.up.railway.app";

// These response headers describe the transfer encoding of the ORIGIN body.
// Cloudflare's fetch may have already decompressed the body, so forwarding them
// verbatim can make the browser try to decode plain bytes → garbage. Drop them
// and let the platform recompute.
const STRIP_RESPONSE_HEADERS = new Set(["content-encoding", "content-length", "transfer-encoding"]);

export const onRequest: PagesFunction<Env> = async ({ request, env }) => {
  const backend = (env.BACKEND_URL || DEFAULT_BACKEND).replace(/\/+$/, "");
  const incoming = new URL(request.url);
  const target = backend + incoming.pathname + incoming.search;

  // Forward the request verbatim: method, headers (Cookie / Authorization /
  // Origin / X-CSRF-Token / Content-Type…) and body. Strip Host so fetch sets it
  // to the backend's host rather than pages.dev.
  const headers = new Headers(request.headers);
  headers.delete("host");

  const init: RequestInit = {
    method: request.method,
    headers,
    redirect: "manual", // hand OAuth 3xx straight back to the browser to follow
  };
  if (request.method !== "GET" && request.method !== "HEAD") {
    init.body = request.body;
  }

  const backendRes = await fetch(target, init);

  // Rebuild the response so each Set-Cookie survives as its own header. The
  // backend issues host-only cookies (no Domain attribute), so proxied through
  // here they bind to the Pages domain — first-party.
  const res = new Response(backendRes.body, {
    status: backendRes.status,
    statusText: backendRes.statusText,
  });
  backendRes.headers.forEach((value, key) => {
    const k = key.toLowerCase();
    if (k === "set-cookie" || STRIP_RESPONSE_HEADERS.has(k)) return;
    res.headers.set(key, value);
  });
  for (const cookie of backendRes.headers.getSetCookie()) {
    res.headers.append("set-cookie", cookie);
  }
  return res;
};
