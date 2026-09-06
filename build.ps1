# build.ps1
$appName = "wit3" # Change to your app name
$outputDir = "dist"

# Platforms list (GOOS/GOARCH)
$platforms = @(
    @{ os = "windows"; arch = "amd64"; ext = ".exe" },
    @{ os = "windows"; arch = "arm64"; ext = ".exe" },
    @{ os = "linux";   arch = "amd64"; ext = "" },
    @{ os = "linux";   arch = "arm64"; ext = "" },
    @{ os = "darwin";  arch = "amd64"; ext = "" },
    @{ os = "darwin";  arch = "arm64"; ext = "" }
)

# Clean and create output directory
if (Test-Path $outputDir) { Remove-Item -Recurse -Force $outputDir }
New-Item -ItemType Directory -Force -Path $outputDir | Out-Null

foreach ($p in $platforms) {
    $os = $p.os
    $arch = $p.arch
    $ext = $p.ext
    
    $targetName = "$appName-$os-$arch"
    $binName = "$targetName$ext"
    $targetDir = Join-Path $outputDir $targetName

    Write-Host "Compiling: $os / $arch ..." -ForegroundColor Cyan

    # Create temporary folder for archiving
    New-Item -ItemType Directory -Force -Path $targetDir | Out-Null

    # Build
    $env:CGO_ENABLED = "0"
    $env:GOOS = $os
    $env:GOARCH = $arch
    go build -ldflags="-s -w" -o (Join-Path $targetDir $binName) main.go

    # Compress to zip
    Compress-Archive -Path "$targetDir\*" -DestinationPath "$outputDir\$targetName.zip" -Force
    
    # Remove temporary folder
    Remove-Item -Recurse -Force $targetDir
}

Write-Host "Build complete! All zip files are saved in '$outputDir' directory." -ForegroundColor Green
