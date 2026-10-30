package core

import "stuntgres/internal/engine/functions"

// DictionaryFiles is captured with dictionary semantic inputs under Engine.Mu.
// The capability itself is immutable startup state and survives catalog changes.
func (h DDLHost) DictionaryFiles() *functions.DictionaryFiles { return h.E.TextSearchFiles }
