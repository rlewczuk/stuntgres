package ids

import "errors"

// ErrCatalogReplaced reports that an identity's catalog epoch was fenced, or
// its catalog replaced, before the operation could publish. Every engine
// subsystem returns this one value, so errors.Is matches across packages.
var ErrCatalogReplaced = errors.New("stuntgres: catalog replaced before publication")
