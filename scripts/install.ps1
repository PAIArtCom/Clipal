# Installs the Clipal CLI from GitHub Releases on Windows.
#
# Usage:
#   powershell -ExecutionPolicy ByPass -c "irm https://clipal.paiart.com/install.ps1 | iex"
#
# Environment:
#   CLIPAL_VERSION       Release version to install (default: latest)
#   CLIPAL_INSTALL_DIR   Directory for clipal.exe (default: %LOCALAPPDATA%\Programs\clipal)
#   CLIPAL_DOWNLOAD_BASE_URL Override the release download base URL (mirror)
#   CLIPAL_DRY_RUN        Set to 1 to print the install plan without changes

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"

$Repo = "PAIArtCom/Clipal"
$DownloadBase = if ($env:CLIPAL_DOWNLOAD_BASE_URL) {
    $env:CLIPAL_DOWNLOAD_BASE_URL
} elseif ($env:CLIPAL_DOWNLOAD_BASE) {
    $env:CLIPAL_DOWNLOAD_BASE
} else {
    "https://github.com/$Repo/releases/download"
}
$Version = $env:CLIPAL_VERSION
$InstallDir = if ($env:CLIPAL_INSTALL_DIR) {
    $env:CLIPAL_INSTALL_DIR
} elseif ($env:LOCALAPPDATA) {
    Join-Path $env:LOCALAPPDATA "Programs\clipal"
} else {
    Join-Path $HOME ".local\bin"
}

try {
    [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
} catch {
    # Newer PowerShell versions negotiate TLS automatically.
}

$Headers = @{ "User-Agent" = "clipal-install" }

$isWindowsHost = $true
if (Get-Variable -Name IsWindows -ErrorAction SilentlyContinue) {
    $isWindowsHost = $IsWindows
} elseif ($env:OS -ne "Windows_NT") {
    $isWindowsHost = $false
}

function Get-ClipalArch {
    $arch = $null
    try {
        $arch = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()
    } catch {
        $arch = $env:PROCESSOR_ARCHITEW6432
        if (-not $arch) {
            $arch = $env:PROCESSOR_ARCHITECTURE
        }
    }

    switch -Regex ($arch) {
        '^(X64|AMD64)$' { return "amd64" }
        '^(Arm64|ARM64)$' { return "arm64" }
        default { throw "unsupported CPU architecture: $arch (supported: x64/amd64, arm64)" }
    }
}

function Get-ClipalFile {
    param(
        [Parameter(Mandatory = $true)][string]$Uri,
        [Parameter(Mandatory = $true)][string]$OutFile
    )
    Invoke-WebRequest -UseBasicParsing -Uri $Uri -OutFile $OutFile -Headers $Headers
}

function Resolve-ClipalTag {
    $latestUri = "https://github.com/$Repo/releases/latest"
    $response = $null

    try {
        $response = Invoke-WebRequest `
            -UseBasicParsing `
            -Uri $latestUri `
            -Method Head `
            -MaximumRedirection 0 `
            -Headers $Headers `
            -ErrorAction Stop
    } catch {
        $response = $_.Exception.Response
    }

    if (-not $response) {
        throw "unable to resolve the latest Clipal release"
    }

    $location = $null
    if ($response.Headers.Location) {
        $location = [string]$response.Headers.Location
    } else {
        $locationValues = $response.Headers["Location"]
        if ($locationValues -is [array]) {
            $location = [string]$locationValues[0]
        } else {
            $location = [string]$locationValues
        }
    }
    if (-not $location) {
        throw "GitHub did not provide a redirect for the latest Clipal release"
    }

    try {
        if ([Uri]::IsWellFormedUriString($location, [UriKind]::Absolute)) {
            $redirectUri = [Uri]$location
        } else {
            $redirectUri = [Uri]::new([Uri]$latestUri, $location)
        }
        $tag = $redirectUri.Segments[-1].TrimEnd('/')
    } catch {
        throw "GitHub returned an invalid latest-release redirect"
    }

    if (-not $tag -or $tag -eq "latest") {
        throw "unable to determine the latest Clipal release tag"
    }
    return $tag
}

if (-not $Version -or $Version -eq "latest") {
    Write-Host "Resolving the latest Clipal release..."
    $Version = Resolve-ClipalTag
}

$Version = $Version -replace '^v', ''
if (-not $Version) {
    throw "could not determine the Clipal version to install"
}
$Tag = "v$Version"

$arch = Get-ClipalArch
$asset = "clipal-windows-$arch.exe"

if ($env:CLIPAL_DRY_RUN -eq "1") {
    Write-Host "Dry run: would download $DownloadBase/$Tag/$asset"
    Write-Host "Would verify $DownloadBase/$Tag/checksums.txt and install to $(Join-Path $InstallDir 'clipal.exe')"
    exit 0
}

$tmpDir = Join-Path ([IO.Path]::GetTempPath()) ("clipal-install-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $tmpDir -Force | Out-Null

try {
    $checksumsPath = Join-Path $tmpDir "checksums.txt"
    $downloadPath = Join-Path $tmpDir $asset

    Write-Host "Downloading Clipal $Tag for windows/$arch..."
    try {
        Get-ClipalFile -Uri "$DownloadBase/$Tag/checksums.txt" -OutFile $checksumsPath
        Get-ClipalFile -Uri "$DownloadBase/$Tag/$asset" -OutFile $downloadPath
    } catch {
        throw "unable to download Clipal $Tag`: $($_.Exception.Message)"
    }

    $expected = $null
    foreach ($line in Get-Content -Path $checksumsPath) {
        $parts = $line -split '\s+', 2
        if ($parts.Count -ge 2 -and $parts[1].TrimStart('*') -eq $asset) {
            $expected = $parts[0].ToLowerInvariant()
            break
        }
    }
    if (-not $expected) {
        throw "checksums.txt for $Tag does not list $asset"
    }

    $actual = (Get-FileHash -Algorithm SHA256 -Path $downloadPath).Hash.ToLowerInvariant()
    if ($actual -ne $expected) {
        throw "checksum mismatch for $asset (expected $expected, got $actual)"
    }

    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
    $target = Join-Path $InstallDir "clipal.exe"
    $staged = Join-Path $InstallDir (".clipal.tmp." + [Guid]::NewGuid().ToString("N"))
    Copy-Item -Path $downloadPath -Destination $staged -Force
    try {
        Move-Item -Force -Path $staged -Destination $target
    } catch {
        Remove-Item -Force -Path $staged -ErrorAction SilentlyContinue
        throw "unable to replace $target; close any running clipal process and try again"
    }

    Write-Host "Installed $target"

    if ($isWindowsHost) {
        $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
        $entries = @()
        if ($userPath) {
            $entries = $userPath -split ';' | Where-Object { $_ }
        }
        $alreadyOnPath = $entries | Where-Object { $_.TrimEnd('\') -ieq $InstallDir.TrimEnd('\') }
        if (-not $alreadyOnPath) {
            $newPath = if ($userPath) { $userPath.TrimEnd(';') + ';' + $InstallDir } else { $InstallDir }
            [Environment]::SetEnvironmentVariable("Path", $newPath, "User")
            if (($env:Path -split ';') -notcontains $InstallDir) {
                $env:Path = "$env:Path;$InstallDir"
            }
            Write-Host "Added $InstallDir to your user PATH. Re-open PowerShell to pick it up in new sessions."
        }

        $versionOutput = & $target --version
        if ($LASTEXITCODE -ne 0) {
            throw "Clipal was installed but did not run; check your platform and try again"
        }
        Write-Host $versionOutput
    } else {
        Write-Host "Skipped --version verification because this is not Windows."
    }
} finally {
    Remove-Item -Recurse -Force -Path $tmpDir -ErrorAction SilentlyContinue
}
