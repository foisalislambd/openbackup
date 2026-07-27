Unicode true

####
## OpenBackup Windows installer — one binary is both the window and the agent.
## Installing over an older copy replaces files (after stopping the running app).
## Uninstall removes the app, login autostart, and local agent state.
####
!include "wails_tools.nsh"

VIProductVersion "${INFO_PRODUCTVERSION}.0"
VIFileVersion    "${INFO_PRODUCTVERSION}.0"

VIAddVersionKey "CompanyName"     "${INFO_COMPANYNAME}"
VIAddVersionKey "FileDescription" "${INFO_PRODUCTNAME} Installer"
VIAddVersionKey "ProductVersion"  "${INFO_PRODUCTVERSION}"
VIAddVersionKey "FileVersion"     "${INFO_PRODUCTVERSION}"
VIAddVersionKey "LegalCopyright"  "${INFO_COPYRIGHT}"
VIAddVersionKey "ProductName"     "${INFO_PRODUCTNAME}"

ManifestDPIAware true

!include "MUI.nsh"

!define MUI_ICON "..\icon.ico"
!define MUI_UNICON "..\icon.ico"
!define MUI_FINISHPAGE_NOAUTOCLOSE
!define MUI_ABORTWARNING

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH

!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "English"

Name "${INFO_PRODUCTNAME}"
OutFile "..\..\bin\${INFO_PROJECTNAME}-${ARCH}-installer.exe"
!ifdef WAILS_INSTALL_SCOPE
  !if "${WAILS_INSTALL_SCOPE}" == "user"
    InstallDir "$LOCALAPPDATA\Programs\${INFO_PRODUCTNAME}"
  !else
    InstallDir "$PROGRAMFILES64\${INFO_COMPANYNAME}\${INFO_PRODUCTNAME}"
  !endif
!else
  InstallDir "$PROGRAMFILES64\${INFO_COMPANYNAME}\${INFO_PRODUCTNAME}"
!endif
ShowInstDetails show

; Stop the running app (window + in-process agent) so files can be replaced
; or removed. Ignores "not found" — that is the usual case on a clean install.
Function StopOpenBackup
  DetailPrint "Stopping OpenBackup if it is running..."
  nsExec::ExecToLog '"$SYSDIR\taskkill.exe" /F /IM ${PRODUCT_EXECUTABLE} /T'
  Pop $0
  Sleep 800
FunctionEnd

; Remove the per-user login Run key so the agent does not come back after uninstall.
Function ClearLoginAutostart
  DetailPrint "Removing login autostart..."
  DeleteRegValue HKCU "Software\Microsoft\Windows\CurrentVersion\Run" "OpenBackup"
FunctionEnd

Function .onInit
   !insertmacro wails.checkArchitecture
FunctionEnd

Section
    !insertmacro wails.setShellContext

    Call StopOpenBackup

    !insertmacro wails.webview2runtime

    SetOutPath $INSTDIR

    !insertmacro wails.files

    CreateShortcut "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"
    CreateShortCut "$DESKTOP\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"

    !insertmacro wails.associateFiles
    !insertmacro wails.associateCustomProtocols

    !insertmacro wails.writeUninstaller
SectionEnd

Section "uninstall"
    !insertmacro wails.setShellContext

    Call un.StopOpenBackup
    Call un.ClearLoginAutostart

    ; Local config, index, and logs (not the server-side backups).
    RMDir /r "$APPDATA\OpenBackup"
    RMDir /r "$LOCALAPPDATA\OpenBackup"

    ; Legacy / mistaken path from older installers.
    RMDir /r "$APPDATA\${PRODUCT_EXECUTABLE}"

    RMDir /r $INSTDIR

    Delete "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk"
    Delete "$DESKTOP\${INFO_PRODUCTNAME}.lnk"

    !insertmacro wails.unassociateFiles
    !insertmacro wails.unassociateCustomProtocols

    !insertmacro wails.deleteUninstaller
SectionEnd

Function un.StopOpenBackup
  DetailPrint "Stopping OpenBackup if it is running..."
  nsExec::ExecToLog '"$SYSDIR\taskkill.exe" /F /IM ${PRODUCT_EXECUTABLE} /T'
  Pop $0
  Sleep 800
FunctionEnd

Function un.ClearLoginAutostart
  DetailPrint "Removing login autostart..."
  DeleteRegValue HKCU "Software\Microsoft\Windows\CurrentVersion\Run" "OpenBackup"
FunctionEnd
