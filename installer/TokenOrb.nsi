; TokenOrb – Windows-Installer (NSIS 3)
; Bauen:  makensis -DVERSION=1.3.0 -DEXE=..\dist\TokenOrb.exe -DOUTFILE=..\dist\TokenOrb-Setup-1.3.0.exe TokenOrb.nsi
;
; - installiert pro Benutzer nach %LOCALAPPDATA%\TokenOrb (keine Adminrechte)
; - Startmenue-Eintrag, Eintrag unter "Apps & Features" mit Deinstallation
; - "TokenOrb.exe --setup" verbindet Claude Code und schaltet beim ersten Mal den Autostart ein
; - still (/S) fuer automatische Updates aus der App: TokenOrb wird beendet,
;   ersetzt und danach wieder gestartet

Unicode true
Target amd64-unicode   ; TokenOrb.exe ist ohnehin 64 Bit
SetCompressor /SOLID lzma
RequestExecutionLevel user

!ifndef VERSION
  !define VERSION "0.0.0"
!endif
!ifndef EXE
  !define EXE "..\dist\TokenOrb.exe"
!endif
!ifndef OUTFILE
  !define OUTFILE "..\dist\TokenOrb-Setup-${VERSION}.exe"
!endif

!define APPNAME "TokenOrb"
!define PUBLISHER "Web-Smith"
!define URL "https://web-smith.de/orb/"
!define UNINSTKEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\TokenOrb"

Name "${APPNAME}"
OutFile "${OUTFILE}"
InstallDir "$LOCALAPPDATA\TokenOrb"
BrandingText "${APPNAME} ${VERSION} · ${PUBLISHER}"
ShowInstDetails nevershow
ShowUninstDetails nevershow

VIProductVersion "${VERSION}.0"
VIAddVersionKey "ProductName" "${APPNAME}"
VIAddVersionKey "CompanyName" "${PUBLISHER}"
VIAddVersionKey "FileDescription" "${APPNAME} Installer"
VIAddVersionKey "FileVersion" "${VERSION}"
VIAddVersionKey "ProductVersion" "${VERSION}"
VIAddVersionKey "LegalCopyright" "${PUBLISHER}"

!include "MUI2.nsh"
!include "FileFunc.nsh"

!define MUI_ICON "..\app\winres\icon.ico"
!define MUI_UNICON "..\app\winres\icon.ico"
!define MUI_ABORTWARNING

!define MUI_WELCOMEPAGE_TITLE "TokenOrb ${VERSION} installieren"
!define MUI_WELCOMEPAGE_TEXT "TokenOrb zeigt deine Claude-Code-Limits auf dem Orb an.$\r$\n$\r$\nDie Installation braucht keine Adminrechte und verbindet sich automatisch mit Claude Code (eine vorhandene Statuszeile bleibt erhalten).$\r$\n$\r$\nKlicke auf Installieren, um fortzufahren."
!define MUI_FINISHPAGE_TITLE "Fertig!"
!define MUI_FINISHPAGE_TEXT "TokenOrb läuft unten rechts im Infobereich (ggf. hinter dem ^-Pfeil) und startet mit Windows.$\r$\n$\r$\nStarte Claude Code einmal neu, damit die Werte ankommen."
!define MUI_FINISHPAGE_RUN "$INSTDIR\TokenOrb.exe"
!define MUI_FINISHPAGE_RUN_PARAMETERS "--open"
!define MUI_FINISHPAGE_RUN_TEXT "TokenOrb jetzt öffnen"

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH

!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "German"

; laufendes TokenOrb beenden (sonst ist die .exe gesperrt)
!macro StopTokenOrb
  nsExec::Exec 'taskkill /F /IM TokenOrb.exe'
  Pop $0
  Sleep 800
!macroend

Section "TokenOrb" SecMain
  SectionIn RO
  !insertmacro StopTokenOrb
  SetOutPath "$INSTDIR"
  ClearErrors
  File "/oname=TokenOrb.exe" "${EXE}"
  IfErrors 0 copied
    ; Datei war noch gesperrt -> nochmal versuchen
    Sleep 1500
    !insertmacro StopTokenOrb
    File "/oname=TokenOrb.exe" "${EXE}"
  copied:
  WriteUninstaller "$INSTDIR\Uninstall.exe"

  ; Startmenue
  CreateShortCut "$SMPROGRAMS\TokenOrb.lnk" "$INSTDIR\TokenOrb.exe" "--open" "$INSTDIR\TokenOrb.exe" 0

  ; Apps & Features
  WriteRegStr HKCU "${UNINSTKEY}" "DisplayName" "${APPNAME}"
  WriteRegStr HKCU "${UNINSTKEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "${UNINSTKEY}" "Publisher" "${PUBLISHER}"
  WriteRegStr HKCU "${UNINSTKEY}" "URLInfoAbout" "${URL}"
  WriteRegStr HKCU "${UNINSTKEY}" "DisplayIcon" "$INSTDIR\TokenOrb.exe"
  WriteRegStr HKCU "${UNINSTKEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKCU "${UNINSTKEY}" "UninstallString" '"$INSTDIR\Uninstall.exe"'
  WriteRegStr HKCU "${UNINSTKEY}" "QuietUninstallString" '"$INSTDIR\Uninstall.exe" /S'
  WriteRegDWORD HKCU "${UNINSTKEY}" "NoModify" 1
  WriteRegDWORD HKCU "${UNINSTKEY}" "NoRepair" 1
  ${GetSize} "$INSTDIR" "/S=0K" $0 $1 $2
  IntFmt $0 "0x%08X" $0
  WriteRegDWORD HKCU "${UNINSTKEY}" "EstimatedSize" "$0"

  ; Claude Code verbinden + Autostart (beim ersten Mal)
  nsExec::Exec '"$INSTDIR\TokenOrb.exe" --setup'
  Pop $0
SectionEnd

; bei stiller Installation (Update aus der App) gleich wieder starten
Function .onInstSuccess
  IfSilent 0 +2
    Exec '"$INSTDIR\TokenOrb.exe"'
FunctionEnd

Section "Uninstall"
  !insertmacro StopTokenOrb
  ; Claude-Code-Statuszeile zuruecksetzen, Autostart entfernen
  nsExec::Exec '"$INSTDIR\TokenOrb.exe" --remove'
  Pop $0
  Delete "$SMPROGRAMS\TokenOrb.lnk"
  DeleteRegKey HKCU "${UNINSTKEY}"
  DeleteRegValue HKCU "Software\Microsoft\Windows\CurrentVersion\Run" "TokenOrb"
  Delete "$INSTDIR\TokenOrb.exe"
  Delete "$INSTDIR\Uninstall.exe"
  ; Einstellungen, Protokoll und Status mit entfernen
  RMDir /r "$INSTDIR"
SectionEnd
