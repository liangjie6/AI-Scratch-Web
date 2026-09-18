@echo off
setlocal
cd /d "%~dp0"
title AI Scratch Web Launcher

set "BACKEND_PYTHON=%CD%\backend\.venv\Scripts\python.exe"
set "FRONTEND_DIR=%CD%\frontend"
goto :main

:stop_project_port
set "TARGET_PORT=%~1"
pwsh -NoProfile -Command "$port=[int]$env:TARGET_PORT; $kind=$env:PORT_KIND; $frontend=$env:FRONTEND_DIR; $listeners=@(Get-NetTCPConnection -State Listen -LocalPort $port -ErrorAction SilentlyContinue); if(-not $listeners){exit 0}; $pids=@($listeners | Select-Object -ExpandProperty OwningProcess -Unique); foreach($processId in $pids){$process=Get-CimInstance Win32_Process -Filter ('ProcessId=' + $processId); $command=[string]$process.CommandLine; $owned=if($kind -eq 'backend'){$command -match 'uvicorn\s+app\.main:app' -and $command -match ('--port\s+' + $port)}else{($command -match 'vite' -or $command -like ('*' + $frontend + '*')) -and ($command -match 'node' -or $command -match 'npm')}; if(-not $owned){Write-Error ('Port ' + $port + ' is occupied by non-project PID ' + $processId + ': ' + $command); exit 2}; Write-Host ('[STOP] Old ' + $kind + ' PID ' + $processId + ' on port ' + $port); Stop-Process -Id $processId -Force -ErrorAction Stop}; $deadline=(Get-Date).AddSeconds(5); while((Get-NetTCPConnection -State Listen -LocalPort $port -ErrorAction SilentlyContinue) -and (Get-Date) -lt $deadline){Start-Sleep -Milliseconds 100}; if(Get-NetTCPConnection -State Listen -LocalPort $port -ErrorAction SilentlyContinue){Write-Error ('Port ' + $port + ' did not close in time'); exit 3}"
exit /b %errorlevel%

:main

rem Local development defaults: show app business-flow DEBUG diagnostics and
rem bounded AI output excerpts. Third-party HTTP transport logs stay quiet.
set "AI_SCRATCH_LOG_LEVEL=DEBUG"
set "AI_BLOCKS_VERBOSE_LOGGING=true"

if not exist "%BACKEND_PYTHON%" (
  echo [ERROR] Backend virtual environment was not found.
  echo Run: python -m venv backend\.venv
  echo Then: backend\.venv\Scripts\python.exe -m pip install -e ".\backend"
  pause
  exit /b 1
)

if not exist "%FRONTEND_DIR%\node_modules\vite\bin\vite.js" (
  echo [ERROR] Frontend Vite dependency was not found.
  echo Run: npm --prefix frontend install
  pause
  exit /b 1
)

if /i "%~1"=="--check" (
  echo [OK] Backend and frontend dependencies are ready.
  exit /b 0
)

set "PORT_KIND=backend"
call :stop_project_port 8000
if errorlevel 1 (
  pause
  exit /b 1
)
echo [START] Backend: http://127.0.0.1:8000 (local proxy only)
pwsh -NoProfile -Command "$p=Start-Process -FilePath '%BACKEND_PYTHON%' -ArgumentList @('-u','-m','uvicorn','app.main:app','--host','127.0.0.1','--port','8000','--log-level','info','--access-log') -WorkingDirectory '%CD%\backend' -WindowStyle Normal -PassThru; Write-Host ('[STARTED] Backend PID ' + $p.Id)"

set "PORT_KIND=frontend"
call :stop_project_port 5173
if errorlevel 1 (
  pause
  exit /b 1
)
echo [START] Frontend: http://127.0.0.1:5173 or http://YOUR-LAN-IP:5173
pwsh -NoProfile -Command "$p=Start-Process -FilePath 'cmd.exe' -ArgumentList @('/k','npm','run','dev') -WorkingDirectory '%FRONTEND_DIR%' -WindowStyle Normal -PassThru; Write-Host ('[STARTED] Frontend PID ' + $p.Id)"

echo [WAIT] Waiting for frontend server to listen on port 5173...
pwsh -NoProfile -Command "$ready=$false; $deadline=(Get-Date).AddSeconds(12); while((Get-Date) -lt $deadline){if(Get-NetTCPConnection -State Listen -LocalPort 5173 -ErrorAction SilentlyContinue){$ready=$true; break}; Start-Sleep -Milliseconds 400}; if($ready){Write-Host '[READY] Frontend is listening on port 5173'}else{Write-Warning '[WARN] Frontend did not start listening on port 5173 within timeout. Please check the Frontend terminal window for errors.'}"

start "" "http://127.0.0.1:5173/"
echo [OK] AI Scratch Web startup commands were launched.
endlocal
exit /b 0
