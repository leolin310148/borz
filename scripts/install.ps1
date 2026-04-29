param(
    [string]$Version = "latest",
    [string]$Repo = "leolin310148/borz",
    [string]$InstallDir = "$env:LOCALAPPDATA\Programs\borz\bin",
    [switch]$NoPath,
    [switch]$Service,
    [switch]$StartService,
    [string]$ServiceName = "borz",
    [string]$HostName = "127.0.0.1",
    [int]$Port = 19824,
    [string]$Token = "",
    [string]$CdpHost = "127.0.0.1",
    [int]$CdpPort = 19825,
    [int]$IdleTabTimeout = 30
)

$ErrorActionPreference = "Stop"

function Get-ReleasePath {
    param([string]$Version)
    if ($Version -eq "latest") {
        return "latest/download"
    }
    if ($Version.StartsWith("v")) {
        return "download/$Version"
    }
    return "download/v$Version"
}

function Add-UserPath {
    param([string]$PathToAdd)
    $current = [Environment]::GetEnvironmentVariable("Path", "User")
    $parts = @()
    if ($current) {
        $parts = $current -split ';' | Where-Object { $_ }
    }
    if ($parts -notcontains $PathToAdd) {
        $newPath = (@($parts) + $PathToAdd) -join ';'
        [Environment]::SetEnvironmentVariable("Path", $newPath, "User")
        $env:Path = "$env:Path;$PathToAdd"
        Write-Host "Added $PathToAdd to the user PATH. Open a new terminal if this one does not see borz."
    }
}

switch ($env:PROCESSOR_ARCHITECTURE) {
    "AMD64" { $arch = "amd64" }
    default {
        throw "Unsupported Windows architecture '$env:PROCESSOR_ARCHITECTURE'. Current releases provide windows/amd64."
    }
}

$asset = "borz-windows-$arch.exe"
$baseUrl = "https://github.com/$Repo/releases/$(Get-ReleasePath $Version)"
$tmp = Join-Path ([IO.Path]::GetTempPath()) ("borz-install-" + [Guid]::NewGuid())
New-Item -ItemType Directory -Path $tmp | Out-Null

try {
    $checksumPath = Join-Path $tmp "checksums.txt"
    $assetPath = Join-Path $tmp $asset
    Write-Host "Installing borz from $Repo ($Version) for windows/$arch..."
    Invoke-WebRequest -UseBasicParsing "$baseUrl/checksums.txt" -OutFile $checksumPath
    Invoke-WebRequest -UseBasicParsing "$baseUrl/$asset" -OutFile $assetPath

    $expected = Get-Content $checksumPath |
        ForEach-Object { $_.Trim() } |
        Where-Object { $_ -match "\s\*?$([regex]::Escape($asset))$" } |
        ForEach-Object { ($_ -split '\s+')[0] } |
        Select-Object -First 1
    if (-not $expected) {
        throw "checksums.txt does not contain $asset"
    }
    $actual = (Get-FileHash -Algorithm SHA256 $assetPath).Hash.ToLowerInvariant()
    if ($actual -ne $expected.ToLowerInvariant()) {
        throw "checksum mismatch: got $actual, expected $expected"
    }

    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    $target = Join-Path $InstallDir "borz.exe"
    Copy-Item -Force $assetPath $target
    Write-Host "Installed borz to $target"

    if (-not $NoPath) {
        Add-UserPath $InstallDir
    }

    & $target version

    if ($Service) {
        $serviceArgs = @(
            "service", "install",
            "--name", $ServiceName,
            "--host", $HostName,
            "--port", $Port,
            "--cdp-host", $CdpHost,
            "--cdp-port", $CdpPort,
            "--idle-tab-timeout", $IdleTabTimeout
        )
        if ($Token) {
            $serviceArgs += @("--token", $Token)
        }
        & $target @serviceArgs
        if ($LASTEXITCODE -ne 0) {
            throw "borz service install failed with exit code $LASTEXITCODE"
        }
        if ($StartService) {
            & $target service start --name $ServiceName
            if ($LASTEXITCODE -ne 0) {
                throw "borz service start failed with exit code $LASTEXITCODE"
            }
        }
    }
}
finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}

