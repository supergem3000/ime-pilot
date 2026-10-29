[CmdletBinding()]
param()

# Compile shared packages for both Mac architectures. This is not a desktop build
# and does not execute macOS tests or verify native input-method behaviour.
$ErrorActionPreference = 'Stop'
$projectRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$names = @('GOOS', 'GOARCH', 'CGO_ENABLED', 'GOTOOLCHAIN', 'GOPROXY', 'GOSUMDB', 'GOMODCACHE', 'GOCACHE', 'GOTMPDIR')
$previous = @{}
foreach ($name in $names) {
    $previous[$name] = [Environment]::GetEnvironmentVariable($name, 'Process')
}
Push-Location -LiteralPath $projectRoot
try {
    $env:GOOS = 'darwin'
    $env:CGO_ENABLED = '0'
    $env:GOTOOLCHAIN = 'local'
    $env:GOPROXY = 'off'
    $env:GOSUMDB = 'off'
    $env:GOMODCACHE = Join-Path $projectRoot '.tmp/go-mod'
    $env:GOCACHE = Join-Path $projectRoot '.tmp/go-build'
    $env:GOTMPDIR = Join-Path $projectRoot '.tmp/go-tmp'
    $output = Join-Path $projectRoot '.tmp/macos-check'
    foreach ($directory in @($env:GOMODCACHE, $env:GOCACHE, $env:GOTMPDIR, $output)) {
        New-Item -ItemType Directory -Path $directory -Force | Out-Null
    }
    foreach ($architecture in @('arm64', 'amd64')) {
        $env:GOARCH = $architecture
        foreach ($package in @('core', 'config', 'automation', 'takeover', 'rpc')) {
            & go test -mod=readonly -c -o (Join-Path $output "$package-$architecture.test") "./internal/$package"
            if ($LASTEXITCODE -ne 0) { throw "macOS compilation failed: $package/$architecture" }
        }
        Write-Output "Shared packages compiled for macOS/$architecture (not executed)."
    }
} finally {
    foreach ($name in $names) {
        if ($null -eq $previous[$name]) {
            Remove-Item -LiteralPath "Env:$name" -ErrorAction SilentlyContinue
        } else {
            [Environment]::SetEnvironmentVariable($name, $previous[$name], 'Process')
        }
    }
    Pop-Location
}
