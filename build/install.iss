; GoAnime Windows Installer
;
; Run from CI with the binaries staged under build\staging:
;   staging\goanime.exe          the terminal app
;   staging\goanime-gui.exe      the desktop app
;   staging\bin\mpv.exe + *.dll  the bundled player
;
; The release workflow rewrites MyAppVersion from the git tag before calling
; ISCC, so the value below only matters for a local build.

#define MyAppName "GoAnime"
#define MyAppVersion "1.8.6"
#define MyAppPublisher "Jaimedsf (GoAnime fork)"
#define MyAppURL "https://github.com/Jaimedsf/goanime-gui"
#define MyAppExeName "goanime.exe"
#define MyAppGuiExeName "goanime-gui.exe"

[Setup]
; Not upstream's AppId: Inno Setup treats two installers with the same id as
; one product, so this one would silently upgrade over (or uninstall) a copy
; of the original GoAnime. The fork ships a different app and needs its own.
AppId={{097150EB-032D-4E60-AFCA-48612D9DDE7F}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
AppVerName={#MyAppName} {#MyAppVersion}
AppPublisher={#MyAppPublisher}
AppPublisherURL={#MyAppURL}
AppSupportURL={#MyAppURL}/issues
AppUpdatesURL={#MyAppURL}/releases
DefaultDirName={autopf}\{#MyAppName}
DefaultGroupName={#MyAppName}
AllowNoIcons=yes
OutputDir=..\dist
OutputBaseFilename=GoAnime-Installer-{#MyAppVersion}
SetupIconFile=icon.ico
UninstallDisplayIcon={app}\{#MyAppGuiExeName}
UninstallDisplayName={#MyAppName} {#MyAppVersion}
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
PrivilegesRequired=admin
ArchitecturesInstallIn64BitMode=x64compatible
ArchitecturesAllowed=x64compatible
; WebView2 and the toolchain target Windows 10 1809 and up.
MinVersion=10.0.17763

; Without this Inno does not broadcast WM_SETTINGCHANGE, and a PATH written by
; the [Registry] section is invisible until logout. Broadcasting is what the
; old `setx` call was really after — see the note in [Registry].
ChangesEnvironment=yes

; Offer to close a running copy instead of failing with "file in use", and
; restart it afterwards.
CloseApplications=yes
RestartApplications=yes

; The installer executable's own Properties -> Details tab.
VersionInfoVersion={#MyAppVersion}
VersionInfoCompany={#MyAppPublisher}
VersionInfoDescription={#MyAppName} Setup
VersionInfoProductName={#MyAppName}
VersionInfoCopyright=Copyright (c) alvarorichard

[Languages]
Name: "brazilianportuguese"; MessagesFile: "compiler:Languages\BrazilianPortuguese.isl"
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:AdditionalIcons}"; Flags: unchecked
Name: "addtopath"; Description: "Add GoAnime and mpv to the system PATH"; GroupDescription: "System Integration:"; Flags: checkedonce

[Files]
Source: "staging\goanime.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "staging\goanime-gui.exe"; DestDir: "{app}"; Flags: ignoreversion skipifsourcedoesntexist

; mpv and its DLLs, so playback works with nothing else installed.
Source: "staging\bin\mpv.exe"; DestDir: "{app}\bin"; Flags: ignoreversion
Source: "staging\bin\*.dll"; DestDir: "{app}\bin"; Flags: ignoreversion skipifsourcedoesntexist

; The WebView2 Evergreen bootstrapper, staged by CI. Copied to {tmp} and run
; only when the runtime is missing, then deleted. See WebView2Installed below.
Source: "staging\MicrosoftEdgeWebview2Setup.exe"; DestDir: "{tmp}"; Flags: deleteafterinstall skipifsourcedoesntexist; Check: not WebView2Installed

[Icons]
; The desktop app is the headline entry; the terminal one is listed under it
; rather than being the only thing a user finds in the Start menu.
Name: "{group}\{#MyAppName}"; Filename: "{app}\{#MyAppGuiExeName}"; Check: FileExists(ExpandConstant('{app}\{#MyAppGuiExeName}'))
Name: "{group}\{#MyAppName} (terminal)"; Filename: "{app}\{#MyAppExeName}"
Name: "{group}\{cm:UninstallProgram,{#MyAppName}}"; Filename: "{uninstallexe}"
Name: "{autodesktop}\{#MyAppName}"; Filename: "{app}\{#MyAppGuiExeName}"; Tasks: desktopicon; Check: FileExists(ExpandConstant('{app}\{#MyAppGuiExeName}'))

[Run]
Filename: "{tmp}\MicrosoftEdgeWebview2Setup.exe"; Parameters: "/silent /install"; StatusMsg: "Instalando o runtime WebView2..."; Check: not WebView2Installed; Flags: waituntilterminated skipifdoesntexist
Filename: "{app}\{#MyAppGuiExeName}"; Description: "{cm:LaunchProgram,{#StringChange(MyAppName, '&', '&&')}}"; Flags: nowait postinstall skipifsilent skipifdoesntexist

[Registry]
; PATH is written here and *only* here.
;
; The previous version also ran `setx PATH "%PATH%;{app};{app}\bin"`, which is
; two separate bugs. %PATH% at that moment is the *merged* system + user PATH,
; while setx writes the *user* one — so every system entry got copied into the
; user variable and appeared twice. And setx silently truncates its value at
; 1024 characters, so on any machine with a normal-length PATH the copy was cut
; mid-entry, corrupting unrelated tools' paths. ChangesEnvironment=yes above
; delivers the "takes effect immediately" part that setx was there for.
Root: HKLM; Subkey: "SYSTEM\CurrentControlSet\Control\Session Manager\Environment"; ValueType: expandsz; ValueName: "Path"; ValueData: "{olddata};{app}"; Tasks: addtopath; Check: NeedsAddPath('{app}')
Root: HKLM; Subkey: "SYSTEM\CurrentControlSet\Control\Session Manager\Environment"; ValueType: expandsz; ValueName: "Path"; ValueData: "{olddata};{app}\bin"; Tasks: addtopath; Check: NeedsAddPath('{app}\bin')

[Code]
const
  EnvKey = 'SYSTEM\CurrentControlSet\Control\Session Manager\Environment';

// NeedsAddPath reports whether an entry is missing from the system PATH.
//
// The delimiters matter: a bare Pos() of the install directory also matches
// inside '{app}\bin', so a fresh install would decide {app} was already
// present the moment {app}\bin had been added.
//
// A registry read that fails answers True — better to attempt the write, which
// Inno does safely with {olddata}, than to silently skip the PATH task.
function NeedsAddPath(Param: string): boolean;
var
  Orig: string;
begin
  if not RegQueryStringValue(HKLM, EnvKey, 'Path', Orig) then
  begin
    Result := True;
    Exit;
  end;
  Result := Pos(';' + Uppercase(Param) + ';', ';' + Uppercase(Orig) + ';') = 0;
end;

// RemovePath drops one entry from the system PATH, matching it whole.
//
// Two rules this function exists to enforce:
//
//   - Match on ';entry;'. The old implementation searched for ';' + Param,
//     which also matched the {app} prefix *inside* the {app}\bin entry, so
//     uninstalling removed the prefix and left a dangling '\bin' behind.
//
//   - Never write a PATH we did not successfully read, and never write one we
//     did not actually change. PATH is shared machine state; a failed read
//     followed by a write is how an uninstaller destroys every other program's
//     entries.
procedure RemovePath(Param: string);
var
  Orig, Padded, Target: string;
  P: Integer;
begin
  if not RegQueryStringValue(HKLM, EnvKey, 'Path', Orig) then
    Exit;
  if Orig = '' then
    Exit;

  Padded := ';' + Orig + ';';
  Target := ';' + Uppercase(Param) + ';';

  P := Pos(Target, Uppercase(Padded));
  while P > 0 do
  begin
    // Length(Target) - 1 keeps one delimiter, so the entries on either side
    // stay separated instead of being joined into one bogus path.
    Delete(Padded, P, Length(Target) - 1);
    P := Pos(Target, Uppercase(Padded));
  end;

  // Strip the delimiters this procedure added.
  Delete(Padded, 1, 1);
  if (Length(Padded) > 0) and (Copy(Padded, Length(Padded), 1) = ';') then
    Delete(Padded, Length(Padded), 1);

  if Padded <> Orig then
    RegWriteExpandStringValue(HKLM, EnvKey, 'Path', Padded);
end;

// --- WebView2 -------------------------------------------------------------
//
// The desktop app is a Wails/WebView2 shell. The Evergreen runtime ships with
// Windows 11 and with current Windows 10, but not with an unpatched 1809, and
// without it goanime-gui.exe starts and shows nothing at all — a blank window
// with no error, which is the worst possible failure to debug from a bug
// report. So detect it and install it from Microsoft's official bootstrapper.
function WebView2Installed(): boolean;
var
  Version: string;
begin
  Result :=
    (RegQueryStringValue(HKLM, 'SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}', 'pv', Version) and (Version <> '') and (Version <> '0.0.0.0')) or
    (RegQueryStringValue(HKLM, 'SOFTWARE\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}', 'pv', Version) and (Version <> '') and (Version <> '0.0.0.0')) or
    (RegQueryStringValue(HKCU, 'SOFTWARE\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}', 'pv', Version) and (Version <> '') and (Version <> '0.0.0.0'));
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
begin
  if CurUninstallStep = usPostUninstall then
  begin
    // Longest first: removing {app} before {app}\bin is only safe because
    // RemovePath matches whole entries, but the order still reads better.
    RemovePath(ExpandConstant('{app}\bin'));
    RemovePath(ExpandConstant('{app}'));
  end;
end;
