package client

import _ "embed"

const ValuesPath = "/_gox/client/values.js"

// ValuesSource is the shared browser argument normalizer.
//
//go:embed values.js
var ValuesSource []byte
