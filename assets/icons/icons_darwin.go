package icons

import _ "embed"

// App is the runtime application icon; the app bundle also contains an ICNS.
//
//go:embed macos/app-1024.png
var App []byte

// Tray contains only a monochrome alpha mask, coloured by macOS at runtime.
//
//go:embed macos/menubar-template@2x.png
var Tray []byte
