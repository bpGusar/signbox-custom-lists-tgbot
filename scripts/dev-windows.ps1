#Requires -Version 5.1
<#
.SYNOPSIS
    Проверяет окружение для локальной разработки lst-signbox-lists-tgbot на Windows.

.DESCRIPTION
    - Проверяет Go, структуру проекта, сеть, права на запись, зависимости и сборку.
    - При ошибках выводит понятные шаги исправления.
    - При успехе создаёт testdata, настраивает переменные окружения и предлагает запустить бота.

.PARAMETER NoWatch
    Не следить за изменениями в коде: запустить бота один раз в текущем окне.

.EXAMPLE
    .\scripts\dev-windows.ps1

.EXAMPLE
    .\scripts\dev-windows.ps1 -NoWatch
#>

[CmdletBinding()]
param(
    [switch]$NoWatch
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$ProjectRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$TestDataDir = Join-Path $ProjectRoot 'testdata'
$EnvFile = Join-Path $ProjectRoot '.env.local'
$BinaryName = 'lst-signbox-lists-tgbot.exe'
$BinaryPath = Join-Path $ProjectRoot $BinaryName
$MinGoVersion = [version]'1.22.0'

$script:ChecksFailed = 0
$script:ChecksWarned = 0

function Write-Title([string]$Text) {
    Write-Host ''
    Write-Host "=== $Text ===" -ForegroundColor Cyan
}

function Write-Ok([string]$Text) {
    Write-Host "  [OK]   $Text" -ForegroundColor Green
}

function Write-Fail([string]$Text) {
    Write-Host "  [FAIL] $Text" -ForegroundColor Red
    $script:ChecksFailed++
}

function Write-Warn([string]$Text) {
    Write-Host "  [WARN] $Text" -ForegroundColor Yellow
    $script:ChecksWarned++
}

function Invoke-Native {
    param([scriptblock]$Script)

    # При $ErrorActionPreference = 'Stop' любой вывод нативной команды в stderr
    # превращается в терминирующую ошибку, поэтому на время вызова снимаем Stop.
    $previous = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        $out = @(& $Script 2>&1 | ForEach-Object { $_.ToString() })
        return [pscustomobject]@{
            ExitCode = $LASTEXITCODE
            Output   = $out
        }
    }
    finally {
        $ErrorActionPreference = $previous
    }
}

function Write-Fix([string[]]$Steps) {
    Write-Host '         Как исправить:' -ForegroundColor DarkGray
    foreach ($step in $Steps) {
        Write-Host "           - $step" -ForegroundColor DarkGray
    }
}

function Test-GoVersion {
    Write-Title 'Go'

    $goCmd = Get-Command go -ErrorAction SilentlyContinue
    if (-not $goCmd) {
        Write-Fail 'Go не установлен или не в PATH.'
        Write-Fix @(
            'Скачайте установщик: https://go.dev/dl/'
            'После установки закройте и откройте терминал заново.'
            'Проверьте: go version'
        )
        return $false
    }

    $version = Invoke-Native { go version }
    $versionLine = ($version.Output -join ' ')
    if ($version.ExitCode -ne 0) {
        Write-Fail "go version завершился с ошибкой: $versionLine"
        return $false
    }

    if ($versionLine -notmatch 'go(\d+\.\d+(?:\.\d+)?)') {
        Write-Fail "Не удалось разобрать версию Go: $versionLine"
        return $false
    }

    $installed = [version]$Matches[1]
    if ($installed -lt $MinGoVersion) {
        Write-Fail "Установлен Go $installed, нужен >= $MinGoVersion."
        Write-Fix @(
            'Обновите Go с https://go.dev/dl/'
            'Перезапустите терминал после установки.'
        )
        return $false
    }

    Write-Ok "Go $installed ($($goCmd.Source))"
    return $true
}

function Test-ProjectLayout {
    Write-Title 'Структура проекта'

    $required = @(
        (Join-Path $ProjectRoot 'go.mod'),
        (Join-Path $ProjectRoot 'go.sum'),
        (Join-Path $ProjectRoot 'cmd\lst-signbox-lists-tgbot\main.go')
    )

    $ok = $true
    foreach ($path in $required) {
        if (-not (Test-Path -LiteralPath $path)) {
            Write-Fail "Не найден: $path"
            $ok = $false
        }
    }

    if (-not $ok) {
        Write-Fix @(
            'Запускайте скрипт из клонированного репозитория.'
            "Текущий корень проекта: $ProjectRoot"
        )
        return $false
    }

    Write-Ok "Корень проекта: $ProjectRoot"
    return $true
}

function Test-Network {
    Write-Title 'Сеть (Telegram API)'

    try {
        $result = Test-NetConnection -ComputerName 'api.telegram.org' -Port 443 -WarningAction SilentlyContinue
        if ($result.TcpTestSucceeded) {
            Write-Ok 'Доступ к api.telegram.org:443 есть.'
            return $true
        }

        Write-Fail 'Нет TCP-доступа к api.telegram.org:443.'
        Write-Fix @(
            'Проверьте интернет-соединение и VPN/прокси.'
            'Убедитесь, что Telegram не заблокирован на уровне сети или файрвола.'
        )
        return $false
    }
    catch {
        Write-Warn "Не удалось проверить сеть: $($_.Exception.Message)"
        Write-Fix @(
            'Проверьте доступ к https://api.telegram.org в браузере.'
        )
        return $true
    }
}

function Test-GoModules {
    Write-Title 'Зависимости Go'

    Push-Location $ProjectRoot
    try {
        $result = Invoke-Native { go mod download }
        if ($result.ExitCode -ne 0) {
            Write-Fail "go mod download не удался."
            foreach ($line in $result.Output) { Write-Host "         $line" -ForegroundColor DarkGray }
            Write-Fix @(
                'Проверьте интернет и прокси (переменные HTTP_PROXY/HTTPS_PROXY).'
                "Выполните вручную: cd `"$ProjectRoot`"; go mod download"
            )
            return $false
        }

        Write-Ok 'go mod download выполнен.'
        return $true
    }
    finally {
        Pop-Location
    }
}

function Initialize-TestData {
    Write-Title 'Тестовые файлы'

    try {
        if (-not (Test-Path -LiteralPath $TestDataDir)) {
            New-Item -ItemType Directory -Path $TestDataDir -Force | Out-Null
            Write-Ok "Создана папка: $TestDataDir"
        }
        else {
            Write-Ok "Папка testdata уже есть."
        }

        $files = @('domain_list.lst', 'ip_list.lst')
        foreach ($name in $files) {
            $path = Join-Path $TestDataDir $name
            if (-not (Test-Path -LiteralPath $path)) {
                New-Item -ItemType File -Path $path -Force | Out-Null
                Write-Ok "Создан файл: $name"
            }
        }

        $probe = Join-Path $TestDataDir '.write-test'
        'ok' | Set-Content -LiteralPath $probe -Encoding utf8 -NoNewline
        Remove-Item -LiteralPath $probe -Force

        Write-Ok 'Права на запись в testdata есть.'
        return $true
    }
    catch {
        Write-Fail "Не удалось подготовить testdata: $($_.Exception.Message)"
        Write-Fix @(
            "Проверьте права на папку: $TestDataDir"
            'Запустите PowerShell от имени пользователя с правами на запись.'
        )
        return $false
    }
}

function Read-EnvFile {
    param([string]$Path)

    $vars = @{}
    if (-not (Test-Path -LiteralPath $Path)) {
        return $vars
    }

    Get-Content -LiteralPath $Path -Encoding utf8 | ForEach-Object {
        $line = $_.Trim()
        if ($line -eq '' -or $line.StartsWith('#')) { return }
        if ($line -match '^\s*([^=]+)=(.*)$') {
            $key = $Matches[1].Trim()
            $value = $Matches[2].Trim()
            if (($value.StartsWith('"') -and $value.EndsWith('"')) -or ($value.StartsWith("'") -and $value.EndsWith("'"))) {
                $value = $value.Substring(1, $value.Length - 2)
            }
            $vars[$key] = $value
        }
    }

    return $vars
}

function Save-EnvFile {
    param(
        [string]$Path,
        [hashtable]$Vars
    )

    $lines = @(
        '# Локальные переменные для dev-windows.ps1. Не коммитьте этот файл.'
        "LST_SIGNBOX_LISTS_TGBOT_TOKEN=$($Vars['LST_SIGNBOX_LISTS_TGBOT_TOKEN'])"
    )
    $lines | Set-Content -LiteralPath $Path -Encoding utf8
}

function Get-BotToken {
    Write-Title 'Токен Telegram-бота'

    if ($env:LST_SIGNBOX_LISTS_TGBOT_TOKEN) {
        Write-Ok 'Токен найден в переменной окружения LST_SIGNBOX_LISTS_TGBOT_TOKEN.'
        return $env:LST_SIGNBOX_LISTS_TGBOT_TOKEN
    }

    $fileVars = Read-EnvFile -Path $EnvFile
    if ($fileVars.ContainsKey('LST_SIGNBOX_LISTS_TGBOT_TOKEN') -and $fileVars['LST_SIGNBOX_LISTS_TGBOT_TOKEN']) {
        Write-Ok "Токен найден в $EnvFile"
        return $fileVars['LST_SIGNBOX_LISTS_TGBOT_TOKEN']
    }

    Write-Host '  [....] Токен бота не задан - запросим ниже.' -ForegroundColor Yellow
    Write-Fix @(
        'Создайте бота через @BotFather в Telegram: команда /newbot'
        'Скопируйте выданный токен.'
    )

    Write-Host ''
    Write-Host '  Введите токен сейчас или нажмите Enter, чтобы пропустить:' -ForegroundColor Yellow
    $secureToken = Read-Host '  Токен' -AsSecureString
    if ($null -eq $secureToken -or $secureToken.Length -eq 0) {
        Write-Fail 'Токен не введён.'
        return $null
    }

    $bstr = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secureToken)
    try {
        $token = [Runtime.InteropServices.Marshal]::PtrToStringAuto($bstr).Trim()
    }
    finally {
        [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($bstr)
    }

    if ([string]::IsNullOrWhiteSpace($token)) {
        Write-Fail 'Токен не введён.'
        return $null
    }

    if ($token -notmatch '^\d+:[A-Za-z0-9_-]+$') {
        Write-Warn 'Токен выглядит необычно — проверьте, что скопировали его полностью.'
    }

    $save = Read-Host '  Сохранить токен в .env.local для следующих запусков? [Y/n]'
    if ($save -eq '' -or $save -match '^[Yy]') {
        Save-EnvFile -Path $EnvFile -Vars @{ LST_SIGNBOX_LISTS_TGBOT_TOKEN = $token }
        Write-Ok "Токен сохранён в $EnvFile"
    }

    return $token
}

function Set-DevEnvironment {
    param([string]$Token)

    $domainList = Join-Path $TestDataDir 'domain_list.lst'
    $ipList = Join-Path $TestDataDir 'ip_list.lst'
    $statePath = Join-Path $TestDataDir 'state.json'
    $logPath = Join-Path $TestDataDir 'bot.log'

    $env:LST_SIGNBOX_LISTS_TGBOT_TOKEN = $Token
    $env:LST_SIGNBOX_LISTS_TGBOT_DOMAIN_LIST = $domainList
    $env:LST_SIGNBOX_LISTS_TGBOT_IP_LIST = $ipList
    $env:LST_SIGNBOX_LISTS_TGBOT_RESTART_CMD = 'echo restart'
    $env:LST_SIGNBOX_LISTS_TGBOT_STATE_PATH = $statePath
    $env:LST_SIGNBOX_LISTS_TGBOT_LOG_PATH = $logPath
    $env:LST_SIGNBOX_LISTS_TGBOT_ENABLED = 'true'

    Write-Title 'Переменные окружения (текущая сессия)'
    Write-Ok "LST_SIGNBOX_LISTS_TGBOT_DOMAIN_LIST = $domainList"
    Write-Ok "LST_SIGNBOX_LISTS_TGBOT_IP_LIST      = $ipList"
    Write-Ok "LST_SIGNBOX_LISTS_TGBOT_STATE_PATH   = $statePath"
    Write-Ok "LST_SIGNBOX_LISTS_TGBOT_LOG_PATH     = $logPath"
    Write-Ok 'LST_SIGNBOX_LISTS_TGBOT_RESTART_CMD  = echo restart'
    Write-Ok 'LST_SIGNBOX_LISTS_TGBOT_ENABLED      = true'
    Write-Ok 'LST_SIGNBOX_LISTS_TGBOT_TOKEN        = *** (скрыт)'
}

function Invoke-GoBuild {
    Push-Location $ProjectRoot
    try {
        $result = Invoke-Native { go build -trimpath -o $BinaryName .\cmd\lst-signbox-lists-tgbot }
        if ($result.ExitCode -ne 0) {
            foreach ($line in $result.Output) {
                Write-Host "         $line" -ForegroundColor DarkGray
            }
            return $false
        }

        return (Test-Path -LiteralPath $BinaryPath)
    }
    finally {
        Pop-Location
    }
}

function Test-Build {
    Write-Title 'Сборка'

    if (-not (Invoke-GoBuild)) {
        Write-Fail 'go build завершился с ошибкой.'
        Write-Fix @(
            "cd `"$ProjectRoot`""
            "go build -trimpath -o $BinaryName .\cmd\lst-signbox-lists-tgbot"
        )
        return $false
    }

    Write-Ok "Собран: $BinaryPath"
    return $true
}

function Get-SourceStamp {
    $files = @(Get-ChildItem -LiteralPath $ProjectRoot -Recurse -File -Filter '*.go' -ErrorAction SilentlyContinue |
        Where-Object { $_.FullName -notlike (Join-Path $ProjectRoot 'testdata\*') -and $_.FullName -notlike (Join-Path $ProjectRoot 'dist\*') })

    foreach ($name in @('go.mod', 'go.sum')) {
        $path = Join-Path $ProjectRoot $name
        if (Test-Path -LiteralPath $path) {
            $files += Get-Item -LiteralPath $path
        }
    }

    if ($files.Count -eq 0) { return '' }

    $parts = $files | Sort-Object FullName | ForEach-Object { "$($_.FullName)|$($_.Length)|$($_.LastWriteTimeUtc.Ticks)" }
    return ($parts -join "`n")
}

function Start-BotProcess {
    # Process.Start вместо Start-Process: так у нас остаётся хендл процесса
    # и после завершения бота доступен его код возврата.
    $psi = New-Object System.Diagnostics.ProcessStartInfo
    $psi.FileName = $BinaryPath
    $psi.WorkingDirectory = $ProjectRoot
    $psi.UseShellExecute = $false
    return [System.Diagnostics.Process]::Start($psi)
}

function Get-ProcessExitCode {
    param($Proc)

    try {
        [void]$Proc.WaitForExit(1000)
        return $Proc.ExitCode
    }
    catch {
        return $null
    }
}

function Stop-BotProcess {
    param($Proc)

    if ($null -eq $Proc) { return }

    try {
        if (-not $Proc.HasExited) {
            Stop-Process -Id $Proc.Id -Force -ErrorAction SilentlyContinue
            [void]$Proc.WaitForExit(5000)
        }
    }
    catch {
        # процесс уже завершился
    }
}

function Start-BotInteractive {
    Write-Title 'Запуск бота'

    Write-Host ''
    Write-Host '  Бот будет работать в этом окне (long polling).' -ForegroundColor White
    Write-Host '  Логи: testdata\bot.log' -ForegroundColor DarkGray
    Write-Host '  Остановка: Ctrl+C' -ForegroundColor DarkGray
    Write-Host '  В Telegram: /start, затем отправьте список доменов или IP.' -ForegroundColor DarkGray
    if ($NoWatch) {
        Write-Host '  Режим -NoWatch: изменения в коде НЕ пересобираются автоматически.' -ForegroundColor DarkGray
    }
    else {
        Write-Host '  Слежу за *.go, go.mod, go.sum: при изменении пересобираю и перезапускаю бота.' -ForegroundColor DarkGray
    }
    Write-Host ''

    $answer = Read-Host '  Запустить бота сейчас? [Y/n]'
    if ($answer -ne '' -and $answer -notmatch '^[Yy]') {
        Write-Host ''
        Write-Host '  Запуск пропущен. Чтобы стартовать позже в этой же сессии:' -ForegroundColor Yellow
        Write-Host "    .\$BinaryName" -ForegroundColor White
        return
    }

    Write-Host ''
    Write-Host '--- Запуск lst-signbox-lists-tgbot ---' -ForegroundColor Cyan
    Write-Host ''

    if ($NoWatch) {
        Push-Location $ProjectRoot
        try {
            & ".\$BinaryName"
            $exitCode = $LASTEXITCODE
            Write-Host ''
            if ($exitCode -ne 0) {
                Write-Host "  Бот завершился с кодом $exitCode. Смотрите testdata\bot.log" -ForegroundColor Red
            }
            else {
                Write-Host '  Бот остановлен.' -ForegroundColor Green
            }
        }
        finally {
            Pop-Location
        }
        return
    }

    Start-BotWatch
}

function Start-BotWatch {
    $stamp = Get-SourceStamp
    $proc = Start-BotProcess
    Write-Host "  [RUN]  Бот запущен (PID $($proc.Id)). Слежу за изменениями в коде..." -ForegroundColor Green

    try {
        while ($true) {
            Start-Sleep -Milliseconds 700

            if ($null -ne $proc -and $proc.HasExited) {
                $exitCode = Get-ProcessExitCode -Proc $proc
                $exitText = if ($null -eq $exitCode) { 'Бот завершился.' } else { "Бот завершился с кодом $exitCode." }
                Write-Host ''
                Write-Host "  [STOP] $exitText Смотрите testdata\bot.log" -ForegroundColor Red
                Write-Host '         Жду изменений в коде, чтобы пересобрать и перезапустить.' -ForegroundColor DarkGray
                $proc = $null
            }

            $current = Get-SourceStamp
            if ($current -eq $stamp) { continue }

            # даём редактору дописать файлы
            Start-Sleep -Milliseconds 400
            $stamp = Get-SourceStamp

            Write-Host ''
            Write-Host '  [BUILD] Код изменился - пересобираю...' -ForegroundColor Cyan

            Stop-BotProcess -Proc $proc
            $proc = $null

            if (Invoke-GoBuild) {
                $proc = Start-BotProcess
                Write-Host "  [RUN]  Пересобран и перезапущен (PID $($proc.Id))." -ForegroundColor Green
            }
            else {
                Write-Host '  [FAIL] Сборка не удалась, бот не запущен. Исправьте код - пересоберу автоматически.' -ForegroundColor Red
            }
        }
    }
    finally {
        Stop-BotProcess -Proc $proc
        Write-Host ''
        Write-Host '  Бот остановлен.' -ForegroundColor Green
    }
}

# --- main ---

Write-Host ''
Write-Host 'lst-signbox-lists-tgbot - настройка локальной разработки (Windows)' -ForegroundColor Cyan
Write-Host "Проект: $ProjectRoot" -ForegroundColor DarkGray

$goOk = Test-GoVersion
$layoutOk = Test-ProjectLayout
$networkOk = Test-Network

$modulesOk = $false
if ($goOk -and $layoutOk) {
    $modulesOk = Test-GoModules
}

$testdataOk = Initialize-TestData

$token = $null
if ($script:ChecksFailed -eq 0) {
    $token = Get-BotToken
    if (-not $token) {
        $script:ChecksFailed++
    }
}

$buildOk = $false
if ($script:ChecksFailed -eq 0 -and $token) {
    Set-DevEnvironment -Token $token
    $buildOk = Test-Build
}

Write-Title 'Итог'

if ($script:ChecksFailed -gt 0) {
    Write-Host "  Проверки с ошибками: $script:ChecksFailed" -ForegroundColor Red
    if ($script:ChecksWarned -gt 0) {
        Write-Host "  Предупреждений: $script:ChecksWarned" -ForegroundColor Yellow
    }
    Write-Host ''
    Write-Host '  Исправьте ошибки выше и запустите скрипт снова:' -ForegroundColor Yellow
    Write-Host '    .\scripts\dev-windows.ps1' -ForegroundColor White
    exit 1
}

if ($script:ChecksWarned -gt 0) {
    Write-Host "  Все обязательные проверки пройдены. Предупреждений: $script:ChecksWarned" -ForegroundColor Yellow
}
else {
    Write-Host '  Все проверки пройдены.' -ForegroundColor Green
}

if (-not $buildOk) {
    exit 1
}

Start-BotInteractive
exit 0
