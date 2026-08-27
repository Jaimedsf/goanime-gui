$ErrorActionPreference = "Stop"

# Configura caminhos absolutos
$SCRIPT_DIR = $PSScriptRoot
$ROOT_DIR = Split-Path -Parent $SCRIPT_DIR
$OUTPUT_DIR = Join-Path $ROOT_DIR "build"
$BINARY_NAME = "goanime.exe"
$BINARY_PATH = Join-Path $OUTPUT_DIR $BINARY_NAME
$GUI_BINARY_NAME = "goanime-gui.exe"
$GUI_BINARY_PATH = Join-Path $OUTPUT_DIR $GUI_BINARY_NAME
$ZIP_NAME = "goanime-windows.zip"
$ZIP_PATH = Join-Path $OUTPUT_DIR $ZIP_NAME
$CHECKSUM_FILE = "$ZIP_PATH.sha256"
$MAIN_PACKAGE = Join-Path $ROOT_DIR "cmd\goanime"
$GUI_PACKAGE = Join-Path $ROOT_DIR "cmd\goanime-gui"

# Detecta arquitetura
$ARCH = $env:PROCESSOR_ARCHITECTURE
if ($ARCH -eq "AMD64") {
    $GOARCH = "amd64"
} elseif ($ARCH -eq "ARM64") {
    $GOARCH = "arm64"
} else {
    Write-Host "Arquitetura não suportada: $ARCH"
    exit 1
}

# Cria diretório de saída
New-Item -ItemType Directory -Force -Path $OUTPUT_DIR | Out-Null

Write-Host "Compilando binário para Windows ($GOARCH)..."
$env:CGO_ENABLED = "1"
$env:GOOS = "windows"
$env:GOARCH = $GOARCH

# Executa a compilação
try {
    go build -o $BINARY_PATH -ldflags="-s -w" -trimpath -tags="windows" $MAIN_PACKAGE
    if (-not (Test-Path $BINARY_PATH)) {
        throw "Binário não gerado"
    }
    Write-Host "Compilação concluída: $BINARY_PATH"
}
catch {
    Write-Host "ERRO na compilação: $_"
    exit 1
}

# Aplicativo desktop (Wails).
#
# Não precisa da CLI do Wails: o frontend são módulos ES escritos à mão que o
# main.go embute com //go:embed all:frontend/dist, então não há bundler nem
# etapa de transpilação. O `go build` é a compilação inteira, e ele já linka o
# rsrc_windows_amd64.syso commitado, de onde vêm o ícone e os metadados.
#
# -H windowsgui é o que importa aqui: sem ele o binário linka no subsistema de
# console e o Windows abre uma janela preta vazia atrás da interface.
Write-Host "Compilando o aplicativo desktop ($GOARCH)..."
try {
    go build -tags "desktop,production" -ldflags "-s -w -H windowsgui" -trimpath -o $GUI_BINARY_PATH $GUI_PACKAGE
    if (-not (Test-Path $GUI_BINARY_PATH)) {
        throw "Binário da GUI não gerado"
    }

    # Confere o subsistema no cabeçalho PE: 2 = GUI, 3 = console. Perder a flag
    # acima só aparece quando alguém abre o app.
    $fs = [IO.File]::OpenRead($GUI_BINARY_PATH)
    $br = New-Object IO.BinaryReader($fs)
    $fs.Seek(0x3C, 'Begin') | Out-Null
    $peOffset = $br.ReadInt32()
    $fs.Seek($peOffset + 0x5C, 'Begin') | Out-Null
    $subsystem = $br.ReadInt16()
    $br.Close()
    if ($subsystem -ne 2) {
        throw "goanime-gui.exe linkado no subsistema $subsystem, esperado 2 (GUI)"
    }

    Write-Host "Compilação concluída: $GUI_BINARY_PATH (subsistema $subsystem, sem console)"
}
catch {
    Write-Host "ERRO na compilação da GUI: $_"
    exit 1
}

# UPX (opcional)
if (Get-Command upx -ErrorAction SilentlyContinue) {
    # Só a CLI. Comprimir o binário da GUI com UPX é uma das heurísticas que
    # mais dispara falso positivo no Defender e no SmartScreen, e num app já
    # não assinado isso é a diferença entre abrir e ser posto em quarentena.
    Write-Host "Comprimindo a CLI com UPX..."
    upx --best --ultra-brute $BINARY_PATH
    Write-Host "Compressão concluída."
}
else {
    Write-Host "UPX não encontrado. Pulando compressão."
}

# Cria ZIP
Write-Host "Criando ZIP..."
try {
    if (Test-Path $ZIP_PATH) {
        Remove-Item $ZIP_PATH -Force
    }
    $compressParams = @{
        Path             = @($BINARY_PATH, $GUI_BINARY_PATH)
        DestinationPath  = $ZIP_PATH
        CompressionLevel = "Optimal"
    }
    Compress-Archive @compressParams -Force -ErrorAction Stop
    Write-Host "ZIP criado: $ZIP_PATH"
}
catch {
    Write-Host "ERRO ao criar ZIP: $_"
    exit 1
}

# Checksum
Write-Host "Gerando checksum SHA256..."
try {
    $hash = Get-FileHash -Path $ZIP_PATH -Algorithm SHA256 -ErrorAction Stop
    $hash.Hash.ToLower() | Out-File -FilePath $CHECKSUM_FILE -Encoding ASCII
    Write-Host "Checksum gerado: $CHECKSUM_FILE"
}
catch {
    Write-Host "ERRO ao gerar checksum: $_"
    exit 1
}

Write-Host "Build concluído com sucesso!"