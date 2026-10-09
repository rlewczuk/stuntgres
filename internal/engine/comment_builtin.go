package engine

import "stuntgres/internal/sqltype"

// Descriptions from PostgreSQL REL_18_0 pg_namespace.dat and pg_type.dat.
// Only descriptors already represented by the native catalog are seeded.
func namespaceCommentBaseline(oid OID) string {
	switch oid {
	case PGCatalogNamespaceOID:
		return "system catalog schema"
	case PublicNamespaceOID:
		return "standard public schema"
	}
	return ""
}

func builtinTypeComment(id sqltype.ID) string {
	switch id {
	case 16:
		return "boolean, format 't'/'f'"
	case 17:
		return "variable-length string, binary values escaped"
	case 18:
		return "single character"
	case 19:
		return "63-byte type for storing system identifiers"
	case 20:
		return "~18 digit integer, 8-byte storage"
	case 21:
		return "-32 thousand to 32 thousand, 2-byte storage"
	case 23:
		return "-2 billion to 2 billion integer, 4-byte storage"
	case 25:
		return "variable-length string, no limit specified"
	case 26:
		return "object identifier(oid), maximum 4 billion"
	case 114:
		return "JSON stored as text"
	case 194:
		return "string representing an internal node tree"
	case 700:
		return "single-precision floating point number, 4-byte storage"
	case 701:
		return "double-precision floating point number, 8-byte storage"
	case 705:
		return "pseudo-type representing an undetermined type"
	case 1042:
		return "'char(length)' blank-padded string, fixed storage length"
	case 1043:
		return "'varchar(length)' non-blank-padded string, variable storage length"
	case 1082:
		return "date"
	case 1083:
		return "time of day"
	case 1114:
		return "date and time"
	case 1184:
		return "date and time with time zone"
	case 1186:
		return "time interval, format 'number units ...'"
	case 1266:
		return "time of day with time zone"
	case 1700:
		return "'numeric(precision, scale)' arbitrary precision number"
	case 2205:
		return "registered class"
	case 2278:
		return "pseudo-type for the result of a function with no real result"
	case 2950:
		return "UUID"
	}
	return ""
}
