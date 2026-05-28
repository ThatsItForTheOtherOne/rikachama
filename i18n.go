package main

import (
	"embed"
	"encoding/json"
	"fmt"
)

//go:embed lang/*.json
var langFiles embed.FS

// messages holds the active language's key→string map. Set once at startup
// by main() after the config is loaded; T reads from it on every call.
var messages map[string]string

func loadMessages(lang string) (map[string]string, error) {
	if lang == "" {
		lang = "ja"
	}
	data, err := langFiles.ReadFile("lang/" + lang + ".json")
	if err != nil {
		return nil, fmt.Errorf("load lang %q: %w", lang, err)
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse lang %q: %w", lang, err)
	}
	return m, nil
}

// T looks up a localized string by key. If args are passed, the value is
// treated as a printf format string. Missing keys return the key itself so
// untranslated strings appear loudly rather than as empty space.
func T(key string, args ...any) string {
	s, ok := messages[key]
	if !ok {
		return key
	}
	if len(args) == 0 {
		return s
	}
	return fmt.Sprintf(s, args...)
}
