"""P4 live end-to-end against the running backend (real Postgres, real HTTP).

Exercises universal relationships + tags through the actual uniform API:
register -> create custom object + company + records -> link -> resolve labels ->
tag (object_links path AND contact_tags path) -> remove -> delete-cascade.
"""
import json
import time
import urllib.request
import urllib.error

BASE = "http://localhost:8080"
TOKEN = None
PASS = 0


def call(method, path, body=None, expect=None):
    url = BASE + path
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(url, data=data, method=method)
    req.add_header("Content-Type", "application/json")
    if TOKEN:
        req.add_header("Authorization", "Bearer " + TOKEN)
    try:
        resp = urllib.request.urlopen(req)
        code, raw = resp.getcode(), resp.read()
    except urllib.error.HTTPError as e:
        code, raw = e.code, e.read()
    payload = json.loads(raw) if raw else {}
    if expect and code != expect:
        raise SystemExit(f"FAIL {method} {path}: expected {expect}, got {code}: {raw[:300]}")
    return code, payload


def ok(label):
    global PASS
    PASS += 1
    print(f"  PASS  {label}")


# 1. Register a fresh org/admin
email = f"p4_{int(time.time())}@example.com"
code, p = call("POST", "/api/auth/register", {
    "org_name": "P4 Test Co", "email": email, "password": "password123",
    "first_name": "P4", "last_name": "Tester",
}, expect=201)
TOKEN = p["data"]["access_token"]
ok(f"registered + got token ({email})")

# 2. Create a custom object def "asset"
call("POST", "/api/objects", {
    "slug": "asset", "label": "Asset", "label_plural": "Assets", "icon": "💻",
    "fields": [{"key": "name", "label": "Name", "type": "text"}],
}, expect=201)
ok("created custom object 'asset'")

# 3. Create a company + an asset record via the UNIFORM endpoint
_, c = call("POST", "/api/companies", {"name": "Acme Corp"}, expect=201)
company_id = c["data"]["id"]
_, a = call("POST", "/api/registry/objects/asset/records", {"fields": {"name": "Laptop-001"}}, expect=201)
asset_id = a["data"]["id"]
ok(f"created company {company_id[:8]} + asset {asset_id[:8]} (uniform)")
assert a["data"]["display"] == "Laptop-001", a["data"]  # R8 read-time display
ok("asset display resolved from field def (R8)")

# 4. custom -> company link
_, l = call("POST", f"/api/registry/objects/asset/records/{asset_id}/links", {
    "relation_key": "owner", "to_slug": "company", "to_id": company_id,
}, expect=201)
link_id = l["data"]["id"]
assert l["data"]["to_display"] == "Acme Corp", l["data"]
ok("linked asset -> company; target resolved to 'Acme Corp' (not a UUID)")

# 5. idempotent re-link returns same edge, no duplicate
_, l2 = call("POST", f"/api/registry/objects/asset/records/{asset_id}/links", {
    "relation_key": "owner", "to_slug": "company", "to_id": company_id,
}, expect=201)
assert l2["data"]["id"] == link_id, (l2["data"], link_id)
ok("re-link is idempotent (same edge id)")

# 6. ListLinks resolves the relationship
_, ll = call("GET", f"/api/registry/objects/asset/records/{asset_id}/links", expect=200)
assert len(ll["data"]) == 1 and ll["data"][0]["to_display"] == "Acme Corp", ll["data"]
ok("ListLinks returns the resolved relationship")

# 7. self-link + tag-edge rejected
code, _ = call("POST", f"/api/registry/objects/asset/records/{asset_id}/links", {
    "relation_key": "x", "to_slug": "asset", "to_id": asset_id})
assert code == 400, code
code, _ = call("POST", f"/api/registry/objects/asset/records/{asset_id}/links", {
    "relation_key": "tags", "to_slug": "company", "to_id": company_id})
assert code == 400, code
ok("self-link and tag-edge rejected with 400")

# 8. Tag a COMPANY via object_links path (uniform tag API)
_, t = call("POST", "/api/tags", {"name": "Important", "color": "#ff0000"}, expect=201)
tag_id = t["data"]["id"]
call("POST", f"/api/registry/objects/company/records/{company_id}/tags", {"tag_id": tag_id}, expect=200)
_, ct = call("GET", f"/api/registry/objects/company/records/{company_id}/tags", expect=200)
assert len(ct["data"]) == 1 and ct["data"][0]["id"] == tag_id, ct["data"]
ok("tagged a company via object_links; ListTags returns it")

# 9. Tag a CONTACT via the legacy contact_tags path (same API)
_, ctc = call("POST", "/api/contacts", {"first_name": "Ada", "last_name": "Lovelace"}, expect=201)
contact_id = ctc["data"]["id"]
call("POST", f"/api/registry/objects/contact/records/{contact_id}/tags", {"tag_id": tag_id}, expect=200)
_, ctt = call("GET", f"/api/registry/objects/contact/records/{contact_id}/tags", expect=200)
assert len(ctt["data"]) == 1 and ctt["data"][0]["id"] == tag_id, ctt["data"]
ok("tagged a contact via contact_tags; SAME uniform API shape")

# 10. Remove a tag (idempotent)
call("DELETE", f"/api/registry/objects/company/records/{company_id}/tags/{tag_id}", expect=200)
_, ct2 = call("GET", f"/api/registry/objects/company/records/{company_id}/tags", expect=200)
assert ct2["data"] == [] or ct2["data"] is None, ct2["data"]
ok("untagged the company")

# 11. Delete the asset -> cascade soft-deletes its link
call("DELETE", f"/api/registry/objects/asset/records/{asset_id}", expect=200)
ok("deleted asset record")
# The link removal endpoint should now 404 (edge already cascaded).
code, _ = call("DELETE", f"/api/registry/links/{link_id}")
assert code == 404, f"expected the cascaded link to be gone (404), got {code}"
ok("deleting the record cascade-removed its link (R3)")

print(f"\nALL {PASS} LIVE E2E CHECKS PASSED")
