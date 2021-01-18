param (
    [Parameter(Mandatory = $true)][string]$version,
    [Parameter(Mandatory = $true)][string]$arch
)

# Install MinGW.
if (-Not(Test-Path "C:\mingw64"))
{
    $file = "x86_64-4.9.2-release-win32-seh-rt_v4-rev3.7z"
    $url = "https://bintray.com/artifact/download/drewwells/generic/$file"
    Invoke-WebRequest -UserAgent wget -Uri $url -OutFile $file
    &7z x -oC:\ $file > $null
}

# Install Procdump
if (-Not(Test-Path "C:\procdump"))
{
    mkdir C:\procdump
    Invoke-WebRequest -UserAgent wget -Uri https://download.sysinternals.com/files/Procdump.zip -OutFile C:\procdump\procdump.zip
    &7z x -oC:\procdump\ C:\procdump\procdump.zip > $null
}

# Install Go
if ($version -eq "golatest")
{
    $version = Invoke-WebRequest -Uri https://golang.org/VERSION?m=text -UseBasicParsing | Select-Object -ExpandProperty Content
}
Write-Host "Go $version on $arch"
$env:GOROOT = "C:\go\$version"
if (-Not(Test-Path $env:GOROOT))
{
    $file = "$version.windows-$arch.zip"
    $url = "https://dl.google.com/go/$file"
    Invoke-WebRequest -UserAgent wget -Uri $url -OutFile $file
    &7z x -oC:\go $file > $null
    Move-Item -Path C:\go\go -Destination $env:GOROOT -force
}

$env:GOPATH = "C:\gopath"
$env:PATH += ";C:\procdump;C:\mingw64\bin;$env:GOROOT\bin;$env:GOPATH\bin"
Write-Host $env:PATH
Write-Host $env:GOROOT
Write-Host $env:GOPATH
go version
go env
mingw32-make test
