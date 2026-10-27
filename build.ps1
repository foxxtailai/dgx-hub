param(
    [ValidateSet('windows-amd64', 'linux-amd64', 'linux-arm64')]
    [string[]]$Targets = @('windows-amd64'),
    [switch]$SkipInstall,
    [switch]$SkipTests
)
$ErrorActionPreference = 'Stop'
Push-Location $PSScriptRoot
$buildEnvironment = @{}
foreach ($buildEnvName in @('GOOS', 'GOARCH', 'CGO_ENABLED', 'GOCACHE', 'GOMODCACHE', 'GOENV')) {
    $buildEnvironment[$buildEnvName] = [Environment]::GetEnvironmentVariable($buildEnvName)
}
try {
    $goCommand = Get-Command go -ErrorAction SilentlyContinue
    $goExecutable = if ($goCommand) { $goCommand.Source } else { Join-Path $PSScriptRoot '.tools\go\bin\go.exe' }
    if (-not (Test-Path -LiteralPath $goExecutable)) { throw 'Install Go 1.26 or newer and put it on PATH.' }
    $npmCommand = Get-Command npm.cmd -ErrorAction SilentlyContinue
    if (-not $npmCommand) { $npmCommand = Get-Command npm -ErrorAction SilentlyContinue }
    if (-not $npmCommand) { throw 'Install Node.js 22.12+ (including npm).' }
    $npmExecutable = $npmCommand.Source
    $env:GOCACHE = Join-Path $PSScriptRoot '.cache\go-build'
    $env:GOMODCACHE = Join-Path $PSScriptRoot '.cache\gomod'
    $env:GOENV = 'off'
    $env:CGO_ENABLED = '0'
    Remove-Item Env:GOOS -ErrorAction SilentlyContinue
    Remove-Item Env:GOARCH -ErrorAction SilentlyContinue
    if (-not $SkipInstall) {
        & $npmExecutable --prefix web ci
        if ($LASTEXITCODE -ne 0) { throw 'npm ci failed' }
    }
    & $npmExecutable --prefix web run build
    if ($LASTEXITCODE -ne 0) { throw 'Frontend build failed' }
    & $goExecutable mod download
    if ($LASTEXITCODE -ne 0) { throw 'Go dependency download failed' }
    & $goExecutable run ./internal/platform/iconresource -out cmd/dgx-hub/resources_windows_amd64.syso -version 0.1.2
    if ($LASTEXITCODE -ne 0) { throw 'Windows icon resource generation failed' }
    if (-not $SkipTests) {
        Remove-Item Env:GOOS -ErrorAction SilentlyContinue
        Remove-Item Env:GOARCH -ErrorAction SilentlyContinue
        & $goExecutable test ./...
        if ($LASTEXITCODE -ne 0) { throw 'Go tests failed' }
    }
    New-Item -ItemType Directory -Path dist -Force | Out-Null
    foreach ($buildTarget in $Targets) {
        $buildParts = $buildTarget.Split('-')
        $env:GOOS = $buildParts[0]
        $env:GOARCH = $buildParts[1]
        $buildFlags = '-s -w -X main.version=0.1.2'
        $buildOutput = Join-Path $PSScriptRoot ('dist\dgx-hub-' + $buildTarget)
        if ($env:GOOS -eq 'windows') {
            $buildFlags = '-H windowsgui ' + $buildFlags
            $buildOutput = Join-Path $PSScriptRoot 'dist\dgx-hub.exe'
        }
        & $goExecutable build -trimpath -ldflags $buildFlags -o $buildOutput ./cmd/dgx-hub
        if ($LASTEXITCODE -ne 0) { throw ('Build failed: ' + $buildTarget) }
        Get-FileHash -LiteralPath $buildOutput -Algorithm SHA256
    }
}
finally {
    foreach ($buildEnvName in $buildEnvironment.Keys) {
        [Environment]::SetEnvironmentVariable($buildEnvName, $buildEnvironment[$buildEnvName], 'Process')
    }
    Pop-Location
}
