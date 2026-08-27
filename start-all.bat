@echo off
setlocal
cd /d "%~dp0"
title AI Scratch Web Launcher

set "BACKEND_PYTHON=%CD%\backend\.venv\Scripts\python.exe"
set "FRONTEND_DIR=%CD%\frontend"

if not exist "%BACKEND_PYTHON%" (
  echo [ERROR] Backend virtual environment was not found.
  echo Run: python -m venv backend\.venv
  echo Then: backend\.venv\Scripts\python.exe -m pip install -e ".\backend[test]"
  pause
  exit /b 1
)

if not exist "%FRONTEND_DIR%\node_modules\.bin\vite.cmd" (
  echo [ERROR] Frontend dependencies were not found.
  echo Run: npm --prefix frontend install
  pause
  exit /b 1
)

if /i "%~1"=="--check" (
  echo [OK] Backend and frontend dependencies are ready.
  exit /b 0
)

pwsh -NoProfile -Command "if (Get-NetTCPConnection -State Listen -LocalPort 8000 -ErrorAction SilentlyContinue) { exit 0 } else { exit 1 }"
if errorlevel 1 (
  echo [START] Backend: http://127.0.0.1:8000 - local proxy only)
  start "AI Scratch Backend" /D "%CD%\backend" cmd /k ""%BACKEND_PYTHON%" -u -m uvicorn app.main:app --host 127.0.0.1 --port 8000 --log-level debug --access-log"
) else (
  echo [SKIP] Port 8000 is already in use.
)

pwsh -NoProfile -Command "if (Get-NetTCPConnection -State Listen -LocalPort 5173 -ErrorAction SilentlyContinue) { exit 0 } else { exit 1 }"
if errorlevel 1 (
  echo [START] Frontend: http://127.0.0.1:5173 or http://YOUR-LAN-IP:5173
  start "AI Scratch Frontend" /D "%FRONTEND_DIR%" cmd /k "npm run dev"
) else (
  echo [SKIP] Port 5173 is already in use.
)

timeout /t 2 /nobreak >nul
start "" "http://127.0.0.1:5173/"
echo [OK] AI Scratch Web startup commands were launched.
endlocal
