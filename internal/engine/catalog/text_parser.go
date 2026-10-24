package catalog

import "stuntgres/internal/engine/ids"

// TextParserIdentity is immutable bootstrap metadata, not a user parser or ACL.
// PostgreSQL's stock parser has no owner and its handlers are internal-only.
type TextParserIdentity struct {
	OID ids.OID
	Namespace ids.OID
	Name string
	Start, Token, End, Headline, LexType ids.OID
}

func DefaultTextParser() TextParserIdentity {
	return TextParserIdentity{3722, ids.PGCatalogNamespaceOID, "default", 3717, 3718, 3719, 3720, 3721}
}

func BuiltinTextParser(oid uint32) (TextParserIdentity, bool) {
	p := DefaultTextParser()
	return p, oid == uint32(p.OID)
}
