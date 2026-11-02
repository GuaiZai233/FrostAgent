#Requires -Version 5.1
[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$registerPath = Join-Path $PSScriptRoot 'Register-FrostComputerBridge.ps1'
$launcherPath = Join-Path $PSScriptRoot 'Start-FrostComputerBridge.ps1'
$asts = @{}
foreach ($path in @($registerPath, $launcherPath)) {
    $tokens = $null
    $parseErrors = $null
    $asts[$path] = [System.Management.Automation.Language.Parser]::ParseFile(
        $path, [ref]$tokens, [ref]$parseErrors)
    if ($parseErrors.Count -ne 0) { throw "PowerShell parse errors in $path" }
}
$register = Get-Content -LiteralPath $registerPath -Raw
$launcher = Get-Content -LiteralPath $launcherPath -Raw
foreach ($required in @('SupportsShouldProcess', '$PSCmdlet.ShouldProcess', '-LogonType Interactive',
        '-AtLogOn', 'RemoteAddress = $allowedIPs', "Profile = @('Domain', 'Private')",
        '-ConfirmDedicatedVM', "MultipleInstances = 'IgnoreNew'")) {
    if (-not $register.Contains($required)) { throw "Missing deployment boundary: $required" }
}
foreach ($required in @("WindowStyle = 'Hidden'", 'PassThru = $true', 'Wait = $true')) {
    if (-not $launcher.Contains($required)) { throw "Missing launcher boundary: $required" }
}
foreach ($ast in $asts.Values) {
    $commands = $ast.FindAll({
        param($node)
        $node -is [System.Management.Automation.Language.CommandAst]
    }, $true)
    foreach ($command in $commands) {
        if ($command.GetCommandName() -in @('Restart-Computer', 'Stop-Computer',
                'Set-ItemProperty', 'New-Service', 'Start-ScheduledTask',
                'Remove-Item', 'Remove-NetFirewallRule', 'Unregister-ScheduledTask')) {
            throw 'Deployment source must not reboot, set autologon registry, start, or destructively replace resources.'
        }
    }
}
$example = Get-Content -LiteralPath (Join-Path $PSScriptRoot 'bridge.example.json') -Raw | ConvertFrom-Json
if ($example.token -match '^[A-Za-z0-9_-]{32,256}$' -or $example.audit_retention_days -ne 7) {
    throw 'Example must have a deliberately invalid secret placeholder and seven-day audit default.'
}
Write-Output 'Deployment source parsing and static boundary checks passed. No VM resources were changed.'
