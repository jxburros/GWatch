; GWatch Windows installer (Inno Setup 6).
;
; Builds gwatch-setup-<version>.exe from an already-built gwatch.exe. This
; script does not compile Go code -- build the executable first (see
; scripts\build-installer.ps1, which does both steps), or pass the pieces in
; by hand:
;
;   iscc /DAppVersion=1.2.3 /DExePath=..\..\dist\gwatch.exe gwatch.iss
;
; It installs gwatch.exe to {autopf}\GWatch, registers it as the "GWatch"
; Windows service (matching `gwatch install`, see main.go), keeps data in
; %ProgramData%\GWatch, and offers a Start Menu / desktop shortcut that runs
; `gwatch open`. See scripts/installer/README.md and docs/INSTALL.md.

#ifndef AppVersion
  #define AppVersion "0.0.0"
#endif
#ifndef ExePath
  #define ExePath "..\..\dist\gwatch.exe"
#endif

#define AppName "GWatch"
#include "brand.iss"

[Setup]
; Fixed installer AppId (GUID). Do not change between releases -- Windows
; uses it to recognise upgrades vs. a fresh install. The doubled "{{" is
; Inno Setup's escape for a literal "{".
AppId={{E4C1B6A2-8F3D-4B9E-9A7C-2D5F1E6B8C4A}
AppName={#AppName}
AppVersion={#AppVersion}
AppVerName={#AppName} {#AppVersion}
AppPublisher={#Publisher}
AppPublisherURL={#ProjectURL}
AppSupportURL={#ProjectURL}/issues
AppUpdatesURL={#ProjectURL}/releases
AppCopyright={#CopyrightLine}
VersionInfoVersion={#FileVersion}
VersionInfoCompany={#Publisher}
VersionInfoCopyright={#CopyrightLine}
VersionInfoDescription={#AppName} -- local network & service monitor
VersionInfoProductName={#AppName}
DefaultDirName={autopf}\{#AppName}
DefaultGroupName={#AppName}
DisableProgramGroupPage=yes
DisableWelcomePage=no
AllowNoIcons=yes
Compression=lzma2/max
SolidCompression=yes
ArchitecturesInstallIn64BitMode=x64compatible
PrivilegesRequired=admin
WizardStyle=modern
OutputDir=Output
OutputBaseFilename=gwatch-setup-{#AppVersion}
UninstallDisplayName={#AppName} {#AppVersion}
; gwatch.exe carries its own icon now (see cmd/gwatch-rsrc and the
; rsrc_windows_*.syso objects), so Add/Remove Programs and the shortcuts
; can point straight at the executable.
UninstallDisplayIcon={app}\gwatch.exe
SetupLogging=yes

; ---- Branding -------------------------------------------------------------
; The licence page carries the GWatch Community License verbatim plus a plain
; digest of the Terms, Privacy Policy and security disclaimer, so nobody has
; to have read the repository to know what they are agreeing to.
LicenseFile=license.txt
SetupIconFile=assets\gwatch.ico
; Two sizes each: Inno picks by the display's DPI rather than upscaling.
WizardImageFile=assets\wizard-large.bmp,assets\wizard-large-2x.bmp
; The inner pages' header is graphite now (see style.iss), so the badge that
; sits on it is the inverted mark rather than the one drawn for white.
WizardSmallImageFile=assets\wizard-small-dark.bmp,assets\wizard-small-dark-2x.bmp
WizardImageStretch=yes
; A little more room than the default: the network page has a paragraph on it
; that should not need three lines to say eight words.
WizardSizePercent=110

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Messages]
WelcomeLabel1=Install [name]
WelcomeLabel2=[name/ver] watches the devices, servers and websites on your network and tells you when one stops answering.%n%nIt runs entirely on this computer: your monitoring data stays in a database here, and nothing is sent to {#Publisher}.%n%nDeveloped by {#Developers}.{#BetaNote}
FinishedHeadingLabel=GWatch is running
FinishedLabel=GWatch is installed as a Windows service and starts with this computer.%n%nOne thing is worth doing now: GWatch has no access password until you set one. Open Settings > Users & access in the web interface before anyone else can reach this computer.

[CustomMessages]
NetworkPageCaption=Network access
NetworkPageDescription=Choose the port GWatch listens on and whether other devices on your network can reach it.

[Tasks]
Name: "desktopicon"; Description: "Create a &desktop icon"; GroupDescription: "Additional icons:"; Flags: unchecked

[Files]
Source: "{#ExePath}"; DestDir: "{app}"; DestName: "gwatch.exe"; Flags: ignoreversion
; The licence and the terms digest travel with the install, so they are still
; readable on a machine that has no way to reach GitHub. The icon does not
; need shipping separately -- it is inside gwatch.exe.
Source: "license.txt"; DestDir: "{app}"; DestName: "LICENSE.txt"; Flags: ignoreversion

[Icons]
Name: "{group}\GWatch Monitor"; Filename: "{app}\gwatch.exe"; Parameters: "open --listen ""{code:GetListenAddr}"""; Comment: "Open the GWatch web interface"
Name: "{group}\GWatch documentation"; Filename: "{#DocsURL}"; Comment: "Guides, recipes and reference on GitHub"
Name: "{group}\Licence and terms"; Filename: "{app}\LICENSE.txt"; Comment: "The GWatch Community License and a summary of the terms"
Name: "{group}\Uninstall GWatch"; Filename: "{uninstallexe}"
Name: "{autodesktop}\GWatch Monitor"; Filename: "{app}\gwatch.exe"; Parameters: "open --listen ""{code:GetListenAddr}"""; Tasks: desktopicon; Comment: "Open the GWatch web interface"

[Run]
; Fresh install: register + start the service (gwatch install starts it
; automatically, see main.go's "install" case).
Filename: "{app}\gwatch.exe"; Parameters: "install --data-dir ""{commonappdata}\GWatch"" --listen ""{code:GetListenAddr}"""; StatusMsg: "Registering the GWatch service..."; Flags: runhidden waituntilterminated; Check: NeedsInstall
; Upgrade: the service is already registered (gwatch install would fail),
; so just start it again -- it was stopped in PrepareToInstall below.
Filename: "{app}\gwatch.exe"; Parameters: "start"; StatusMsg: "Starting the GWatch service..."; Flags: runhidden waituntilterminated; Check: NeedsStart
; Optional firewall rule for LAN access.
Filename: "netsh"; Parameters: "advfirewall firewall add rule name=""GWatch"" dir=in action=allow protocol=TCP localport={code:GetPort}"; StatusMsg: "Adding a Windows Firewall rule for GWatch..."; Flags: runhidden; Check: WantLan
; Finish-page checkbox.
Filename: "{app}\gwatch.exe"; Parameters: "open --listen ""{code:GetListenAddr}"""; Description: "Open GWatch in your browser"; Flags: postinstall skipifsilent nowait

[UninstallRun]
Filename: "{app}\gwatch.exe"; Parameters: "uninstall"; RunOnceId: "GWatchServiceUninstall"; Flags: runhidden waituntilterminated
Filename: "netsh"; Parameters: "advfirewall firewall delete rule name=""GWatch"""; RunOnceId: "GWatchFirewallRuleRemove"; Flags: runhidden

[Code]
var
  NetworkPage: TWizardPage;
  PortEdit: TNewEdit;
  LanCheck: TNewCheckBox;
  NoteLabel: TNewStaticText;

// The wizard skin. It is included here, after this script's own
// declarations, so that its procedures are defined before
// InitializeWizard below calls them.
#include "style.iss"

{ ---------------------------------------------------------------------- }
{ Custom "Network access" wizard page: port + "allow other devices"      }
{ checkbox, shown between the install-folder page and the ready page.    }
{ ---------------------------------------------------------------------- }
procedure InitializeWizard;
var
  PortLabel: TNewStaticText;
begin
  NetworkPage := CreateCustomPage(wpSelectDir,
    ExpandConstant('{cm:NetworkPageCaption}'),
    ExpandConstant('{cm:NetworkPageDescription}'));

  PortLabel := TNewStaticText.Create(NetworkPage);
  PortLabel.Parent := NetworkPage.Surface;
  PortLabel.Left := 0;
  PortLabel.Top := 8;
  PortLabel.Width := 40;
  PortLabel.Caption := 'Port:';

  PortEdit := TNewEdit.Create(NetworkPage);
  PortEdit.Parent := NetworkPage.Surface;
  PortEdit.Left := PortLabel.Left + 48;
  PortEdit.Top := PortLabel.Top - 4;
  PortEdit.Width := 80;
  PortEdit.Text := ExpandConstant('{param:PORT|8080}');

  LanCheck := TNewCheckBox.Create(NetworkPage);
  LanCheck.Parent := NetworkPage.Surface;
  LanCheck.Left := 0;
  LanCheck.Top := PortEdit.Top + 32;
  LanCheck.Width := NetworkPage.SurfaceWidth;
  LanCheck.Caption := 'Allow other devices on my network to open GWatch';
  LanCheck.Checked := (ExpandConstant('{param:LAN|0}') = '1');

  NoteLabel := TNewStaticText.Create(NetworkPage);
  NoteLabel.Parent := NetworkPage.Surface;
  NoteLabel.Left := 0;
  NoteLabel.Top := LanCheck.Top + 28;
  NoteLabel.Width := NetworkPage.SurfaceWidth;
  NoteLabel.AutoSize := False;
  NoteLabel.WordWrap := True;
  NoteLabel.Height := 84;
  SkinMono(PortEdit);
  SkinNote(NoteLabel);

  NoteLabel.Caption :=
    'GWatch has no access password by default. After installing, open ' +
    'Settings > Users & access in the web interface and set a password (or ' +
    'create user accounts) before relying on the checkbox above -- anyone ' +
    'who can reach this port on your network will otherwise see everything.' + #13#10 + #13#10 +
    'Do not forward this port to the internet. See docs/REMOTE-ACCESS.md for ' +
    'the safe way to reach GWatch from outside your home.';

  ApplyGWatchSkin;
end;

{ Every page is skinned as it is shown: some of the wizard's controls do not
  exist until their page is first needed, and a page that arrived unskinned
  would be a white rectangle in the middle of a dark wizard. }
procedure CurPageChanged(CurPageID: Integer);
begin
  ApplyGWatchSkin;
end;

{ Validate the port field before leaving the Network access page. }
function NextButtonClick(CurPageID: Integer): Boolean;
var
  PortNum: Integer;
begin
  Result := True;
  if (NetworkPage <> nil) and (CurPageID = NetworkPage.ID) then
  begin
    PortNum := StrToIntDef(Trim(PortEdit.Text), -1);
    if (PortNum < 1) or (PortNum > 65535) then
    begin
      MsgBox('Enter a valid port number between 1 and 65535.', mbError, MB_OK);
      Result := False;
    end;
  end;
end;

// ------------------------------------------------------------------------
// Helpers shared between the wizard, the [Run]/[Icons] code lookups and
// silent installs (/VERYSILENT /PORT=8080 /LAN=1). Written as line comments:
// a "{code:...}" inside a brace comment would end the comment early.
// ------------------------------------------------------------------------
function GetPort(Param: String): String;
begin
  if WizardSilent then
    Result := ExpandConstant('{param:PORT|8080}')
  else
    Result := Trim(PortEdit.Text);
end;

function WantLan: Boolean;
begin
  if WizardSilent then
    Result := (ExpandConstant('{param:LAN|0}') = '1')
  else
    Result := LanCheck.Checked;
end;

function GetListenAddr(Param: String): String;
begin
  if WantLan then
    Result := '0.0.0.0:' + GetPort('')
  else
    Result := '127.0.0.1:' + GetPort('');
end;

{ Detects an existing "GWatch" service registration via `sc query`.       }
function ServiceExists: Boolean;
var
  ResultCode: Integer;
begin
  Exec('sc.exe', 'query GWatch', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
  Result := (ResultCode = 0);
end;

function NeedsInstall: Boolean;
begin
  Result := not ServiceExists;
end;

function NeedsStart: Boolean;
begin
  Result := ServiceExists;
end;

{ Stop the running service (if any) before its executable is overwritten. }
function PrepareToInstall(var NeedsRestart: Boolean): String;
var
  ResultCode: Integer;
  ExistingExe: String;
begin
  Result := '';
  NeedsRestart := False;
  if ServiceExists then
  begin
    ExistingExe := ExpandConstant('{app}\gwatch.exe');
    if FileExists(ExistingExe) then
      Exec(ExistingExe, 'stop', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
  end;
end;

{ ---------------------------------------------------------------------- }
{ Uninstall: ask whether to remove the data directory (default: keep).   }
{ ---------------------------------------------------------------------- }
procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var
  DataDir: String;
begin
  if CurUninstallStep = usPostUninstall then
  begin
    DataDir := ExpandConstant('{commonappdata}\GWatch');
    if DirExists(DataDir) then
    begin
      if MsgBox(
        'Delete the GWatch data directory (' + DataDir + '), including the ' +
        'database, logs and backups?' + #13#10 + #13#10 +
        'Choose No to keep your monitoring history and settings.',
        mbConfirmation, MB_YESNO or MB_DEFBUTTON2) = IDYES then
        DelTree(DataDir, True, True, True);
    end;
  end;
end;
