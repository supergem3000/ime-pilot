[CmdletBinding()]
param(
    [switch]$DownloadDependencies,
    [switch]$RunTests,
    [switch]$BuildProbe
)

$ErrorActionPreference = 'Stop'
$projectRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))

if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    throw 'Go was not found. This script does not install tools.'
}
if (-not (Get-Command wails3 -ErrorAction SilentlyContinue)) {
    throw 'Wails CLI (wails3) is required to embed the executable icon. This script does not install tools.'
}
# Change only this PowerShell process while the command runs, then restore it.
# Do not use go env -w, setx, package managers or persistent PATH changes.
$environmentNames = @('PATH', 'CC', 'CXX', 'CGO_ENABLED', 'GOTOOLCHAIN', 'GOPROXY', 'GOSUMDB', 'GOMODCACHE', 'GOCACHE', 'GOTMPDIR')
$previousEnvironment = @{}
foreach ($environmentName in $environmentNames) {
    $previousEnvironment[$environmentName] = [Environment]::GetEnvironmentVariable($environmentName, 'Process')
}

Push-Location -LiteralPath $projectRoot
$resourcePath = $null
try {
    # Windows native APIs, including TSF COM, are called directly from Go.
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

    if ($RunTests) {
        & go test -mod=readonly ./internal/core ./internal/config ./internal/rpc ./internal/schemes/verification
        if ($LASTEXITCODE -ne 0) { throw 'Tests failed.' }
    }

    $outputDirectory = Join-Path $projectRoot 'dist'
    New-Item -ItemType Directory -Path $outputDirectory -Force | Out-Null
    $outputPath = Join-Path $outputDirectory 'ime-pilot.exe'
    # Go links this resource object into the EXE, so Explorer sees the icon too.
    $architecture = (& go env GOARCH).Trim()
    if ($LASTEXITCODE -ne 0) { throw 'Reading the Go target architecture failed.' }
    $resourcePath = Join-Path $projectRoot "cmd/ime-pilot/rsrc_windows_$architecture.syso"
    & wails3 generate syso -arch $architecture -icon 'assets/icons/windows/ime-pilot.ico' -manifest 'cmd/ime-pilot/ime-pilot.manifest' -out $resourcePath
    if ($LASTEXITCODE -ne 0) { throw 'Generating the Windows icon resource failed.' }
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
    # Generated objects must not linger when switching target architectures.
    if ($resourcePath -and (Test-Path -LiteralPath $resourcePath)) {
        Remove-Item -LiteralPath $resourcePath -Force
    }
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
