package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePrimaryKeyBoundaries(t *testing.T) {
	for _, c := range []struct{ name, setup, query, message, token, key, insert string }{
		{"deferrable", "", "CREATE TABLE t(a INT PRIMARY KEY DEFERRABLE INITIALLY DEFERRED)", "PRIMARY KEY deferrability is unsupported", "DEFERRABLE", "t_pkey", "INSERT INTO t VALUES(1)"},
		{"not_deferrable", "", "CREATE TABLE t(a INT PRIMARY KEY NOT DEFERRABLE)", "PRIMARY KEY deferrability is unsupported", "NOT DEFERRABLE", "t_pkey", "INSERT INTO t VALUES(1)"},
		{"initially", "", "CREATE TABLE t(a INT PRIMARY KEY INITIALLY IMMEDIATE)", "PRIMARY KEY deferrability is unsupported", "INITIALLY", "t_pkey", "INSERT INTO t VALUES(1)"},
		{"alter_add", "CREATE TABLE t(a INT)", "ALTER TABLE t ADD CONSTRAINT pk PRIMARY KEY(a)", "ALTER TABLE action is unsupported", "ADD", "pk", "INSERT INTO t VALUES(1)"},
		{"add_column", "CREATE TABLE t(a INT)", "ALTER TABLE t ADD COLUMN b INT PRIMARY KEY", "ALTER TABLE action is unsupported", "ADD", "t_pkey", "INSERT INTO t VALUES(1,1)"},
		{"add_column_if_not_exists", "CREATE TABLE t(a INT)", "ALTER TABLE t ADD COLUMN IF NOT EXISTS b INT PRIMARY KEY", "ALTER TABLE action is unsupported", "ADD", "t_pkey", "INSERT INTO t VALUES(1,1)"},
		{"using_index", "CREATE TABLE t(a INT); CREATE UNIQUE INDEX prebuilt ON t(a)", "ALTER TABLE t ADD CONSTRAINT pk PRIMARY KEY USING INDEX prebuilt", "ALTER TABLE action is unsupported", "ADD", "pk", "INSERT INTO t VALUES(1)"},
		{"rename_constraint", "CREATE TABLE t(a INT CONSTRAINT pk PRIMARY KEY)", "ALTER TABLE t RENAME CONSTRAINT pk TO renamed_pk", "RENAME CONSTRAINT is unsupported", "CONSTRAINT", "renamed_pk", "INSERT INTO t VALUES(1)"},
		{"include", "", "CREATE TABLE t(a INT,b TEXT,PRIMARY KEY(a) INCLUDE(b))", "PRIMARY KEY index parameters are unsupported", "INCLUDE", "t_pkey", "INSERT INTO t VALUES(1,'x')"},
		{"reloptions", "", "CREATE TABLE t(a INT,PRIMARY KEY(a) WITH(fillfactor=70))", "PRIMARY KEY index parameters are unsupported", "WITH", "t_pkey", "INSERT INTO t VALUES(1)"},
		{"tablespace", "", "CREATE TABLE t(a INT,PRIMARY KEY(a) USING INDEX TABLESPACE pg_default)", "PRIMARY KEY index parameters are unsupported", "USING", "t_pkey", "INSERT INTO t VALUES(1)"},
		// Both keys use PostgreSQL's builtin GiST range provider; this valid
		// temporal boundary needs no extension or native range implementation.
		{"without_overlaps", "", "CREATE TABLE t(bucket INT4RANGE,period INT4RANGE,PRIMARY KEY(bucket,period WITHOUT OVERLAPS))", "PRIMARY KEY WITHOUT OVERLAPS is unsupported", "WITHOUT", "t_pkey", "INSERT INTO t VALUES('[1,2)'::int4range,'[1,3)'::int4range)"},
	} {
		t.Run(c.name, func(t *testing.T) {
			fixture.PrimaryKeyReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				namespace := fixture.UniqueReferencePublicNamespace(t, p.Ctx, b.DB, b.PG)
				for _, q := range strings.Split(c.setup, "; ") {
					if q != "" {
						run(fixture.Exec(q, 0))
					}
				}
				if b.PG {
					run(fixture.Prepared(fixture.Exec(c.query, 0)))
					run(fixture.Select("SELECT contype FROM pg_constraint WHERE conname=$1", []string{"contype"}, []string{"CHAR"}, [][]any{{"p"}}, c.key))
					run(fixture.Exec(c.insert, 1))
					run(fixture.Select("SELECT count(*) AS n FROM t", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
					run(fixture.Exec("DROP TABLE t", 0))
				} else {
					run(fixture.ImplicitError(c.query, fixture.Diagnostic{Code: "0A000", Message: c.message, Position: fixture.Position(strings.Index(c.query, c.token) + 1)}))
					var want [][]any
					if c.setup != "" {
						want = [][]any{{"t", "r"}}
					}
					if c.name == "using_index" {
						want = [][]any{{"prebuilt", "i"}, {"t", "r"}}
					}
					if c.name == "rename_constraint" {
						want = [][]any{{"pk", "i"}, {"t", "r"}}
					}
					run(fixture.Select("SELECT relname,relkind FROM pg_class WHERE relnamespace=$1 ORDER BY relname", []string{"relname", "relkind"}, []string{"NAME", "CHAR"}, want, namespace))
					if c.setup != "" {
						run(fixture.Select("SELECT * FROM t", []string{"a"}, []string{"INT4"}, nil))
					}
				}
			})
		})
	}
}

func TestReferencePrimaryKeyMalformedBoundarySuffix(t *testing.T) {
	fixture.PrimaryKeyReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		q := "CREATE TABLE t(a INT PRIMARY KEY DEFERRABLE); nonsense"
		want := fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "nonsense"`)
		want.Position = fixture.Position(strings.Index(q, "nonsense") + 1)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.ErrorDiagnostic(q, want))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT relname FROM pg_class WHERE relname='t' OR relname='t_pkey'", []string{"relname"}, []string{"NAME"}, nil))
	})
}
