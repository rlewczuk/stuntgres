package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePrimaryKeyRetentionNames(t *testing.T) {
	for _, c := range []struct {
		name, setup, ddl string
		keys             [][]any
	}{
		{"unique_before", "", "CREATE TABLE t(a INT UNIQUE PRIMARY KEY)", [][]any{{"t_pkey", "p", "PRIMARY KEY (a)"}}},
		{"pk_before", "", "CREATE TABLE t(a INT PRIMARY KEY UNIQUE)", [][]any{{"t_pkey", "p", "PRIMARY KEY (a)"}}},
		{"name_transfer_before", "", "CREATE TABLE t(a INT CONSTRAINT chosen UNIQUE PRIMARY KEY)", [][]any{{"chosen", "p", "PRIMARY KEY (a)"}}},
		{"name_transfer_after", "", "CREATE TABLE t(a INT PRIMARY KEY CONSTRAINT chosen UNIQUE)", [][]any{{"chosen", "p", "PRIMARY KEY (a)"}}},
		{"explicit_pk_wins", "", "CREATE TABLE t(a INT CONSTRAINT u UNIQUE CONSTRAINT pk PRIMARY KEY)", [][]any{{"pk", "p", "PRIMARY KEY (a)"}}},
		{"table_redundancy", "", "CREATE TABLE t(a INT,b INT,UNIQUE(a,b),PRIMARY KEY(a,b))", [][]any{{"t_pkey", "p", "PRIMARY KEY (a, b)"}}},
		{"reverse_order", "", "CREATE TABLE t(a INT,b INT,UNIQUE(b,a),PRIMARY KEY(a,b))", [][]any{{"t_b_a_key", "u", "UNIQUE (b, a)"}, {"t_pkey", "p", "PRIMARY KEY (a, b)"}}},
		{"nnd_distinct_policy", "", "CREATE TABLE t(a INT,UNIQUE NULLS NOT DISTINCT(a),PRIMARY KEY(a))", [][]any{{"t_a_key", "u", "UNIQUE NULLS NOT DISTINCT (a)"}, {"t_pkey", "p", "PRIMARY KEY (a)"}}},
		{"relation_collision", "CREATE TABLE t_pkey(x INT)", "CREATE TABLE t(a INT PRIMARY KEY)", [][]any{{"t_pkey1", "p", "PRIMARY KEY (a)"}}},
		{"index_collision", "CREATE TABLE other(x INT); CREATE INDEX t_pkey ON other(x)", "CREATE TABLE t(a INT PRIMARY KEY)", [][]any{{"t_pkey1", "p", "PRIMARY KEY (a)"}}},
		{"constraint_collision", "CREATE TABLE other(x INT CONSTRAINT t_pkey CHECK(x>0))", "CREATE TABLE t(a INT PRIMARY KEY)", [][]any{{"t_pkey1", "p", "PRIMARY KEY (a)"}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			fixture.PrimaryKeyReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				for _, q := range strings.Split(c.setup, "; ") {
					if q != "" {
						run(fixture.Exec(q, 0))
					}
				}
				run(fixture.Exec(c.ddl, 0))
				run(fixture.Select("SELECT c.conname,c.contype,pg_get_constraintdef(c.oid) FROM pg_constraint c JOIN pg_class r ON c.conrelid=r.oid WHERE r.relname='t' AND (c.contype='p' OR c.contype='u') ORDER BY c.conname", []string{"conname", "contype", "pg_get_constraintdef"}, []string{"NAME", "CHAR", "TEXT"}, c.keys))
				for _, row := range c.keys {
					run(fixture.Select("SELECT relkind FROM pg_class WHERE relname=$1", []string{"relkind"}, []string{"CHAR"}, [][]any{{"i"}}, row[0]))
				}
			})
		})
	}
}

func TestReferencePrimaryKeyNameCollisionOrdering(t *testing.T) {
	for _, c := range []struct{ name, setup, ddl, code, message string }{
		{"future_unique_after", "", "CREATE TABLE t(a INT,b INT,PRIMARY KEY(a),CONSTRAINT t_pkey UNIQUE(b))", "42P07", `relation "t_pkey" already exists`},
		{"future_unique_before", "", "CREATE TABLE t(a INT,b INT,CONSTRAINT t_pkey UNIQUE(b),PRIMARY KEY(a))", "42P07", `relation "t_pkey" already exists`},
		{"nn_before", "", "CREATE TABLE t(a INT CONSTRAINT pk NOT NULL,CONSTRAINT pk PRIMARY KEY(a))", "42710", `constraint "pk" for relation "t" already exists`},
		{"nn_after", "", "CREATE TABLE t(a INT CONSTRAINT pk PRIMARY KEY CONSTRAINT pk NOT NULL)", "42710", `constraint "pk" for relation "t" already exists`},
		{"check_before", "", "CREATE TABLE t(a INT,CONSTRAINT pk CHECK(a>0),CONSTRAINT pk PRIMARY KEY(a))", "42710", `constraint "pk" for relation "t" already exists`},
		{"check_after", "", "CREATE TABLE t(a INT,CONSTRAINT pk PRIMARY KEY(a),CONSTRAINT pk CHECK(a>0))", "42710", `constraint "pk" for relation "t" already exists`},
		{"relation_and_nn", "CREATE TABLE pk(x INT)", "CREATE TABLE t(a INT CONSTRAINT pk NOT NULL,CONSTRAINT pk PRIMARY KEY(a))", "42P07", `relation "pk" already exists`},
		{"explicit_relation", "CREATE TABLE pk(x INT)", "CREATE TABLE t(a INT CONSTRAINT pk PRIMARY KEY)", "42P07", `relation "pk" already exists`},
	} {
		t.Run(c.name, func(t *testing.T) {
			fixture.PrimaryKeyReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				if c.setup != "" {
					run(fixture.Exec(c.setup, 0))
				}
				run(fixture.Prepared(fixture.ImplicitError(c.ddl, fixture.Diagnostic{Code: c.code, Message: c.message})))
				run(fixture.Select("SELECT relname FROM pg_class WHERE relname='t' OR relname='t_pkey'", []string{"relname"}, []string{"NAME"}, nil))
				run(fixture.Select("SELECT c.conname FROM pg_constraint c JOIN pg_class r ON c.conrelid=r.oid WHERE r.relname='t'", []string{"conname"}, []string{"NAME"}, nil))
			})
		})
	}
}

func TestReferencePrimaryKeyUTF8Names(t *testing.T) {
	fixture.PrimaryKeyReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		// 60 bytes plus '_pkey' requires clipping to 57 bytes on codepoint
		// boundaries. The collision suffix1 then fits exactly in 63 bytes.
		table := strings.Repeat("界", 20)
		first := strings.Repeat("界", 19) + "_pkey"
		second := strings.Repeat("界", 19) + "_pkey1"
		run(fixture.Exec(`CREATE TABLE "`+first+`"(x INT)`, 0))
		run(fixture.Exec(`CREATE TABLE "`+table+`"(a INT PRIMARY KEY)`, 0))
		ids := fixture.PrimaryKeyReferenceIDs(t, p, b, second)
		run(fixture.Select("SELECT conname,contype FROM pg_constraint WHERE conrelid=$1 AND contype='p'", []string{"conname", "contype"}, []string{"NAME", "CHAR"}, [][]any{{second, "p"}}, ids[1]))
		run(fixture.Exec(`INSERT INTO "`+table+`" VALUES(1)`, 1))
		run(fixture.ErrorDiagnostic(`INSERT INTO "`+table+`" VALUES(1)`, fixture.UniqueReferenceDuplicate(table, second, "Key (a)=(1) already exists.")))
	})
}
