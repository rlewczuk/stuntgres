package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// addColumnPKRaw captures a complete result set for before/after ownership
// comparisons; it is used only within one backend where scan representations
// are stable.
func addColumnPKRaw(t *testing.T, p *fixture.Pair, c *sql.Conn, query string, args ...any) [][]any {
	t.Helper()
	rows, err := c.QueryContext(p.Ctx, query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Error(err)
		}
	}()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var bag [][]any
	for rows.Next() {
		values := make([]any, len(columns))
		dest := make([]any, len(columns))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		bag = append(bag, values)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return bag
}

func addColumnPKDefinitions(table string, rows ...[]any) fixture.Step {
	return fixture.Select("SELECT conname,contype,pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid=$1::regclass ORDER BY conname", []string{"conname", "contype", "pg_get_constraintdef"}, []string{"NAME", "CHAR", "TEXT"}, rows, table)
}

func TestReferenceAlterAddColumnPrimaryKeyBoundaries(t *testing.T) {
	fixture.PrimaryKeyReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		addColumnPKForEachRoute(t, p, b, func(t *testing.T, s addColumnPKSession) {
			bt := s.name("bt")
			s.exec(t, "CREATE TABLE "+bt+"(id INT PRIMARY KEY)", 0)
			s.run(t, fixture.ImplicitError("ALTER TABLE "+bt+" ADD COLUMN k INT PRIMARY KEY", fixture.Diagnostic{Code: "42P16", Message: `multiple primary keys for table "` + bt + `" are not allowed`}))
			s.run(t, fixture.Select("SELECT conname,contype FROM pg_constraint WHERE conrelid=$1::regclass ORDER BY conname", []string{"conname", "contype"}, []string{"NAME", "CHAR"}, [][]any{{bt + "_id_not_null", "n"}, {bt + "_pkey", "p"}}, bt))
			s.run(t, addColumnPKAttributeNames(bt, []any{"id"}))

			bt2 := s.name("bt2")
			s.exec(t, "CREATE TABLE "+bt2+"(id INT)", 0)
			s.run(t, fixture.ImplicitError("ALTER TABLE "+bt2+" ADD COLUMN k INT PRIMARY KEY, ADD COLUMN j INT PRIMARY KEY", fixture.Diagnostic{Code: "42P16", Message: `multiple primary keys for table "` + bt2 + `" are not allowed`}))
			s.run(t, addColumnPKAttributeNames(bt2, []any{"id"}))

			bt3 := s.name("bt3")
			s.exec(t, "CREATE TABLE "+bt3+"(id INT)", 0)
			s.exec(t, "INSERT INTO "+bt3+" VALUES(1),(2)", 2)
			nulls := fixture.ControlsReferenceDiagnostic("23502", `column "k" of relation "`+bt3+`" contains null values`)
			nulls.Schema, nulls.Table, nulls.Column = "public", bt3, "k"
			s.run(t, fixture.ErrorDiagnostic("ALTER TABLE "+bt3+" ADD COLUMN k INT NOT NULL PRIMARY KEY", nulls))
			s.run(t, fixture.Select("SELECT id FROM "+bt3+" ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
			s.run(t, fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid=$1::regclass", []string{"conname"}, []string{"NAME"}, nil, bt3))
			s.run(t, addColumnPKAttributeNames(bt3, []any{"id"}))
			duplicate := fixture.ControlsReferenceDiagnostic("23505", `could not create unique index "`+bt3+`_pkey"`)
			duplicate.Detail, duplicate.Schema, duplicate.Table, duplicate.Constraint = "Key (k)=(7) is duplicated.", "public", bt3, bt3+"_pkey"
			s.run(t, fixture.ErrorDiagnostic("ALTER TABLE "+bt3+" ADD COLUMN k INT NOT NULL PRIMARY KEY DEFAULT 7", duplicate))
			s.run(t, fixture.Select("SELECT id FROM "+bt3+" ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))

			for _, tc := range []struct{ name, clause, token string }{
				{"bt4a", "NULL PRIMARY KEY", "PRIMARY"},
				{"bt4b", "PRIMARY KEY NULL", "NULL"},
				{"bt4c", "NOT NULL NULL PRIMARY KEY", "NULL"},
				{"bt4d", "NULL CONSTRAINT " + s.name("pk4") + " PRIMARY KEY", "CONSTRAINT"},
			} {
				table := s.name(tc.name)
				s.exec(t, "CREATE TABLE "+table+"(id INT)", 0)
				query := "ALTER TABLE " + table + " ADD COLUMN k INT " + tc.clause
				position := strings.Index(query, tc.token) + 1
				if tc.name == "bt4c" {
					position = strings.LastIndex(query, "NULL") + 1
				}
				want := fixture.ControlsReferenceDiagnostic("42601", fmt.Sprintf(`conflicting NULL/NOT NULL declarations for column "k" of table "%s"`, table))
				want.Position = fixture.Position(position)
				step := fixture.ImplicitError(query, want)
				step.ErrorStage = "execute"
				s.run(t, step)
				s.run(t, addColumnPKAttributeNames(table, []any{"id"}))
				s.run(t, fixture.Select("SELECT conname,contype FROM pg_constraint WHERE conrelid=$1::regclass", []string{"conname", "contype"}, []string{"NAME", "CHAR"}, nil, table))
			}

			existing := s.name("nm_pkey")
			nm := s.name("nm")
			s.exec(t, "CREATE TABLE "+existing+"(x INT)", 0)
			s.exec(t, "CREATE TABLE "+nm+"(id INT)", 0)
			s.run(t, fixture.ImplicitError("ALTER TABLE "+nm+" ADD COLUMN k INT CONSTRAINT "+existing+" PRIMARY KEY", fixture.ControlsReferenceDiagnostic("42P07", `relation "`+existing+`" already exists`)))
			s.run(t, addColumnPKAttributeNames(nm, []any{"id"}))

			reldup := s.name("reldup")
			reldupExisting := reldup + "_pkey"
			s.exec(t, "CREATE TABLE "+reldupExisting+"(x INT)", 0)
			s.exec(t, "CREATE TABLE "+reldup+"(id INT)", 0)
			s.exec(t, "ALTER TABLE "+reldup+" ADD COLUMN k INT PRIMARY KEY", 0)
			s.run(t, fixture.PrimaryKeyReferenceCatalog(reldup, [][]any{
				{reldup + "_k_not_null", "n", "NOT NULL k", nil},
				{reldup + "_pkey1", "p", "PRIMARY KEY (k)", nil},
			}))

			ref := s.name("ref")
			fk := s.name("fk")
			s.exec(t, "CREATE TABLE "+ref+"(x INT)", 0)
			s.exec(t, "CREATE TABLE "+fk+"(id INT)", 0)
			foreign := "ALTER TABLE " + fk + " ADD COLUMN k INT PRIMARY KEY REFERENCES " + ref + "(x)"
			s.run(t, fixture.ImplicitError(foreign, fixture.ControlsReferenceDiagnostic("42830", `there is no unique constraint matching given keys for referenced table "`+ref+`"`)))
			s.exec(t, "ALTER TABLE "+ref+" ADD UNIQUE(x)", 0)
			s.exec(t, foreign, 0)
			s.run(t, addColumnPKDefinitions(fk,
				[]any{fk + "_k_fkey", "f", "FOREIGN KEY (k) REFERENCES " + ref + "(x)"},
				[]any{fk + "_k_not_null", "n", "NOT NULL k"},
				[]any{fk + "_pkey", "p", "PRIMARY KEY (k)"},
			))

			tl, tl2 := s.name("tl"), s.name("tl2")
			tlPK := s.name("tl_pk")
			s.exec(t, "CREATE TABLE "+tl+"(a INT)", 0)
			s.exec(t, "ALTER TABLE "+tl+" ADD CONSTRAINT "+tlPK+" PRIMARY KEY(a)", 0)
			s.exec(t, "CREATE TABLE "+tl2+"(a INT)", 0)
			s.exec(t, "ALTER TABLE "+tl2+" ADD PRIMARY KEY(a)", 0)
			s.run(t, fixture.Select("SELECT conname,contype FROM pg_constraint WHERE conrelid=$1::regclass AND contype='p'", []string{"conname", "contype"}, []string{"NAME", "CHAR"}, [][]any{{tlPK, "p"}}, tl))
			s.run(t, fixture.Select("SELECT conname,contype FROM pg_constraint WHERE conrelid=$1::regclass AND contype='p'", []string{"conname", "contype"}, []string{"NAME", "CHAR"}, [][]any{{tl2 + "_pkey", "p"}}, tl2))
		})
	})
}

func TestReferenceAlterAddColumnPrimaryKeyInheritance(t *testing.T) {
	fixture.PrimaryKeyReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		addColumnPKForEachRoute(t, p, b, func(t *testing.T, s addColumnPKSession) {
			parent, child := s.name("parent"), s.name("child")
			s.exec(t, "CREATE TABLE "+parent+"(a INT)", 0)
			s.exec(t, "CREATE TABLE "+child+"() INHERITS ("+parent+")", 0)
			s.exec(t, "ALTER TABLE "+parent+" ADD COLUMN k INT PRIMARY KEY", 0)
			s.run(t, fixture.PrimaryKeyReferenceCatalog(parent, [][]any{
				{parent + "_k_not_null", "n", "NOT NULL k", nil},
				{parent + "_pkey", "p", "PRIMARY KEY (k)", nil},
			}))
			s.run(t, fixture.Select("SELECT attname,attnotnull,attinhcount FROM pg_attribute WHERE attrelid=$1::regclass AND attname='k'", []string{"attname", "attnotnull", "attinhcount"}, []string{"NAME", "BOOL", "INT2"}, [][]any{{"k", true, int64(1)}}, child))
			s.run(t, fixture.Select("SELECT conname,contype FROM pg_constraint WHERE conrelid=$1::regclass AND contype IN ('p','u')", []string{"conname", "contype"}, []string{"NAME", "CHAR"}, nil, child))
			s.exec(t, "INSERT INTO "+child+" VALUES(1,2)", 1)
			s.exec(t, "INSERT INTO "+child+" VALUES(3,2)", 1)
			s.exec(t, "INSERT INTO "+parent+" VALUES(5,7)", 1)
			s.run(t, fixture.ErrorDiagnostic("INSERT INTO "+parent+" VALUES(6,7)", fixture.UniqueReferenceDuplicate(parent, parent+"_pkey", "Key (k)=(7) already exists.")))
		})
	})
}

func TestReferenceAlterAddColumnPrimaryKeyAtomicity(t *testing.T) {
	fixture.PrimaryKeyReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		addColumnPKForEachRoute(t, p, b, func(t *testing.T, s addColumnPKSession) {
			at := s.name("at")
			missing := s.name("missing")
			type atomicityCase struct {
				name      string
				create    string
				fail      string
				want      fixture.Diagnostic
				createFix string
				retry     string
				kept      []string
				after     [][]any
			}
			nulls := fixture.ControlsReferenceDiagnostic("23502", `column "k" of relation "`+at+`" contains null values`)
			nulls.Schema, nulls.Table, nulls.Column = "public", at, "k"
			duplicate := fixture.ControlsReferenceDiagnostic("23505", `could not create unique index "`+at+`_pkey"`)
			duplicate.Detail, duplicate.Schema, duplicate.Table, duplicate.Constraint = "Key (k)=(7) is duplicated.", "public", at, at+"_pkey"
			cases := []atomicityCase{
				{name: "null_backfill", create: "CREATE TABLE " + at + "(id INT)", fail: "ALTER TABLE " + at + " ADD COLUMN k INT NOT NULL PRIMARY KEY", want: nulls, retry: "ALTER TABLE " + at + " ADD COLUMN k INT NOT NULL PRIMARY KEY", after: [][]any{{at + "_k_not_null", "n", "NOT NULL k"}, {at + "_pkey", "p", "PRIMARY KEY (k)"}}},
				{name: "duplicate_backfill", create: "CREATE TABLE " + at + "(id INT)", fail: "ALTER TABLE " + at + " ADD COLUMN k INT NOT NULL PRIMARY KEY DEFAULT 7", want: duplicate, retry: "ALTER TABLE " + at + " ADD COLUMN k INT NOT NULL PRIMARY KEY DEFAULT 7", after: [][]any{{at + "_k_not_null", "n", "NOT NULL k"}, {at + "_pkey", "p", "PRIMARY KEY (k)"}}},
				{name: "existing_primary_key", create: "CREATE TABLE " + at + "(id INT PRIMARY KEY)", fail: "ALTER TABLE " + at + " ADD COLUMN k INT PRIMARY KEY", want: fixture.Diagnostic{Code: "42P16", Message: `multiple primary keys for table "` + at + `" are not allowed`}, retry: "ALTER TABLE " + at + " ADD COLUMN k INT", kept: []string{at + "_pkey"}, after: [][]any{{at + "_id_not_null", "n", "NOT NULL id"}, {at + "_pkey", "p", "PRIMARY KEY (id)"}}},
				{name: "missing_reference", create: "CREATE TABLE " + at + "(id INT)", fail: "ALTER TABLE " + at + " ADD COLUMN k INT PRIMARY KEY REFERENCES " + missing + "(x)", want: fixture.ControlsReferenceDiagnostic("42P01", `relation "`+missing+`" does not exist`), createFix: "CREATE TABLE " + missing + "(x INT UNIQUE)", retry: "ALTER TABLE " + at + " ADD COLUMN k INT PRIMARY KEY REFERENCES " + missing + "(x)", after: [][]any{{at + "_k_fkey", "f", "FOREIGN KEY (k) REFERENCES " + missing + "(x)"}, {at + "_k_not_null", "n", "NOT NULL k"}, {at + "_pkey", "p", "PRIMARY KEY (k)"}}},
				{name: "second_primary_key", create: "CREATE TABLE " + at + "(id INT)", fail: "ALTER TABLE " + at + " ADD COLUMN j INT PRIMARY KEY, ADD COLUMN g INT PRIMARY KEY", want: fixture.Diagnostic{Code: "42P16", Message: `multiple primary keys for table "` + at + `" are not allowed`}, retry: "ALTER TABLE " + at + " ADD COLUMN g INT PRIMARY KEY", after: [][]any{{at + "_g_not_null", "n", "NOT NULL g"}, {at + "_pkey", "p", "PRIMARY KEY (g)"}}},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					s.exec(t, "DROP TABLE IF EXISTS "+at+" CASCADE", 0)
					s.exec(t, tc.create, 0)
					s.exec(t, "INSERT INTO "+at+" VALUES(1),(2)", 2)
					rows := addColumnPKRaw(t, p, s.conn, "SELECT * FROM "+at+" ORDER BY id")
					attributes := addColumnPKRaw(t, p, s.conn, "SELECT attname,attnotnull,attnum FROM pg_attribute WHERE attrelid=$1::regclass AND attnum>0 ORDER BY attnum", at)
					constraints := addColumnPKRaw(t, p, s.conn, "SELECT conname,contype,pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid=$1::regclass ORDER BY conname", at)
					s.run(t, fixture.ImplicitError(tc.fail, tc.want))
					if got := addColumnPKRaw(t, p, s.conn, "SELECT * FROM "+at+" ORDER BY id"); !reflect.DeepEqual(got, rows) {
						t.Fatalf("failed ADD changed rows: %#v -> %#v", rows, got)
					}
					if got := addColumnPKRaw(t, p, s.conn, "SELECT attname,attnotnull,attnum FROM pg_attribute WHERE attrelid=$1::regclass AND attnum>0 ORDER BY attnum", at); !reflect.DeepEqual(got, attributes) {
						t.Fatalf("failed ADD changed attributes: %#v -> %#v", attributes, got)
					}
					if got := addColumnPKRaw(t, p, s.conn, "SELECT conname,contype,pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid=$1::regclass ORDER BY conname", at); !reflect.DeepEqual(got, constraints) {
						t.Fatalf("failed ADD changed constraints: %#v -> %#v", constraints, got)
					}
					orphans := []string{at + "_pkey", at + "_pkey1", at + "_k_key", at + "_g_key"}
					quoted := make([]string, len(orphans))
					for i, name := range orphans {
						quoted[i] = "'" + name + "'"
					}
					var kept [][]any
					for _, name := range tc.kept {
						kept = append(kept, []any{name})
					}
					s.run(t, fixture.Select("SELECT relname FROM pg_class WHERE relname IN ("+strings.Join(quoted, ",")+") ORDER BY relname", []string{"relname"}, []string{"NAME"}, kept))
					if tc.createFix != "" {
						s.exec(t, tc.createFix, 0)
					}
					s.exec(t, "DELETE FROM "+at, 2)
					s.exec(t, tc.retry, 0)
					s.run(t, addColumnPKDefinitions(at, tc.after...))
				})
			}
		})
	})
}

func TestReferenceAlterAddColumnPrimaryKeyFeatureSmoke(t *testing.T) {
	fixture.PrimaryKeyReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		addColumnPKForEachRoute(t, p, b, func(t *testing.T, s addColumnPKSession) {
			first := s.name("grin_pk1")
			s.exec(t, "CREATE TABLE "+first+"(id INT)", 0)
			s.exec(t, "ALTER TABLE "+first+" ADD COLUMN k int NOT NULL PRIMARY KEY", 0)
			s.run(t, fixture.Select("SELECT conname,contype FROM pg_constraint WHERE conrelid=$1::regclass ORDER BY conname", []string{"conname", "contype"}, []string{"NAME", "CHAR"}, [][]any{{first + "_k_not_null", "n"}, {first + "_pkey", "p"}}, first))
			s.run(t, fixture.Select("SELECT indisprimary,indisunique,indimmediate,indisvalid FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid WHERE c.relname=$1", []string{"indisprimary", "indisunique", "indimmediate", "indisvalid"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true}}, first+"_pkey"))
			s.exec(t, "INSERT INTO "+first+" VALUES(1,10),(2,20)", 2)
			s.run(t, fixture.ErrorDiagnostic("INSERT INTO "+first+" VALUES(3,10)", fixture.UniqueReferenceDuplicate(first, first+"_pkey", "Key (k)=(10) already exists.")))
			s.run(t, fixture.ErrorDiagnostic("INSERT INTO "+first+" VALUES(4,NULL)", fixture.PrimaryKeyReferenceNull(first, "k", "4, null")))
			fixture.TableDDLMetadata(t, p.Ctx, s.conn, b.PG, s.prepared, "SELECT * FROM "+first+" ORDER BY id", []string{"id", "k"}, []string{"INT4", "INT4"}, []bool{true, false}, 2)

			second := s.name("grin_pk2")
			s.exec(t, "CREATE TABLE "+second+"(id INT)", 0)
			s.exec(t, "INSERT INTO "+second+" VALUES(1),(2)", 2)
			s.exec(t, "ALTER TABLE "+second+" ADD COLUMN k2 uuid NOT NULL PRIMARY KEY DEFAULT gen_random_uuid()", 0)
			s.run(t, fixture.Select("SELECT count(*),count(DISTINCT k2) FROM "+second, []string{"count", "count"}, []string{"INT8", "INT8"}, [][]any{{int64(2), int64(2)}}))
			s.run(t, fixture.PrimaryKeyReferenceCatalog(second, [][]any{
				{second + "_k2_not_null", "n", "NOT NULL k2", nil},
				{second + "_pkey", "p", "PRIMARY KEY (k2)", nil},
			}))
			s.exec(t, "INSERT INTO "+second+"(id) VALUES(3)", 1)
			s.run(t, fixture.Select("SELECT count(*),count(DISTINCT k2) FROM "+second, []string{"count", "count"}, []string{"INT8", "INT8"}, [][]any{{int64(3), int64(3)}}))

			swap := s.name("swap")
			s.exec(t, "CREATE TABLE "+swap+"(a INT PRIMARY KEY)", 0)
			s.exec(t, "ALTER TABLE "+swap+" DROP CONSTRAINT "+swap+"_pkey, ADD COLUMN k INT PRIMARY KEY", 0)
			s.run(t, addColumnPKDefinitions(swap,
				[]any{swap + "_a_not_null", "n", "NOT NULL a"},
				[]any{swap + "_k_not_null", "n", "NOT NULL k"},
				[]any{swap + "_pkey", "p", "PRIMARY KEY (k)"},
			))
			s.exec(t, "INSERT INTO "+swap+" VALUES(1,10)", 1)
			s.run(t, fixture.ErrorDiagnostic("INSERT INTO "+swap+" VALUES(2,10)", fixture.UniqueReferenceDuplicate(swap, swap+"_pkey", "Key (k)=(10) already exists.")))

			dedup := s.name("dedup")
			dedupPK := s.name("u")
			s.exec(t, "CREATE TABLE "+dedup+"(id INT)", 0)
			s.exec(t, "ALTER TABLE "+dedup+" ADD COLUMN k INT CONSTRAINT "+dedupPK+" UNIQUE PRIMARY KEY", 0)
			s.run(t, addColumnPKConstraints(dedup, []any{dedupPK, "p", "PRIMARY KEY (k)"}))
			s.exec(t, "INSERT INTO "+dedup+" VALUES(1,1)", 1)
			s.run(t, fixture.ErrorDiagnostic("INSERT INTO "+dedup+" VALUES(2,1)", fixture.UniqueReferenceDuplicate(dedup, dedupPK, "Key (k)=(1) already exists.")))

			s.run(t, fixture.ImplicitError("ALTER TABLE "+first+" ADD COLUMN other INT PRIMARY KEY", fixture.Diagnostic{Code: "42P16", Message: `multiple primary keys for table "` + first + `" are not allowed`}))
			s.run(t, fixture.Select("SELECT count(*) FROM "+first, []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}))
			s.exec(t, "INSERT INTO "+first+" VALUES(5,50)", 1)
		})
	})
}
