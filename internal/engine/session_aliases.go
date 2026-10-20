package engine

import "stuntgres/internal/engine/session"

// Temporary aliases for the journal types that moved into session
// (ENGINE-SPLIT step 13d.1). Step 13e removes them.
type (
	catalogChanges               = session.Journal
	catalogDependencyObservation = session.DependencyObservation
	catalogStorageChange         = session.StorageChange
)
