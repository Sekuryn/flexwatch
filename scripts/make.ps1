<#
.SYNOPSIS
    Équivalent PowerShell du Makefile, pour développer sous Windows sans make.

.EXAMPLE
    .\scripts\make.ps1 check
    .\scripts\make.ps1 once
    .\scripts\make.ps1 build-linux-arm64
#>
[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [ValidateSet('help', 'fmt', 'fmt-check', 'vet', 'lint', 'staticcheck', 'vuln',
        'test', 'cover', 'check', 'build', 'build-linux-arm64', 'build-postgres',
        'run', 'once', 'probe', 'e2e', 'docker', 'tools', 'clean')]
    [string]$Task = 'help'
)

$ErrorActionPreference = 'Stop'
Set-Location (Join-Path $PSScriptRoot '..')

$Binary = 'flexwatch'
$Pkg = './cmd/flexwatch'

$Version = 'dev'
try {
    $described = git describe --tags --always --dirty 2>$null
    if ($LASTEXITCODE -eq 0 -and $described) { $Version = $described }
} catch {
    # Pas de dépôt git : on garde "dev".
}
$LdFlags = "-s -w -X main.version=$Version"

function Invoke-Step {
    param([string]$Label, [scriptblock]$Body)
    Write-Host "==> $Label" -ForegroundColor Cyan
    & $Body
    if ($LASTEXITCODE -ne 0) { throw "$Label a echoue (code $LASTEXITCODE)" }
}

function Task-Fmt { Invoke-Step 'gofmt -s -w .' { gofmt -s -w . } }

function Task-FmtCheck {
    Write-Host '==> gofmt -s -l .' -ForegroundColor Cyan
    $unformatted = gofmt -s -l .
    if ($unformatted) {
        Write-Host 'Fichiers non formates :' -ForegroundColor Red
        $unformatted | ForEach-Object { Write-Host "  $_" }
        throw 'Corriger avec : .\scripts\make.ps1 fmt'
    }
}

function Task-Vet { Invoke-Step 'go vet ./...' { go vet ./... } }
function Task-Lint { Invoke-Step 'golangci-lint run ./...' { golangci-lint run ./... } }
function Task-Staticcheck { Invoke-Step 'staticcheck ./...' { staticcheck ./... } }
function Task-Vuln { Invoke-Step 'govulncheck ./...' { govulncheck ./... } }
function Task-Test { Invoke-Step 'go test -race ./...' { go test -race -count=1 ./... } }

function Task-Cover {
    Invoke-Step 'go test -cover' { go test -race -count=1 -coverprofile=coverage.out -covermode=atomic ./cmd/... ./internal/... }
    go tool cover -func=coverage.out | Select-Object -Last 1
}

function Task-Build {
    $env:CGO_ENABLED = '0'
    Invoke-Step "build $Binary" { go build -trimpath -ldflags="$LdFlags" -o "bin/$Binary.exe" $Pkg }
}

function Task-BuildLinuxArm64 {
    # Cible EC2 t4g (Graviton). Go cross-compile sans toolchain externe.
    $env:CGO_ENABLED = '0'; $env:GOOS = 'linux'; $env:GOARCH = 'arm64'
    try {
        Invoke-Step 'build linux/arm64' { go build -trimpath -ldflags="$LdFlags" -o "bin/$Binary-linux-arm64" $Pkg }
    } finally {
        Remove-Item Env:GOOS, Env:GOARCH -ErrorAction SilentlyContinue
    }
}

function Task-BuildPostgres {
    $env:CGO_ENABLED = '0'
    Invoke-Step 'go get pgx' { go get github.com/jackc/pgx/v5@v5.7.2 }
    Invoke-Step 'go mod tidy' { go mod tidy }
    Invoke-Step 'build -tags postgres' { go build -trimpath -tags postgres -ldflags="$LdFlags" -o "bin/$Binary-postgres.exe" $Pkg }
}

function Task-Run { Invoke-Step 'go run' { go run $Pkg } }
function Task-Once { Invoke-Step 'go run -once' { go run $Pkg -once } }

function Task-Probe {
    # Appel brut de l'API, pour verifier le schema a la main.
    $url = 'https://restapifrontoffice.reservauto.net/api/v2/Vehicle/FreeFloatingAvailability' +
    '?CityId=59&MaxLatitude=45.55&MinLatitude=45.45&MaxLongitude=-73.50&MinLongitude=-73.65'
    $resp = Invoke-RestMethod -Uri $url -Headers @{ Accept = 'application/json' } -UserAgent 'flexwatch-probe/1.0'
    $resp | ConvertTo-Json -Depth 4 | Select-Object -First 60
}

function Task-E2E {
    # Le banc d'essai est en bash et la cible est Linux : on passe par WSL,
    # ce qui teste au passage le binaire dans l'environnement de production.
    $wslRepo = (wsl wslpath -u "$((Get-Location).Path)").Trim()
    Write-Host '==> test de bout en bout dans WSL (~60 s)' -ForegroundColor Cyan
    wsl -e bash -lc "cd '$wslRepo' && bash test/e2e/run.sh"
    if ($LASTEXITCODE -ne 0) { throw "e2e a echoue (code $LASTEXITCODE)" }
}

function Task-Docker {
    Invoke-Step 'docker build' {
        docker build --build-arg VERSION=$Version --build-arg TARGETARCH=arm64 -t "flexwatch:$Version" .
    }
}

function Task-Tools {
    Invoke-Step 'install golangci-lint' { go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.0 }
    Invoke-Step 'install staticcheck' { go install honnef.co/go/tools/cmd/staticcheck@2026.2.1 }
    Invoke-Step 'install govulncheck' { go install golang.org/x/vuln/cmd/govulncheck@latest }
}

function Task-Clean {
    Remove-Item -Recurse -Force bin, coverage.out, sbom.json -ErrorAction SilentlyContinue
    Write-Host 'Artefacts supprimes.'
}

function Task-Help {
    Write-Host 'Cibles disponibles :' -ForegroundColor Cyan
    @(
        'fmt                formate le code',
        'fmt-check          echoue si du code n''est pas formate',
        'vet / lint         analyse statique',
        'staticcheck        staticcheck seul',
        'vuln               govulncheck',
        'test / cover       tests unitaires (race detector)',
        'check              fmt-check + vet + lint + staticcheck + vuln + test',
        'build              binaire windows local',
        'build-linux-arm64  binaire pour EC2 t4g',
        'build-postgres     binaire avec persistance Postgres',
        'run / once         lance le bot / un seul poll',
        'probe              appelle l''API brute',
        'e2e                test de bout en bout local (WSL, ~60 s)',
        'docker             construit l''image arm64',
        'tools              installe les outils de lint',
        'clean              supprime les artefacts'
    ) | ForEach-Object { Write-Host "  $_" }
}

switch ($Task) {
    'fmt' { Task-Fmt }
    'fmt-check' { Task-FmtCheck }
    'vet' { Task-Vet }
    'lint' { Task-Lint }
    'staticcheck' { Task-Staticcheck }
    'vuln' { Task-Vuln }
    'test' { Task-Test }
    'cover' { Task-Cover }
    'check' { Task-FmtCheck; Task-Vet; Task-Lint; Task-Staticcheck; Task-Vuln; Task-Test }
    'build' { Task-Build }
    'build-linux-arm64' { Task-BuildLinuxArm64 }
    'build-postgres' { Task-BuildPostgres }
    'run' { Task-Run }
    'once' { Task-Once }
    'probe' { Task-Probe }
    'e2e' { Task-E2E }
    'docker' { Task-Docker }
    'tools' { Task-Tools }
    'clean' { Task-Clean }
    default { Task-Help }
}
