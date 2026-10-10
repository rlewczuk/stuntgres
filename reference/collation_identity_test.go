package reference_test

import (
	"database/sql"
	"reflect"
	"testing"
)

func TestReferenceCollationIdentity(t *testing.T) {
	collationScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		stmt, err := b.db.PrepareContext(p.ctx, `CREATE COLLATION coll_identity_a FROM pg_catalog."C"`)
		if err != nil {
			t.Fatal(err)
		}
		collationAbsent(t, p, b, "coll_identity_a")
		result, err := stmt.ExecContext(p.ctx)
		stmt.Close()
		deleteReferenceCount(t, result, err, 0)
		collationExec(t, p, b, `CREATE COLLATION coll_identity_b FROM pg_catalog."C"`, `CREATE COLLATION coll_identity_libc (LOCALE='C')`, `CREATE COLLATION coll_identity_builtin (PROVIDER=builtin,LOCALE='C')`)
		a, other := collationOID(t, b.db, "coll_identity_a"), collationOID(t, b.db, "coll_identity_b")
		if a == other {
			t.Fatal("copies share identity", a)
		}
		collationRoutes(t, p, b, referenceSelect(`SELECT collname,collprovider,collisdeterministic,collencoding,collcollate,collctype,colllocale,collicurules,collversion FROM pg_catalog.pg_collation WHERE collname LIKE 'coll_identity_%' ORDER BY collname`,
			[]string{"collname", "collprovider", "collisdeterministic", "collencoding", "collcollate", "collctype", "colllocale", "collicurules", "collversion"},
			[]string{"NAME", "CHAR", "BOOL", "INT4", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{
				{"coll_identity_a", "c", true, int64(-1), "C", "C", nil, nil, nil},
				{"coll_identity_b", "c", true, int64(-1), "C", "C", nil, nil, nil},
				{"coll_identity_builtin", "b", true, int64(-1), nil, nil, "C", nil, "1"},
				{"coll_identity_libc", "c", true, int64(6), "C", "C", nil, nil, nil},
			}))
		collationExec(t, p, b, `CREATE TABLE coll_identity_t(id INT,v TEXT COLLATE coll_identity_a)`, `CREATE VIEW coll_identity_v AS SELECT v COLLATE coll_identity_b AS v FROM coll_identity_t`)
		collationRoutes(t, p, b, referenceSelect(`SELECT d.refobjid::bigint AS refobjid,d.deptype FROM pg_catalog.pg_depend d JOIN pg_catalog.pg_class c ON d.objid=c.oid WHERE d.classid=1259 AND c.relname='coll_identity_t' AND d.refclassid=3456`, []string{"refobjid", "deptype"}, []string{"INT8", "CHAR"}, [][]any{{a, "n"}}))
		rows, err := b.db.QueryContext(p.ctx, `CREATE COLLATION coll_identity_query (FROM=pg_catalog."C")`)
		if err != nil {
			t.Fatal(err)
		}
		cols, err := rows.Columns()
		if err != nil || len(cols) != 0 || rows.Next() || rows.Err() != nil {
			t.Fatal(cols, err, rows.Err())
		}
		rows.Close()
		collationOID(t, b.db, "coll_identity_query")
	})
}

func TestReferenceCollationProvider(t *testing.T) {
	collationScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		collationExec(t, p, b, `CREATE COLLATION coll_provider_c FROM pg_catalog."C"`, `CREATE COLLATION coll_provider_simple FROM pg_catalog.pg_c_utf8`, `CREATE COLLATION coll_provider_full FROM pg_catalog.pg_unicode_fast`, `CREATE COLLATION coll_provider_alias (PROVIDER=builtin,LOCALE='C.UTF8')`, `CREATE TABLE coll_provider_t(v TEXT COLLATE coll_provider_c)`)
		collationStep(t, p, b, referenceExec(`INSERT INTO coll_provider_t VALUES('é'),('a'),('Z'),('A')`, 4))
		collationRoutes(t, p, b, referenceSelect(`SELECT v FROM coll_provider_t ORDER BY v`, []string{"v"}, []string{"TEXT"}, [][]any{{"A"}, {"Z"}, {"a"}, {"é"}}))
		s := referenceSelect(`SELECT upper($1::text COLLATE coll_provider_c) AS ascii,upper($2::text COLLATE coll_provider_simple) AS simple,upper($2::text COLLATE coll_provider_full) AS full`, []string{"ascii", "simple", "full"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"é", "ß", "SS"}}, "é", "ß")
		s.checkMetadata = func(t *testing.T, rows *sql.Rows) {
			meta, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			for _, typ := range meta {
				if typ.DatabaseTypeName() != "TEXT" {
					t.Fatal(typ)
				}
				if !b.pg && typ.ScanType() != reflect.TypeFor[string]() {
					t.Fatal(typ.ScanType())
				}
			}
		}
		collationRoutes(t, p, b, s)
		collationRoutes(t, p, b, referenceSelect(`SELECT colllocale FROM pg_catalog.pg_collation WHERE collname='coll_provider_alias'`, []string{"colllocale"}, []string{"TEXT"}, [][]any{{"C.UTF-8"}}))
		collationExec(t, p, b, `CREATE COLLATION coll_provider_other FROM pg_catalog."C"`, `CREATE TABLE coll_provider_mix(a TEXT COLLATE coll_provider_c,b TEXT COLLATE coll_provider_other)`)
		collationStep(t, p, b, referenceExec(`INSERT INTO coll_provider_mix VALUES('a','a')`, 1))
		collationError(t, p, b, `SELECT a LIKE b FROM coll_provider_mix`, referenceDiagnostic{Code: "42P22", Message: "could not determine which collation to use for LIKE", Hint: "Use the COLLATE clause to set the collation explicitly."})
		collationRoutes(t, p, b, referenceSelect(`SELECT a COLLATE coll_provider_c LIKE b AS matched FROM coll_provider_mix`, []string{"matched"}, []string{"BOOL"}, [][]any{{true}}))
		collationError(t, p, b, `SELECT 'a' COLLATE coll_provider_c = 'a' COLLATE coll_provider_other`, referenceDiagnostic{Code: "42P21", Message: `collation mismatch between explicit collations "coll_provider_c" and "coll_provider_other"`, Position: "42"})
		if b.pg {
			collationRoutes(t, p, b, referenceSelect(`SELECT 'é' COLLATE coll_provider_simple ILIKE 'É' AS matched`, []string{"matched"}, []string{"BOOL"}, [][]any{{true}}))
		} else {
			collationError(t, p, b, `SELECT 'é' COLLATE coll_provider_simple ILIKE 'É'`, referenceDiagnostic{Code: "0A000", Message: "ILIKE is unsupported for this collation", Position: "41"})
		}
	})
}
