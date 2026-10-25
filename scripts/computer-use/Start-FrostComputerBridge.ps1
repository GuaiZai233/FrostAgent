#Requires -Version 5.1
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string] $BridgePath,
    [Parameter(Mandatory = $true)]
    [string] $ConfigPath
)

$ErrorActionPreference = 'Stop'
try {
    $bridgeFile = Get-Item -LiteralPath $BridgePath -ErrorAction Stop
    $configFile = Get-Item -LiteralPath $ConfigPath -ErrorAction Stop
    if ($bridgeFile.PSIsContainer -or $configFile.PSIsContainer) {
        throw 'Bridge and configuration must be files.'
    }
    if ($configFile.FullName.Contains('"')) {
        throw 'Unsupported configuration path.'
    }
    if ([System.Diagnostics.Process]::GetCurrentProcess().SessionId -eq 0) {
        throw 'An interactive Windows logon is required.'
    }
    $startOptions = @{
        FilePath = $bridgeFile.FullName
        ArgumentList = @('-config', ('"' + $configFile.FullName + '"'))
        WorkingDirectory = $bridgeFile.DirectoryName
        WindowStyle = 'Hidden'
        PassThru = $true
        Wait = $true
    }
    $bridgeProcess = Start-Process @startOptions
    exit $bridgeProcess.ExitCode
}
catch {
    Write-Error 'Computer bridge launcher failed. Check VM-local configuration and interactive logon.'
    exit 1
}
