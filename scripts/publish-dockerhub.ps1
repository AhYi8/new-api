<#
.SYNOPSIS
从 personal 分支一键构建、验证并发布个人 Docker Hub 多架构镜像。

.DESCRIPTION
默认根据 upstream/main 最近的官方版本标签和 Docker Hub 现有个人版本，自动选择下一个
personal-<官方版本>-rN 标签，同时更新 personal-latest。发布前要求工作区干净、当前提交
已推送到 origin/personal，并且已经包含最新 upstream/main。

.EXAMPLE
.\scripts\publish-dockerhub.ps1

.EXAMPLE
.\scripts\publish-dockerhub.ps1 -PreflightOnly

.EXAMPLE
.\scripts\publish-dockerhub.ps1 -Version personal-v1.0.0-rc.21-r4
#>
[CmdletBinding()]
param(
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
$script:HostProxyUri = $null

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

function Get-BuildxBuilderNames {
    $output = Get-NativeOutput -Command 'docker' -Arguments @('buildx', 'ls', '--format', '{{.Name}}')
    if (-not $output) {
        return @()
    }

    return @(
        $output -split '\r?\n' |
            ForEach-Object { $_.Trim() } |
            Where-Object { $_ }
    )
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

function Invoke-DockerHubRestMethod {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Uri
    )

    $parameters = @{
        Method = 'Get'
        Uri = $Uri
        TimeoutSec = 30
    }
    if ($script:HostProxyUri) {
        $parameters.Proxy = $script:HostProxyUri
    }

    return Invoke-RestMethod @parameters
}

function Invoke-DockerHubWebRequest {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Uri,

        [Parameter()]
        [hashtable]$Headers = @{},

        [Parameter()]
        [ValidateSet('Get', 'Head')]
        [string]$Method = 'Get'
    )

    $parameters = @{
        UseBasicParsing = $true
        Method = $Method
        Uri = $Uri
        Headers = $Headers
        TimeoutSec = 30
    }
    if ($script:HostProxyUri) {
        $parameters.Proxy = $script:HostProxyUri
    }

    return Invoke-WebRequest @parameters
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
    $tokenResponse = Invoke-DockerHubRestMethod -Uri $tokenUri
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
        $response = Invoke-DockerHubWebRequest -Method Head -Uri "https://registry-1.docker.io/v2/$ImagePath/manifests/$Tag" -Headers $headers
        return $response.StatusCode -eq 200
    }
    catch {
        if ($_.Exception.Response -and [int]$_.Exception.Response.StatusCode -eq 404) {
            return $false
        }

        throw
    }
}

function Test-DockerHubNetwork {
    param(
        [Parameter(Mandatory = $true)]
        [string]$ImagePath
    )

    try {
        $token = Get-AnonymousDockerHubToken -ImagePath $ImagePath
        $headers = @{ Authorization = "Bearer $token" }
        $response = Invoke-DockerHubWebRequest -Uri 'https://registry-1.docker.io/v2/' -Headers $headers
        return $response.StatusCode -eq 200
    }
    catch {
        return $false
    }
}

function Test-BuildxDockerHubNetwork {
    param(
        [Parameter(Mandatory = $true)]
        [string]$BuilderName,

        [Parameter(Mandatory = $true)]
        [string]$Image,

        [Parameter(Mandatory = $true)]
        [string]$Platforms
    )

    $probeRoot = Join-Path ([System.IO.Path]::GetTempPath()) "new-api-buildx-network-probe-$([Guid]::NewGuid().ToString('N'))"
    New-Item -ItemType Directory -Path $probeRoot -Force | Out-Null
    $probeDockerfile = Join-Path $probeRoot 'Dockerfile'
    [System.IO.File]::WriteAllText($probeDockerfile, "FROM $Image`n", (New-Object System.Text.UTF8Encoding($false)))

    $previousErrorActionPreference = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        & docker buildx build `
            --builder $BuilderName `
            --file $probeDockerfile `
            --platform $Platforms `
            --pull `
            --output type=cacheonly `
            $probeRoot *> $null
        return $LASTEXITCODE -eq 0
    }
    finally {
        $ErrorActionPreference = $previousErrorActionPreference
        $resolvedProbeRoot = [System.IO.Path]::GetFullPath($probeRoot)
        $resolvedSystemTemp = [System.IO.Path]::GetFullPath([System.IO.Path]::GetTempPath()).TrimEnd([System.IO.Path]::DirectorySeparatorChar)
        $expectedPrefix = $resolvedSystemTemp + [System.IO.Path]::DirectorySeparatorChar + 'new-api-buildx-network-probe-'
        if ($resolvedProbeRoot.StartsWith($expectedPrefix, [System.StringComparison]::OrdinalIgnoreCase)) {
            Remove-Item -LiteralPath $resolvedProbeRoot -Recurse -Force
        }
    }
}

function Get-PublicIPv4Address {
    param(
        [Parameter(Mandatory = $true)]
        [string]$HostName
    )

    $headers = @{ Accept = 'application/dns-json' }
    $response = Invoke-RestMethod -Method Get -Uri "https://cloudflare-dns.com/dns-query?name=$HostName&type=A" -Headers $headers -TimeoutSec 30
    foreach ($answer in @($response.Answer)) {
        if ($answer.type -ne 1) {
            continue
        }

        $address = $null
        if ([System.Net.IPAddress]::TryParse([string]$answer.data, [ref]$address) -and $address.AddressFamily -eq [System.Net.Sockets.AddressFamily]::InterNetwork) {
            return $address.IPAddressToString
        }
    }

    throw (Get-Message -Key 'PublicDnsLookupFailed' -Values @($HostName))
}

function Get-OfficialVersion {
    $officialVersion = Get-NativeOutput -Command 'git' -Arguments @('describe', '--tags', '--abbrev=0', '--match', 'v*', 'upstream/main')
    if ($officialVersion -notmatch '^v[0-9A-Za-z][0-9A-Za-z._-]*$') {
        throw (Get-Message -Key 'OfficialVersionInvalid' -Values @($officialVersion))
    }

    return $officialVersion
}

function Get-NextPersonalVersion {
    param(
        [Parameter(Mandatory = $true)]
        [string]$ImagePath,

        [Parameter(Mandatory = $true)]
        [string]$OfficialVersion
    )

    $prefix = "personal-$OfficialVersion-r"
    $escapedPrefix = [Uri]::EscapeDataString($prefix)
    $nextUri = "https://hub.docker.com/v2/repositories/$ImagePath/tags?page_size=100&name=$escapedPrefix"
    $highestRevision = 0
    $versionPattern = '^' + [Regex]::Escape($prefix) + '(\d+)$'

    while ($nextUri) {
        $response = Invoke-DockerHubRestMethod -Uri $nextUri
        foreach ($tag in @($response.results)) {
            if ($tag.name -match $versionPattern) {
                $revision = 0
                if (-not [int]::TryParse($Matches[1], [ref]$revision)) {
                    continue
                }
                if ($revision -gt $highestRevision) {
                    $highestRevision = $revision
                }
            }
        }
        $nextUri = $response.next
    }

    return "$prefix$($highestRevision + 1)"
}

function Get-DockerHubTagInfo {
    param(
        [Parameter(Mandatory = $true)]
        [string]$ImagePath,

        [Parameter(Mandatory = $true)]
        [string]$Tag
    )

    $escapedTag = [Uri]::EscapeDataString($Tag)
    return Invoke-DockerHubRestMethod -Uri "https://hub.docker.com/v2/repositories/$ImagePath/tags/$escapedTag"
}

function Get-DockerHubTagInfoWithRetry {
    param(
        [Parameter(Mandatory = $true)]
        [string]$ImagePath,

        [Parameter(Mandatory = $true)]
        [string]$Tag,

        [Parameter()]
        [int]$Attempts = 10
    )

    $lastError = $null
    for ($attempt = 1; $attempt -le $Attempts; $attempt++) {
        try {
            return Get-DockerHubTagInfo -ImagePath $ImagePath -Tag $Tag
        }
        catch {
            $lastError = $_.Exception.Message
            if ($attempt -lt $Attempts) {
                Start-Sleep -Seconds 3
            }
        }
    }

    throw (Get-Message -Key 'DockerHubTagQueryFailed' -Values @($Tag, $lastError))
}

function Invoke-ImageSmokeTest {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Image,

        [Parameter(Mandatory = $true)]
        [string]$ContainerName
    )

    $containerId = $null
    try {
        $containerId = Get-NativeOutput -Command 'docker' -Arguments @(
            'run', '--detach', '--platform', 'linux/amd64',
            '--name', $ContainerName,
            '--publish', '127.0.0.1::3000',
            $Image
        )
        if (-not $containerId) {
            throw (Get-Message -Key 'SmokeContainerStartFailed')
        }

        $portOutput = Get-NativeOutput -Command 'docker' -Arguments @('port', $ContainerName, '3000/tcp')
        if ($portOutput -notmatch ':(\d+)\s*$') {
            throw (Get-Message -Key 'SmokePortParseFailed' -Values @($portOutput))
        }
        $statusUri = "http://127.0.0.1:$($Matches[1])/api/status"

        $lastSmokeError = $null
        for ($attempt = 1; $attempt -le 60; $attempt++) {
            try {
                $response = Invoke-WebRequest -UseBasicParsing -Uri $statusUri -TimeoutSec 5
                if ($response.StatusCode -eq 200) {
                    Write-Host (Get-Message -Key 'SmokePassed' -Values @($statusUri))
                    return
                }
            }
            catch {
                $lastSmokeError = $_.Exception.Message
            }
            Start-Sleep -Seconds 2
        }

        Write-Host (Get-Message -Key 'SmokeLogs')
        & docker logs --tail 200 $ContainerName
        throw (Get-Message -Key 'SmokeTimeout' -Values @($statusUri, $lastSmokeError))
    }
    finally {
        if ($containerId) {
            $previousErrorActionPreference = $ErrorActionPreference
            $ErrorActionPreference = 'SilentlyContinue'
            try {
                & docker rm --force $ContainerName *> $null
            }
            finally {
                $ErrorActionPreference = $previousErrorActionPreference
            }
        }
    }
}

$repositoryRoot = Split-Path -Parent $PSScriptRoot
$imagePath = "ahyi/$Repository"
$latestImage = "${imagePath}:personal-latest"
$localPlatform = 'linux/amd64'
$publishPlatforms = 'linux/amd64,linux/arm64'
$directBuilderName = 'new-api-publisher'
$proxyBuilderName = 'new-api-dockerhub-publisher-proxy'
$proxyContainerName = 'new-api-dockerhub-release-proxy'
$proxyNetworkName = 'new-api-dockerhub-release-net'
$proxyNetworkAlias = 'release-proxy'
$proxyImage = 'vimagick/tinyproxy@sha256:72b441b95ee1e641af948f68f09492f9f795ead72b73954414e339168c98ad8c'
$builderName = $directBuilderName
$temporaryRoot = $null
$localImage = $null
$remoteImageLoaded = $false
$proxyStarted = $false
$originalHttpProxy = $env:HTTP_PROXY
$originalHttpsProxy = $env:HTTPS_PROXY

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

    Invoke-NativeCommand -Command 'git' -Arguments @('fetch', 'origin', 'personal', '--prune')
    Invoke-NativeCommand -Command 'git' -Arguments @('fetch', 'upstream', '--prune', '--tags')

    $trackingCounts = Get-NativeOutput -Command 'git' -Arguments @('rev-list', '--left-right', '--count', 'origin/personal...HEAD')
    $countParts = $trackingCounts -split '\s+'
    if ($countParts.Count -ne 2 -or $countParts[0] -ne '0' -or $countParts[1] -ne '0') {
        throw (Get-Message -Key 'TrackingMismatch' -Values @('origin/personal', $countParts[0], $countParts[1]))
    }

    $upstreamBehindCount = Get-NativeOutput -Command 'git' -Arguments @('rev-list', '--count', 'HEAD..upstream/main')
    if ($upstreamBehindCount -ne '0') {
        throw (Get-Message -Key 'UpstreamNotMerged' -Values @($upstreamBehindCount))
    }

    Invoke-NativeCommand -Command 'docker' -Arguments @('version')
    Invoke-NativeCommand -Command 'docker' -Arguments @('buildx', 'version')

    $dockerInfo = Get-NativeOutput -Command 'docker' -Arguments @('info', '--format', '{{json .}}') | ConvertFrom-Json
    $daemonHttpProxy = [string]$dockerInfo.HttpProxy
    $daemonHttpsProxy = [string]$dockerInfo.HttpsProxy
    $daemonNoProxy = [string]$dockerInfo.NoProxy
    if ($daemonHttpProxy -and $daemonHttpProxy -notmatch '^[a-z][a-z0-9+.-]*://') {
        $daemonHttpProxy = "http://$daemonHttpProxy"
    }
    if ($daemonHttpsProxy -and $daemonHttpsProxy -notmatch '^[a-z][a-z0-9+.-]*://') {
        $daemonHttpsProxy = "http://$daemonHttpsProxy"
    }

    if (-not (Test-DockerHubCredential)) {
        throw (Get-Message -Key 'DockerHubCredentialMissing')
    }

    try {
        $repositoryInfo = Invoke-DockerHubRestMethod -Uri "https://hub.docker.com/v2/repositories/$imagePath/"
    }
    catch {
        throw (Get-Message -Key 'PublicRepositoryUnavailable' -Values @($imagePath, $_.Exception.Message))
    }

    if ($repositoryInfo.is_private) {
        throw (Get-Message -Key 'RepositoryIsPrivate' -Values @($imagePath))
    }

    $useDockerHubProxy = -not (Test-DockerHubNetwork -ImagePath $imagePath)
    if (-not $useDockerHubProxy) {
        $existingBuilder = @(Get-BuildxBuilderNames)
        if ($existingBuilder -contains $builderName -and ($daemonHttpProxy -or $daemonHttpsProxy -or $daemonNoProxy)) {
            $builderContainerName = "buildx_buildkit_$($builderName)0"
            $builderEnvironment = Get-NativeOutput -Command 'docker' -Arguments @('inspect', '--format', '{{json .Config.Env}}', $builderContainerName)
            $builderProxyMismatch =
                ($daemonHttpProxy -and $builderEnvironment -notmatch [Regex]::Escape("HTTP_PROXY=$daemonHttpProxy")) -or
                ($daemonHttpsProxy -and $builderEnvironment -notmatch [Regex]::Escape("HTTPS_PROXY=$daemonHttpsProxy")) -or
                ($daemonNoProxy -and $builderEnvironment -notmatch [Regex]::Escape("NO_PROXY=$daemonNoProxy"))
            if ($builderProxyMismatch) {
                Write-Host '检测到Docker守护进程代理与Buildx构建器不一致，正在重建构建器...'
                Invoke-NativeCommand -Command 'docker' -Arguments @('buildx', 'rm', $builderName)
                $existingBuilder = @()
            }
        }
        if ($existingBuilder -notcontains $builderName) {
            Write-Host (Get-Message -Key 'CreatingBuilder' -Values @($builderName))
            $builderArguments = @(
                'buildx', 'create', '--name', $builderName,
                '--driver', 'docker-container'
            )
            if ($daemonHttpProxy) {
                $builderArguments += @('--driver-opt', "env.HTTP_PROXY=$daemonHttpProxy")
            }
            if ($daemonHttpsProxy) {
                $builderArguments += @('--driver-opt', "env.HTTPS_PROXY=$daemonHttpsProxy")
            }
            if ($daemonNoProxy) {
                $builderArguments += @('--driver-opt', "env.NO_PROXY=$daemonNoProxy")
            }
            $builderArguments += '--bootstrap'
            Invoke-NativeCommand -Command 'docker' -Arguments $builderArguments
        }

        $networkProbeImage = Get-Content -LiteralPath (Join-Path $repositoryRoot 'Dockerfile') |
            Where-Object { $_ -match '^FROM\s+' } |
            ForEach-Object { ($_ -split '\s+')[1] } |
            Select-Object -First 1
        if (-not (Test-BuildxDockerHubNetwork -BuilderName $builderName -Image $networkProbeImage -Platforms $publishPlatforms)) {
            $useDockerHubProxy = $true
        }
    }

    if ($useDockerHubProxy) {
        Write-Host (Get-Message -Key 'DockerHubNetworkFallback')
        $authAddress = Get-PublicIPv4Address -HostName 'auth.docker.io'
        $registryAddress = Get-PublicIPv4Address -HostName 'registry-1.docker.io'
        $cdnAddress = Get-PublicIPv4Address -HostName 'production.cloudflare.docker.com'

        $existingProxy = Get-NativeOutput -Command 'docker' -Arguments @('ps', '--all', '--quiet', '--filter', "name=^/$proxyContainerName$")
        if ($existingProxy) {
            $existingLabelsJson = Get-NativeOutput -Command 'docker' -Arguments @('inspect', '--format', '{{json .Config.Labels}}', $proxyContainerName)
            $existingLabels = $existingLabelsJson | ConvertFrom-Json
            $existingLabel = $existingLabels.'com.ahyi.new-api.release-proxy'
            if ($existingLabel -ne 'true') {
                throw (Get-Message -Key 'ProxyContainerConflict' -Values @($proxyContainerName))
            }
            Invoke-NativeCommand -Command 'docker' -Arguments @('rm', '--force', $proxyContainerName)
        }

        $existingNetwork = Get-NativeOutput -Command 'docker' -Arguments @('network', 'ls', '--quiet', '--filter', "name=^$proxyNetworkName$")
        if (-not $existingNetwork) {
            Invoke-NativeCommand -Command 'docker' -Arguments @('network', 'create', '--label', 'com.ahyi.new-api.release-proxy=true', $proxyNetworkName)
        }
        else {
            $networkLabelsJson = Get-NativeOutput -Command 'docker' -Arguments @('network', 'inspect', '--format', '{{json .Labels}}', $proxyNetworkName)
            $networkLabels = $networkLabelsJson | ConvertFrom-Json
            $networkLabel = $networkLabels.'com.ahyi.new-api.release-proxy'
            if ($networkLabel -ne 'true') {
                throw (Get-Message -Key 'ProxyNetworkConflict' -Values @($proxyNetworkName))
            }
        }

        Invoke-NativeCommandWithRetry -Command 'docker' -Arguments @('pull', $proxyImage)
        Invoke-NativeCommand -Command 'docker' -Arguments @(
            'run', '--detach', '--name', $proxyContainerName,
            '--label', 'com.ahyi.new-api.release-proxy=true',
            '--network', $proxyNetworkName,
            '--network-alias', $proxyNetworkAlias,
            '--publish', '127.0.0.1::8888',
            '--add-host', "auth.docker.io:$authAddress",
            '--add-host', "registry-1.docker.io:$registryAddress",
            '--add-host', "production.cloudflare.docker.com:$cdnAddress",
            $proxyImage
        )
        $proxyStarted = $true

        # Docker Desktop 可能在容器启动后短暂返回空端口映射，等待映射稳定后再配置宿主机代理。
        $proxyPortOutput = ''
        $proxyHostPort = ''
        for ($attempt = 1; $attempt -le 10; $attempt++) {
            $proxyPortOutput = Get-NativeOutput -Command 'docker' -Arguments @('port', $proxyContainerName, '8888/tcp')
            if ($proxyPortOutput -match ':(\d+)\s*$') {
                $proxyHostPort = $Matches[1]
                break
            }
            if ($attempt -lt 10) {
                Start-Sleep -Milliseconds 500
            }
        }
        if (-not $proxyHostPort) {
            throw (Get-Message -Key 'ProxyPortParseFailed' -Values @($proxyPortOutput))
        }
        $script:HostProxyUri = "http://127.0.0.1:$proxyHostPort"
        $env:HTTP_PROXY = $script:HostProxyUri
        $env:HTTPS_PROXY = $script:HostProxyUri

        if (-not (Test-DockerHubNetwork -ImagePath $imagePath)) {
            throw (Get-Message -Key 'ProxyConnectivityFailed')
        }

        $builderName = $proxyBuilderName
        $existingBuilder = @(Get-BuildxBuilderNames)
        if ($existingBuilder -contains $builderName) {
            Invoke-NativeCommand -Command 'docker' -Arguments @('buildx', 'inspect', $builderName, '--bootstrap')
            $builderContainerName = "buildx_buildkit_$($builderName)0"
            $builderEnvironment = Get-NativeOutput -Command 'docker' -Arguments @('inspect', '--format', '{{json .Config.Env}}', $builderContainerName)
            $builderNetworks = Get-NativeOutput -Command 'docker' -Arguments @('inspect', '--format', '{{json .NetworkSettings.Networks}}', $builderContainerName)
            if ($builderEnvironment -notmatch [Regex]::Escape("HTTPS_PROXY=http://${proxyNetworkAlias}:8888") -or $builderNetworks -notmatch [Regex]::Escape($proxyNetworkName)) {
                Write-Host (Get-Message -Key 'RecreatingProxyBuilder' -Values @($builderName))
                Invoke-NativeCommand -Command 'docker' -Arguments @('buildx', 'rm', $builderName)
                $existingBuilder = @()
            }
        }

        if ($existingBuilder -notcontains $builderName) {
            Write-Host (Get-Message -Key 'CreatingBuilder' -Values @($builderName))
            Invoke-NativeCommand -Command 'docker' -Arguments @(
                'buildx', 'create', '--name', $builderName,
                '--driver', 'docker-container',
                '--driver-opt', "network=$proxyNetworkName",
                '--driver-opt', "env.HTTP_PROXY=http://${proxyNetworkAlias}:8888",
                '--driver-opt', "env.HTTPS_PROXY=http://${proxyNetworkAlias}:8888",
                '--bootstrap'
            )
        }
    }

    Write-Host (Get-Message -Key 'PreparingBuilder' -Values @($builderName))
    $builderDetails = Get-NativeOutput -Command 'docker' -Arguments @('buildx', 'inspect', $builderName, '--bootstrap')
    if ($builderDetails -notmatch '(?m)^Driver:\s+docker-container\s*$') {
        throw (Get-Message -Key 'InvalidBuilderDriver' -Values @($builderName))
    }

    $officialVersion = Get-OfficialVersion
    if (-not $Version) {
        $Version = Get-NextPersonalVersion -ImagePath $imagePath -OfficialVersion $officialVersion
        Write-Host (Get-Message -Key 'AutoVersionSelected' -Values @($Version))
    }
    $versionImage = "${imagePath}:$Version"

    if ((Test-PublicDockerManifest -ImagePath $imagePath -Tag $Version) -and -not $ForceVersionOverwrite) {
        throw (Get-Message -Key 'VersionAlreadyExists' -Values @($versionImage))
    }

    $commitSha = Get-NativeOutput -Command 'git' -Arguments @('rev-parse', 'HEAD')
    $shortSha = Get-NativeOutput -Command 'git' -Arguments @('rev-parse', '--short=12', 'HEAD')
    $upstreamRevision = Get-NativeOutput -Command 'git' -Arguments @('rev-parse', 'upstream/main')
    $originUrl = Get-NativeOutput -Command 'git' -Arguments @('config', '--get', 'remote.origin.url')
    if ($originUrl -match '^git@github\.com:(.+)$') {
        $sourceUrl = "https://github.com/$($Matches[1])"
    }
    else {
        $sourceUrl = $originUrl
    }
    $sourceUrl = $sourceUrl -replace '\.git$', ''

    Write-Host (Get-Message -Key 'OfficialBaseline' -Values @($officialVersion, $upstreamRevision))
    Write-Host (Get-Message -Key 'SourceCommit' -Values @($commitSha))
    Write-Host (Get-Message -Key 'TargetImage' -Values @($versionImage))
    if (-not $SkipLatest) {
        Write-Host (Get-Message -Key 'RollingTag' -Values @($latestImage))
    }
    Write-Host (Get-Message -Key 'TargetPlatform' -Values @($publishPlatforms))

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
    $verificationDockerfile = Join-Path $buildContext 'Dockerfile.verify'
    $dockerfileContent = [System.IO.File]::ReadAllText((Join-Path $buildContext 'Dockerfile'))
    $verificationStage = "`nFROM builder2 AS test-runner`nRUN go test ./...`n"
    [System.IO.File]::WriteAllText($verificationDockerfile, $dockerfileContent + $verificationStage, (New-Object System.Text.UTF8Encoding($false)))

    $localImage = "new-api-local-verify:$shortSha"
    $commonBuildArguments = @(
        'buildx', 'build',
        '--builder', $builderName,
        '--label', "org.opencontainers.image.source=$sourceUrl",
        '--label', "org.opencontainers.image.revision=$commitSha",
        '--label', "org.opencontainers.image.version=$Version",
        '--label', "io.new-api.upstream-revision=$upstreamRevision",
        '--label', 'org.opencontainers.image.licenses=AGPL-3.0-only'
    )

    $baseImages = @(
        Get-Content -LiteralPath (Join-Path $buildContext 'Dockerfile') |
            Where-Object { $_ -match '^FROM\s+' } |
            ForEach-Object { ($_ -split '\s+')[1] } |
            Sort-Object -Unique
    )
    foreach ($baseImage in $baseImages) {
        # 使用实际发布构建器预热多架构缓存，避免Docker宿主镜像存储无法用同一清单摘要同时保存不同平台。
        Write-Host (Get-Message -Key 'PullingBaseImage' -Values @($baseImage, $publishPlatforms))
        Push-Location $buildContext
        try {
            Invoke-NativeCommandWithRetry -Command 'docker' -Arguments @(
                'buildx', 'build',
                '--builder', $builderName,
                '--file', 'scripts/Dockerfile.base-image-probe',
                '--build-arg', "BASE_IMAGE=$baseImage",
                '--platform', $publishPlatforms,
                '--output', 'type=cacheonly',
                '.'
            )
        }
        finally {
            Pop-Location
        }
    }

    Write-Host (Get-Message -Key 'RunningAutomatedTests')
    Invoke-NativeCommandWithRetry -Command 'docker' -Arguments ($commonBuildArguments + @(
        '--file', $verificationDockerfile,
        '--target', 'test-runner',
        '--platform', $localPlatform,
        '--output', 'type=cacheonly',
        $buildContext
    ))

    Write-Host (Get-Message -Key 'BuildingLocalImage')
    Invoke-NativeCommandWithRetry -Command 'docker' -Arguments ($commonBuildArguments + @(
        '--platform', $localPlatform,
        '--tag', $localImage,
        '--load',
        $buildContext
    ))

    Write-Host (Get-Message -Key 'RunningSmokeTest')
    Invoke-ImageSmokeTest -Image $localImage -ContainerName "new-api-smoke-local-$($shortSha.Substring(0, 8))"

    Write-Host (Get-Message -Key 'PublishingImage')
    $publishArguments = $commonBuildArguments + @(
        '--platform', $publishPlatforms,
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

    $versionInfo = Get-DockerHubTagInfoWithRetry -ImagePath $imagePath -Tag $Version
    $publishedPlatforms = @(
        $versionInfo.images |
            Where-Object { $_.architecture -ne 'unknown' } |
            ForEach-Object { "$($_.os)/$($_.architecture)" }
    )
    foreach ($requiredPlatform in @('linux/amd64', 'linux/arm64')) {
        if ($publishedPlatforms -notcontains $requiredPlatform) {
            throw (Get-Message -Key 'PublishedPlatformMissing' -Values @($requiredPlatform, ($publishedPlatforms -join ', ')))
        }
    }

    if (-not $SkipLatest) {
        $latestInfo = Get-DockerHubTagInfoWithRetry -ImagePath $imagePath -Tag 'personal-latest'
        if ($latestInfo.digest -ne $versionInfo.digest) {
            throw (Get-Message -Key 'RollingDigestMismatch' -Values @($versionInfo.digest, $latestInfo.digest))
        }
    }

    Write-Host (Get-Message -Key 'RunningRemoteSmokeTest')
    Invoke-NativeCommandWithRetry -Command 'docker' -Arguments @('pull', '--platform', $localPlatform, $versionImage)
    $remoteImageLoaded = $true
    Invoke-ImageSmokeTest -Image $versionImage -ContainerName "new-api-smoke-remote-$($shortSha.Substring(0, 8))"

    $remoteLabelsJson = Get-NativeOutput -Command 'docker' -Arguments @('image', 'inspect', '--format', '{{json .Config.Labels}}', $versionImage)
    $remoteLabels = $remoteLabelsJson | ConvertFrom-Json
    if ($remoteLabels.'org.opencontainers.image.revision' -ne $commitSha -or $remoteLabels.'io.new-api.upstream-revision' -ne $upstreamRevision) {
        throw (Get-Message -Key 'PublishedLabelMismatch')
    }

    Write-Host ''
    Write-Host (Get-Message -Key 'PublishCompleted')
    Write-Host (Get-Message -Key 'PublicImage' -Values @("docker.io/$versionImage"))
    Write-Host (Get-Message -Key 'ManifestDigest' -Values @($versionInfo.digest))
    Write-Host (Get-Message -Key 'SourceCommit' -Values @($commitSha))
    Write-Host (Get-Message -Key 'TargetPlatform' -Values @(($publishedPlatforms -join ', ')))
}
finally {
    $previousErrorActionPreference = $ErrorActionPreference
    $ErrorActionPreference = 'SilentlyContinue'
    try {
        if ($localImage) {
            & docker image inspect $localImage *> $null
            if ($LASTEXITCODE -eq 0) {
                & docker image rm --force $localImage *> $null
            }
        }
        if ($remoteImageLoaded -and $Version) {
            $publishedImage = "${imagePath}:$Version"
            & docker image inspect $publishedImage *> $null
            if ($LASTEXITCODE -eq 0) {
                & docker image rm $publishedImage *> $null
            }
        }
        if ($temporaryRoot -and (Test-Path -LiteralPath $temporaryRoot)) {
            $resolvedTemporaryRoot = [System.IO.Path]::GetFullPath($temporaryRoot)
            $resolvedSystemTemp = [System.IO.Path]::GetFullPath([System.IO.Path]::GetTempPath()).TrimEnd([System.IO.Path]::DirectorySeparatorChar)
            $expectedPrefix = $resolvedSystemTemp + [System.IO.Path]::DirectorySeparatorChar + 'new-api-docker-'
            if ($resolvedTemporaryRoot.StartsWith($expectedPrefix, [System.StringComparison]::OrdinalIgnoreCase)) {
                Remove-Item -LiteralPath $resolvedTemporaryRoot -Recurse -Force
            }
        }
        if ($proxyStarted) {
            & docker rm --force $proxyContainerName *> $null
        }
    }
    finally {
        if ($null -eq $originalHttpProxy) {
            Remove-Item Env:HTTP_PROXY -ErrorAction SilentlyContinue
        }
        else {
            $env:HTTP_PROXY = $originalHttpProxy
        }
        if ($null -eq $originalHttpsProxy) {
            Remove-Item Env:HTTPS_PROXY -ErrorAction SilentlyContinue
        }
        else {
            $env:HTTPS_PROXY = $originalHttpsProxy
        }
        $script:HostProxyUri = $null
        $ErrorActionPreference = $previousErrorActionPreference
        Pop-Location
    }
}
