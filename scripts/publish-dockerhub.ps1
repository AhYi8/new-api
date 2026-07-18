[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [ValidatePattern('^[a-z0-9_][a-z0-9_.-]{0,127}$')]
    [string]$Version,

    [ValidatePattern('^[a-z0-9]+(?:[._-][a-z0-9]+)*$')]
    [string]$Repository = 'new-api',

    [switch]$SkipLatest,

    [switch]$ForceVersionOverwrite,

    [switch]$PreflightOnly
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$messagePath = Join-Path $PSScriptRoot 'publish-dockerhub.messages.zh-CN.json'
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

function Test-DockerHubCredential {
    $dockerConfigPath = Join-Path $HOME '.docker\config.json'
    if (-not (Test-Path -LiteralPath $dockerConfigPath)) {
        return $false
    }

    $dockerConfig = Get-Content -Raw -LiteralPath $dockerConfigPath | ConvertFrom-Json
    $dockerHubEndpoints = @(
        'https://index.docker.io/v1/',
        'index.docker.io',
        'registry-1.docker.io'
    )

    if ($dockerConfig.auths) {
        foreach ($endpoint in $dockerConfig.auths.PSObject.Properties.Name) {
            if ($dockerHubEndpoints -contains $endpoint) {
                return $true
            }
        }
    }

    if ($dockerConfig.credsStore) {
        $credentialHelper = Get-Command "docker-credential-$($dockerConfig.credsStore)" -ErrorAction SilentlyContinue
        if ($credentialHelper) {
            $credentialJson = & $credentialHelper.Source list 2>$null
            if ($LASTEXITCODE -eq 0 -and $credentialJson) {
                $credentials = $credentialJson | ConvertFrom-Json
                foreach ($endpoint in $credentials.PSObject.Properties.Name) {
                    if ($dockerHubEndpoints -contains $endpoint) {
                        return $true
                    }
                }
            }
        }
    }

    return $false
}

function Get-AnonymousDockerHubToken {
    param(
        [Parameter(Mandatory = $true)]
        [string]$ImagePath
    )

    $tokenUri = "https://auth.docker.io/token?service=registry.docker.io&scope=repository:$ImagePath`:pull"
    $tokenResponse = Invoke-RestMethod -Method Get -Uri $tokenUri -TimeoutSec 30
    if (-not $tokenResponse.token) {
        throw (Get-Message -Key 'AnonymousTokenMissing')
    }

    return $tokenResponse.token
}

function Test-PublicDockerManifest {
    param(
        [Parameter(Mandatory = $true)]
        [string]$ImagePath,

        [Parameter(Mandatory = $true)]
        [string]$Tag
    )

    $token = Get-AnonymousDockerHubToken -ImagePath $ImagePath
    $headers = @{
        Authorization = "Bearer $token"
        Accept = 'application/vnd.oci.image.index.v1+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.docker.distribution.manifest.v2+json'
    }

    try {
        $response = Invoke-WebRequest -UseBasicParsing -Method Head -Uri "https://registry-1.docker.io/v2/$ImagePath/manifests/$Tag" -Headers $headers -TimeoutSec 30
        return $response.StatusCode -eq 200
    }
    catch {
        if ($_.Exception.Response -and [int]$_.Exception.Response.StatusCode -eq 404) {
            return $false
        }

        throw
    }
}

$repositoryRoot = Split-Path -Parent $PSScriptRoot
$imagePath = "ahyi/$Repository"
$versionImage = "${imagePath}:$Version"
$latestImage = "${imagePath}:personal-latest"
$platform = 'linux/amd64'
$builderName = 'new-api-publisher'
$temporaryRoot = $null
$smokeContainer = $null
$localImage = $null

Push-Location $repositoryRoot
try {
    Write-Host (Get-Message -Key 'RunningPreflight')

    foreach ($command in @('git', 'docker', 'tar')) {
        if (-not (Get-Command $command -ErrorAction SilentlyContinue)) {
            throw (Get-Message -Key 'RequiredCommandMissing' -Values @($command))
        }
    }

    $currentBranch = Get-NativeOutput -Command 'git' -Arguments @('branch', '--show-current')
    if ($currentBranch -ne 'personal') {
        throw (Get-Message -Key 'InvalidBranch' -Values @($currentBranch))
    }

    $workingTreeStatus = Get-NativeOutput -Command 'git' -Arguments @('status', '--porcelain=v1')
    if ($workingTreeStatus) {
        throw (Get-Message -Key 'DirtyWorktree')
    }

    $upstreamBranch = Get-NativeOutput -Command 'git' -Arguments @('rev-parse', '--abbrev-ref', '--symbolic-full-name', '@{upstream}')
    Invoke-NativeCommand -Command 'git' -Arguments @('fetch', '--quiet')

    $trackingCounts = Get-NativeOutput -Command 'git' -Arguments @('rev-list', '--left-right', '--count', "$upstreamBranch...HEAD")
    $countParts = $trackingCounts -split '\s+'
    if ($countParts.Count -ne 2 -or $countParts[0] -ne '0' -or $countParts[1] -ne '0') {
        throw (Get-Message -Key 'TrackingMismatch' -Values @($upstreamBranch, $countParts[0], $countParts[1]))
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

    if (-not (Test-DockerHubCredential)) {
        throw (Get-Message -Key 'DockerHubCredentialMissing')
    }

    try {
        $repositoryInfo = Invoke-RestMethod -Method Get -Uri "https://hub.docker.com/v2/repositories/$imagePath/" -TimeoutSec 30
    }
    catch {
        throw (Get-Message -Key 'PublicRepositoryUnavailable' -Values @($imagePath, $_.Exception.Message))
    }

    if ($repositoryInfo.is_private) {
        throw (Get-Message -Key 'RepositoryIsPrivate' -Values @($imagePath))
    }

    if ((Test-PublicDockerManifest -ImagePath $imagePath -Tag $Version) -and -not $ForceVersionOverwrite) {
        throw (Get-Message -Key 'VersionAlreadyExists' -Values @($versionImage))
    }

    $commitSha = Get-NativeOutput -Command 'git' -Arguments @('rev-parse', 'HEAD')
    $shortSha = Get-NativeOutput -Command 'git' -Arguments @('rev-parse', '--short=12', 'HEAD')
    $originUrl = Get-NativeOutput -Command 'git' -Arguments @('config', '--get', 'remote.origin.url')
    if ($originUrl -match '^git@github\.com:(.+)$') {
        $sourceUrl = "https://github.com/$($Matches[1])"
    }
    else {
        $sourceUrl = $originUrl
    }
    $sourceUrl = $sourceUrl -replace '\.git$', ''

    Write-Host (Get-Message -Key 'SourceCommit' -Values @($commitSha))
    Write-Host (Get-Message -Key 'TargetImage' -Values @($versionImage))
    if (-not $SkipLatest) {
        Write-Host (Get-Message -Key 'RollingTag' -Values @($latestImage))
    }
    Write-Host (Get-Message -Key 'TargetPlatform' -Values @($platform))

    if ($PreflightOnly) {
        Write-Host (Get-Message -Key 'PreflightPassed')
        return
    }

    $temporaryRoot = Join-Path ([System.IO.Path]::GetTempPath()) "new-api-docker-$([Guid]::NewGuid().ToString('N'))"
    $buildContext = Join-Path $temporaryRoot 'context'
    $archivePath = Join-Path $temporaryRoot 'source.tar'
    New-Item -ItemType Directory -Path $buildContext -Force | Out-Null

    Invoke-NativeCommand -Command 'git' -Arguments @('archive', '--format=tar', '--output', $archivePath, 'HEAD')
    Invoke-NativeCommand -Command 'tar' -Arguments @('-xf', $archivePath, '-C', $buildContext)
    [System.IO.File]::WriteAllText((Join-Path $buildContext 'VERSION'), "$Version`n", (New-Object System.Text.UTF8Encoding($false)))

    $localImage = "new-api-local-verify:$shortSha"
    $commonBuildArguments = @(
        'buildx', 'build',
        '--builder', $builderName,
        '--platform', $platform,
        '--label', "org.opencontainers.image.source=$sourceUrl",
        '--label', "org.opencontainers.image.revision=$commitSha",
        '--label', "org.opencontainers.image.version=$Version",
        '--label', 'org.opencontainers.image.licenses=AGPL-3.0-only'
    )

    $baseImages = @(
        Get-Content -LiteralPath (Join-Path $buildContext 'Dockerfile') |
            Where-Object { $_ -match '^FROM\s+' } |
            ForEach-Object { ($_ -split '\s+')[1] } |
            Sort-Object -Unique
    )
    foreach ($baseImage in $baseImages) {
        Write-Host (Get-Message -Key 'PullingBaseImage' -Values @($baseImage))
        Invoke-NativeCommandWithRetry -Command 'docker' -Arguments @('pull', '--platform', $platform, $baseImage)
    }

    Write-Host (Get-Message -Key 'BuildingLocalImage')
    Invoke-NativeCommandWithRetry -Command 'docker' -Arguments ($commonBuildArguments + @('--tag', $localImage, '--load', $buildContext))

    Write-Host (Get-Message -Key 'RunningSmokeTest')
    $smokeContainer = "new-api-smoke-$($shortSha.Substring(0, 8))"
    $containerId = Get-NativeOutput -Command 'docker' -Arguments @(
        'run', '--detach', '--name', $smokeContainer,
        '--publish', '127.0.0.1::3000',
        $localImage
    )
    if (-not $containerId) {
        throw (Get-Message -Key 'SmokeContainerStartFailed')
    }

    $portOutput = Get-NativeOutput -Command 'docker' -Arguments @('port', $smokeContainer, '3000/tcp')
    if ($portOutput -notmatch ':(\d+)\s*$') {
        throw (Get-Message -Key 'SmokePortParseFailed' -Values @($portOutput))
    }
    $statusUri = "http://127.0.0.1:$($Matches[1])/api/status"

    $smokeSucceeded = $false
    $lastSmokeError = $null
    for ($attempt = 1; $attempt -le 30; $attempt++) {
        try {
            $response = Invoke-WebRequest -UseBasicParsing -Uri $statusUri -TimeoutSec 5
            if ($response.StatusCode -eq 200) {
                $smokeSucceeded = $true
                break
            }
        }
        catch {
            $lastSmokeError = $_.Exception.Message
        }
        Start-Sleep -Seconds 2
    }

    if (-not $smokeSucceeded) {
        Write-Host (Get-Message -Key 'SmokeLogs')
        & docker logs --tail 200 $smokeContainer
        throw (Get-Message -Key 'SmokeTimeout' -Values @($statusUri, $lastSmokeError))
    }
    Write-Host (Get-Message -Key 'SmokePassed' -Values @($statusUri))

    Invoke-NativeCommand -Command 'docker' -Arguments @('rm', '--force', $smokeContainer)
    $smokeContainer = $null

    Write-Host (Get-Message -Key 'PublishingImage')
    $publishArguments = $commonBuildArguments + @(
        '--tag', $versionImage,
        '--provenance=mode=max',
        '--sbom=true',
        '--push'
    )
    if (-not $SkipLatest) {
        $publishArguments += @('--tag', $latestImage)
    }
    $publishArguments += $buildContext
    Invoke-NativeCommandWithRetry -Command 'docker' -Arguments $publishArguments

    Invoke-NativeCommandWithRetry -Command 'docker' -Arguments @('buildx', 'imagetools', 'inspect', $versionImage)
    if (-not (Test-PublicDockerManifest -ImagePath $imagePath -Tag $Version)) {
        throw (Get-Message -Key 'AnonymousPullFailed' -Values @($versionImage))
    }

    Write-Host ''
    Write-Host (Get-Message -Key 'PublishCompleted')
    Write-Host (Get-Message -Key 'PublicImage' -Values @("docker.io/$versionImage"))
    Write-Host (Get-Message -Key 'SourceCommit' -Values @($commitSha))
    Write-Host (Get-Message -Key 'TargetPlatform' -Values @($platform))
}
finally {
    $previousErrorActionPreference = $ErrorActionPreference
    $ErrorActionPreference = 'SilentlyContinue'
    try {
        if ($smokeContainer) {
            & docker container inspect $smokeContainer *> $null
            if ($LASTEXITCODE -eq 0) {
                & docker rm --force $smokeContainer *> $null
            }
        }
        if ($localImage) {
            & docker image inspect $localImage *> $null
            if ($LASTEXITCODE -eq 0) {
                & docker image rm --force $localImage *> $null
            }
        }
        if ($temporaryRoot -and (Test-Path -LiteralPath $temporaryRoot)) {
            Remove-Item -LiteralPath $temporaryRoot -Recurse -Force
        }
    }
    finally {
        $ErrorActionPreference = $previousErrorActionPreference
        Pop-Location
    }
}
