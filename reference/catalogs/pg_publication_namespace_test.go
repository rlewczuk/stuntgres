package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

var publicationNamespaceNames = []string{"oid", "pnpubid", "pnnspid"}
var publicationNamespaceTypes = []string{"OID", "OID", "OID"}

func publicationNamespaceMetadata(t *testing.T, pg bool, columns []*sql.ColumnType) {
	t.Helper()
	if len(columns) != 3 {
		t.Fatal("publication namespace metadata width", columns)
	}
	for i, c := range columns {
		nullable, known := c.Nullable()
		if c.Name() != publicationNamespaceNames[i] || c.DatabaseTypeName() != "OID" || known == pg || !pg && nullable {
			t.Fatal("publication namespace metadata", i, c, nullable, known)
		}
		scan := reflect.TypeFor[int64]()
		if pg {
			scan = reflect.TypeFor[any]()
		}
		if c.ScanType() != scan {
			t.Fatal("publication namespace ScanType", i, c.ScanType(), scan)
		}
		if _, ok := c.Length(); ok {
			t.Fatal("unexpected OID length", c)
		}
		if _, _, ok := c.DecimalSize(); ok {
			t.Fatal("unexpected OID precision", c)
		}
	}
}

func publicationNamespaceEmpty(pg bool, query string, args ...any) fixture.Step {
	s := fixture.Select(query, publicationNamespaceNames, publicationNamespaceTypes, nil, args...)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		publicationNamespaceMetadata(t, pg, columns)
	}
	return s
}

func publicationNamespaceCount() fixture.Step {
	return fixture.Select("SELECT count(*) FROM pg_catalog.pg_publication_namespace", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}})
}

func TestReferencePGPublicationNamespaceReads(t *testing.T) {
	withPublicationNamespaceReader(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, admin, actor *sql.Conn) {
		held := make([]fixture.HeldStatement, 0, 2)
		for _, e := range []fixture.Executor{admin, actor} {
			s := publicationNamespaceEmpty(b.PG, "SELECT * FROM pg_catalog.pg_publication_namespace")
			fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Prepared(s))
			fixture.RunStep(t, p.Ctx, e, b.Recorder, publicationNamespaceCount())
			stmt, err := e.PrepareContext(p.Ctx, s.Query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			h := fixture.HeldStatement{Executor: e, Statement: stmt, SQL: s.Query}
			held = append(held, h)
			fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
		}
		fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Exec("CREATE SCHEMA publication_namespace_unrelated", 0))
		for _, h := range held {
			fixture.RunStep(t, p.Ctx, h, b.Recorder, publicationNamespaceEmpty(b.PG, h.SQL))
		}
		fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Exec("CREATE TABLE publication_namespace_unrelated.items(id integer)", 0))
		fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Exec("INSERT INTO publication_namespace_unrelated.items VALUES(7)", 1))
		fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Select("SELECT id FROM publication_namespace_unrelated.items", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		for _, h := range held {
			fixture.RunStep(t, p.Ctx, h, b.Recorder, publicationNamespaceEmpty(b.PG, h.SQL))
		}
		fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Exec("DROP TABLE publication_namespace_unrelated.items", 0))
		fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Exec("DROP SCHEMA publication_namespace_unrelated", 0))
		for _, h := range held {
			fixture.RunStep(t, p.Ctx, h, b.Recorder, publicationNamespaceEmpty(b.PG, h.SQL))
		}
		for _, e := range []fixture.Executor{admin, actor} {
			for _, source := range []string{"pg_catalog.pg_publication_namespace", "pg_publication_namespace"} {
				for _, suffix := range []string{"", " WHERE FALSE", " WHERE oid=$1", " WHERE pnpubid=$1", " WHERE pnnspid=$1", " LIMIT 0"} {
					q := "SELECT * FROM " + source + suffix
					var args []any
					if suffix == " WHERE oid=$1" || suffix == " WHERE pnpubid=$1" || suffix == " WHERE pnnspid=$1" {
						args = []any{int64(0)}
					}
					s := publicationNamespaceEmpty(b.PG, q, args...)
					fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Prepared(s))
					stmt, err := e.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: e, Statement: stmt, SQL: q}, b.Recorder, s)
					if len(args) != 0 {
						null := publicationNamespaceEmpty(b.PG, q, nil)
						fixture.RunStep(t, p.Ctx, e, b.Recorder, null)
						fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Prepared(null))
						fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: e, Statement: stmt, SQL: q}, b.Recorder, null)
					}
					if err := stmt.Close(); err != nil {
						t.Fatal(err)
					}
				}
			}
			for _, s := range []fixture.Step{publicationNamespaceCount(), fixture.Select("SELECT 1 FROM pg_catalog.pg_publication_namespace", []string{"?column?"}, []string{"INT4"}, nil)} {
				fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Prepared(s))
				stmt, err := e.PrepareContext(p.Ctx, s.Query)
				if err != nil {
					t.Fatal(err)
				}
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: e, Statement: stmt, SQL: s.Query}, b.Recorder, s)
				if err := stmt.Close(); err != nil {
					t.Fatal(err)
				}
			}
			rows, err := e.QueryContext(p.Ctx, "SELECT * FROM pg_catalog.pg_publication_namespace")
			if err != nil {
				t.Fatal(err)
			}
			names, err := rows.Columns()
			if err != nil || !reflect.DeepEqual(names, publicationNamespaceNames) {
				rows.Close()
				t.Fatal("publication namespace retained names", names, err)
			}
			columns, err := rows.ColumnTypes()
			if err != nil {
				rows.Close()
				t.Fatal(err)
			}
			publicationNamespaceMetadata(t, b.PG, columns)
			if rows.Next() || rows.Err() != nil {
				rows.Close()
				t.Fatal("expected actual empty source", rows.Err())
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			// database/sql closes at EOF: inspect the descriptors captured while open.
			publicationNamespaceMetadata(t, b.PG, columns)
		}
	})
}

func publicationNamespaceStoredACL(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, public, reader bool) {
	t.Helper()
	var want [][]any
	for _, privilege := range []string{"DELETE", "INSERT", "MAINTAIN", "REFERENCES", "SELECT", "TRIGGER", "TRUNCATE", "UPDATE"} {
		want = append(want, []any{true, true, false, privilege, false})
	}
	if public {
		want = append(want, []any{true, false, true, "SELECT", false})
	}
	if reader {
		want = append(want, []any{true, false, false, "SELECT", false})
	}
	s := fixture.Select("SELECT a.grantor=c.relowner AS owner_grantor,a.grantee=c.relowner AS owner_grantee,a.grantee=0 AS public_grantee,a.privilege_type,a.is_grantable FROM pg_catalog.pg_class c CROSS JOIN LATERAL aclexplode(c.relacl) a WHERE c.oid=6237", []string{"owner_grantor", "owner_grantee", "public_grantee", "privilege_type", "is_grantable"}, []string{"BOOL", "BOOL", "BOOL", "TEXT", "BOOL"}, want)
	s.Bag = true
	for _, prepared := range []bool{false, true} {
		explicit := fixture.Select("SELECT relacl IS NOT NULL AS explicit_acl FROM pg_catalog.pg_class WHERE oid=6237", []string{"explicit_acl"}, []string{"BOOL"}, [][]any{{true}})
		explicit.Prepared = prepared
		fixture.RunStep(t, p.Ctx, e, b.Recorder, explicit)
		next := s
		next.Prepared = prepared
		fixture.RunStep(t, p.Ctx, e, b.Recorder, next)
	}
}

func publicationNamespaceShape(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) {
	t.Helper()
	var row, array int64
	rows, err := e.QueryContext(p.Ctx, "SELECT reltype::bigint FROM pg_catalog.pg_class WHERE oid=6237")
	if err != nil {
		t.Fatal(err)
	}
	if !rows.Next() {
		rows.Close()
		t.Fatal("missing publicationNamespace")
	}
	if err := rows.Scan(&row); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	if rows.Next() || rows.Err() != nil {
		rows.Close()
		t.Fatal("duplicate publicationNamespace", rows.Err())
	}
	rows.Close()
	rows, err = e.QueryContext(p.Ctx, "SELECT typarray::bigint FROM pg_catalog.pg_type WHERE oid=$1", row)
	if err != nil {
		t.Fatal(err)
	}
	if !rows.Next() {
		rows.Close()
		t.Fatal("missing publicationNamespace row type")
	}
	if err := rows.Scan(&array); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	if rows.Next() || rows.Err() != nil {
		rows.Close()
		t.Fatal("duplicate row type", rows.Err())
	}
	rows.Close()
	if row <= 0 || array <= 0 || row == array || row == 6237 || array == 6237 {
		t.Fatal("invalid reciprocal type identities", row, array)
	}
	run := func(s fixture.Step) {
		fixture.OidReferenceRows(t, p.Ctx, b, e, s)
		fixture.OidReferenceRows(t, p.Ctx, b, e, fixture.Prepared(s))
	}
	run(fixture.Select("SELECT oid,relname,relnamespace,relowner,reltype,relnatts,relkind,relpersistence,relisshared,relhasindex,reltablespace FROM pg_catalog.pg_class WHERE oid=6237", []string{"oid", "relname", "relnamespace", "relowner", "reltype", "relnatts", "relkind", "relpersistence", "relisshared", "relhasindex", "reltablespace"}, []string{"OID", "NAME", "OID", "OID", "OID", "INT2", "CHAR", "CHAR", "BOOL", "BOOL", "OID"}, [][]any{{int64(6237), "pg_publication_namespace", int64(11), int64(10), row, int64(3), "r", "p", false, true, int64(0)}}))
	run(fixture.Select("SELECT attnum,attname,atttypid,attnotnull,atttypmod,attndims,attcollation,attacl IS NULL AS absent_acl FROM pg_catalog.pg_attribute WHERE attrelid=6237 AND attnum>0 ORDER BY attnum", []string{"attnum", "attname", "atttypid", "attnotnull", "atttypmod", "attndims", "attcollation", "absent_acl"}, []string{"INT2", "NAME", "OID", "BOOL", "INT4", "INT2", "OID", "BOOL"}, [][]any{
		{int64(1), "oid", int64(26), true, int64(-1), int64(0), int64(0), true},
		{int64(2), "pnpubid", int64(26), true, int64(-1), int64(0), int64(0), true},
		{int64(3), "pnnspid", int64(26), true, int64(-1), int64(0), int64(0), true},
	}))
	run(fixture.Select("SELECT typname,typtype,typrelid,typnamespace,typowner,typarray,typelem FROM pg_catalog.pg_type WHERE oid=$1", []string{"typname", "typtype", "typrelid", "typnamespace", "typowner", "typarray", "typelem"}, []string{"NAME", "CHAR", "OID", "OID", "OID", "OID", "OID"}, [][]any{{"pg_publication_namespace", "c", int64(6237), int64(11), int64(10), array, int64(0)}}, row))
	run(fixture.Select("SELECT typname,typtype,typelem,typrelid,typnamespace,typowner FROM pg_catalog.pg_type WHERE oid=$1", []string{"typname", "typtype", "typelem", "typrelid", "typnamespace", "typowner"}, []string{"NAME", "CHAR", "OID", "OID", "OID", "OID"}, [][]any{{"_pg_publication_namespace", "b", row, int64(0), int64(11), int64(10)}}, array))
	publicationNamespaceStoredACL(t, p, b, e, true, false)
}

func TestReferencePGPublicationNamespaceShape(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			publicationNamespaceShape(t, p, b, b.DB)
		})
	}
}

func TestReferencePGPublicationNamespaceDiagnostics(t *testing.T) {
	withPublicationNamespaceReader(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, admin, actor *sql.Conn) {
		for _, e := range []fixture.Executor{admin, actor} {
			for _, tc := range []struct {
				query, state, message string
			}{
				{"SELECT missing FROM pg_catalog.pg_publication_namespace", "42703", `column "missing" does not exist`},
				{"SELECT 'bad'::oid FROM pg_catalog.pg_publication_namespace WHERE FALSE", "22P02", `invalid input syntax for type oid: "bad"`},
				{"SELECT '4294967296'::oid FROM pg_catalog.pg_publication_namespace WHERE FALSE", "22003", `value "4294967296" is out of range for type oid`},
			} {
				for _, prepared := range []bool{false, true} {
					want := fixture.ControlsReferenceDiagnostic(tc.state, tc.message)
					want.Position = "8"
					s := fixture.ErrorDiagnostic(tc.query, want)
					s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, e, b.Recorder, publicationNamespaceEmpty(b.PG, "SELECT * FROM pg_catalog.pg_publication_namespace"))
					fixture.RunStep(t, p.Ctx, e, b.Recorder, publicationNamespaceCount())
				}
			}
		}
	})
}
