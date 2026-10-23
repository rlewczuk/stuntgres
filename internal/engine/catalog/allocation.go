package catalog

import "errors"

// ErrObjectIdentityExhausted distinguishes finite allocator capacity from an
// operation failure. Structural import planning may still use retained IDs.
var ErrObjectIdentityExhausted = errors.New("stuntgres: catalog object identity exhausted")
