@echo off
setlocal EnableExtensions DisableDelayedExpansion
title Cloudflare WARP Node Scanner Artemis Lab (From YouTube) V1.0
set "WARP_SCRIPT_DIR=%~dp0"
set "WARP_SCRIPT_FILE=%~f0"
rem Load the embedded PowerShell source. No external PS1 file is created.
powershell.exe -NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -Command "$text=[IO.File]::ReadAllText($env:WARP_SCRIPT_FILE);$marker=':'+'__POWERSHELL__';$offset=$text.IndexOf($marker);if($offset -lt 0){throw 'Embedded source marker missing.'};& ([scriptblock]::Create($text.Substring($offset+$marker.Length)))"
set "WARP_EXIT=%ERRORLEVEL%"
if not "%WARP_EXIT%"=="0" (
    echo.
    pause
    endlocal & exit /b %WARP_EXIT%
)
pause
endlocal & exit /b %WARP_EXIT%

goto :eof
:__POWERSHELL__
$ErrorActionPreference = 'Stop'

function Get-YamlValue([string] $Text, [string] $Key) {
    $match = [regex]::Match($Text, ('(?m)^\s*' + [regex]::Escape($Key) + '\s*:\s*(\S+)'))
    if (-not $match.Success) { return $null }
    return $match.Groups[1].Value.Trim().Trim('"').Trim("'")
}

function Quote-Yaml([string] $Value) {
    return "'" + $Value.Replace("'", "''") + "'"
}

function Invoke-Warpscout([string] $ArgumentLine) {
    # warpscout writes normal progress messages to stderr. Start-Process keeps
    # PowerShell from turning those messages into NativeCommandError records.
    $stamp = [Guid]::NewGuid().ToString('N')
    $stdoutFile = Join-Path $warpscoutDir ('warpscout-' + $stamp + '.stdout.tmp')
    $stderrFile = Join-Path $warpscoutDir ('warpscout-' + $stamp + '.stderr.tmp')
    try {
        $process = Start-Process -FilePath $warpscoutExe -ArgumentList $ArgumentLine -WorkingDirectory $warpscoutDir -NoNewWindow -Wait -PassThru -RedirectStandardOutput $stdoutFile -RedirectStandardError $stderrFile
        return $process.ExitCode
    } finally {
        Remove-Item -LiteralPath $stdoutFile, $stderrFile -Force -ErrorAction SilentlyContinue
    }
}

function Invoke-WarpscoutInteractive([string] $ArgumentLine) {
    # Keep warpscout's native scan UI in this console. Start-Process avoids
    # PowerShell treating the program's stderr progress messages as errors.
    $process = Start-Process -FilePath $warpscoutExe -ArgumentList $ArgumentLine -WorkingDirectory $warpscoutDir -NoNewWindow -Wait -PassThru
    return $process.ExitCode
}

function Write-Title {
    Clear-Host
    Write-Host '========================================' -ForegroundColor DarkCyan
    Write-Host '      Cloudflare WARP MASQUE-H2' -ForegroundColor Cyan
    Write-Host '           Node Generator' -ForegroundColor Cyan
    Write-Host ''
    Write-Host '       by Artemis Lab (From YouTube) V1.0' -ForegroundColor Yellow
    Write-Host '========================================' -ForegroundColor DarkCyan
}

try {
    $root = [Environment]::GetEnvironmentVariable('WARP_SCRIPT_DIR')
    if ([string]::IsNullOrWhiteSpace($root)) { throw 'Could not determine the script directory.' }
    $root = [IO.Path]::GetFullPath($root)
    $toolsDir = Join-Path $root 'tools'
    $warpscoutDir = Join-Path $toolsDir 'warpscout'
    $warpscoutExe = Join-Path $warpscoutDir 'warpscout.exe'
    $accountFile = Join-Path $warpscoutDir 'warpscout-account.json'
    $reportFile = Join-Path $warpscoutDir 'scan-report.txt'
    $bestFile = Join-Path $warpscoutDir 'best-mihomo.yaml'
    $outputFile = Join-Path $root 'warp-multi.yaml'
    $zipFile = Join-Path $toolsDir 'warpscout-download.zip'

    Write-Title
    Write-Host ''
    Write-Host '[1/5] Preparing warpscout...' -ForegroundColor Cyan
    New-Item -ItemType Directory -Force -Path $warpscoutDir | Out-Null
    [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

    if (-not (Test-Path -LiteralPath $warpscoutExe -PathType Leaf)) {
        $warpscoutUrl = 'https://pub-453eabf12730419aa802d8e819e1333d.r2.dev/warpscout_0.16.0_windows_amd64.zip'
        Write-Host 'Downloading warpscout 0.16.0 from R2...'
        Remove-Item -LiteralPath $zipFile -Force -ErrorAction SilentlyContinue
        try {
            Invoke-WebRequest -Uri $warpscoutUrl -OutFile $zipFile -UseBasicParsing
            if (-not (Test-Path -LiteralPath $zipFile -PathType Leaf) -or (Get-Item -LiteralPath $zipFile).Length -eq 0) {
                throw 'R2 returned an empty ZIP download.'
            }
            Expand-Archive -LiteralPath $zipFile -DestinationPath $warpscoutDir -Force
        } catch {
            throw 'Could not download or extract warpscout from R2. Check the network, disk space, and folder permissions.'
        } finally {
            Remove-Item -LiteralPath $zipFile -Force -ErrorAction SilentlyContinue
        }
        if (-not (Test-Path -LiteralPath $warpscoutExe -PathType Leaf)) {
            $extracted = Get-ChildItem -LiteralPath $warpscoutDir -Filter 'warpscout.exe' -File -Recurse | Select-Object -First 1
            if ($null -ne $extracted) { Move-Item -LiteralPath $extracted.FullName -Destination $warpscoutExe -Force }
        }
    } else {
        Write-Host 'Existing warpscout.exe found; download skipped.'
    }
    if (-not (Test-Path -LiteralPath $warpscoutExe -PathType Leaf)) { throw 'warpscout.exe was not found after extraction.' }
    $version = (& $warpscoutExe version 2>$null | Select-Object -First 1)
    if ([string]::IsNullOrWhiteSpace($version)) { $version = 'unknown' }
    Write-Host ('Current version: ' + $version)

    Push-Location -LiteralPath $warpscoutDir
    try {
        Write-Host ''
        Write-Host '[2/5] Checking the WARP account...' -ForegroundColor Cyan
        if (Test-Path -LiteralPath $accountFile -PathType Leaf) {
            Write-Host 'Existing WARP account found; registration skipped.'
        } else {
            Write-Host 'Registering a new WARP account. Please wait...'
            $registerExitCode = Invoke-Warpscout 'register'
            if ($registerExitCode -ne 0) { throw 'WARP account registration failed. Check your network and try again.' }
            if (-not (Test-Path -LiteralPath $accountFile -PathType Leaf)) { throw 'Registration completed but warpscout-account.json was not created.' }
        }
        Write-Host 'WARP account is ready.' -ForegroundColor Green

        Write-Host ''
        Write-Host '[3/5] Scanning MASQUE-H2 endpoints...' -ForegroundColor Cyan
        Write-Host 'This scan tests about 280 endpoints. Please wait...'
        Remove-Item -LiteralPath $reportFile, $bestFile -Force -ErrorAction SilentlyContinue
        $scanExitCode = Invoke-WarpscoutInteractive 'scan -p masque-h2 -n 20 -o scan-report.txt -conf best-mihomo.yaml -conf-type mihomo'
        if ($scanExitCode -ne 0) { throw 'MASQUE-H2 scan failed. Check your network and try again.' }
    } finally {
        Pop-Location
    }

    if (-not (Test-Path -LiteralPath $reportFile -PathType Leaf)) { throw 'The scan completed but scan-report.txt was not created.' }
    if (-not (Test-Path -LiteralPath $bestFile -PathType Leaf)) { throw 'The scan completed but best-mihomo.yaml was not created.' }

    Write-Host ''
    Write-Host 'Scan completed.'
    $reportLines = Get-Content -LiteralPath $reportFile
    $workingEndpoints = New-Object 'System.Collections.Generic.List[object]'
    $seen = @{}
    $inWorkingTable = $false
    foreach ($line in $reportLines) {
        if ($line -match '^ENDPOINT\s+') { $inWorkingTable = $true; continue }
        if ($line -match '^#\s+\d+\s+torn down\b') { break }
        if (-not $inWorkingTable) { continue }
        $endpointMatch = [regex]::Match($line, '^\s*((?:\d{1,3}\.){3}\d{1,3}:\d{1,5})\s+(\d+(?:\.\d+)?ms|\?)\s+')
        if ($endpointMatch.Success) {
            $endpoint = $endpointMatch.Groups[1].Value
            if (-not $seen.ContainsKey($endpoint)) {
                $pingText = $endpointMatch.Groups[2].Value
                $ping = [Double]::MaxValue
                if ($pingText -ne '?') { $ping = [Double]::Parse(($pingText -replace 'ms$',''), [Globalization.CultureInfo]::InvariantCulture) }
                $seen[$endpoint] = $true
                [void]$workingEndpoints.Add([PSCustomObject]@{ Endpoint = $endpoint; Ping = $ping })
            }
        }
    }
    if ($workingEndpoints.Count -eq 0) { throw 'No working MASQUE-H2 endpoint was found in this scan.' }
    Write-Host ('Working endpoints found: ' + $workingEndpoints.Count) -ForegroundColor Green
    Write-Host 'Selecting up to 8 endpoints with the lowest endpoint ping...'
    $endpoints = @($workingEndpoints | Sort-Object Ping, Endpoint | Select-Object -First 8)

    Write-Host ''
    Write-Host '[4/5] Generating the Mihomo configuration...' -ForegroundColor Cyan

    $best = Get-Content -LiteralPath $bestFile -Raw
    $privateKey = Get-YamlValue $best 'private-key'
    $publicKey = Get-YamlValue $best 'public-key'
    $ip = Get-YamlValue $best 'ip'
    $ipv6 = Get-YamlValue $best 'ipv6'
    $sni = Get-YamlValue $best 'sni'
    foreach ($required in @(@{ Name = 'private-key'; Value = $privateKey }, @{ Name = 'public-key'; Value = $publicKey }, @{ Name = 'ip'; Value = $ip }, @{ Name = 'sni'; Value = $sni })) {
        if ([string]::IsNullOrWhiteSpace($required.Value)) { throw ('best-mihomo.yaml is missing required field: ' + $required.Name + '.') }
    }

    $yaml = New-Object 'System.Collections.Generic.List[string]'
    [void]$yaml.Add('mixed-port: 7890')
    [void]$yaml.Add('allow-lan: false')
    [void]$yaml.Add('mode: rule')
    [void]$yaml.Add('log-level: info')
    [void]$yaml.Add('')
    [void]$yaml.Add('proxies:')
    for ($index = 0; $index -lt $endpoints.Count; $index++) {
        $parts = $endpoints[$index].Endpoint.Split(':')
        $name = ('WARP-H2-{0:D2}' -f ($index + 1))
        [void]$yaml.Add(('  - name: ' + (Quote-Yaml $name)))
        [void]$yaml.Add('    type: masque')
        [void]$yaml.Add(('    server: ' + $parts[0]))
        [void]$yaml.Add(('    port: ' + $parts[1]))
        [void]$yaml.Add('    network: h2')
        [void]$yaml.Add(('    sni: ' + (Quote-Yaml $sni)))
        [void]$yaml.Add(('    private-key: ' + (Quote-Yaml $privateKey)))
        [void]$yaml.Add(('    public-key: ' + (Quote-Yaml $publicKey)))
        [void]$yaml.Add(('    ip: ' + (Quote-Yaml $ip)))
        if (-not [string]::IsNullOrWhiteSpace($ipv6)) { [void]$yaml.Add(('    ipv6: ' + (Quote-Yaml $ipv6))) }
        [void]$yaml.Add('    udp: true')
        [void]$yaml.Add('    remote-dns-resolve: true')
        [void]$yaml.Add('    dns: [1.1.1.1, 1.0.0.1]')
        [void]$yaml.Add('')
    }
    [void]$yaml.Add('proxy-groups:')
    [void]$yaml.Add('  - name: WARP-AUTO')
    [void]$yaml.Add('    type: url-test')
    [void]$yaml.Add('    proxies:')
    for ($index = 0; $index -lt $endpoints.Count; $index++) { [void]$yaml.Add(('      - WARP-H2-{0:D2}' -f ($index + 1))) }
    [void]$yaml.Add('    url: https://www.cloudflare.com/cdn-cgi/trace')
    [void]$yaml.Add('    interval: 300')
    [void]$yaml.Add('    tolerance: 50')
    [void]$yaml.Add('')
    [void]$yaml.Add('rules:')
    [void]$yaml.Add('  - MATCH,WARP-AUTO')
    $temporaryOutput = $outputFile + '.tmp'
    [IO.File]::WriteAllLines($temporaryOutput, $yaml, (New-Object System.Text.UTF8Encoding($false)))
    Move-Item -LiteralPath $temporaryOutput -Destination $outputFile -Force

    if (-not (Test-Path -LiteralPath $outputFile -PathType Leaf)) { throw 'Configuration generation failed: warp-multi.yaml was not created.' }
    Write-Host ''
    Write-Host '[5/5] Completed.' -ForegroundColor Green
    Write-Host ''
    Write-Host '========================================' -ForegroundColor DarkCyan
    Write-Host '              SUCCESS' -ForegroundColor Green
    Write-Host '========================================' -ForegroundColor DarkCyan
    Write-Host ''
    Write-Host ('Selected nodes: ' + $endpoints.Count)
    Write-Host ''
    Write-Host 'Configuration file:'
    Write-Host 'warp-multi.yaml' -ForegroundColor Yellow
    Write-Host ''
    Write-Host 'Import this file into a Mihomo / Clash-compatible client.'
    Write-Host 'WARP-AUTO will automatically select a lower-latency node.'
    exit 0
} catch {
    Write-Host ''
    Write-Host ('[ERROR] ' + $_.Exception.Message) -ForegroundColor Red
    exit 1
}
