# Checks the Inno Setup scripts for the mistakes that are easy to make here and
# expensive to find, because ISCC only runs on Windows and stops at the first
# error it hits.
#
#   pwsh -File scripts\installer\lint.ps1
#
# It is not a substitute for compiling. It catches what a compile reports badly
# or not at all:
#
#   1. A line inside [Code] whose first non-whitespace character is "#". ISPP
#      reads that as a preprocessor directive, so a Pascal continuation line
#      starting with #13#10 aborts the compile with "Unknown preprocessor
#      directive" pointing at a line that looks perfectly ordinary.
#   2. Any byte outside ASCII. Inno reads a .iss without a UTF-8 BOM in the
#      system ANSI codepage, so a typographic dash reaches the wizard as
#      mojibake -- which compiles cleanly and looks wrong only on screen.
#   3. Unbalanced begin/end in [Code].
#   4. A routine in [Code] that ends with unbalanced parentheses -- what is left
#      behind when a call is refactored into something else and its closing
#      paren is not removed with it.
#   5. A {code:...} reference with no matching function.
#   6. A Source/LicenseFile/icon/bitmap that is not actually there.
$ErrorActionPreference = "Stop"
$dir = $PSScriptRoot
$failed = $false
function Fail([string]$File, [int]$Line, [string]$Message) {
    $script:failed = $true
    if ($Line -gt 0) { Write-Host "::error file=$File,line=$Line::$Message" }
    else { Write-Host "::error file=$File::$Message" }
}

foreach ($iss in Get-ChildItem -Path $dir -Filter *.iss) {
    $raw = Get-Content $iss.FullName -Raw
    $lines = $raw -split "`r?`n"
    $inCode = $false

    for ($i = 0; $i -lt $lines.Count; $i++) {
        $line = $lines[$i]
        $n = $i + 1
        if ($line -match '^\[(\w+)\]\s*$') { $inCode = ($matches[1] -eq 'Code'); continue }
        if ($inCode -and $line -match '^\s+#') {
            Fail $iss.Name $n "a line inside [Code] starts with '#', which ISPP reads as a preprocessor directive. Move it onto the end of the previous line."
        }
        if ($line -match '[^\x09\x20-\x7E]') {
            Fail $iss.Name $n "non-ASCII character. Inno reads a BOM-less .iss in the system codepage, so this reaches the wizard as mojibake. Use an ASCII equivalent."
        }
    }

    # Strip string literals first (a brace inside one is not a comment), then
    # brace comments across lines, then line comments.
    $code = if ($raw -match '(?s)\[Code\](.*)$') { $matches[1] } else { "" }
    $stripped = [regex]::Replace($code, "'(?:[^'\n]|'')*'", "''")
    $stripped = [regex]::Replace($stripped, '\{[^}]*\}', ' ', 'Singleline')
    $stripped = [regex]::Replace($stripped, '//[^\n]*', ' ')

    $depth = 0
    foreach ($m in [regex]::Matches($stripped, '\b(begin|end|case|record)\b', 'IgnoreCase')) {
        if ($m.Groups[1].Value.ToLower() -in @('begin', 'case', 'record')) { $depth++ } else { $depth-- }
        if ($depth -lt 0) { Fail $iss.Name 0 "an 'end' with no matching 'begin' in [Code]."; break }
    }
    if ($depth -gt 0) { Fail $iss.Name 0 "$depth unclosed begin/case/record block(s) in [Code]." }

    # Parenthesis balance, checked per routine so the report names the routine
    # that is wrong rather than the end of the file. Strings are blanked first:
    # a bracket inside one is text.
    $depth = 0
    $codeStart = ($raw -split "`r?`n").Count - ($code -split "`r?`n").Count
    $codeLines = $code -split "`r?`n"
    for ($i = 0; $i -lt $codeLines.Count; $i++) {
        $t = [regex]::Replace($codeLines[$i], "'(?:[^'\n]|'')*'", "''")
        $t = [regex]::Replace($t, '\{[^}]*\}', ' ')
        $t = [regex]::Replace($t, '//.*$', '')
        foreach ($ch in $t.ToCharArray()) {
            if ($ch -eq '(') { $depth++ }
            elseif ($ch -eq ')') {
                $depth--
                if ($depth -lt 0) { Fail $iss.Name ($codeStart + $i + 1) "an unmatched ')'."; $depth = 0 }
            }
        }
        if ($codeLines[$i] -match '^\s*end;\s*$' -and $depth -ne 0) {
            Fail $iss.Name ($codeStart + $i + 1) "this routine ends with $depth unclosed parenthesis/es."
            $depth = 0
        }
    }

    $defined = @{}
    foreach ($m in [regex]::Matches($stripped, '\b(?:procedure|function)\s+(\w+)', 'IgnoreCase')) {
        $defined[$m.Groups[1].Value.ToLower()] = $true
    }
    foreach ($m in [regex]::Matches($raw, '\{code:(\w+)')) {
        $name = $m.Groups[1].Value
        if (-not $defined.ContainsKey($name.ToLower())) {
            Fail $iss.Name 0 "{code:$name} has no matching function in [Code]."
        }
    }

    foreach ($m in [regex]::Matches($raw, '(?m)^\s*(?:Source:\s*"|LicenseFile=|SetupIconFile=|WizardImageFile=|WizardSmallImageFile=)([^";\r\n]+)')) {
        foreach ($ref in $m.Groups[1].Value -split ',') {
            $ref = $ref.Trim()
            if (-not $ref -or $ref.StartsWith('{')) { continue }   # a constant, resolved at run time
            if (-not (Test-Path (Join-Path $dir $ref))) {
                Fail $iss.Name 0 "references '$ref', which does not exist."
            }
        }
    }
    if (-not $failed) { Write-Host "ok $($iss.Name)" }
}
if ($failed) { exit 1 }
Write-Host "Inno Setup scripts look sane."
