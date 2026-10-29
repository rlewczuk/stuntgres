package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

var publicationNames = []string{"oid", "pubname", "pubowner", "puballtables", "pubinsert", "pubupdate", "pubdelete", "pubtruncate", "pubviaroot", "pubgencols"}
var publicationTypes = []string{"OID", "NAME", "OID", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "CHAR"}

func publicationMetadata(t *testing.T, pg bool, columns []*sql.ColumnType) {
	t.Helper()
	if len(columns) != 10 {
		t.Fatal("publication metadata width", columns)
	}
	for i, c := range columns {
		nullable, known := c.Nullable()
		if c.Name() != publicationNames[i] || c.DatabaseTypeName() != publicationTypes[i] || known == pg || !pg && nullable {
			t.Fatal("publication metadata", i, c, nullable, known)
		}
		scan := reflect.TypeFor[string]()
		if i == 0 || i == 2 {
			scan = reflect.TypeFor[int64]()
		}
		if i >= 3 && i <= 8 {
			scan = reflect.TypeFor[bool]()
		}
		if pg && (i < 3 || i == 9) {
			scan = reflect.TypeFor[any]()
		}
		if c.ScanType() != scan {
			t.Fatal("publication ScanType", i, c.ScanType(), scan)
		}
	}
}

func publicationEmpty(pg bool, query string, args ...any) fixture.Step {
	s := fixture.Select(query, publicationNames, publicationTypes, nil, args...)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		publicationMetadata(t, pg, columns)
	}
	return s
}

func publicationCount() fixture.Step {
	return fixture.Select("SELECT count(*) FROM pg_catalog.pg_publication", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}})
}

func TestReferencePGPublicationReads(t *testing.T) {
	withPublicationReader(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, admin, actor *sql.Conn) {
		held := make([]fixture.HeldStatement, 0, 2)
		for _, e := range []fixture.Executor{admin, actor} {
			s := publicationEmpty(b.PG, "SELECT * FROM pg_catalog.pg_publication")
			fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Prepared(s))
			fixture.RunStep(t, p.Ctx, e, b.Recorder, publicationCount())
			stmt, err := e.PrepareContext(p.Ctx, s.Query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			h := fixture.HeldStatement{Executor: e, Statement: stmt, SQL: s.Query}
			held = append(held, h)
			fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
		}
		fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Exec("CREATE TABLE publication_unrelated(id integer)", 0))
		fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Exec("INSERT INTO publication_unrelated VALUES(7)", 1))
		for _, h := range held {
			fixture.RunStep(t, p.Ctx, h, b.Recorder, publicationEmpty(b.PG, h.SQL))
		}
		for _, e := range []fixture.Executor{admin, actor} {
			for _, source := range []string{"pg_catalog.pg_publication", "pg_publication"} {
				for _, suffix := range []string{"", " WHERE FALSE", " WHERE oid=$1", " WHERE pubname=$1"} {
					q := "SELECT * FROM " + source + suffix
					var args []any
					if suffix == " WHERE oid=$1" {
						args = []any{int64(0)}
					}
					if suffix == " WHERE pubname=$1" {
						args = []any{"absent"}
					}
					s := publicationEmpty(b.PG, q, args...)
					fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Prepared(s))
					stmt, err := e.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: e, Statement: stmt, SQL: q}, b.Recorder, s)
					if err := stmt.Close(); err != nil {
						t.Fatal(err)
					}
				}
			}
			for _, s := range []fixture.Step{publicationCount(), fixture.Select("SELECT 1 FROM pg_catalog.pg_publication", []string{"?column?"}, []string{"INT4"}, nil)} {
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
			rows, err := e.QueryContext(p.Ctx, "SELECT * FROM pg_catalog.pg_publication")
			if err != nil {
				t.Fatal(err)
			}
			columns, err := rows.ColumnTypes()
			if err != nil {
				rows.Close()
				t.Fatal(err)
			}
			publicationMetadata(t, b.PG, columns)
			if rows.Next() || rows.Err() != nil {
				rows.Close()
				t.Fatal("expected actual empty source", rows.Err())
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			// database/sql closes at EOF: inspect the descriptors captured while open.
			publicationMetadata(t, b.PG, columns)
		}
	})
}

func publicationStoredACL(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, public, reader bool) {
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
	s := fixture.Select("SELECT a.grantor=c.relowner AS owner_grantor,a.grantee=c.relowner AS owner_grantee,a.grantee=0 AS public_grantee,a.privilege_type,a.is_grantable FROM pg_catalog.pg_class c CROSS JOIN LATERAL aclexplode(c.relacl) a WHERE c.oid=6104", []string{"owner_grantor", "owner_grantee", "public_grantee", "privilege_type", "is_grantable"}, []string{"BOOL", "BOOL", "BOOL", "TEXT", "BOOL"}, want)
	s.Bag = true
	for _, prepared := range []bool{false, true} {
		explicit := fixture.Select("SELECT relacl IS NOT NULL AS explicit_acl FROM pg_catalog.pg_class WHERE oid=6104", []string{"explicit_acl"}, []string{"BOOL"}, [][]any{{true}})
		explicit.Prepared = prepared
		fixture.RunStep(t, p.Ctx, e, b.Recorder, explicit)
		next := s
		next.Prepared = prepared
		fixture.RunStep(t, p.Ctx, e, b.Recorder, next)
	}
}

func publicationShape(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) {
	t.Helper()
	var row, array int64
	rows, err := e.QueryContext(p.Ctx, "SELECT reltype::bigint FROM pg_catalog.pg_class WHERE oid=6104")
	if err != nil {
		t.Fatal(err)
	}
	if !rows.Next() {
		rows.Close()
		t.Fatal("missing publication")
	}
	if err := rows.Scan(&row); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	if rows.Next() || rows.Err() != nil {
		rows.Close()
		t.Fatal("duplicate publication", rows.Err())
	}
	rows.Close()
	rows, err = e.QueryContext(p.Ctx, "SELECT typarray::bigint FROM pg_catalog.pg_type WHERE oid=$1", row)
	if err != nil {
		t.Fatal(err)
	}
	if !rows.Next() {
		rows.Close()
		t.Fatal("missing publication row type")
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
	if row <= 0 || array <= 0 || row == array || row == 6104 || array == 6104 {
		t.Fatal("invalid reciprocal type identities", row, array)
	}
	run := func(s fixture.Step) {
		fixture.OidReferenceRows(t, p.Ctx, b, e, s)
		fixture.OidReferenceRows(t, p.Ctx, b, e, fixture.Prepared(s))
	}
	run(fixture.Select("SELECT oid,relname,relnamespace,relowner,reltype,relnatts,relkind,relpersistence,relisshared,relhasindex,reltablespace FROM pg_catalog.pg_class WHERE oid=6104", []string{"oid", "relname", "relnamespace", "relowner", "reltype", "relnatts", "relkind", "relpersistence", "relisshared", "relhasindex", "reltablespace"}, []string{"OID", "NAME", "OID", "OID", "OID", "INT2", "CHAR", "CHAR", "BOOL", "BOOL", "OID"}, [][]any{{int64(6104), "pg_publication", int64(11), int64(10), row, int64(10), "r", "p", false, true, int64(0)}}))
	run(fixture.Select("SELECT attnum,attname,atttypid,attnotnull,atttypmod,attndims,attcollation,attacl IS NULL AS absent_acl FROM pg_catalog.pg_attribute WHERE attrelid=6104 AND attnum>0 ORDER BY attnum", []string{"attnum", "attname", "atttypid", "attnotnull", "atttypmod", "attndims", "attcollation", "absent_acl"}, []string{"INT2", "NAME", "OID", "BOOL", "INT4", "INT2", "OID", "BOOL"}, [][]any{
		{int64(1), "oid", int64(26), true, int64(-1), int64(0), int64(0), true},
		{int64(2), "pubname", int64(19), true, int64(-1), int64(0), int64(950), true},
		{int64(3), "pubowner", int64(26), true, int64(-1), int64(0), int64(0), true},
		{int64(4), "puballtables", int64(16), true, int64(-1), int64(0), int64(0), true},
		{int64(5), "pubinsert", int64(16), true, int64(-1), int64(0), int64(0), true},
		{int64(6), "pubupdate", int64(16), true, int64(-1), int64(0), int64(0), true},
		{int64(7), "pubdelete", int64(16), true, int64(-1), int64(0), int64(0), true},
		{int64(8), "pubtruncate", int64(16), true, int64(-1), int64(0), int64(0), true},
		{int64(9), "pubviaroot", int64(16), true, int64(-1), int64(0), int64(0), true},
		{int64(10), "pubgencols", int64(18), true, int64(-1), int64(0), int64(0), true},
	}))
	run(fixture.Select("SELECT typname,typtype,typrelid,typnamespace,typowner,typarray,typelem FROM pg_catalog.pg_type WHERE oid=$1", []string{"typname", "typtype", "typrelid", "typnamespace", "typowner", "typarray", "typelem"}, []string{"NAME", "CHAR", "OID", "OID", "OID", "OID", "OID"}, [][]any{{"pg_publication", "c", int64(6104), int64(11), int64(10), array, int64(0)}}, row))
	run(fixture.Select("SELECT typname,typtype,typelem,typrelid,typnamespace,typowner FROM pg_catalog.pg_type WHERE oid=$1", []string{"typname", "typtype", "typelem", "typrelid", "typnamespace", "typowner"}, []string{"NAME", "CHAR", "OID", "OID", "OID", "OID"}, [][]any{{"_pg_publication", "b", row, int64(0), int64(11), int64(10)}}, array))
	publicationStoredACL(t, p, b, e, true, false)
}

func TestReferencePGPublicationShape(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			publicationShape(t, p, b, b.DB)
		})
	}
}

func TestReferencePGPublicationDiagnostics(t *testing.T) {
	withPublicationReader(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, admin, actor *sql.Conn) {
		for _, e := range []fixture.Executor{admin, actor} {
			for _, tc := range []struct {
				query, state, message string
			}{
				{"SELECT missing FROM pg_catalog.pg_publication", "42703", `column "missing" does not exist`},
				{"SELECT 'bad'::oid FROM pg_catalog.pg_publication WHERE FALSE", "22P02", `invalid input syntax for type oid: "bad"`},
				{"SELECT '4294967296'::oid FROM pg_catalog.pg_publication WHERE FALSE", "22003", `value "4294967296" is out of range for type oid`},
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
					fixture.RunStep(t, p.Ctx, e, b.Recorder, publicationEmpty(b.PG, "SELECT * FROM pg_catalog.pg_publication"))
					fixture.RunStep(t, p.Ctx, e, b.Recorder, publicationCount())
				}
			}
		}
	})
}
