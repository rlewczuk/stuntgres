package analyze

import "stuntgres/internal/engine/catalog"

// Authority is the catalog and search-path authority a binding runs under.
// The engine implements it over its authorization view, which holds engine
// and session state. The binder reads only the catalog itself and passes the
// authority back to the host for everything else (names, privileges,
// definition reads, captures). A nil Authority means none is stored.
type Authority interface {
	AnalyzeAuthority()
	ReadCatalog() catalog.Read
	RuleReplica() bool
}
