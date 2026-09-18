; Shared GWatch installer branding -- included by gwatch.iss and
; gwatch-agent.iss so the two setup programs cannot drift apart.
;
; The look is the app's own "Signal" skin (see web/app.css): warm graphite,
; square corners, a teal accent, and status colour used only for status. The
; wizard bitmaps in assets\ are generated from the master logo; see README.md.

#define Publisher "JX Holdings, LLC"
#define Developers "Jeffrey Guntly and Garrett Guntly"
#define ProjectURL "https://github.com/jxburros/GWatch"
#define DocsURL "https://github.com/jxburros/GWatch/blob/main/docs"
#define CopyrightLine "Copyright (c) 2026 JX Holdings. Original developers: Jeffrey Guntly and Garrett Guntly."

; A 0.x version means a beta, so the welcome page says so outright instead of
; leaving people to infer it from the number. It switches itself off at 1.0.
#if Copy(AppVersion, 1, 2) == "0."
  #define BetaNote "%n%nThis is a beta release. It works and your data is kept safely, but features and the layout can still change between releases, and bugs are likelier here than they will be at 1.0."
#else
  #define BetaNote ""
#endif

; VersionInfoVersion must be purely numeric (a.b.c.d), so a pre-release or
; CI suffix such as "1.2.3-rc1" or "0.0.0-ci-abc1234" is cut at the first "-".
#define VersionCut Pos("-", AppVersion)
#if VersionCut > 0
  #define FileVersion Copy(AppVersion, 1, VersionCut - 1)
#else
  #define FileVersion AppVersion
#endif
