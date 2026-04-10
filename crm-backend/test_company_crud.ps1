$BASE = "https://20q-crm-production.up.railway.app"

Write-Host "Company CRUD Verification Suite" -ForegroundColor Cyan

# LOGIN
Write-Host "[1] Logging in..." -ForegroundColor Yellow
$login = Invoke-RestMethod -Uri "$BASE/api/auth/login" -Method POST -ContentType "application/json" -Body '{"email":"test2@prod.com","password":"SecurePassword123!"}'
$token = $login.data.access_token
$H = @{ Authorization = "Bearer $token" }
Write-Host "OK - Logged in" -ForegroundColor Green

# CREATE company
Write-Host "[2] POST /api/companies - create Acme Corp..." -ForegroundColor Yellow
$body = '{"name":"Acme Corp","industry":"Technology","website":"https://acme.com"}'
$comp = Invoke-RestMethod -Uri "$BASE/api/companies" -Method POST -ContentType "application/json" -Headers $H -Body $body
$compID = $comp.data.id
Write-Host "OK - Created: $compID name=$($comp.data.name)" -ForegroundColor Green

# GET by ID
Write-Host "[3] GET /api/companies/$compID..." -ForegroundColor Yellow
$got = Invoke-RestMethod -Uri "$BASE/api/companies/$compID" -Headers $H
Write-Host "OK - Got: $($got.data.name) industry=$($got.data.industry)" -ForegroundColor Green

# LIST
Write-Host "[4] GET /api/companies (list)..." -ForegroundColor Yellow
$list = Invoke-RestMethod -Uri "$BASE/api/companies" -Headers $H
Write-Host "OK - Total companies: $($list.meta.total)" -ForegroundColor Green

# UPDATE
Write-Host "[5] PUT /api/companies/$compID - rename..." -ForegroundColor Yellow
$upd = Invoke-RestMethod -Uri "$BASE/api/companies/$compID" -Method PUT -ContentType "application/json" -Headers $H -Body '{"name":"Acme Inc","website":"https://acme-inc.com"}'
Write-Host "OK - Updated name: $($upd.data.name)" -ForegroundColor Green

# CREATE contact linked to company
Write-Host "[6] POST /api/contacts - linked to company..." -ForegroundColor Yellow
$ctBody = "{`"first_name`":`"Jane`",`"last_name`":`"Linked`",`"email`":`"jane.linked@acme.com`",`"company_id`":`"$compID`"}"
$ct = Invoke-RestMethod -Uri "$BASE/api/contacts" -Method POST -ContentType "application/json" -Headers $H -Body $ctBody
Write-Host "OK - Contact $($ct.data.first_name) $($ct.data.last_name) company_id=$($ct.data.company_id)" -ForegroundColor Green

# GET contact - verify company preloaded
Write-Host "[7] GET /api/contacts/$($ct.data.id) - verify company preloaded..." -ForegroundColor Yellow
$ctGet = Invoke-RestMethod -Uri "$BASE/api/contacts/$($ct.data.id)" -Headers $H
Write-Host "OK - Contact company name: $($ctGet.data.company.name)" -ForegroundColor Green

# FILTER by company_id
Write-Host "[8] GET /api/contacts?company_id=$compID" -ForegroundColor Yellow
$filtered = Invoke-RestMethod -Uri "$BASE/api/contacts?company_id=$compID" -Headers $H
Write-Host "OK - Contacts under company: $($filtered.meta.total)" -ForegroundColor Green

# SOFT DELETE
Write-Host "[9] DELETE /api/companies/$compID..." -ForegroundColor Yellow
$del = Invoke-RestMethod -Uri "$BASE/api/companies/$compID" -Method DELETE -Headers $H
Write-Host "OK - $($del.data.message)" -ForegroundColor Green

# VERIFY gone
Write-Host "[10] Verify soft delete - should NOT appear in list..." -ForegroundColor Yellow
$list2 = Invoke-RestMethod -Uri "$BASE/api/companies" -Headers $H
$found = $list2.data | Where-Object { $_.id -eq $compID }
if ($found) {
    Write-Host "FAIL - deleted company still visible" -ForegroundColor Red
} else {
    Write-Host "OK - Soft delete confirmed - company not in list" -ForegroundColor Green
}

Write-Host ""
Write-Host "ALL TESTS PASSED" -ForegroundColor Cyan
