package reference_test

// Constants are independent release records captured by root's pinned PG18.0
// tests-first rounds (tests-2.md), not computed from the native implementation.
func sequenceExpectedDiagnostic(query string) (referenceDiagnostic, bool) {
	records := map[string]referenceDiagnostic{
		"ALTER SEQUENCE s OWNED BY t.v":                   controlsReferenceDiagnostic("55000", "sequence must have same owner as table it is linked to"),
		"ALTER SEQUENCE s OWNED BY other.t.v":             controlsReferenceDiagnostic("55000", "sequence must be in same schema as table it is linked to"),
		"ALTER SEQUENCE s OWNER TO sequence_probe_reader": {Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: `cannot change owner of sequence "s"`, Detail: `Sequence "s" is linked to table "t".`},
		"ALTER SEQUENCE detached SET SCHEMA moved":        {Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "cannot move an owned sequence into another schema", Detail: `Sequence "detached" is linked to table "t".`},
		"SELECT nextval('transient')":                     {Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: `relation "transient" does not exist`, Position: "16"},
		"SELECT nextval('owned')":                         {Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: `relation "owned" does not exist`, Position: "16"},
		"SELECT nextval('t')":                             {Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: `cannot open relation "t"`, Detail: "This operation is not supported for tables."},
		"SELECT currval('t')":                             {Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: `cannot open relation "t"`, Detail: "This operation is not supported for tables."},
		"SELECT setval('t',1)":                            {Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: `cannot open relation "t"`, Detail: "This operation is not supported for tables."},
		"ALTER SEQUENCE t RESTART":                        {Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: `cannot open relation "t"`, Detail: "This operation is not supported for tables."},
		"SELECT nextval('0')":                             controlsReferenceDiagnostic("XX000", "could not open relation with OID 0"),
		"SELECT nextval('-')":                             controlsReferenceDiagnostic("XX000", "could not open relation with OID 0"),
		"SELECT nextval('4294967295')":                    controlsReferenceDiagnostic("XX000", "could not open relation with OID 4294967295"),
		"SELECT nextval('a.b.c')":                         {Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: `cross-database references are not implemented: "a.b.c"`, Position: "16"},
		"SELECT nextval('bad..name')":                     {Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42602", Message: "invalid name syntax", Position: "16"},
		"SELECT /*é*/ nextval('missing')":                 {Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: `relation "missing" does not exist`, Position: "22"},
		"CREATE SEQUENCE s":                               controlsReferenceDiagnostic("42P07", `relation "s" already exists`),
		"CREATE SEQUENCE bad CACHE 1 CACHE 2":             {Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "conflicting or redundant options", Position: "29"},
		"CREATE SEQUENCE bad OWNED BY t":                  {Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "invalid OWNED BY option", Hint: "Specify OWNED BY table.column or OWNED BY NONE."},
		"CREATE SEQUENCE bad OWNED BY t.missing":          controlsReferenceDiagnostic("42703", `column "missing" of relation "t" does not exist`),
		"DROP SEQUENCE missing":                           controlsReferenceDiagnostic("42P01", `sequence "missing" does not exist`),
		"DROP SEQUENCE t":                                 {Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: `"t" is not a sequence`, Hint: "Use DROP TABLE to remove a table."},
		"ALTER SEQUENCE s AS SMALLINT MINVALUE -32769":    controlsReferenceDiagnostic("22023", "MINVALUE (-32769) is out of range for sequence data type smallint"),
		"ALTER SEQUENCE s MAXVALUE 1":                     controlsReferenceDiagnostic("22023", "MINVALUE (1) must be less than MAXVALUE (1)"),
	}
	d, ok := records[query]
	return d, ok
}
