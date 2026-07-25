$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$Repository = "yuzu-ux/ycode"
$Version = if ($env:YCODE_VERSION) { $env:YCODE_VERSION } else { "latest" }

$Architecture = switch ($env:PROCESSOR_ARCHITECTURE) {
    "AMD64" { "amd64" }
    "ARM64" { "arm64" }
    default { throw "YCode does not support Windows architecture: $env:PROCESSOR_ARCHITECTURE" }
}

$Asset = "ycode-windows-$Architecture.zip"

if ($env:YCODE_RELEASE_BASE_URL) {
    $DownloadBase = $env:YCODE_RELEASE_BASE_URL.TrimEnd("/")
} elseif ($Version -eq "latest") {
    $DownloadBase = "https://github.com/$Repository/releases/latest/download"
} else {
    $Tag = if ($Version.StartsWith("v")) { $Version } else { "v$Version" }
    $DownloadBase = "https://github.com/$Repository/releases/download/$Tag"
}

if ($env:YCODE_INSTALL_DIR) {
    $InstallDir = $env:YCODE_INSTALL_DIR
} elseif ($env:LOCALAPPDATA) {
    $InstallDir = Join-Path $env:LOCALAPPDATA "YCode\bin"
} else {
    throw "LOCALAPPDATA is not set; provide YCODE_INSTALL_DIR"
}

function Get-YCodeFile {
    param(
        [Parameter(Mandatory = $true)][string]$Uri,
        [Parameter(Mandatory = $true)][string]$OutFile
    )

    $ParsedUri = [Uri]$Uri
    if ($ParsedUri.IsFile) {
        Copy-Item -LiteralPath $ParsedUri.LocalPath -Destination $OutFile
    } else {
        Invoke-WebRequest -UseBasicParsing -Uri $Uri -OutFile $OutFile
    }
}

$TempDir = Join-Path ([IO.Path]::GetTempPath()) ("ycode-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $TempDir | Out-Null

try {
    $Archive = Join-Path $TempDir $Asset
    $ChecksumFile = Join-Path $TempDir "SHA256SUMS"

    Write-Host "Downloading $Asset..."
    Get-YCodeFile -Uri "$DownloadBase/$Asset" -OutFile $Archive
    Get-YCodeFile -Uri "$DownloadBase/SHA256SUMS" -OutFile $ChecksumFile

    $EscapedAsset = [Regex]::Escape($Asset)
    $ChecksumMatch = Select-String -Path $ChecksumFile -Pattern "^([A-Fa-f0-9]{64})\s+\*?$EscapedAsset$" |
        Select-Object -First 1
    if (-not $ChecksumMatch) {
        throw "No checksum found for $Asset"
    }

    $Expected = $ChecksumMatch.Matches[0].Groups[1].Value
    $Actual = (Get-FileHash -Path $Archive -Algorithm SHA256).Hash
    if ($Actual -ne $Expected) {
        throw "Checksum verification failed for $Asset"
    }
    Write-Host "Checksum verified."

    $ExtractDir = Join-Path $TempDir "extract"
    Expand-Archive -LiteralPath $Archive -DestinationPath $ExtractDir
    $SourceBinary = Join-Path $ExtractDir "ycode.exe"
    if (-not (Test-Path -LiteralPath $SourceBinary -PathType Leaf)) {
        throw "Release archive does not contain ycode.exe"
    }

    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    $Destination = Join-Path $InstallDir "ycode.exe"
    Copy-Item -LiteralPath $SourceBinary -Destination $Destination -Force

    $PathEntries = @($env:Path -split ";" | Where-Object { $_ })
    if ($InstallDir -notin $PathEntries) {
        $env:Path = "$InstallDir;$env:Path"

        $UserPath = [Environment]::GetEnvironmentVariable("Path", "User")
        $UserEntries = @($UserPath -split ";" | Where-Object { $_ })
        if ($InstallDir -notin $UserEntries) {
            $NewUserPath = (@($UserEntries) + $InstallDir) -join ";"
            [Environment]::SetEnvironmentVariable("Path", $NewUserPath, "User")
            Write-Host "Added $InstallDir to your user PATH. Open a new terminal to use it there."
        }
    }

    Write-Host "Installed YCode to $Destination"
    & $Destination version
} finally {
    Remove-Item -LiteralPath $TempDir -Recurse -Force -ErrorAction SilentlyContinue
}
