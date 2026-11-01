param(
    [string]$SourceIcon = (Join-Path $PSScriptRoot '..\assets\app-icon.png')
)
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Drawing
$projectRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$nativeDirectory = Join-Path $projectRoot 'internal\platform\assets'
$webDirectory = Join-Path $projectRoot 'web\public'
$webAssetsDirectory = Join-Path $projectRoot 'web\src\assets'
New-Item -ItemType Directory -Path $nativeDirectory, $webDirectory, $webAssetsDirectory -Force | Out-Null
$sourceImage = [Drawing.Image]::FromFile([IO.Path]::GetFullPath($SourceIcon))
try {
    $frames = foreach ($size in @(16, 20, 24, 32, 40, 48, 64, 128, 256)) {
        $bitmap = [Drawing.Bitmap]::new($size, $size, [Drawing.Imaging.PixelFormat]::Format32bppArgb)
        try {
            $graphics = [Drawing.Graphics]::FromImage($bitmap)
            try {
                $graphics.Clear([Drawing.Color]::Transparent)
                $graphics.CompositingMode = [Drawing.Drawing2D.CompositingMode]::SourceCopy
                $graphics.InterpolationMode = [Drawing.Drawing2D.InterpolationMode]::HighQualityBicubic
                $graphics.PixelOffsetMode = [Drawing.Drawing2D.PixelOffsetMode]::HighQuality
                $graphics.DrawImage($sourceImage, [Drawing.Rectangle]::new(0, 0, $size, $size))
            }
            finally { $graphics.Dispose() }
            $stream = [IO.MemoryStream]::new()
            try {
                if ($size -eq 32) {
                    $bitmap.Save((Join-Path $webAssetsDirectory 'favicon.png'), [Drawing.Imaging.ImageFormat]::Png)
                }
                if ($size -eq 256) {
                    $bitmap.Save($stream, [Drawing.Imaging.ImageFormat]::Png)
                    [IO.File]::WriteAllBytes((Join-Path $webAssetsDirectory 'app-icon.png'), $stream.ToArray())
                }
                else {
                    # Native bottom-up BGRA DIB plus a DWORD-aligned AND mask.
                    $maskStride = [int]([Math]::Ceiling($size / 32.0) * 4)
                    $writer = [IO.BinaryWriter]::new($stream, [Text.Encoding]::UTF8, $true)
                    try {
                        $writer.Write([uint32]40)
                        $writer.Write([int32]$size)
                        $writer.Write([int32]($size * 2))
                        $writer.Write([uint16]1)
                        $writer.Write([uint16]32)
                        $writer.Write([uint32]0)
                        $writer.Write([uint32]($size * $size * 4))
                        $writer.Write([byte[]]::new(16))
                        $mask = [byte[]]::new($maskStride * $size)
                        for ($y = $size - 1; $y -ge 0; $y--) {
                            for ($x = 0; $x -lt $size; $x++) {
                                $pixel = $bitmap.GetPixel($x, $y)
                                $writer.Write([byte[]]@($pixel.B, $pixel.G, $pixel.R, $pixel.A))
                                if ($pixel.A -lt 128) {
                                    $maskIndex = ($size - 1 - $y) * $maskStride + [int][Math]::Floor($x / 8)
                                    $mask[$maskIndex] = $mask[$maskIndex] -bor (128 -shr ($x % 8))
                                }
                            }
                        }
                        $writer.Write($mask)
                    }
                    finally { $writer.Dispose() }
                }
                [pscustomobject]@{ Size = $size; Data = $stream.ToArray() }
            }
            finally { $stream.Dispose() }
        }
        finally { $bitmap.Dispose() }
    }
    $iconStream = [IO.MemoryStream]::new()
    $iconWriter = [IO.BinaryWriter]::new($iconStream)
    try {
        $iconWriter.Write([uint16]0)
        $iconWriter.Write([uint16]1)
        $iconWriter.Write([uint16]$frames.Count)
        $offset = 6 + 16 * $frames.Count
        foreach ($frame in $frames) {
            $dimension = if ($frame.Size -eq 256) { 0 } else { $frame.Size }
            $iconWriter.Write([byte[]]@($dimension, $dimension, 0, 0))
            $iconWriter.Write([uint16]1)
            $iconWriter.Write([uint16]32)
            $iconWriter.Write([uint32]$frame.Data.Length)
            $iconWriter.Write([uint32]$offset)
            $offset += $frame.Data.Length
        }
        foreach ($frame in $frames) { $iconWriter.Write([byte[]]$frame.Data) }
        $iconBytes = $iconStream.ToArray()
        [IO.File]::WriteAllBytes((Join-Path $nativeDirectory 'app.ico'), $iconBytes)
        [IO.File]::WriteAllBytes((Join-Path $webDirectory 'favicon.ico'), $iconBytes)
    }
    finally { $iconWriter.Dispose(); $iconStream.Dispose() }
}
finally { $sourceImage.Dispose() }
Write-Output 'Updated Windows icon, favicon and dashboard logo from assets/app-icon.png.'
