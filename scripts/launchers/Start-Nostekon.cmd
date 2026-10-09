@echo off
echo Nostekon opens at http://127.0.0.1:8080 unless --addr overrides it. Stop with Ctrl+C.
"%~dp0nostekon-api.exe" %*
if errorlevel 1 pause