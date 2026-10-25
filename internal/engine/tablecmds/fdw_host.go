package tablecmds

import "stuntgres/internal/engine/fdw"

// TableHost implements fdw.Host: table.Host plus RemoveObject and
// ResolveRelation, which come from DDLHost.
var _ fdw.Host = TableHost{}
