package main

import (
	"encoding/json"
	"os"
)

var outputJSON bool

func emitJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}
