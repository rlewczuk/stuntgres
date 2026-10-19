package engine

import "stuntgres/internal/engine/visibility"

// Temporary aliases for the declarations that moved to package visibility in
// ENGINE-SPLIT step 7b. Step 7c replaces their uses and removes them.

type (
	VisibilityView          = visibility.View
	commitTimestampEntry    = visibility.CommitTimestamp
	deleteCandidate         = visibility.DeleteCandidate
	privateDeletionRoot     = visibility.PrivateDeletes
	serializableTransaction = visibility.SerializableTx
	serializableWrite       = visibility.SerializableWrite
	serializableWriteKey    = visibility.WriteKey
	transactionRoot         = visibility.Root
	transactionVisibility   = visibility.Entry
	visibilityData          = visibility.Data
	visibilityGeneration    = visibility.Generation
	visibilityRegistry      = visibility.Registry
)
