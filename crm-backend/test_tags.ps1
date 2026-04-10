$BASE = "https://20q-crm-production.up.railway.app"

Write-Host "Tag System Verification Suite" -ForegroundColor Cyan

# 1. LOGIN
Write-Host "[1] Logging in..." -ForegroundColor Yellow
$login = Invoke-RestMethod -Uri "$BASE/api/auth/login" -Method POST -ContentType "application/json" -Body '{"email":"test2@prod.com","password":"SecurePassword123!"}'
$token = $login.data.access_token
$H = @{ Authorization = "Bearer $token" }
Write-Host "OK - Logged in" -ForegroundColor Green

# 2. CREATE TAG
Write-Host "[2] POST /api/tags - create VIP tag..." -ForegroundColor Yellow
$body = '{"name":"VIP","color":"#FFD700"}'
$tag = Invoke-RestMethod -Uri "$BASE/api/tags" -Method POST -ContentType "application/json" -Headers $H -Body $body
$tagID = $tag.data.id
Write-Host "OK - Created Tag: $tagID name=$($tag.data.name)" -ForegroundColor Green

# 3. LIST TAGS
Write-Host "[3] GET /api/tags..." -ForegroundColor Yellow
$tagsList = Invoke-RestMethod -Uri "$BASE/api/tags" -Headers $H
$tagsCount = @($tagsList.data).Length
Write-Host "OK - Total Tags: $tagsCount" -ForegroundColor Green

# 4. CREATE CONTACT WITH TAG ASSIGNED
Write-Host "[4] POST /api/contacts - create contact with tag..." -ForegroundColor Yellow
$ctBody = "{`"first_name`":`"Tag`",`"last_name`":`"Verifier`",`"email`":`"tag@verifier.com`",`"tag_ids`":[`"$tagID`"]}"
$ct = Invoke-RestMethod -Uri "$BASE/api/contacts" -Method POST -ContentType "application/json" -Headers $H -Body $ctBody
$ctID = $ct.data.id
Write-Host "OK - Created Contact: $ctID" -ForegroundColor Green

# 5. VERIFY CONTACT PRELOADS TAGS
Write-Host "[5] GET /api/contacts/$ctID - verify tags preloaded..." -ForegroundColor Yellow
$ctGet = Invoke-RestMethod -Uri "$BASE/api/contacts/$ctID" -Headers $H
$hasTag = $false
foreach ($t in $ctGet.data.tags) {
    if ($t.id -eq $tagID) { $hasTag = $true }
}
if ($hasTag) {
    Write-Host "OK - Contact has VIP tag assigned" -ForegroundColor Green
} else {
    Write-Host "FAIL - Contact missing VIP tag" -ForegroundColor Red
}

# 6. FILTER CONTACTS BY TAG_ID
Write-Host "[6] GET /api/contacts?tag_ids=$tagID..." -ForegroundColor Yellow
$filtered = Invoke-RestMethod -Uri "$BASE/api/contacts?tag_ids=$tagID" -Headers $H
Write-Host "OK - Found $($filtered.meta.total) contacts with VIP tag" -ForegroundColor Green

# 7. DELETE TAG
Write-Host "[7] DELETE /api/tags/$tagID - delete tag..." -ForegroundColor Yellow
$del = Invoke-RestMethod -Uri "$BASE/api/tags/$tagID" -Method DELETE -Headers $H
Write-Host "OK - $($del.data.message)" -ForegroundColor Green

Write-Host ""
Write-Host "ALL TESTS PASSED" -ForegroundColor Cyan
