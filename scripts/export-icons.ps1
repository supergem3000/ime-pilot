[CmdletBinding()]
param()

# Package the approved artwork without redrawing it. Uses Windows/.NET only.
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Drawing
$iconRoot = Join-Path (Split-Path $PSScriptRoot -Parent) 'assets/icons'

function Export-SquarePng {
    param([string]$Source, [string]$Destination, [int]$Size, [double]$Fill)

    $sourceImage = [Drawing.Bitmap]::new($Source)
    try {
        # Use alpha, not RGB: transparent pixels can contain arbitrary colors.
        $left = $sourceImage.Width
        $top = $sourceImage.Height
        $right = -1
        $bottom = -1
        for ($y = 0; $y -lt $sourceImage.Height; $y++) {
            for ($x = 0; $x -lt $sourceImage.Width; $x++) {
                if ($sourceImage.GetPixel($x, $y).A -gt 8) {
                    $left = [Math]::Min($left, $x)
                    $top = [Math]::Min($top, $y)
                    $right = [Math]::Max($right, $x)
                    $bottom = [Math]::Max($bottom, $y)
                }
            }
        }
        if ($right -lt $left) { throw "Empty icon: $Source" }
        $bounds = [Drawing.Rectangle]::new($left, $top, $right - $left + 1, $bottom - $top + 1)
        $scale = $Size * $Fill / [Math]::Max($bounds.Width, $bounds.Height)
        $width = [int][Math]::Round($bounds.Width * $scale)
        $height = [int][Math]::Round($bounds.Height * $scale)
        $destinationRect = [Drawing.Rectangle]::new(
            [int](($Size - $width) / 2), [int](($Size - $height) / 2), $width, $height)
        $output = [Drawing.Bitmap]::new($Size, $Size, [Drawing.Imaging.PixelFormat]::Format32bppArgb)
        $graphics = [Drawing.Graphics]::FromImage($output)
        try {
            $graphics.Clear([Drawing.Color]::Transparent)
            $graphics.CompositingMode = [Drawing.Drawing2D.CompositingMode]::SourceCopy
            $graphics.InterpolationMode = [Drawing.Drawing2D.InterpolationMode]::HighQualityBicubic
            $graphics.PixelOffsetMode = [Drawing.Drawing2D.PixelOffsetMode]::HighQuality
            $graphics.DrawImage($sourceImage, $destinationRect, $bounds, [Drawing.GraphicsUnit]::Pixel)
            $output.Save($Destination, [Drawing.Imaging.ImageFormat]::Png)
        } finally {
            $graphics.Dispose()
            $output.Dispose()
        }
    } finally {
        $sourceImage.Dispose()
    }
}

function Export-PngSize {
    param([string]$Source, [string]$Destination, [int]$Size)
    $sourceImage = [Drawing.Bitmap]::new($Source)
    $output = [Drawing.Bitmap]::new($Size, $Size, [Drawing.Imaging.PixelFormat]::Format32bppArgb)
    $graphics = [Drawing.Graphics]::FromImage($output)
    try {
        $graphics.Clear([Drawing.Color]::Transparent)
        $graphics.CompositingMode = [Drawing.Drawing2D.CompositingMode]::SourceCopy
        $graphics.InterpolationMode = [Drawing.Drawing2D.InterpolationMode]::HighQualityBicubic
        $graphics.PixelOffsetMode = [Drawing.Drawing2D.PixelOffsetMode]::HighQuality
        $graphics.DrawImage($sourceImage, [Drawing.Rectangle]::new(0, 0, $Size, $Size))
        $output.Save($Destination, [Drawing.Imaging.ImageFormat]::Png)
    } finally {
        $graphics.Dispose()
        $output.Dispose()
        $sourceImage.Dispose()
    }
}

function Write-BigEndianUInt32 {
    param([IO.BinaryWriter]$Writer, [uint32]$Value)
    $bytes = [BitConverter]::GetBytes($Value)
    [Array]::Reverse($bytes)
    $Writer.Write($bytes)
}

# Normalize only scale and canvas; all shapes and shading stay in the source PNG.
Export-SquarePng "$iconRoot/sources/macos-app.png" "$iconRoot/macos/app-1024.png" 1024 0.82
Export-SquarePng "$iconRoot/sources/windows-app.png" "$iconRoot/windows/app-1024.png" 1024 0.88
Export-SquarePng "$iconRoot/sources/macos-template.png" "$iconRoot/macos/template-source.png" 1024 0.82

$windowsSizes = @(16, 20, 24, 32, 48, 64, 128, 256)
foreach ($size in $windowsSizes) {
    Export-PngSize "$iconRoot/windows/app-1024.png" "$iconRoot/windows/app-$size.png" $size
}
foreach ($size in @(16, 32, 64, 128, 256, 512)) {
    Export-PngSize "$iconRoot/macos/app-1024.png" "$iconRoot/macos/app-$size.png" $size
}
Export-PngSize "$iconRoot/macos/template-source.png" "$iconRoot/macos/menubar-template.png" 22
Export-PngSize "$iconRoot/macos/template-source.png" "$iconRoot/macos/menubar-template@2x.png" 44

# Windows ICO directory followed by PNG frames, supported by modern Windows.
$frames = @($windowsSizes | ForEach-Object { ,([IO.File]::ReadAllBytes("$iconRoot/windows/app-$_.png")) })
$stream = [IO.File]::Create("$iconRoot/windows/ime-pilot.ico")
$writer = [IO.BinaryWriter]::new($stream)
try {
    $writer.Write([uint16]0)
    $writer.Write([uint16]1)
    $writer.Write([uint16]$frames.Count)
    $offset = 6 + 16 * $frames.Count
    for ($i = 0; $i -lt $frames.Count; $i++) {
        $sizeByte = if ($windowsSizes[$i] -eq 256) { 0 } else { $windowsSizes[$i] }
        $writer.Write([byte]$sizeByte)
        $writer.Write([byte]$sizeByte)
        $writer.Write([byte]0)
        $writer.Write([byte]0)
        $writer.Write([uint16]1)
        $writer.Write([uint16]32)
        $writer.Write([uint32]$frames[$i].Length)
        $writer.Write([uint32]$offset)
        $offset += $frames[$i].Length
    }
    foreach ($frame in $frames) { $writer.Write([byte[]]$frame) }
} finally {
    $writer.Dispose()
}

# ICNS uses big-endian chunk lengths; these chunk types carry PNG data.
$macTypes = [ordered]@{ icp4 = 16; icp5 = 32; icp6 = 64; ic07 = 128; ic08 = 256; ic09 = 512; ic10 = 1024 }
$chunks = @($macTypes.GetEnumerator() | ForEach-Object {
    @{ Type = $_.Key; Data = [IO.File]::ReadAllBytes("$iconRoot/macos/app-$($_.Value).png") }
})
$total = 8
foreach ($chunk in $chunks) { $total += 8 + $chunk.Data.Length }
$stream = [IO.File]::Create("$iconRoot/macos/ime-pilot.icns")
$writer = [IO.BinaryWriter]::new($stream)
try {
    $writer.Write([Text.Encoding]::ASCII.GetBytes('icns'))
    Write-BigEndianUInt32 $writer $total
    foreach ($chunk in $chunks) {
        $writer.Write([Text.Encoding]::ASCII.GetBytes($chunk.Type))
        Write-BigEndianUInt32 $writer (8 + $chunk.Data.Length)
        $writer.Write([byte[]]$chunk.Data)
    }
} finally {
    $writer.Dispose()
}
Write-Output "Exported PNG, ICO and ICNS assets to $iconRoot"
