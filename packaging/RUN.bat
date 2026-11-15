@echo off
rem IDUNA, run anywhere. Settings come from iduna.env next to this file (copy iduna.env.example); none are required.
rem With the WOTAN site in wotan\ (release bundles carry it) the arcade is served too, /api/ answered by this IDUNA.
cd /d "%~dp0"
if exist iduna.env for /f "usebackq eol=# tokens=1,* delims==" %%a in ("iduna.env") do set "%%a=%%b"
if "%IDUNA_ADDR%"=="" set "IDUNA_ADDR=127.0.0.1:8080"
if "%WOTAN_SITE_DIR%"=="" if exist wotan\index.html set "WOTAN_SITE_DIR=wotan"
if "%WOTAN_ADDR%"=="" set "WOTAN_ADDR=127.0.0.1:8090"
if not exist var mkdir var
set /p IDUNA_VERSION=<VERSION
echo IDUNA %IDUNA_VERSION% on http://%IDUNA_ADDR%   (log: iduna.log, data: var\iduna.db)
if not "%WOTAN_SITE_DIR%"=="" (
  echo WOTAN arcade: http://%WOTAN_ADDR%/   -- opening it in your browser
  start "" /b cmd /c "ping -n 3 127.0.0.1 >nul & start http://%WOTAN_ADDR%/"
)
echo Close this window to stop it.
iduna.exe >> iduna.log 2>&1
echo IDUNA stopped (exit %ERRORLEVEL%). The last lines of iduna.log say why.
pause
