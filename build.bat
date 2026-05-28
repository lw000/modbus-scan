@echo off
setlocal

echo ============================
echo  Modbus Collector Build
echo ============================

:: Set output binary name
set BINARY=modbus-scan

:: Set target platform
set GOOS=windows
set GOARCH=amd64

:: Build the project
echo [INFO] Building %BINARY% for %GOOS%/%GOARCH% ...
go build -o %BINARY%.exe ./cmd/modbus-scan

if %ERRORLEVEL% neq 0 (
    echo [ERROR] Build failed with exit code %ERRORLEVEL%
    exit /b %ERRORLEVEL%
)

echo [INFO] Build successful: %BINARY%.exe

:: Run vet
echo [INFO] Running go vet ...
go vet ./...
if %ERRORLEVEL% neq 0 (
    echo [WARN] go vet reported issues
) else (
    echo [INFO] go vet passed
)

:: Check required config files
if not exist configs\config.toml (
    echo [WARN] configs\config.toml not found, please create it before running
)
if not exist configs\points.csv (
    echo [WARN] configs\points.csv not found, please create it before running
)

echo ============================
echo  Build Complete
echo ============================
endlocal
