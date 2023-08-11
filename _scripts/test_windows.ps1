param (
    [Parameter(Mandatory = $true)][string]$version,
    [Parameter(Mandatory = $true)][string]$arch,
    [Parameter(Mandatory = $false)][string]$binDir
)

if ($binDir -eq "") {
    $binDir = Resolve-Path "../../system" # working directory
}


Set-MpPreference -DisableRealtimeMonitoring $true -ErrorAction SilentlyContinue

# Install MinGW.
if ($arch -eq "amd64")
{
    # Install Chocolatey
    #Set-ExecutionPolicy Bypass -Scope Process -Force; [System.Net.ServicePointManager]::SecurityProtocol = [System.Net.ServicePointManager]::SecurityProtocol -bor 3072; iex ((New-Object System.Net.WebClient).DownloadString('https://chocolatey.org/install.ps1'))
    choco install -y mingw
} elseif ($arch -eq "arm64") {
    $llvmVersion = "20220906"
    $name = "llvm-mingw-$llvmVersion-ucrt-aarch64"
    if (-Not(Test-Path "$binDir\llvm-mingw\$name"))
    {
        New-Item "$binDir\llvm-mingw" -ItemType Directory -ErrorAction SilentlyContinue
        $url = "https://github.com/mstorsjo/llvm-mingw/releases/download/$llvmVersion/$name.zip"
        Invoke-WebRequest -UserAgent wget -Uri $url -OutFile "$env:TEMP\$name.zip" -ErrorAction Stop
        Expand-Archive -Force -LiteralPath "$env:TEMP\$name.zip" -DestinationPath "$binDir\llvm-mingw\"
    }
    $env:PATH = "$binDir\llvm-mingw\$name\bin;$env:PATH"
} else {
    Throw "Unsupported architecture: $arch"
}

# Install Procdump
if (-Not(Test-Path "$binDir\procdump"))
{
    New-Item "$binDir\procdump" -ItemType Directory
    Invoke-WebRequest -UserAgent wget -Uri "https://download.sysinternals.com/files/Procdump.zip" -OutFile "$env:TEMP\procdump.zip"
    Expand-Archive -Force -LiteralPath "$env:TEMP\procdump.zip" -DestinationPath "$binDir\procdump"
}

$env:PATH = "$binDir\procdump;$env:PATH"

function GetGo($version) {
    $env:GOROOT = "$binDir\go\$version"
    if (-Not(Test-Path $env:GOROOT))
    {
        $file = "$version.windows-$arch.zip"
        $url = "https://dl.google.com/go/$file"
        Invoke-WebRequest -UserAgent wget -Uri $url -OutFile "$env:TEMP\$file" -ErrorAction Stop
        Expand-Archive -Force -LiteralPath "$env:TEMP\$file" -DestinationPath "$env:TEMP\$version"
        New-Item $env:GOROOT -ItemType Directory
        Move-Item -Path "$env:TEMP\$version\go\*" -Destination $env:GOROOT -Force
    }
}

if ($version -eq "gotip") {
    #Exit 0
    $latest = (Invoke-WebRequest -Uri "https://golang.org/VERSION?m=text" -UseBasicParsing | Select-Object -ExpandProperty Content -ErrorAction Stop).Split([Environment]::NewLine) | select -first 1
    GetGo $latest
    $env:GOROOT_BOOTSTRAP = $env:GOROOT
    $env:GOROOT = "$binDir\go\go-tip"
    Write-Host "Building Go with GOROOT_BOOTSTRAP $env:GOROOT_BOOTSTRAP"
    if (-Not(Test-Path $env:GOROOT)) {
        git clone "https://go.googlesource.com/go" "$binDir\go\go-tip"
        Push-Location -Path "$binDir\go\go-tip\src"
    } else {
        Push-Location -Path "$binDir\go\go-tip\src"
        git pull
    }
    .\make.bat
    Pop-Location
} else {
    # Install Go
    Write-Host "Finding latest patch version for $version"
    $versions = Invoke-WebRequest -Uri "https://golang.org/dl/?mode=json&include=all" -UseBasicParsing | foreach {$_.Content} | ConvertFrom-Json -ErrorAction Stop
    $v = $versions | foreach {$_.version} | Select-String -Pattern "^$version($|\.)" | Sort-Object -Descending | Select-Object -First 1
    if ($v -eq $null) {
      $v = $versions | foreach {$_.version} | Select-String -Pattern "^$version(rc)" | Sort-Object -Descending | Select-Object -First 1
    }
    if ($v -eq $null) {
      $v = $versions | foreach {$_.version} | Select-String -Pattern "^$version(beta)" | Sort-Object -Descending | Select-Object -First 1
    }
    Write-Host "Go $v on $arch"
    GetGo $v
}

$env:GOPATH = "$binDir\gopath"
$env:PATH = "$env:GOROOT\bin;$env:GOPATH\bin;$env:PATH"
Write-Host $env:PATH
Write-Host $env:GOROOT
Write-Host $env:GOPATH

Get-Command go
go version
go env
go run _scripts/make.go test -v
$x = $LastExitCode
if ($version -ne "gotip") {
    Exit $x
}
