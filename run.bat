@echo off
setlocal enabledelayedexpansion
title stzbHelper Environment Setup

echo ============================================
echo   stzbHelper - Environment Setup Script
echo ============================================
echo.

:: ---------- Ensure Go bin in PATH ----------
if exist "C:\Go\bin\go.exe" set PATH=C:\Go\bin;%PATH%
if exist "%USERPROFILE%\go\bin\wails.exe" set PATH=%USERPROFILE%\go\bin;%PATH%

:: ---------- Check Admin ----------
net session >nul 2>&1
if %errorlevel% neq 0 (
    echo [WARN] Not running as Administrator.
    echo        Npcap packet capture may not work.
    echo        Right-click and select "Run as administrator".
    echo.
)

:: ---------- Check Node.js ----------
echo [1/5] Checking Node.js...
node --version >nul 2>&1
if %errorlevel% neq 0 (
    echo [ERROR] Node.js not found. Please install Node.js v20+
    echo        Download: https://nodejs.org/
    pause
    exit /b 1
)
for /f "tokens=*" %%i in ('node --version') do set NODE_VER=%%i
echo        Node.js %NODE_VER% OK

call npm --version >nul 2>&1
if %errorlevel% neq 0 (
    echo [ERROR] npm not found.
    pause
    exit /b 1
)
echo        npm OK

:: ---------- Check/Install Go ----------
echo [2/5] Checking Go...
go version >nul 2>&1
if %errorlevel% neq 0 (
    echo        Go not found. Downloading...
    powershell -Command "$ProgressPreference='SilentlyContinue'; try{Invoke-WebRequest -Uri 'https://golang.google.cn/dl/go1.24.2.windows-amd64.zip' -OutFile '%TEMP%\go.zip' -ErrorAction Stop}catch{Invoke-WebRequest -Uri 'https://go.dev/dl/go1.24.2.windows-amd64.zip' -OutFile '%TEMP%\go.zip'}"
    if !errorlevel! neq 0 (
        echo [ERROR] Go download failed. Please install manually:
        echo        https://go.dev/dl/
        pause
        exit /b 1
    )
    echo        Extracting to C:\Go...
    powershell -Command "Microsoft.PowerShell.Archive\Expand-Archive -Path '%TEMP%\go.zip' -DestinationPath 'C:\Go' -Force"
    if exist "C:\Go\bin\go.exe" (
        echo        Go installed to C:\Go OK
        set PATH=C:\Go\bin;!PATH!
    ) else (
        echo [ERROR] Go installation failed.
        pause
        exit /b 1
    )
) else (
    for /f "tokens=*" %%i in ('go version') do set GO_VER=%%i
    echo        !GO_VER! OK
)

:: ---------- Set Go Proxy ----------
echo [3/5] Setting Go module proxy...
go env -w GO111MODULE=on >nul 2>&1
go env -w GOPROXY=https://goproxy.cn,direct >nul 2>&1
echo        GOPROXY=https://goproxy.cn,direct OK

:: ---------- Install Wails ----------
echo [4/5] Checking Wails CLI...
wails version >nul 2>&1
if %errorlevel% neq 0 (
    echo        Wails not found. Installing...
    call go install github.com/wailsapp/wails/v2/cmd/wails@latest
    if !errorlevel! neq 0 (
        echo [ERROR] Wails installation failed. Check network.
        pause
        exit /b 1
    )
    set PATH=%USERPROFILE%\go\bin;!PATH!
    echo        Wails CLI installed OK
) else (
    for /f "tokens=*" %%i in ('wails version') do set WAILS_VER=%%i
    echo        Wails !WAILS_VER! OK
)

:: ---------- Install Frontend Dependencies ----------
echo [5/5] Installing frontend dependencies...
cd /d "%~dp0"
if exist "frontend\package.json" (
    pushd frontend
    call npm install
    popd
    echo        Frontend dependencies OK
) else (
    echo        frontend\package.json not found, skipping OK
)

echo.
echo ============================================
echo   Building project...
echo ============================================
echo.

:: ---------- Build ----------
call wails build
if %errorlevel% neq 0 (
    echo [ERROR] Build failed. Check error messages above.
    pause
    exit /b 1
)

echo.
echo ============================================
echo   Build successful! Starting stzbHelper...
echo ============================================
start "" "build\bin\stzbHelper-wails.exe"
echo.
echo   Program launched.
echo.
echo   Note: Requires Npcap for packet capture.
echo   Download: https://npcap.com/#download
echo.
pause
