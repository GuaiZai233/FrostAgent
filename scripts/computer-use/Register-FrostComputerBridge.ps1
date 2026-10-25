#Requires -Version 5.1
#Requires -RunAsAdministrator
[CmdletBinding(SupportsShouldProcess = $true, ConfirmImpact = 'High')]
param(
    [Parameter(Mandatory = $true)]
    [string] $BridgePath,
    [Parameter(Mandatory = $true)]
    [string] $ConfigPath,
    [Parameter(Mandatory = $true)]
    [string] $User,
    [Parameter(Mandatory = $true)]
    [string[]] $AllowedAddress,
    [ValidatePattern('^[A-Za-z0-9_-]{1,80}$')]
    [string] $TaskName = 'FrostAgent-ComputerBridge',
    [switch] $ConfirmDedicatedVM
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

function Test-PrivateAddress {
    param([System.Net.IPAddress] $Address)
    if ([System.Net.IPAddress]::IsLoopback($Address)) { return $true }
    if ($Address.IsIPv4MappedToIPv6) { $Address = $Address.MapToIPv4() }
    $bytes = $Address.GetAddressBytes()
    if ($bytes.Length -eq 4) {
        return $bytes[0] -eq 10 -or
            ($bytes[0] -eq 172 -and $bytes[1] -ge 16 -and $bytes[1] -le 31) -or
            ($bytes[0] -eq 192 -and $bytes[1] -eq 168)
    }
    return $bytes.Length -eq 16 -and ($bytes[0] -band 254) -eq 252
}

function Get-LocalFile {
    param([string] $Path)
    if (-not [System.IO.Path]::IsPathRooted($Path) -or $Path.StartsWith('\\')) {
        throw 'Use an absolute local file path.'
    }
    $item = Get-Item -LiteralPath $Path -ErrorAction Stop
    if ($item.PSIsContainer -or $item.FullName.Contains('"') -or
        $item.FullName.Contains([char]13) -or $item.FullName.Contains([char]10)) {
        throw 'Invalid local file path.'
    }
    return $item
}

if (-not $ConfirmDedicatedVM) {
    throw 'Run only inside the dedicated VM, then explicitly pass -ConfirmDedicatedVM.'
}
if ([System.Diagnostics.Process]::GetCurrentProcess().SessionId -eq 0) {
    throw 'Register from an interactive VM desktop, not Session 0.'
}
$bridgeFile = Get-LocalFile $BridgePath
$configFile = Get-LocalFile $ConfigPath
$launcherFile = Get-LocalFile (Join-Path $PSScriptRoot 'Start-FrostComputerBridge.ps1')
try { $config = Get-Content -LiteralPath $configFile.FullName -Raw | ConvertFrom-Json }
catch { throw 'Unable to read VM bridge JSON configuration. Verify locally without sharing its contents.' }
if ([string]::IsNullOrWhiteSpace($config.instance_id) -or
    [string]$config.token -notmatch '^[A-Za-z0-9_-]{32,256}$') {
    throw 'Missing instance binding or invalid bridge token. Do not use the example placeholder.'
}
foreach ($key in @('certificate', 'key', 'git')) {
    $null = Get-LocalFile ([string]$config.$key)
}
foreach ($key in @('state_dir', 'audit_dir', 'project_root')) {
    $configuredPath = [string]$config.$key
    if ([string]::IsNullOrWhiteSpace($configuredPath) -or
        -not [System.IO.Path]::IsPathRooted($configuredPath) -or
        $configuredPath.StartsWith('\\')) {
        throw 'State, audit and project directories must use absolute VM-local paths.'
    }
}
if ([string]$config.listen -notmatch '^(\[[^\]]+\]|[^:\s]+):[0-9]{1,5}$') {
    throw 'Configure a numeric private listen address with an explicit port.'
}
try {
    $listen = [System.Uri]('https://' + [string]$config.listen)
    $listenIP = [System.Net.IPAddress]::Parse($listen.DnsSafeHost.Trim('[', ']'))
}
catch { throw 'Configure a numeric private listen address and explicit port.' }
if ($listen.Port -le 0 -or -not (Test-PrivateAddress $listenIP) -or
    $listen.AbsolutePath -ne '/' -or $listen.Query -or $listen.Fragment -or $listen.UserInfo) {
    throw 'Only a private/loopback HTTPS listen endpoint is supported.'
}
$allowedIPs = @()
foreach ($entry in $AllowedAddress) {
    try { $address = [System.Net.IPAddress]::Parse($entry) }
    catch { throw 'AllowedAddress must contain individual numeric private addresses, not Any or a subnet.' }
    if (-not (Test-PrivateAddress $address)) {
        throw 'Firewall access must be scoped to private FrostAgent host addresses.'
    }
    $allowedIPs += $address.ToString()
}
if ($allowedIPs.Count -eq 0) { throw 'At least one FrostAgent host address is required.' }

$taskUser = New-Object System.Security.Principal.NTAccount($User)
$null = $taskUser.Translate([System.Security.Principal.SecurityIdentifier])
$firewallName = $TaskName + '-HTTPS'
if (Get-ScheduledTask -TaskPath '\' -TaskName $TaskName -ErrorAction SilentlyContinue) {
    throw 'The scheduled task already exists. Inspect and update it explicitly; this script never overwrites it.'
}
if (Get-NetFirewallRule -Name $firewallName -ErrorAction SilentlyContinue) {
    throw 'The firewall rule already exists. Inspect it before reconfiguration.'
}
$powershellPath = Join-Path $env:WINDIR 'System32\WindowsPowerShell\v1.0\powershell.exe'
$arguments = '-NoLogo -NoProfile -NonInteractive -WindowStyle Hidden -File "' +
    $launcherFile.FullName + '" -BridgePath "' + $bridgeFile.FullName +
    '" -ConfigPath "' + $configFile.FullName + '"'
$actionOptions = @{
    Execute = $powershellPath
    Argument = $arguments
    WorkingDirectory = $bridgeFile.DirectoryName
}
$action = New-ScheduledTaskAction @actionOptions
$principal = New-ScheduledTaskPrincipal -UserId $User -LogonType Interactive -RunLevel Highest
$trigger = New-ScheduledTaskTrigger -AtLogOn -User $User
$settingsOptions = @{
    MultipleInstances = 'IgnoreNew'
    ExecutionTimeLimit = [TimeSpan]::Zero
    RestartCount = 3
    RestartInterval = New-TimeSpan -Minutes 1
    StartWhenAvailable = $true
    AllowStartIfOnBatteries = $true
    DontStopIfGoingOnBatteries = $true
}
$settings = New-ScheduledTaskSettingsSet @settingsOptions
if ($PSCmdlet.ShouldProcess($TaskName, 'Register dedicated-VM interactive bridge startup and scoped private HTTPS firewall rule')) {
    $firewallOptions = @{
        Name = $firewallName
        DisplayName = $firewallName
        Direction = 'Inbound'
        Action = 'Allow'
        Protocol = 'TCP'
        Program = $bridgeFile.FullName
        LocalAddress = $listenIP.ToString()
        LocalPort = $listen.Port
        RemoteAddress = $allowedIPs
        Profile = @('Domain', 'Private')
    }
    $null = New-NetFirewallRule @firewallOptions
    try {
        $registrationOptions = @{
            TaskPath = '\'
            TaskName = $TaskName
            Action = $action
            Principal = $principal
            Trigger = $trigger
            Settings = $settings
        }
        $null = Register-ScheduledTask @registrationOptions
    }
    catch {
        Write-Warning "Task registration failed; inspect the newly created firewall rule '$firewallName'. No existing task or file was removed."
        throw 'Bridge startup registration failed.'
    }
    Write-Output "Registered $TaskName. Not started. Sign in as the configured VM user, then start the task explicitly for verification."
}
