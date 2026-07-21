[CmdletBinding()]
param(
    [ValidateRange(1, 65535)]
    [int]$Port = 13000,

    [ValidateRange(30, 1800)]
    [int]$StartupTimeoutSeconds = 600,

    [switch]$SkipBuild
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$messagePath = Join-Path $PSScriptRoot 'start-local.messages.zh-CN.json'
$script:Messages = Get-Content -Raw -Encoding UTF8 -LiteralPath $messagePath | ConvertFrom-Json

function Get-Message {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Key,

        [Parameter()]
        [object[]]$Values = @()
    )

    $template = $script:Messages.$Key
    return [string]::Format([System.Globalization.CultureInfo]::InvariantCulture, $template, $Values)
}

function Invoke-NativeCommand {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Command,

        [Parameter()]
        [string[]]$Arguments = @()
    )

    & $Command @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw (Get-Message -Key 'NativeCommandFailed' -Values @($LASTEXITCODE, $Command, ($Arguments -join ' ')))
    }
}

function Get-NativeOutput {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Command,

        [Parameter()]
        [string[]]$Arguments = @()
    )

    $output = & $Command @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw (Get-Message -Key 'NativeCommandFailed' -Values @($LASTEXITCODE, $Command, ($Arguments -join ' ')))
    }

    return ($output | Out-String).Trim()
}

function Test-EnvSetting {
    param(
        [Parameter(Mandatory = $true)]
        [string[]]$Content,

        [Parameter(Mandatory = $true)]
        [string]$Name
    )

    $pattern = '^\s*' + [Regex]::Escape($Name) + '\s*=\s*(.*?)\s*$'
    $value = $null
    foreach ($line in $Content) {
        if ($line -match $pattern) {
            $value = $Matches[1].Trim()
        }
    }

    return $null -ne $value -and $value -ne '' -and $value -ne '""' -and $value -ne "''"
}

function Get-ManagedProcess {
    param(
        [Parameter(Mandatory = $true)]
        [string]$PidFile,

        [Parameter(Mandatory = $true)]
        [string]$ExpectedExecutable
    )

    if (-not (Test-Path -LiteralPath $PidFile)) {
        return $null
    }

    $rawProcessId = (Get-Content -Raw -Encoding UTF8 -LiteralPath $PidFile).Trim()
    $processId = 0
    if (-not [int]::TryParse($rawProcessId, [ref]$processId) -or $processId -le 0) {
        Remove-Item -LiteralPath $PidFile -Force
        return $null
    }

    $process = Get-Process -Id $processId -ErrorAction SilentlyContinue
    if ($null -eq $process) {
        Remove-Item -LiteralPath $PidFile -Force
        return $null
    }

    try {
        $actualExecutable = [System.IO.Path]::GetFullPath($process.Path)
    }
    catch {
        throw (Get-Message -Key 'ManagedProcessCannotBeVerified' -Values @($processId))
    }

    $expectedFullPath = [System.IO.Path]::GetFullPath($ExpectedExecutable)
    if (-not [string]::Equals($actualExecutable, $expectedFullPath, [System.StringComparison]::OrdinalIgnoreCase)) {
        throw (Get-Message -Key 'ManagedProcessMismatch' -Values @($processId, $actualExecutable, $expectedFullPath))
    }

    return $process
}

function Write-ProcessLogs {
    param(
        [Parameter(Mandatory = $true)]
        [string]$StandardOutputPath,

        [Parameter(Mandatory = $true)]
        [string]$StandardErrorPath
    )

    foreach ($logPath in @($StandardOutputPath, $StandardErrorPath)) {
        if (Test-Path -LiteralPath $logPath) {
            Get-Content -Encoding UTF8 -Tail 100 -LiteralPath $logPath | ForEach-Object { Write-Host $_ }
        }
    }
}

function Test-PortBindingAvailable {
    param(
        [Parameter(Mandatory = $true)]
        [int]$Port
    )

    $listener = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, $Port)
    try {
        $listener.Start()
        return $true
    }
    catch {
        return $false
    }
    finally {
        $listener.Stop()
    }
}

function Get-DirectHttpStatusCode {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Uri,

        [Parameter()]
        [int]$TimeoutSeconds = 5
    )

    $request = [System.Net.HttpWebRequest]::Create($Uri)
    $request.Method = 'GET'
    $request.Proxy = $null
    $request.Timeout = $TimeoutSeconds * 1000
    $request.ReadWriteTimeout = $TimeoutSeconds * 1000
    $response = $null
    try {
        $response = [System.Net.HttpWebResponse]$request.GetResponse()
        return [int]$response.StatusCode
    }
    finally {
        if ($null -ne $response) {
            $response.Dispose()
        }
    }
}

$repositoryRoot = Split-Path -Parent $PSScriptRoot
$envPath = Join-Path $repositoryRoot '.env'
$webRoot = Join-Path $repositoryRoot 'web'
$defaultWebRoot = Join-Path $webRoot 'default'
$classicWebRoot = Join-Path $webRoot 'classic'
$defaultIndexPath = Join-Path $defaultWebRoot 'dist\index.html'
$classicIndexPath = Join-Path $classicWebRoot 'dist\index.html'
$runtimeRoot = Join-Path $repositoryRoot '.local-tests\start-local'
$executablePath = Join-Path $runtimeRoot 'new-api-local.exe'
$nextExecutablePath = Join-Path $runtimeRoot 'new-api-local.next.exe'
$pidPath = Join-Path $runtimeRoot 'new-api-local.pid'
$standardOutputPath = Join-Path $runtimeRoot 'stdout.log'
$standardErrorPath = Join-Path $runtimeRoot 'stderr.log'
$statusUri = "http://127.0.0.1:$Port/api/status"
$startedProcess = $null
$startupSucceeded = $false

Push-Location $repositoryRoot
try {
    if ($env:OS -ne 'Windows_NT') {
        throw (Get-Message -Key 'WindowsRequired')
    }

    foreach ($command in @('git', 'go', 'bun')) {
        if (-not (Get-Command $command -ErrorAction SilentlyContinue)) {
            throw (Get-Message -Key 'RequiredCommandMissing' -Values @($command))
        }
    }

    if (-not (Test-Path -LiteralPath $envPath)) {
        throw (Get-Message -Key 'EnvFileMissing' -Values @($envPath))
    }

    $previousErrorActionPreference = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        & git check-ignore --quiet -- .env
        $envIgnoredByGit = $LASTEXITCODE -eq 0
    }
    finally {
        $ErrorActionPreference = $previousErrorActionPreference
    }
    if (-not $envIgnoredByGit) {
        throw (Get-Message -Key 'EnvNotIgnoredByGit')
    }

    $envContent = @(Get-Content -Encoding UTF8 -LiteralPath $envPath)
    Write-Host (Get-Message -Key 'DevelopmentDatabaseWarning')
    if (-not (Test-EnvSetting -Content $envContent -Name 'SQL_DSN')) {
        Write-Host (Get-Message -Key 'UsingSQLite')
    }
    if (-not (Test-EnvSetting -Content $envContent -Name 'REDIS_CONN_STRING')) {
        Write-Host (Get-Message -Key 'RedisDisabled')
    }
    if (-not (Test-EnvSetting -Content $envContent -Name 'SESSION_SECRET')) {
        Write-Host (Get-Message -Key 'SessionSecretWarning')
    }

    New-Item -ItemType Directory -Force -Path $runtimeRoot | Out-Null

    $commitSha = Get-NativeOutput -Command 'git' -Arguments @('rev-parse', 'HEAD')
    Write-Host (Get-Message -Key 'SourceCommit' -Values @($commitSha))
    Write-Host (Get-Message -Key 'RuntimeDirectory' -Values @($runtimeRoot))

    $managedProcess = Get-ManagedProcess -PidFile $pidPath -ExpectedExecutable $executablePath
    if ($null -ne $managedProcess) {
        Write-Host (Get-Message -Key 'StoppingProcess' -Values @($managedProcess.Id))
        Stop-Process -Id $managedProcess.Id -Force
        Wait-Process -Id $managedProcess.Id -Timeout 30 -ErrorAction SilentlyContinue
        Remove-Item -LiteralPath $pidPath -Force -ErrorAction SilentlyContinue
    }

    if ($SkipBuild) {
        foreach ($artifact in @($defaultIndexPath, $classicIndexPath, $executablePath)) {
            if (-not (Test-Path -LiteralPath $artifact)) {
                throw (Get-Message -Key 'SkippedBuildArtifactMissing' -Values @($artifact))
            }
        }
        Write-Host (Get-Message -Key 'SkippedBuild')
    }
    else {
        Write-Host (Get-Message -Key 'InstallingWebDependencies')
        Push-Location $webRoot
        try {
            Invoke-NativeCommand -Command 'bun' -Arguments @('install', '--frozen-lockfile')
        }
        finally {
            Pop-Location
        }

        $originalFrontendVersion = [Environment]::GetEnvironmentVariable('VITE_REACT_APP_VERSION', 'Process')
        $env:VITE_REACT_APP_VERSION = $commitSha.Substring(0, 8)
        try {
            Write-Host (Get-Message -Key 'BuildingDefaultWeb')
            Push-Location $defaultWebRoot
            try {
                Invoke-NativeCommand -Command 'bun' -Arguments @('run', 'build')
            }
            finally {
                Pop-Location
            }

            Write-Host (Get-Message -Key 'BuildingClassicWeb')
            Push-Location $classicWebRoot
            try {
                Invoke-NativeCommand -Command 'bun' -Arguments @('run', 'build')
            }
            finally {
                Pop-Location
            }
        }
        finally {
            if ($null -eq $originalFrontendVersion) {
                Remove-Item -LiteralPath 'Env:VITE_REACT_APP_VERSION' -ErrorAction SilentlyContinue
            }
            else {
                [Environment]::SetEnvironmentVariable('VITE_REACT_APP_VERSION', $originalFrontendVersion, 'Process')
            }
        }

        Write-Host (Get-Message -Key 'BuildingExecutable')
        Invoke-NativeCommand -Command 'go' -Arguments @('build', '-o', $nextExecutablePath, '.')
    }

    if (-not $SkipBuild) {
        Move-Item -LiteralPath $nextExecutablePath -Destination $executablePath -Force
    }

    $portReleaseDeadline = [DateTime]::UtcNow.AddSeconds(30)
    $waitingForPort = $false
    while (-not (Test-PortBindingAvailable -Port $Port)) {
        if (-not $waitingForPort) {
            Write-Host (Get-Message -Key 'WaitingForPortRelease' -Values @($Port))
            $waitingForPort = $true
        }
        if ([DateTime]::UtcNow -ge $portReleaseDeadline) {
            break
        }
        Start-Sleep -Seconds 1
    }
    if (-not (Test-PortBindingAvailable -Port $Port)) {
        throw (Get-Message -Key 'PortBusy' -Values @($Port))
    }

    Remove-Item -LiteralPath $standardOutputPath -Force -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $standardErrorPath -Force -ErrorAction SilentlyContinue

    Write-Host (Get-Message -Key 'StartingProcess')
    $originalPort = [Environment]::GetEnvironmentVariable('PORT', 'Process')
    $env:PORT = [string]$Port
    try {
        $startedProcess = Start-Process `
            -FilePath $executablePath `
            -WorkingDirectory $repositoryRoot `
            -RedirectStandardOutput $standardOutputPath `
            -RedirectStandardError $standardErrorPath `
            -WindowStyle Hidden `
            -PassThru
    }
    finally {
        if ($null -eq $originalPort) {
            Remove-Item -LiteralPath 'Env:PORT' -ErrorAction SilentlyContinue
        }
        else {
            [Environment]::SetEnvironmentVariable('PORT', $originalPort, 'Process')
        }
    }
    if ($null -eq $startedProcess) {
        throw (Get-Message -Key 'ProcessStartFailed')
    }

    Set-Content -NoNewline -Encoding ASCII -LiteralPath $pidPath -Value ([string]$startedProcess.Id)

    $deadline = [DateTime]::UtcNow.AddSeconds($StartupTimeoutSeconds)
    $lastHealthError = $null
    $consecutiveHealthyResponses = 0
    while ([DateTime]::UtcNow -lt $deadline) {
        $startedProcess.Refresh()
        if ($startedProcess.HasExited) {
            Write-Host (Get-Message -Key 'ProcessLogs')
            Write-ProcessLogs -StandardOutputPath $standardOutputPath -StandardErrorPath $standardErrorPath
            throw (Get-Message -Key 'ProcessExited' -Values @($startedProcess.ExitCode))
        }

        try {
            $statusCode = Get-DirectHttpStatusCode -Uri $statusUri
            if ($statusCode -eq 200) {
                $consecutiveHealthyResponses++
            }
            else {
                $consecutiveHealthyResponses = 0
            }
            if ($consecutiveHealthyResponses -ge 2) {
                $startedProcess.Refresh()
                if ($startedProcess.HasExited) {
                    continue
                }
                $startupSucceeded = $true
                Write-Host ''
                Write-Host (Get-Message -Key 'StartupPassed' -Values @($statusUri))
                Write-Host (Get-Message -Key 'ProcessRunning' -Values @($startedProcess.Id, $statusCode))
                return
            }
        }
        catch {
            $consecutiveHealthyResponses = 0
            $lastHealthError = $_.Exception.Message
        }

        Start-Sleep -Seconds 2
    }

    Write-Host (Get-Message -Key 'ProcessLogs')
    Write-ProcessLogs -StandardOutputPath $standardOutputPath -StandardErrorPath $standardErrorPath
    throw (Get-Message -Key 'StartupTimeout' -Values @($StartupTimeoutSeconds, $statusUri, $lastHealthError))
}
finally {
    if (-not $startupSucceeded -and $null -ne $startedProcess) {
        $startedProcess.Refresh()
        if (-not $startedProcess.HasExited) {
            Stop-Process -Id $startedProcess.Id -Force -ErrorAction SilentlyContinue
        }
        Remove-Item -LiteralPath $pidPath -Force -ErrorAction SilentlyContinue
    }
    Pop-Location
}
