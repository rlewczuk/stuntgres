package catalog

// Released estimates for the set declarations already exposed by this catalog.
// The default is 1000; exceptions are keyed by declaration identity, not labels.
// Source: https://raw.githubusercontent.com/postgres/postgres/REL_18_6/src/include/catalog/pg_proc.dat
func builtinRoutineRows(d *BuiltinRoutineDeclaration) float32 {
	switch d.Identity.ID {
	case 3689, 3690: // ts_stat
		return 10000
	case 2289: // pg_options_to_table(text[])
		return 3
	case 2763: // regexp_matches(text,text)
		return 1
	case 1689, 2764, 3035, 3425, 6353: // ACL expansion, flagged matches, channels, ancestors, modules
		return 10
	case 3713, 3714: // ts_token_type(oid) and ts_token_type(text)
		return 16
	case 3353, 3354, 5029, 5030, 5031, 6270, 6271, 6272, 6400: // physical directory record listings
		return 20
	case 2947, 4100, 5052, 5064, 6224: // legacy/modern snapshot XIPs, shared-memory allocations, WAL resource managers
		return 50
	case 1293, 2331, // multirange and array unnest
		3084,       // extension update paths
		3566, 4568, // event-trigger inspection
		6321, 6322, // physical WAL summaries
		3205, 3208, 3219, 3465, 3475, 3491, 3931, 3932, // JSONB sets
		3955, 3957, 3958, 3959, 3961, 3969: // JSON sets
		return 100
	case 6159: // pg_get_catalog_foreign_keys
		return 250
	case 1686: // pg_get_keywords
		return 500
	default:
		return 1000
	}
}
