@echo off
set PORT=8686
set EXE=wrxbpq.exe

cd /d "%~dp0"

if not exist "%EXE%" (
    echo ERROR: %EXE% not found in this folder.
    echo        Keep start.bat together with the built %EXE%.
    pause
    exit /b 1
)

echo Starting wrxbpq local server on port %PORT% ...
echo (a server window will open and stay open while running)
start "" "%~dp0%EXE%"

set tries=0
:wait
powershell -NoProfile -Command "try { (Invoke-WebRequest -Uri 'http://127.0.0.1:%PORT%/api/health' -UseBasicParsing -TimeoutSec 1).StatusCode | Out-Null } catch { exit 1 }" >nul 2>&1
if not errorlevel 1 goto ready
set /a tries=%tries%+1
if %tries% geq 40 (
    echo.
    echo ERROR: server did not respond within 40s.
    echo The server window may have closed. Double-click %EXE% directly to see the error.
    pause
    exit /b 1
)
timeout /t 1 >nul
goto wait

:ready
echo ============================================================
echo  wrxbpq server is running
echo  Open:  http://127.0.0.1:%PORT%
echo  Close the server window (or this window) to stop.
echo ============================================================
start "" "http://127.0.0.1:%PORT%"
echo Press any key to stop the server and exit...
pause >nul
taskkill /f /im "%EXE%" >nul 2>&1
exit /b 0
