#ifndef AppVersion
  #define AppVersion "1.0.0"
#endif

[Setup]
AppName=TrimShelf
AppVersion={#AppVersion}
AppPublisher=TrimShelf Project
AppPublisherURL=https://github.com/Node-Dog-Consulting/clippy
DefaultDirName={autopf}\TrimShelf
DefaultGroupName=TrimShelf
OutputBaseFilename=TrimShelf-Setup
OutputDir=..
Compression=lzma2
SolidCompression=yes
ArchitecturesInstallIn64BitMode=x64compatible
LicenseFile=..\LICENSE
PrivilegesRequired=admin
UninstallDisplayName=TrimShelf
SetupIconFile=..\trimshelf.ico
UninstallDisplayIcon={app}\trimshelf.ico

[Files]
Source: "..\trimshelf.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\libmpv-2.dll"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\trimshelf.ico"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{group}\TrimShelf"; Filename: "{app}\trimshelf.exe"; IconFilename: "{app}\trimshelf.ico"
Name: "{group}\Uninstall TrimShelf"; Filename: "{uninstallexe}"
Name: "{autodesktop}\TrimShelf"; Filename: "{app}\trimshelf.exe"; IconFilename: "{app}\trimshelf.ico"; Tasks: desktopicon

[Tasks]
Name: "desktopicon"; Description: "Create a &desktop shortcut"; GroupDescription: "Additional shortcuts:"

[Run]
Filename: "{app}\trimshelf.exe"; Description: "Launch TrimShelf"; Flags: nowait postinstall skipifsilent
