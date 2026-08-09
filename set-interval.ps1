param([int]$IntervalMs = -1)

$ErrorActionPreference = "Stop"
$scrollFile = Join-Path $PSScriptRoot "scroll.go"
$encoding = [System.Text.UTF8Encoding]::new($true)  # UTF8 with BOM

$line = Select-String -Path $scrollFile -Pattern 'time\.After\(\d+\s*\*\s*time\.Millisecond\)'
if ($line -and $line.Matches.Count -gt 0) {
    $m = [regex]::Match($line.Line, '(\d+)\s*\*\s*time\.Millisecond')
    if ($m.Success) {
        Write-Host ("current interval: " + $m.Groups[1].Value + " ms")
    }
}

if ($IntervalMs -le 0) {
    $v = Read-Host "enter new interval (ms)"
    if ($v -match '^\d+$') { $IntervalMs = [int]$v }
    if ($IntervalMs -le 0) { Write-Host "invalid input"; exit 1 }
}

Write-Host ("set interval to " + $IntervalMs + " ms ...")
$content = [System.IO.File]::ReadAllText($scrollFile)
$content = $content -replace '(time\.After\()\d+(\s*\*\s*time\.Millisecond\))', ('${1}' + $IntervalMs + '${2}')
[System.IO.File]::WriteAllText($scrollFile, $content, $encoding)

Write-Host "building ..."
$wails = "C:\Users\fyh\go\bin\wails.exe"
if (-not (Test-Path $wails)) {
    $wails = (Get-Command "wails.exe" -ErrorAction SilentlyContinue).Source
    if (-not $wails) { Write-Host "wails.exe not found"; exit 1 }
}
Set-Location $PSScriptRoot
& $wails build
if ($LASTEXITCODE -eq 0) { Write-Host "build ok" }
else { Write-Host "build failed" }
