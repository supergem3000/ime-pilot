package icons

import _ "embed"

// App is the window icon. Explorer uses the ICO linked into the executable.
//
//go:embed windows/app-256.png
var App []byte

//go:embed windows/app-64.png
var Tray []byte
