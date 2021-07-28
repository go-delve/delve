param (
    [Parameter(Mandatory = $true)][string]$version,
    [Parameter(Mandatory = $true)][string]$arch
)

# Install Chocolatey
#Set-ExecutionPolicy Bypass -Scope Process -Force; [System.Net.ServicePointManager]::SecurityProtocol = [System.Net.ServicePointManager]::SecurityProtocol -bor 3072; iex ((New-Object System.Net.WebClient).DownloadString('https://chocolatey.org/install.ps1'))

# Install MinGW.
choco install -y mingw

# Install Procdump
if (-Not(Test-Path "C:\procdump"))
{
    mkdir C:\procdump
    Invoke-WebRequest -UserAgent wget -Uri https://download.sysinternals.com/files/Procdump.zip -OutFile C:\procdump\procdump.zip
    &7z x -oC:\procdump\ C:\procdump\procdump.zip > $null
}

$env:PATH += ";C:\procdump;C:\mingw64\bin"

function GetGo($version) {
    $env:GOROOT = "C:\go\$version"
    if (-Not(Test-Path $env:GOROOT))
    {
        $file = "$version.windows-$arch.zip"
        $url = "https://dl.google.com/go/$file"
        Invoke-WebRequest -UserAgent wget -Uri $url -OutFile $file
        &7z x -oC:\go $file > $null
        Move-Item -Path C:\go\go -Destination $env:GOROOT -force
    }
}

if ($version -eq "gotip") {
    Exit 0
    $latest = Invoke-WebRequest -Uri https://golang.org/VERSION?m=text -UseBasicParsing | Select-Object -ExpandProperty Content
    GetGo $latest
    $env:GOROOT_BOOTSTRAP = $env:GOROOT
    $env:GOROOT = "C:\go\go-tip"
    Write-Host "Building Go with GOROOT_BOOTSTRAP $env:GOROOT_BOOTSTRAP"
    if (-Not(Test-Path $env:GOROOT)) {
        git clone https://go.googlesource.com/go C:\go\go-tip
        Push-Location -Path C:\go\go-tip\src
    } else {
        Push-Location -Path C:\go\go-tip\src
        git pull
    }
    .\make.bat
    Pop-Location
} else {
    # Install Go
    Write-Host "Finding latest patch version for $version"
    $versions = Invoke-WebRequest -Uri 'https://golang.org/dl/?mode=json&include=all' -UseBasicParsing | foreach {$_.Content} | ConvertFrom-Json
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

$env:GOPATH = "C:\gopath"
$env:PATH += ";$env:GOROOT\bin;$env:GOPATH\bin"
Write-Host $env:PATH
Write-Host $env:GOROOT
Write-Host $env:GOPATH

go version
go env
go run _scripts/make.go test
