@echo off
rem shado - shadow workspace controller (Windows shim)
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0shado.ps1" %*
