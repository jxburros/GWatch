; GWatch Agent Windows installer (Inno Setup 6).
;
; The agent is the small program that goes on the OTHER machines -- the ones
; you want GWatch to report the health of. It is a separate setup program from
; gwatch.iss on purpose: this is what you carry to a file server or a spare
; laptop, and it must not drag the monitor, its database or its web interface
; along with it.
;
; Builds gwatch-agent-setup-<version>.exe from an already-built
; gwatch-agent.exe:
;
;   iscc /DAppVersion=1.2.3 /DExePath=..\..\dist\gwatch-agent-windows-amd64.exe gwatch-agent.iss
;
; The wizard asks for the GWatch server's address and a pairing code (get one
; in GWatch under Hardware), hands both to `gwatch-agent install --code`, and
; that command exchanges the code for this machine's own submit-only token
; before registering the "GWatchAgent" service. See docs/HARDWARE.md.

#ifndef AppVersion
  #define AppVersion "0.0.0"
#endif
#ifndef ExePath
  #define ExePath "..\..\dist\gwatch-agent-windows-amd64.exe"
#endif

#define AppName "GWatch Agent"
#define ServiceName "GWatchAgent"
#include "brand.iss"

[Setup]
; Its own AppId, distinct from the monitor's, so installing the agent on a
; machine that also runs GWatch does not look like an upgrade of either.
AppId={{7B2F9D14-6C58-4A31-B0E7-93A4C1D82F65}
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
VersionInfoDescription={#AppName} -- reports this computer's hardware health
VersionInfoProductName={#AppName}
DefaultDirName={autopf}\GWatch Agent
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
OutputBaseFilename=gwatch-agent-setup-{#AppVersion}
UninstallDisplayName={#AppName} {#AppVersion}
; gwatch-agent.exe carries its own icon now (see cmd/gwatch-rsrc and the
; rsrc_windows_*.syso objects), so Add/Remove Programs and the shortcuts
; can point straight at the executable.
UninstallDisplayIcon={app}\gwatch-agent.exe
SetupLogging=yes

LicenseFile=license.txt
SetupIconFile=assets\gwatch.ico
WizardImageFile=assets\wizard-large.bmp,assets\wizard-large-2x.bmp
WizardSmallImageFile=assets\wizard-small.bmp,assets\wizard-small-2x.bmp
WizardImageStretch=yes

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Messages]
WelcomeLabel1=Install [name]
WelcomeLabel2=The GWatch Agent reports this computer's processor, memory, disk and network health to a GWatch server you already run.%n%nIt only ever sends readings out. Nothing can reach this computer through it, and the credential it ends up holding can do one thing: submit this machine's readings.%n%nDeveloped by {#Developers}.{#BetaNote}
FinishedHeadingLabel=This machine is reporting
FinishedLabel=The agent is installed as a Windows service and starts with this computer.%n%nIt should appear on the GWatch server, on its own node, within a minute.

[CustomMessages]
ConnectCaption=Connect to GWatch
ConnectDescription=Tell the agent which GWatch server to report to, and prove it is allowed to.

[Files]
Source: "{#ExePath}"; DestDir: "{app}"; DestName: "gwatch-agent.exe"; Flags: ignoreversion
Source: "license.txt"; DestDir: "{app}"; DestName: "LICENSE.txt"; Flags: ignoreversion

[Icons]
Name: "{group}\Agent status"; Filename: "{cmd}"; Parameters: "/k """"{app}\gwatch-agent.exe"" status"""; IconFilename: "{app}\gwatch-agent.exe"; Comment: "Show whether the agent service is running"
Name: "{group}\Hardware monitoring guide"; Filename: "{#DocsURL}/HARDWARE.md"; Comment: "What the agent reports and how to read it"
Name: "{group}\Licence and terms"; Filename: "{app}\LICENSE.txt"
Name: "{group}\Uninstall GWatch Agent"; Filename: "{uninstallexe}"

[UninstallRun]
Filename: "{app}\gwatch-agent.exe"; Parameters: "uninstall"; RunOnceId: "GWatchAgentServiceUninstall"; Flags: runhidden waituntilterminated

[Code]
var
  ConnectPage: TWizardPage;
  ServerEdit: TNewEdit;
  CodeEdit: TNewEdit;
  NameEdit: TNewEdit;
  InsecureCheck: TNewCheckBox;

{ The alphabet a pairing code is drawn from. Kept in step with
  auth.PairingCodeAlphabet in internal/auth/token.go -- no I, L, O, U, 0 or 1,
  so nothing a person types can be a misread of something else. }
const
  CodeAlphabet = '23456789ABCDEFGHJKMNPQRSTVWXYZ';
  CodeLen = 8;

procedure AddLabel(Page: TWizardPage; ATop: Integer; ACaption: String);
var
  L: TNewStaticText;
begin
  L := TNewStaticText.Create(Page);
  L.Parent := Page.Surface;
  L.Left := 0;
  L.Top := ATop;
  L.Width := Page.SurfaceWidth;
  L.Caption := ACaption;
end;

procedure InitializeWizard;
var
  Note: TNewStaticText;
begin
  ConnectPage := CreateCustomPage(wpSelectDir,
    ExpandConstant('{cm:ConnectCaption}'),
    ExpandConstant('{cm:ConnectDescription}'));

  AddLabel(ConnectPage, 0, 'GWatch server address');
  ServerEdit := TNewEdit.Create(ConnectPage);
  ServerEdit.Parent := ConnectPage.Surface;
  ServerEdit.Left := 0;
  ServerEdit.Top := 16;
  ServerEdit.Width := ConnectPage.SurfaceWidth;
  ServerEdit.Text := ExpandConstant('{param:SERVER|http://gwatch.lan:8080}');

  AddLabel(ConnectPage, 48, 'Pairing code (GWatch > Hardware > Pair a machine)');
  CodeEdit := TNewEdit.Create(ConnectPage);
  CodeEdit.Parent := ConnectPage.Surface;
  CodeEdit.Left := 0;
  CodeEdit.Top := 64;
  CodeEdit.Width := 160;
  CodeEdit.Text := ExpandConstant('{param:CODE|}');
  CodeEdit.CharCase := ecUpperCase;

  AddLabel(ConnectPage, 96, 'Name for this machine in GWatch (optional)');
  NameEdit := TNewEdit.Create(ConnectPage);
  NameEdit.Parent := ConnectPage.Surface;
  NameEdit.Left := 0;
  NameEdit.Top := 112;
  NameEdit.Width := ConnectPage.SurfaceWidth;
  NameEdit.Text := ExpandConstant('{param:NAME|}');

  InsecureCheck := TNewCheckBox.Create(ConnectPage);
  InsecureCheck.Parent := ConnectPage.Surface;
  InsecureCheck.Left := 0;
  InsecureCheck.Top := 144;
  InsecureCheck.Width := ConnectPage.SurfaceWidth;
  InsecureCheck.Caption := 'The server uses a self-signed certificate';
  InsecureCheck.Checked := (ExpandConstant('{param:INSECURE|0}') = '1');

  Note := TNewStaticText.Create(ConnectPage);
  Note.Parent := ConnectPage.Surface;
  Note.Left := 0;
  Note.Top := 172;
  Note.Width := ConnectPage.SurfaceWidth;
  Note.Caption :=
    'A pairing code is good for one machine and expires after about fifteen ' +
    'minutes. It is exchanged during installation for a token that can only ' +
    'submit this machine''s readings -- it grants no other access to GWatch, ' +
    'and GWatch is given no way back into this computer.';
  Note.AutoSize := False;
  Note.WordWrap := True;
  Note.Height := 60;
end;

{ Strips dashes, spaces and case so the code can be typed however it reads
  most naturally, then checks the shape. Same normalisation as
  auth.NormalizePairingCode; the server is the real authority, this only
  catches a typo before the wizard bothers contacting it. }
function NormalizeCode(S: String): String;
var
  I: Integer;
  C: Char;
begin
  Result := '';
  S := Uppercase(Trim(S));
  for I := 1 to Length(S) do
  begin
    C := S[I];
    { Dashes and spaces are presentation only and are dropped; anything that
      is not in the alphabet means this is not a pairing code at all. }
    if (C <> '-') and (C <> ' ') then
    begin
      if Pos(C, CodeAlphabet) = 0 then
      begin
        Result := '';
        Exit;
      end;
      Result := Result + C;
    end;
  end;
  if Length(Result) <> CodeLen then
    Result := '';
end;

function GetServer: String;
begin
  if WizardSilent then Result := ExpandConstant('{param:SERVER|}')
  else Result := Trim(ServerEdit.Text);
end;

function GetCode: String;
begin
  if WizardSilent then Result := NormalizeCode(ExpandConstant('{param:CODE|}'))
  else Result := NormalizeCode(CodeEdit.Text);
end;

function GetMachineName: String;
begin
  if WizardSilent then Result := ExpandConstant('{param:NAME|}')
  else Result := Trim(NameEdit.Text);
end;

function WantInsecure: Boolean;
begin
  if WizardSilent then Result := (ExpandConstant('{param:INSECURE|0}') = '1')
  else Result := InsecureCheck.Checked;
end;

function NextButtonClick(CurPageID: Integer): Boolean;
begin
  Result := True;
  if (ConnectPage <> nil) and (CurPageID = ConnectPage.ID) then
  begin
    if GetServer = '' then
    begin
      MsgBox('Enter the address of your GWatch server, for example' + #13#10 +
             'http://gwatch.lan:8080', mbError, MB_OK);
      Result := False;
    end
    else if GetCode = '' then
    begin
      MsgBox('Enter the eight-character pairing code shown in GWatch under ' +
             'Hardware.' + #13#10 + #13#10 +
             'It looks like ABCD-2345. The letters I, L, O and U and the ' +
             'digits 0 and 1 never appear in one.', mbError, MB_OK);
      Result := False;
    end;
  end;
end;

function ServiceExists: Boolean;
var
  ResultCode: Integer;
begin
  Exec('sc.exe', 'query {#ServiceName}', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
  Result := (ResultCode = 0);
end;

{ Stop a running agent before its executable is replaced. }
function PrepareToInstall(var NeedsRestart: Boolean): String;
var
  ResultCode: Integer;
  ExistingExe: String;
begin
  Result := '';
  NeedsRestart := False;
  if ServiceExists then
  begin
    ExistingExe := ExpandConstant('{app}\gwatch-agent.exe');
    if FileExists(ExistingExe) then
      Exec(ExistingExe, 'stop', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
  end;
end;

{ Pairing happens here rather than in [Run] because it is the one step that
  can fail for a reason the person can fix -- a mistyped code, a code that has
  already been used, a server that is not answering -- and a [Run] entry would
  swallow the exit status and leave a service installed that never reports. }
procedure CurStepChanged(CurStep: TSetupStep);
var
  Exe, Args: String;
  ResultCode, Answer: Integer;
begin
  if CurStep <> ssPostInstall then Exit;
  Exe := ExpandConstant('{app}\gwatch-agent.exe');

  if ServiceExists then
  begin
    { An upgrade: the machine is already paired and holds its token, so the
      service only has to come back up with the newly installed executable. }
    Exec(Exe, 'start', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
    Exit;
  end;

  Args := 'install --server "' + GetServer + '" --code ' + GetCode;
  if GetMachineName <> '' then
    Args := Args + ' --name "' + GetMachineName + '"';
  if WantInsecure then
    Args := Args + ' --insecure';

  if not Exec(Exe, Args, '', SW_HIDE, ewWaitUntilTerminated, ResultCode) then
    ResultCode := -1;
  if ResultCode <> 0 then
    Answer := SuppressibleMsgBox(
      'The agent could not pair with ' + GetServer + '.' + #13#10 + #13#10 +
      'The usual causes are a pairing code that has expired or already been ' +
      'used, a server address this computer cannot reach, or GWatch not ' +
      'running. The files have been installed, so you can finish the pairing ' +
      'from an Administrator command prompt once you have a fresh code:' + #13#10 + #13#10 +
      '  "' + Exe + '" install --server <address> --code <code>',
      mbError, MB_OK, IDOK);
end;
