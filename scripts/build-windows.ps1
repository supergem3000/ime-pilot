[CmdletBinding()]
param(
    [switch]$DownloadDependencies,
    [switch]$BuildProbe
)

$ErrorActionPreference = 'Stop'
$projectRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))

if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    throw 'Go was not found. This script does not install tools.'
}
# Change only this PowerShell process while the command runs, then restore it.
# Do not use go env -w, setx, package managers or persistent PATH changes.
$environmentNames = @('PATH', 'CC', 'CXX', 'CGO_ENABLED', 'GOTOOLCHAIN', 'GOPROXY', 'GOSUMDB', 'GOMODCACHE', 'GOCACHE', 'GOTMPDIR')
$previousEnvironment = @{}
foreach ($environmentName in $environmentNames) {
    $previousEnvironment[$environmentName] = [Environment]::GetEnvironmentVariable($environmentName, 'Process')
}

Push-Location -LiteralPath $projectRoot
try {
    # Wails v3 on Windows builds without the Fyne C/OpenGL toolchain.
    $env:CGO_ENABLED = '0'
    $env:GOTOOLCHAIN = 'local'
    # Keep all Go downloads and build scratch files inside this project, so
    # building never requires write access to the Go installation directory.
    $localCache = Join-Path $projectRoot '.tmp'
    $env:GOMODCACHE = Join-Path $localCache 'go-mod'
    $env:GOCACHE = Join-Path $localCache 'go-build'
    $env:GOTMPDIR = Join-Path $localCache 'go-tmp'
    foreach ($cacheDirectory in @($env:GOMODCACHE, $env:GOCACHE, $env:GOTMPDIR)) {
        New-Item -ItemType Directory -Path $cacheDirectory -Force | Out-Null
    }

    if ($DownloadDependencies) {
        # The explicit switch authorizes downloading project Go modules only.
        # It never installs Go, MSYS2, Wails CLI or changes persistent settings.
        & go mod tidy
        if ($LASTEXITCODE -ne 0) { throw 'Downloading/resolving Go modules failed.' }
    } else {
        # Default is offline. Missing modules or go.sum are a clear failure.
        $env:GOPROXY = 'off'
        $env:GOSUMDB = 'off'
    }

    & go test -mod=readonly ./internal/core ./internal/config ./internal/automation ./internal/rpc ./internal/takeover
    if ($LASTEXITCODE -ne 0) { throw 'Core/configuration/automation tests failed.' }

    $outputDirectory = Join-Path $projectRoot 'dist'
    New-Item -ItemType Directory -Path $outputDirectory -Force | Out-Null
    $outputPath = Join-Path $outputDirectory 'ime-pilot.exe'
    # Frontend assets are embedded directly; no npm/Vite install or server.
    & go build -mod=readonly -tags production -trimpath -ldflags '-H=windowsgui' -o $outputPath ./cmd/ime-pilot
    if ($LASTEXITCODE -ne 0) {
        throw 'Build failed. If project modules are missing, obtain confirmation before using -DownloadDependencies.'
    }
    Write-Output "Built: $outputPath"
    if ($BuildProbe) {
        $probePath = Join-Path $outputDirectory 'ime-probe.exe'
        & go build -mod=readonly -trimpath -o $probePath ./cmd/ime-probe
        if ($LASTEXITCODE -ne 0) { throw 'IME laboratory probe build failed.' }
        Write-Output "Built laboratory tool (not a product feature): $probePath"
    }
    Write-Output 'The executable was not launched; no input-method settings were changed.'
} finally {
    foreach ($environmentName in $environmentNames) {
        if ($null -eq $previousEnvironment[$environmentName]) {
            # PowerShell's .NET argument binding can turn null into an empty
            # string. Remove an originally absent variable via the provider.
            Remove-Item -LiteralPath ("Env:" + $environmentName) -ErrorAction SilentlyContinue
        } else {
            [Environment]::SetEnvironmentVariable($environmentName, $previousEnvironment[$environmentName], 'Process')
        }
    }
    Pop-Location
}
