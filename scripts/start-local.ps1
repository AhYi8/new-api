[CmdletBinding()]
param(
    [ValidateRange(1, 65535)]
    [int]$Port = 3000,

    [ValidatePattern('^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$')]
    [string]$ContainerName = 'new-api-local',

    [ValidatePattern('^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$')]
    [string]$VolumeName = 'new-api-local-data',

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

function Invoke-NativeCommandWithRetry {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Command,

        [Parameter()]
        [string[]]$Arguments = @(),

        [Parameter()]
        [int]$Attempts = 3,

        [Parameter()]
        [int]$DelaySeconds = 5
    )

    $nativeExitCode = 0
    for ($attempt = 1; $attempt -le $Attempts; $attempt++) {
        $previousErrorActionPreference = $ErrorActionPreference
        $ErrorActionPreference = 'Continue'
        try {
            & $Command @Arguments
            $nativeExitCode = $LASTEXITCODE
        }
        finally {
            $ErrorActionPreference = $previousErrorActionPreference
        }

        if ($nativeExitCode -eq 0) {
            return
        }

        if ($attempt -lt $Attempts) {
            $delay = $DelaySeconds * $attempt
            Write-Host (Get-Message -Key 'RetryingCommand' -Values @($delay, ($attempt + 1), $Attempts, $Command, ($Arguments -join ' ')))
            Start-Sleep -Seconds $delay
        }
    }

    throw (Get-Message -Key 'NativeCommandFailed' -Values @($nativeExitCode, $Command, ($Arguments -join ' ')))
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

function Write-ContainerLogs {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Name
    )

    $previousErrorActionPreference = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        & docker logs --tail 100 $Name 2>&1 | ForEach-Object { Write-Host $_ }
    }
    finally {
        $ErrorActionPreference = $previousErrorActionPreference
    }
}

$repositoryRoot = Split-Path -Parent $PSScriptRoot
$envPath = Join-Path $repositoryRoot '.env'
$dockerIgnorePath = Join-Path $repositoryRoot '.dockerignore'
$builderName = 'new-api-publisher'
$platform = 'linux/amd64'
$image = 'new-api-local-dev:personal'
$statusUri = "http://127.0.0.1:$Port/api/status"

Push-Location $repositoryRoot
try {
    foreach ($command in @('git', 'docker')) {
        if (-not (Get-Command $command -ErrorAction SilentlyContinue)) {
            throw (Get-Message -Key 'RequiredCommandMissing' -Values @($command))
        }
    }

    if (-not (Test-Path -LiteralPath $envPath)) {
        throw (Get-Message -Key 'EnvFileMissing' -Values @($envPath))
    }

    $envContent = @(Get-Content -Encoding UTF8 -LiteralPath $envPath)
    foreach ($setting in @('SQL_DSN', 'REDIS_CONN_STRING')) {
        if (-not (Test-EnvSetting -Content $envContent -Name $setting)) {
            throw (Get-Message -Key 'EnvSettingMissing' -Values @($setting))
        }
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

    $dockerIgnoreEntries = @(
        Get-Content -Encoding UTF8 -LiteralPath $dockerIgnorePath |
            ForEach-Object { $_.Trim() } |
            Where-Object { $_ -and -not $_.StartsWith('#') }
    )
    if ($dockerIgnoreEntries -notcontains '.env' -and $dockerIgnoreEntries -notcontains '/.env') {
        throw (Get-Message -Key 'EnvNotIgnoredByDocker')
    }

    Invoke-NativeCommand -Command 'docker' -Arguments @('version')
    Invoke-NativeCommand -Command 'docker' -Arguments @('buildx', 'version')

    $previousErrorActionPreference = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        & docker buildx inspect $builderName *> $null
        $builderExists = $LASTEXITCODE -eq 0
    }
    finally {
        $ErrorActionPreference = $previousErrorActionPreference
    }

    if (-not $builderExists) {
        Write-Host (Get-Message -Key 'CreatingBuilder' -Values @($builderName))
        Invoke-NativeCommand -Command 'docker' -Arguments @(
            'buildx', 'create',
            '--name', $builderName,
            '--driver', 'docker-container',
            '--bootstrap'
        )
    }

    Write-Host (Get-Message -Key 'PreparingBuilder' -Values @($builderName))
    $builderDetails = Get-NativeOutput -Command 'docker' -Arguments @('buildx', 'inspect', $builderName, '--bootstrap')
    if ($builderDetails -notmatch '(?m)^Driver:\s+docker-container\s*$') {
        throw (Get-Message -Key 'InvalidBuilderDriver' -Values @($builderName))
    }

    $commitSha = Get-NativeOutput -Command 'git' -Arguments @('rev-parse', 'HEAD')
    Write-Host (Get-Message -Key 'DevelopmentDatabaseWarning')
    Write-Host (Get-Message -Key 'SourceCommit' -Values @($commitSha))
    Write-Host (Get-Message -Key 'TargetImage' -Values @($image))
    Write-Host (Get-Message -Key 'TargetContainer' -Values @($ContainerName))

    if ($SkipBuild) {
        $previousErrorActionPreference = $ErrorActionPreference
        $ErrorActionPreference = 'Continue'
        try {
            & docker image inspect $image *> $null
            $imageExists = $LASTEXITCODE -eq 0
        }
        finally {
            $ErrorActionPreference = $previousErrorActionPreference
        }
        if (-not $imageExists) {
            throw (Get-Message -Key 'SkippedBuildImageMissing' -Values @($image))
        }
        Write-Host (Get-Message -Key 'SkippedBuild')
    }
    else {
        Write-Host (Get-Message -Key 'BuildingImage')
        $buildArguments = @(
            'buildx', 'build',
            '--builder', $builderName,
            '--platform', $platform,
            '--label', "org.opencontainers.image.revision=$commitSha",
            '--label', 'com.ahyi.new-api.local=true',
            '--tag', $image,
            '--load',
            $repositoryRoot
        )
        Invoke-NativeCommandWithRetry -Command 'docker' -Arguments $buildArguments
    }

    $existingContainer = Get-NativeOutput -Command 'docker' -Arguments @(
        'ps', '--all', '--quiet',
        '--filter', "name=^/$ContainerName$"
    )
    if ($existingContainer) {
        Write-Host (Get-Message -Key 'RemovingContainer' -Values @($ContainerName))
        Invoke-NativeCommand -Command 'docker' -Arguments @('rm', '--force', $ContainerName)
    }

    $listener = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, $Port)
    try {
        $listener.Start()
    }
    catch {
        throw (Get-Message -Key 'PortBusy' -Values @($Port))
    }
    finally {
        $listener.Stop()
    }

    Write-Host (Get-Message -Key 'StartingContainer')
    $containerId = Get-NativeOutput -Command 'docker' -Arguments @(
        'run', '--detach',
        '--name', $ContainerName,
        '--label', 'com.ahyi.new-api.local=true',
        '--env-file', $envPath,
        '--publish', "127.0.0.1:${Port}:3000",
        '--volume', "${VolumeName}:/data",
        $image
    )
    if (-not $containerId) {
        throw (Get-Message -Key 'ContainerStartFailed')
    }

    $deadline = [DateTime]::UtcNow.AddSeconds($StartupTimeoutSeconds)
    $lastHealthError = $null
    while ([DateTime]::UtcNow -lt $deadline) {
        $running = Get-NativeOutput -Command 'docker' -Arguments @('inspect', '--format', '{{.State.Running}}', $ContainerName)
        if ($running -ne 'true') {
            $status = Get-NativeOutput -Command 'docker' -Arguments @('inspect', '--format', '{{.State.Status}} (exit={{.State.ExitCode}})', $ContainerName)
            Write-Host (Get-Message -Key 'ContainerLogs')
            Write-ContainerLogs -Name $ContainerName
            throw (Get-Message -Key 'ContainerExited' -Values @($status))
        }

        try {
            $response = Invoke-WebRequest -UseBasicParsing -Uri $statusUri -TimeoutSec 5
            if ($response.StatusCode -eq 200) {
                Write-Host ''
                Write-Host (Get-Message -Key 'StartupPassed' -Values @($statusUri))
                Write-Host (Get-Message -Key 'ContainerRunning' -Values @($ContainerName, $response.StatusCode))
                return
            }
        }
        catch {
            $lastHealthError = $_.Exception.Message
        }

        Start-Sleep -Seconds 2
    }

    Write-Host (Get-Message -Key 'ContainerLogs')
    Write-ContainerLogs -Name $ContainerName
    throw (Get-Message -Key 'StartupTimeout' -Values @($StartupTimeoutSeconds, $statusUri, $lastHealthError))
}
finally {
    Pop-Location
}
