$ErrorActionPreference = "Stop"

$files = Get-ChildItem -Path ".\services" -Recurse -Filter "*.sql" |
    Where-Object { $_.FullName -match "\migrations\" } |
    Sort-Object FullName

foreach ($file in $files) {
    Write-Host "Applying $($file.FullName)"
    Get-Content $file.FullName -Raw |
        docker compose exec -T postgres psql -U compound -d compound
}

Write-Host "All migrations applied successfully."
