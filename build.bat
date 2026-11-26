@echo off
setlocal EnableExtensions
chcp 65001 >nul
cd /d "%~dp0"

echo [資訊] iFileManager 建置腳本

where go >nul 2>nul
if errorlevel 1 (
    echo [錯誤] 找不到 go，請先安裝 Go 1.27 以上版本：https://go.dev/dl/
    exit /b 1
)
for /f "delims=" %%v in ('go version') do echo [資訊] %%v

for /f "delims=" %%p in ('go env GOPATH') do set "GOPATH_DIR=%%p"
set "PATH=%PATH%;%GOPATH_DIR%\bin"

where wails >nul 2>nul
if errorlevel 1 (
    echo [執行] 安裝 Wails CLI，需要網路，只需執行一次
    go install github.com/wailsapp/wails/v2/cmd/wails@latest
    if errorlevel 1 (
        echo [錯誤] 安裝 Wails CLI 失敗，請確認網路連線
        exit /b 1
    )
)

echo [執行] go mod tidy
go mod tidy
if errorlevel 1 (
    echo [錯誤] go mod tidy 失敗，請確認網路連線
    exit /b 1
)

echo [執行] go test ./internal/...
go test ./internal/... -count=1
if errorlevel 1 (
    echo [錯誤] 單元測試未通過，已停止建置
    exit /b 1
)

echo [執行] 系統功能自我檢測：右鍵選單、快速存取、磁碟
set "FM_SHELL_SMOKE=1"
go test ./internal/fsops -run Smoke -count=1 -v
if errorlevel 1 (
    echo [錯誤] 自我檢測有項目未通過，程式仍會繼續建置；右鍵選單或快速存取可能無法使用，請把上方訊息提供給我
) else (
    echo [資訊] 自我檢測全部通過
)
set "FM_SHELL_SMOKE="

echo [執行] 關閉執行中的 iFileManager，避免輸出檔被鎖住
taskkill /f /im iFileManager.exe >nul 2>nul
if not errorlevel 1 timeout /t 1 /nobreak >nul

echo [執行] wails build -clean -ldflags "-s -w"
wails build -clean -ldflags "-s -w"
if errorlevel 1 (
    echo [錯誤] wails build 失敗，請把上方訊息提供給我
    exit /b 1
)

if not exist "build\bin\iFileManager.exe" (
    echo [錯誤] 找不到輸出檔 build\bin\iFileManager.exe
    exit /b 1
)

where upx >nul 2>nul
if errorlevel 1 (
    echo [警告] 找不到 upx，略過壓縮。需要縮小檔案時請安裝 UPX 並加入 PATH：https://upx.github.io/
) else (
    echo [執行] upx --best --lzma 壓縮執行檔；部分防毒軟體可能誤判 UPX 壓縮過的檔案
    upx --best --lzma "build\bin\iFileManager.exe" >nul
    if errorlevel 1 echo [警告] upx 壓縮失敗，保留未壓縮的執行檔
)

for %%F in ("build\bin\iFileManager.exe") do echo [資訊] 檔案大小：%%~zF bytes
echo [資訊] 建置完成：%CD%\build\bin\iFileManager.exe
exit /b 0
