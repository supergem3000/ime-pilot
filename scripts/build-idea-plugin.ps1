[CmdletBinding()]
param(
    [string]$IdeaHome = 'C:\Users\zheng\AppData\Local\Programs\IntelliJ IDEA Ultimate',
    [string]$IdeaVimHome = 'C:\Users\zheng\AppData\Roaming\JetBrains\IntelliJIdea2026.1\plugins\IdeaVIM'
)
$ErrorActionPreference = 'Stop'
$root = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$javac = Join-Path $IdeaHome 'jbr\bin\javac.exe'
$java = Join-Path $IdeaHome 'jbr\bin\java.exe'
if (!(Test-Path -LiteralPath $javac) -or !(Test-Path -LiteralPath (Join-Path $IdeaVimHome 'lib\IdeaVIM-2.47.3.jar'))) {
    throw 'Requires an installed IDEA 2026.1 SDK/JBR and IdeaVim 2.47.3. Supply -IdeaHome/-IdeaVimHome; this script installs nothing.'
}
$product = Get-Content -LiteralPath (Join-Path $IdeaHome 'product-info.json') -Raw | ConvertFrom-Json
if ($product.buildNumber -notmatch '^261\.') { throw 'This adapter targets IDEA build 261 only.' }
$source = Join-Path $root 'plugins\jetbrains\ime-pilot'
$scratch = Join-Path $root ('.tmp\idea-plugin\' + [guid]::NewGuid().ToString('N'))
$classes = Join-Path $scratch 'classes'
$package = Join-Path $scratch 'package'
$lib = Join-Path $package 'ime-pilot\lib'
New-Item -ItemType Directory -Force $classes,$lib,(Join-Path $root 'dist') | Out-Null
$sources = @(Get-ChildItem -LiteralPath (Join-Path $source 'src') -Recurse -Filter *.java | ForEach-Object FullName)
$classpath = "$IdeaHome\lib\*;$IdeaVimHome\lib\*"
& $javac --release 21 -encoding UTF-8 -cp $classpath -d $classes @sources
if ($LASTEXITCODE -ne 0) { throw 'Plugin compilation failed.' }
Copy-Item -LiteralPath (Join-Path $source 'resources\META-INF') -Destination $classes -Recurse
$tests = @(Get-ChildItem -LiteralPath (Join-Path $source 'test') -Recurse -Filter *.java | ForEach-Object FullName)
& $javac --release 21 -encoding UTF-8 -cp "$classes;$classpath" -d $classes @tests
if ($LASTEXITCODE -ne 0) { throw 'Plugin test compilation failed.' }
& $java --enable-native-access=ALL-UNNAMED "-Djna.boot.library.path=$IdeaHome\lib\jna\amd64" -cp "$classes;$classpath" local.imepilot.ClientContractTest
if ($LASTEXITCODE -ne 0) { throw 'Plugin contract tests failed.' }
# Do not ship test classes.
Remove-Item -LiteralPath (Join-Path $classes 'local\imepilot\ClientContractTest.class')
Add-Type -AssemblyName System.IO.Compression.FileSystem
[IO.Compression.ZipFile]::CreateFromDirectory($classes, (Join-Path $lib 'ime-pilot.jar'))
$output = Join-Path $root 'dist\ime-pilot-idea-0.3.0.zip'
if (Test-Path -LiteralPath $output) { Remove-Item -LiteralPath $output }
[IO.Compression.ZipFile]::CreateFromDirectory($package, $output)
Write-Output "Built: $output"
Get-FileHash -LiteralPath $output -Algorithm SHA256
Write-Output 'No downloads, IDE installation, configuration edits or environment changes were performed.'
