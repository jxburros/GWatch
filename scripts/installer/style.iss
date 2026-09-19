// Shared wizard skin -- included from inside the [Code] section of gwatch.iss
// and gwatch-agent.iss, so both setup programs look like the thing they are
// installing rather than like a generic Windows wizard.
//
// The palette is the application's own "Signal" skin (web/app.css): warm
// graphite, a teal accent, square corners. Delphi's TColor is $00BBGGRR, so the
// constants below read backwards compared with the CSS they come from -- the
// hex comment on each line is the CSS value it matches.
//
// This file is Pascal, not an .iss section, so its comments are "//" and not
// ";" -- a ";" here is a statement separator and the compiler stops on it.
// It carries no section header either: each script includes it inside its own
// [Code] section, after that script's own var block, which keeps these
// procedures defined before they are called and keeps the "#" of the include
// directive in column one, where ISPP expects it.
//
// Nothing here is load-bearing. If a future Inno Setup renames a control, the
// skin is skipped and the wizard is the default one -- an installer that looks
// plain still installs, and an installer that raises an error box on its first
// page does not.
const
  clGWatchBg      = $14110F;  { #0f1114 -- the field }
  clGWatchCard    = $1D1816;  { #16181d -- panels and inputs }
  clGWatchElev    = $2D2622;  { #22262d -- raised surfaces }
  clGWatchText    = $F6F4F2;  { #f2f4f6 -- body text }
  clGWatchMuted   = $ACA39A;  { #9aa3ac -- secondary text }
  clGWatchAccent  = $C0C943;  { #43c9c0 -- the accent }

var
  GWatchAccentRule: TPanel;

{ SkinTree walks a page and colours what it finds. It is written against the
  control classes rather than against named fields so that a page this file has
  never heard of -- a custom one a script adds, or one a later Inno Setup
  introduces -- is skinned on the same terms as the built-in ones. }
procedure SkinTree(Parent: TWinControl);
var
  I: Integer;
  C: TControl;
begin
  for I := 0 to Parent.ControlCount - 1 do
  begin
    C := Parent.Controls[I];
    if C is TNewStaticText then
      TNewStaticText(C).Font.Color := clGWatchText
    else if C is TNewCheckBox then
      TNewCheckBox(C).Font.Color := clGWatchText
    else if C is TNewRadioButton then
      TNewRadioButton(C).Font.Color := clGWatchText
    else if C is TNewEdit then
    begin
      TNewEdit(C).Color := clGWatchCard;
      TNewEdit(C).Font.Color := clGWatchText;
    end
    else if C is TNewMemo then
    begin
      TNewMemo(C).Color := clGWatchElev;
      TNewMemo(C).Font.Color := clGWatchText;
    end
    else if C is TNewCheckListBox then
    begin
      TNewCheckListBox(C).Color := clGWatchCard;
      TNewCheckListBox(C).Font.Color := clGWatchText;
    end
    else if C is TNewNotebookPage then
      TNewNotebookPage(C).Color := clGWatchBg
    else if C is TPanel then
      TPanel(C).Color := clGWatchBg;

    if C is TWinControl then
      SkinTree(TWinControl(C));
  end;
end;

{ The header band. The application puts a two-pixel accent rule under its top
  bar; the wizard gets the same, drawn as a panel pinned to the foot of the
  header so it survives a resize. }
procedure SkinHeader;
begin
  WizardForm.MainPanel.Color := clGWatchCard;
  WizardForm.PageNameLabel.Font.Color := clGWatchText;
  WizardForm.PageDescriptionLabel.Font.Color := clGWatchMuted;
  { Inno's own separator lines sit on a white assumption, so they go and the
    accent rule stands in for them. }
  WizardForm.Bevel.Visible := False;
  if GWatchAccentRule = nil then
  begin
    GWatchAccentRule := TPanel.Create(WizardForm);
    GWatchAccentRule.Parent := WizardForm;
    GWatchAccentRule.BevelOuter := bvNone;
    GWatchAccentRule.Color := clGWatchAccent;
  end;
  GWatchAccentRule.SetBounds(0, WizardForm.MainPanel.Top + WizardForm.MainPanel.Height,
    WizardForm.ClientWidth, ScaleY(2));
end;

{ ApplyGWatchSkin is safe to call as often as you like: every page is skinned
  each time, which is what catches a control the wizard only creates when its
  page is first shown. }
procedure ApplyGWatchSkin;
begin
  try
    WizardForm.Color := clGWatchBg;
    SkinTree(WizardForm);
    SkinHeader;
    { The welcome and finished pages carry the large bitmap, which is already
      the application's field colour, so their text is set against that rather
      than against the page. }
    WizardForm.WelcomeLabel1.Font.Color := clGWatchText;
    WizardForm.WelcomeLabel2.Font.Color := clGWatchMuted;
    WizardForm.FinishedHeadingLabel.Font.Color := clGWatchText;
    WizardForm.FinishedLabel.Font.Color := clGWatchMuted;
  except
    { A skin is not worth failing an install over. }
  end;
end;

{ SkinMono puts an input that holds machine text -- a port, a pairing code, an
  address -- into the monospaced face the application uses for the same thing. }
procedure SkinMono(E: TNewEdit);
begin
  try
    E.Font.Name := 'Consolas';
    E.Font.Size := 10;
  except
    { Consolas is on every supported Windows; if it is not, the default face
      is perfectly readable. }
  end;
end;

{ SkinNote styles the explanatory paragraph at the foot of a custom page the
  way the application styles a note: smaller, quieter, and not competing with
  the thing it is explaining. }
procedure SkinNote(L: TNewStaticText);
begin
  try
    L.Font.Color := clGWatchMuted;
    L.Font.Size := 8;
  except
  end;
end;
