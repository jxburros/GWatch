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
; in GWatch under Nodes > Add node > GWatch agent), hands both to
; `gwatch-agent install --code`, and that command exchanges the code for this
; machine's own submit-only token before registering the "GWatchAgent"
; service. See docs/HARDWARE.md.

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
; Plain "modern" is Inno's light wizard. The installers are light only, so
; this deliberately names no dark or dynamic variant (newer Inno Setup releases
; accept one), which would follow or force the system dark mode under style.iss.
WizardStyle=modern
OutputDir=Output
OutputBaseFilename=gwatch-agent-setup-{#AppVersion}
UninstallDisplayName={#AppName} {#AppVersion}
; gwatch-agent.exe carries its own icon -- the agent's mark, from
; assets\gwatch-agent.ico (see cmd/gwatch-rsrc and the rsrc_windows_*.syso
; objects) -- so Add/Remove Programs and the shortcuts can point straight at
; the executable.
UninstallDisplayIcon={app}\gwatch-agent.exe
SetupLogging=yes

LicenseFile=license.txt
; The agent's own mark -- the G in a deerstalker -- rather than GWatch's, so
; the setup program looks like the thing it installs. The icon is also the
; uninstaller's, and the one gwatch-agent.exe carries (see UninstallDisplayIcon
; above). All of it is generated from assets\agent-logo-master.png by
; make-assets.py; see README.md. Two sizes of each bitmap: Inno picks by the
; display's DPI rather than upscaling.
SetupIconFile=assets\gwatch-agent.ico
WizardImageFile=assets\agent-wizard-large.bmp,assets\agent-wizard-large-2x.bmp
; The inner pages' header is white (see style.iss) -- the installers are
; light only -- so the badge that sits on it is the mark drawn on white.
WizardSmallImageFile=assets\agent-wizard-small.bmp,assets\agent-wizard-small-2x.bmp
WizardImageStretch=yes
; The connect page asks four questions, has a Test connection button with room
; for its answer, and explains the code underneath them, which is more than the
; default wizard size is comfortable with.
WizardSizePercent=120

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
; The control panel is the agent's front door for someone who does not use a
; terminal (#64). It asks for an administrator's rights itself (UAC) when the
; settings are the administrators' alone, which after this setup they are.
Name: "{group}\GWatch Agent control panel"; Filename: "{app}\gwatch-agent.exe"; Parameters: "ui"; IconFilename: "{app}\gwatch-agent.exe"; Comment: "Status, settings, the log, updates and pairing for this machine's agent"
Name: "{group}\Agent status"; Filename: "{cmd}"; Parameters: "/k """"{app}\gwatch-agent.exe"" status"""; IconFilename: "{app}\gwatch-agent.exe"; Comment: "Show whether the agent service is running"
Name: "{group}\Hardware monitoring guide"; Filename: "{#DocsURL}/HARDWARE.md"; Comment: "What the agent reports and how to read it"
Name: "{group}\Licence and terms"; Filename: "{app}\LICENSE.txt"
Name: "{group}\Uninstall GWatch Agent"; Filename: "{uninstallexe}"

[Run]
; Both are offered on the last page (the first ticked, the second not), and run as
; the person who started setup rather than as the administrator it elevated
; to: a browser should not run elevated, and the notification-area icon
; belongs in that person's sign-in, not the administrator's.
Filename: "{code:GetMachineURL}"; Description: "Open this machine in GWatch"; Flags: postinstall shellexec nowait skipifsilent runasoriginaluser; Check: HasMachineURL
; Optional and off by default (#53). `tray start-at-login` adds a value under
; that person's HKCU Run key and then shows the icon; the uninstaller removes
; the value again (below).
Filename: "{app}\gwatch-agent.exe"; Parameters: "tray start-at-login"; Description: "Show the agent in the notification area (starts when you sign in)"; Flags: postinstall nowait skipifsilent runasoriginaluser runhidden unchecked

[UninstallRun]
Filename: "{app}\gwatch-agent.exe"; Parameters: "uninstall"; RunOnceId: "GWatchAgentServiceUninstall"; Flags: runhidden waituntilterminated
Filename: "{sys}\sc.exe"; Parameters: "delete GWatchAgent"; RunOnceId: "GWatchAgentServiceFallback"; Flags: runhidden waituntilterminated
; The notification-area icon: stop it starting at sign-in, then end any copy
; still running (the service is gone by now, so every gwatch-agent.exe left is
; an icon or a control panel), so that its executable can be removed.
Filename: "{app}\gwatch-agent.exe"; Parameters: "tray stop-at-login"; RunOnceId: "GWatchAgentTrayLogin"; Flags: runhidden waituntilterminated
Filename: "{sys}\taskkill.exe"; Parameters: "/F /IM gwatch-agent.exe"; RunOnceId: "GWatchAgentTrayStop"; Flags: runhidden waituntilterminated

[Code]
var
  ConnectPage: TWizardPage;
  ServerEdit: TNewEdit;
  CodeEdit: TNewEdit;
  NameEdit: TNewEdit;
  InsecureCheck: TNewCheckBox;
  TestButton: TNewButton;
  TestResult: TNewStaticText;
  { How the pairing went, for the last page to report (#69): 0 an upgrade
    (already paired), 1 paired just now, 2 the pairing failed. }
  PairOutcome: Integer;
  { The last connection test (#63): what was tested, and whether it worked,
    so that pressing Next after a successful test does not test again. }
  TestedServer: String;
  TestedInsecure: Boolean;
  TestedOK: Boolean;
  TestMessage: String;

// The wizard skin. It is included here, after this script's own
// declarations, so that its procedures are defined before
// InitializeWizard below calls them.
#include "style.iss"

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

{ Where `gwatch-agent probe` and `gwatch-agent install` leave their outcome
  (--result-file): a small INI file, because GetIniString reads one in a
  single call. The agent writes it as UTF-16 with a byte-order mark, which is
  what lets a machine name that is not plain ASCII come through intact. }
function ProbeResultFile: String;
begin
  Result := ExpandConstant('{tmp}\probe-result.ini');
end;

function InstallResultFile: String;
begin
  Result := ExpandConstant('{tmp}\install-result.ini');
end;

{ Runs `gwatch-agent probe` on the address, from a copy of the agent taken
  out of this setup program, since nothing is installed yet. It reaches the
  server, checks its certificate under the setting chosen, and checks that
  what answers is a GWatch -- and presents no pairing code, so a test spends
  nothing. Returns whether it worked; TestMessage says how, in one line. }
function RunProbe(Server: String; Insecure: Boolean): Boolean;
var
  Exe, Args: String;
  ResultCode: Integer;
begin
  Exe := ExpandConstant('{tmp}\gwatch-agent.exe');
  { ExtractTemporaryFile finds the file by the name it is installed under
    (DestName in [Files]), which is gwatch-agent.exe. }
  if not FileExists(Exe) then
    ExtractTemporaryFile('gwatch-agent.exe');
  DeleteFile(ProbeResultFile);
  Args := 'probe --server "' + Server + '" --result-file "' + ProbeResultFile + '"';
  if Insecure then
    Args := Args + ' --insecure';
  if not Exec(Exe, Args, '', SW_HIDE, ewWaitUntilTerminated, ResultCode) then
    ResultCode := -1;
  Result := (ResultCode = 0);
  TestMessage := GetIniString('result', 'message', '', ProbeResultFile);
  if TestMessage = '' then
    TestMessage := 'The connection could not be tested (the agent did not run).';
  TestedServer := Server;
  TestedInsecure := Insecure;
  TestedOK := Result;
end;

procedure TestButtonClick(Sender: TObject);
begin
  if Trim(ServerEdit.Text) = '' then
  begin
    TestResult.Caption := 'Enter the server address first.';
    Exit;
  end;
  TestButton.Enabled := False;
  TestResult.Caption := 'Testing...';
  if RunProbe(Trim(ServerEdit.Text), InsecureCheck.Checked) then
    TestResult.Caption := 'Connected. ' + TestMessage
  else
    TestResult.Caption := 'Not connected: ' + TestMessage;
  TestButton.Enabled := True;
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
  ServerEdit.Text := ExpandConstant('{param:SERVER|http://gwatch.lan:7230}');

  AddLabel(ConnectPage, 48, 'Pairing code (GWatch > Nodes > Add node > GWatch agent)');
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

  { Test connection (#63): proves the address before the service is
    installed, without presenting the pairing code, which is good once. }
  TestButton := TNewButton.Create(ConnectPage);
  TestButton.Parent := ConnectPage.Surface;
  TestButton.Left := 0;
  TestButton.Top := 168;
  TestButton.Width := 120;
  TestButton.Height := 23;
  TestButton.Caption := 'Test connection';
  TestButton.OnClick := @TestButtonClick;

  TestResult := TNewStaticText.Create(ConnectPage);
  TestResult.Parent := ConnectPage.Surface;
  TestResult.Left := 128;
  TestResult.Top := 168;
  TestResult.Width := ConnectPage.SurfaceWidth - 128;
  TestResult.AutoSize := False;
  TestResult.WordWrap := True;
  TestResult.Height := 34;
  TestResult.Caption := 'Checks the address answers as a GWatch. The code is not used.';

  Note := TNewStaticText.Create(ConnectPage);
  Note.Parent := ConnectPage.Surface;
  Note.Left := 0;
  Note.Top := 206;
  Note.Width := ConnectPage.SurfaceWidth;
  Note.Caption :=
    'A pairing code is good for one machine and expires after about fifteen ' +
    'minutes. It is exchanged during installation for a token that can only ' +
    'submit this machine''s readings -- it grants no other access to GWatch, ' +
    'and GWatch is given no way back into this computer.';
  Note.AutoSize := False;
  Note.WordWrap := True;
  Note.Height := 56;

  { The server address and the pairing code are machine text, so they are typed
    in the face the application shows machine text in. }
  SkinMono(ServerEdit);
  SkinMono(CodeEdit);
  SkinNote(Note);
  SkinNote(TestResult);
  ApplyGWatchSkin;
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
             'http://gwatch.lan:7230', mbError, MB_OK);
      Result := False;
    end
    else if GetCode = '' then
    begin
      MsgBox('Enter the eight-character pairing code shown in GWatch under ' +
             'Hardware.' + #13#10 + #13#10 +
             'It looks like ABCD-2345. The letters I, L, O and U and the ' +
             'digits 0 and 1 never appear in one.', mbError, MB_OK);
      Result := False;
    end
    else if not (TestedOK and (TestedServer = GetServer) and (TestedInsecure = WantInsecure)) then
    begin
      { Not tested yet, or tested with something else: test now, since the
        code is spent the moment the service is installed. A failure is the
        person's call -- the server may simply be down for a minute -- and a
        silent install goes ahead, as it always has, and reports the outcome
        in its exit status. }
      if not RunProbe(GetServer, WantInsecure) then
      begin
        if not WizardSilent then
          TestResult.Caption := 'Not connected: ' + TestMessage;
        if SuppressibleMsgBox('This computer could not confirm that ' + GetServer +
             ' is a GWatch server it can reach:' + #13#10 + #13#10 + TestMessage + #13#10 + #13#10 +
             'Installing now would very likely spend the pairing code for nothing. ' +
             'Install anyway?', mbConfirmation, MB_YESNO, IDYES) = IDNO then
          Result := False;
      end
      else if not WizardSilent then
        TestResult.Caption := 'Connected. ' + TestMessage;
    end;
  end;
end;

{ For the "Open this machine in GWatch" box on the last page: the link the
  install step's result names, when pairing worked. }
function GetMachineURL(Param: String): String;
begin
  Result := GetIniString('result', 'url', '', InstallResultFile);
end;

function HasMachineURL: Boolean;
begin
  Result := (PairOutcome = 1) and (GetMachineURL('') <> '');
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
  // With the service stopped, any gwatch-agent.exe still running from the
  // install folder is the notification-area icon or a control panel, and
  // either would keep the executable from being replaced. The icon comes back
  // at the next sign-in; the temporary copy that tested the connection has
  // already finished. (A brace comment cannot hold the folder's constant: its
  // closing brace would end the comment.)
  if FileExists(ExpandConstant('{app}\gwatch-agent.exe')) then
    Exec(ExpandConstant('{sys}\taskkill.exe'), '/F /IM gwatch-agent.exe', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
end;

{ The last page says what actually happened (#69). Pairing is the step the
  whole install is for, so a person who typed a code is told plainly that it
  worked -- and which server this machine now reports to -- rather than being
  shown the same words whether it worked or not. }
procedure ShowPairOutcome;
var
  PairedName, Service, Why: String;
begin
  { What the install step itself said (#63): the name GWatch gave this
    machine, the state of the service, or the reason it failed, rather than
    the wizard's guess at them. }
  PairedName := GetIniString('result', 'name', '', InstallResultFile);
  Service := GetIniString('result', 'service', '', InstallResultFile);
  Why := GetIniString('result', 'message', '', InstallResultFile);
  { The agent names the machine only once GWatch has paired it, so a failure
    that still carries a name failed after pairing: most often the service
    would not start, or the first reading did not go through. }
  if (PairOutcome = 2) and (PairedName <> '') then
  begin
    WizardForm.FinishedHeadingLabel.Caption := 'Paired, but not reporting yet';
    WizardForm.FinishedLabel.Caption :=
      'Paired with ' + GetServer + ' as ' + PairedName + ', but then:' + #13#10 + #13#10 +
      Why + #13#10 + #13#10 +
      'The GWatch Agent control panel in the Start menu shows the service and its ' +
      'log, and can start it or pair the machine again.';
    Exit;
  end;
  if PairedName = '' then
    PairedName := GetMachineName;
  if Service = '' then
    Service := 'running';
  case PairOutcome of
    1:
      begin
        WizardForm.FinishedHeadingLabel.Caption := 'Paired with GWatch';
        if PairedName <> '' then
          WizardForm.FinishedLabel.Caption := 'Paired as ' + PairedName + ', service ' + Service + '.'
        else
          WizardForm.FinishedLabel.Caption := 'Paired, service ' + Service + '.';
        WizardForm.FinishedLabel.Caption := WizardForm.FinishedLabel.Caption + #13#10 + #13#10 +
          'This computer reports to ' + GetServer + ', and GWatch has accepted its ' +
          'first reading. The agent runs as a Windows service and starts with this ' +
          'computer; the GWatch Agent control panel in the Start menu shows what it ' +
          'is doing.';
      end;
    2:
      begin
        WizardForm.FinishedHeadingLabel.Caption := 'Installed, but not paired';
        if Why = '' then
          Why := 'the agent did not say why';
        WizardForm.FinishedLabel.Caption :=
          'The agent is installed but could not pair with ' + GetServer + ', so ' +
          'this computer is not reporting yet:' + #13#10 + #13#10 + Why + #13#10 + #13#10 +
          'Get a fresh pairing code in GWatch and pair it from the GWatch Agent ' +
          'control panel in the Start menu, or from an Administrator command prompt:' + #13#10 + #13#10 +
          '  gwatch-agent install --server <address> --code <code>';
      end;
  end;
end;

{ Every page is skinned as it is shown: some of the wizard's controls do not
  exist until their page is first needed, and a page that arrived unskinned
  would be a patch of stock Windows grey in the middle of the wizard. }
procedure CurPageChanged(CurPageID: Integer);
begin
  ApplyGWatchSkin;
  if CurPageID = wpFinished then
    ShowPairOutcome;
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

  if ServiceExists and (GetCode = '') then
  begin
    { An upgrade: the machine is already paired and holds its token, so the
      service only has to come back up with the newly installed executable. }
    PairOutcome := 0;
    Exec(Exe, 'start', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
    Exit;
  end;

  if ServiceExists then
    Exec(Exe, 'uninstall', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);

  DeleteFile(InstallResultFile);
  Args := 'install --server "' + GetServer + '" --code ' + GetCode +
    ' --result-file "' + InstallResultFile + '"';
  if GetMachineName <> '' then
    Args := Args + ' --name "' + GetMachineName + '"';
  if WantInsecure then
    Args := Args + ' --insecure';

  if not Exec(Exe, Args, '', SW_HIDE, ewWaitUntilTerminated, ResultCode) then
    ResultCode := -1;
  if ResultCode = 0 then
    PairOutcome := 1
  else
    PairOutcome := 2;
  if ResultCode <> 0 then
    Answer := SuppressibleMsgBox(
      'The agent could not pair with ' + GetServer + ':' + #13#10 + #13#10 +
      GetIniString('result', 'message', '(no reason was given)', InstallResultFile) + #13#10 + #13#10 +
      'The usual causes are a pairing code that has expired or already been ' +
      'used, a server address this computer cannot reach, or GWatch not ' +
      'running. The files have been installed, so you can finish the pairing ' +
      'from an Administrator command prompt once you have a fresh code:' + #13#10 + #13#10 +
      '  "' + Exe + '" install --server <address> --code <code>',
      mbError, MB_OK, IDOK);
end;
