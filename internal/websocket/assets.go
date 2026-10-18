package websocket

import _ "embed"

// ClientSource is the JavaScript module served from ClientPath.
//
//go:embed client.js
var ClientSource []byte
