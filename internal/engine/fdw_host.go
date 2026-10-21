package engine

import "stuntgres/internal/engine/fdw"

// ddlHost implements fdw.Host: the table host plus RemoveObject
// (schema_host.go) and ResolveRelation (role_host.go), shared with the other
// feature hosts.
var _ fdw.Host = ddlHost{}
