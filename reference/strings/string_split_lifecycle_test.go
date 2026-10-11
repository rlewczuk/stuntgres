package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func stringSplitStoredSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE TABLE ss_stored(id int,s text DEFAULT split_part('a,b',',',-1),a text[] DEFAULT string_to_array('x,x,,',',',''),g text GENERATED ALWAYS AS(split_part(s,',',1)) STORED,h text[] GENERATED ALWAYS AS(string_to_array(s,',')) STORED,CHECK(split_part(s,',',1)<>'bad'),CHECK(string_to_array(s,',') IS NOT NULL))", 0),
		fixture.Exec("CREATE INDEX ss_scalar_index ON ss_stored((split_part(s,',',1)))", 0),
		stringSplitArrayIndexStep(b),
		fixture.Exec("INSERT INTO ss_stored(id) VALUES(1)", 1),
		fixture.Exec("INSERT INTO ss_stored(id,s,a) VALUES(2,'c,d',string_to_array('',NULL))", 1),
		fixture.Exec("CREATE VIEW ss_scalar_view AS SELECT id,split_part(s,',',-1) AS v,string_to_array(s,',') AS a FROM ss_stored", 0),
		fixture.Exec("CREATE VIEW ss_target_view AS SELECT id,string_to_table(s,',') AS v FROM ss_stored", 0),
		fixture.Exec("CREATE VIEW ss_from_view AS SELECT s.id,t.a,t.b,t.n FROM ss_stored s CROSS JOIN LATERAL ROWS FROM(string_to_table(s.s,','),string_to_table('x',',')) WITH ORDINALITY AS t(a,b,n)", 0),
		fixture.Exec("CREATE VIEW ss_contextual_target_view AS SELECT NULL AS tag,string_to_table('a,b',',') AS v UNION ALL SELECT 'kept',string_to_table('z',',')", 0),
	} {
		if !t.Run(s.Query, func(t *testing.T) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }) {
			t.FailNow()
		}
	}
	if !b.PG {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(stringSplitArrayIndexStep(b)))
	}
	var indexes [][]any
	if b.PG {
		indexes = [][]any{{"ss_array_index"}}
	}
	stringReplaceBoth(t, p, b, c, fixture.Select("SELECT relname FROM pg_catalog.pg_class WHERE relname='ss_array_index'", []string{"relname"}, []string{"NAME"}, indexes))
}

func stringSplitArrayIndexStep(b fixture.Backend) fixture.Step {
	const query = "CREATE INDEX ss_array_index ON ss_stored((string_to_array(s,',')))"
	if b.PG {
		return fixture.Exec(query, 0)
	}
	d := fixture.ControlsReferenceDiagnostic("42704", `data type text[] has no default operator class for access method "btree"`)
	d.Hint = "You must specify an operator class for the index or define a default operator class for the data type."
	s := fixture.ErrorDiagnostic(query, d)
	s.ErrorStage = "execute"
	return s
}

func stringSplitStoredWants() []fixture.Step {
	return []fixture.Step{
		fixture.Select("SELECT id,s,a,g,h FROM ss_stored ORDER BY id", []string{"id", "s", "a", "g", "h"}, []string{"INT4", "TEXT", "_TEXT", "TEXT", "_TEXT"}, [][]any{{int64(1), "b", `{x,x,NULL,NULL}`, "b", `{b}`}, {int64(2), "c,d", `{}`, "c", `{c,d}`}}),
		fixture.Select("SELECT id,v,a FROM ss_scalar_view ORDER BY id", []string{"id", "v", "a"}, []string{"INT4", "TEXT", "_TEXT"}, [][]any{{int64(1), "b", `{b}`}, {int64(2), "d", `{c,d}`}}),
		fixture.Select("SELECT id,v FROM ss_target_view ORDER BY id,v", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "b"}, {int64(2), "c"}, {int64(2), "d"}}),
		fixture.Select("SELECT id,a,b,n FROM ss_from_view ORDER BY id,n", []string{"id", "a", "b", "n"}, []string{"INT4", "TEXT", "TEXT", "INT8"}, [][]any{{int64(1), "b", "x", int64(1)}, {int64(2), "c", "x", int64(1)}, {int64(2), "d", nil, int64(2)}}),
		fixture.Select("SELECT tag,v FROM ss_contextual_target_view ORDER BY v", []string{"tag", "v"}, []string{"TEXT", "TEXT"}, [][]any{{nil, "a"}, {nil, "b"}, {"kept", "z"}}),
	}
}

func TestReferenceStringSplitStored(t *testing.T) {
	stringReplaceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		stringSplitStoredSetup(t, p, b, c)
		stringSplitStoredDependency(t, p, b, c)
		stringSplitReadOnlyViews(t, p, b, c)
		for _, s := range stringSplitStoredWants() {
			stringReplaceBoth(t, p, b, c, s)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ALTER TABLE ss_stored RENAME COLUMN s TO source", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ALTER TABLE ss_stored RENAME TO ss_renamed", 0))
		for _, s := range stringSplitStoredWants()[1:] {
			stringReplaceBoth(t, p, b, c, s)
		}
		// Deparse must remain executable ordinary SQL after identity-based rename.
		for _, name := range []string{"ss_scalar_view", "ss_target_view", "ss_from_view"} {
			var definition string
			if err := c.QueryRowContext(p.Ctx, "SELECT pg_get_viewdef($1::regclass,true)", name).Scan(&definition); err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE VIEW "+name+"_copy AS "+definition, 0))
			for _, want := range stringSplitStoredWants()[1:] {
				if strings.Contains(want.Query, name+" ") {
					want.Query = strings.Replace(want.Query, name+" ", name+"_copy ", 1)
					stringReplaceBoth(t, p, b, c, want)
				}
			}
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP TABLE ss_renamed CASCADE", 0))
		for _, name := range []string{"ss_scalar_view", "ss_target_view", "ss_from_view"} {
			stringReplaceBoth(t, p, b, c, fixture.Select("SELECT relname FROM pg_catalog.pg_class WHERE relname=$1", []string{"relname"}, []string{"NAME"}, nil, name))
		}
	})
}

func TestReferenceStringSplitLifecycle(t *testing.T) {
	stringReplaceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE ss_atomic(v text)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ss_atomic VALUES('sentinel')", 1))
		for _, prepared := range []bool{false, true} {
			d := fixture.ControlsReferenceDiagnostic("22023", "field position must not be zero")
			s := fixture.ErrorDiagnostic("INSERT INTO ss_atomic VALUES(split_part('a,b',',',$1)),(split_part('a,b',',',$2))", d, 1, 0)
			s.ErrorStage = "execute"
			if prepared {
				s = fixture.Prepared(s)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			stringReplaceBoth(t, p, b, c, fixture.Select("SELECT v FROM ss_atomic", []string{"v"}, []string{"TEXT"}, [][]any{{"sentinel"}}))
		}
		// The native adapter guarantees rejection before publication for a
		// precanceled context. Fast lib/pq queries do not guarantee this timing.
		// Portable feature SQL, atomicity and stored lifecycle stay shared below.
		if !b.PG {
			for _, q := range []string{"SELECT split_part($1,',',-1)", "SELECT string_to_array($1,NULL)", "SELECT string_to_table($1,',')"} {
				st, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				for _, prepared := range []bool{false, true} {
					var rows *sql.Rows
					if prepared {
						rows, err = st.QueryContext(ctx, "a,b")
					} else {
						rows, err = c.QueryContext(ctx, q, "a,b")
					}
					if rows != nil {
						rows.Close()
						t.Fatal("canceled query exposed rows")
					}
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("cancel %T %v", err, err)
					}
				}
				if err := st.Close(); err != nil {
					t.Fatal(err)
				}
			}
		}
		stringSplitStoredSetup(t, p, b, c)
		for _, s := range stringSplitStoredWants() {
			stringReplaceBoth(t, p, b, c, s)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TEMP TABLE ss_temp(v text DEFAULT split_part('a,b',',',-1),a text[] DEFAULT string_to_array('x,x,,',',',''))", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ss_temp DEFAULT VALUES", 1))
		tempWant := fixture.Select("SELECT v,a FROM ss_temp", []string{"v", "a"}, []string{"TEXT", "_TEXT"}, [][]any{{"b", `{x,x,NULL,NULL}`}})
		stringReplaceBoth(t, p, b, c, tempWant)
		if b.PG {
			return
		} // Native controls have no PostgreSQL API equivalent.
		var dump strings.Builder
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(dump.String(), "ss_array_index") {
			t.Fatal("failed array index appeared in native dump")
		}
		want := stringSplitStoredWants()[0]
		old, err := c.QueryContext(p.Ctx, want.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		meta, err := old.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
			t.Fatal(err)
		}
		stringReplaceBoth(t, p, b, c, fixture.Select("SELECT relname FROM pg_catalog.pg_class WHERE relname='ss_array_index'", []string{"relname"}, []string{"NAME"}, nil))
		fixture.StringCoreRetainedRows(t, old, nil, want.Columns, want.Types, want.Rows, false)
		for i, typ := range want.Types {
			if meta[i].DatabaseTypeName() != typ {
				t.Fatal(meta)
			}
		}
		for _, s := range stringSplitStoredWants() {
			stringReplaceBoth(t, p, b, c, s)
		}
		stringReplaceBoth(t, p, b, c, tempWant)
		var before strings.Builder
		if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
			t.Fatal(err)
		}
		if before.String() != dump.String() {
			t.Fatal("canonical roundtrip changed")
		}
		bad := "CREATE TABLE ss_bad(v text); INSERT INTO ss_bad VALUES('prefix'); INSERT INTO ss_bad VALUES(split_part('a',',',0));"
		d := fixture.ControlsReferenceDiagnostic("22023", "field position must not be zero")
		fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)), &d)
		var after strings.Builder
		if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
			t.Fatal(err)
		}
		if before.String() != after.String() {
			t.Fatal("failed Restore published prefix")
		}
		for _, s := range stringSplitStoredWants() {
			stringReplaceBoth(t, p, b, c, s)
		}
		if p.Postgres != nil {
			if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				t.Fatal(err)
			}
			if _, err := p.Postgres.ExecContext(p.Ctx, after.String()); err != nil {
				t.Fatal("dump replay", err)
			}
			for _, s := range stringSplitStoredWants() {
				fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, s)
			}
			if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				t.Fatal(err)
			}
		}
	})
}

func TestReferenceStringSplitFeatureSmoke(t *testing.T) {
	stringReplaceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		stringReplaceBoth(t, p, b, c, fixture.Select("SELECT split_part('a,b,c',',',-2) AS s,string_to_array('x,x,,',',','') AS a", []string{"s", "a"}, []string{"TEXT", "_TEXT"}, [][]any{{"b", `{x,x,NULL,NULL}`}}))
		stringReplaceBoth(t, p, b, c, fixture.Select("SELECT string_to_table('a,b',',') AS a,string_to_table('x',',') AS b ORDER BY a", []string{"a", "b"}, []string{"TEXT", "TEXT"}, [][]any{{"a", "x"}, {"b", nil}}))
		s := fixture.Select("SELECT upper(string_to_table(string_to_table('a:b,c:d',','),':')) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"A"}, {"B"}, {"C"}, {"D"}})
		s.Bag = true
		stringReplaceBoth(t, p, b, c, s)
		stringReplaceBoth(t, p, b, c, fixture.Select("SELECT a,b,n FROM ROWS FROM(string_to_table('a,b',','),string_to_table('x',',')) WITH ORDINALITY AS t(a,b,n) ORDER BY n", []string{"a", "b", "n"}, []string{"TEXT", "TEXT", "INT8"}, [][]any{{"a", "x", int64(1)}, {"b", nil, int64(2)}}))
		stringReplaceBoth(t, p, b, c, fixture.Select("SELECT string_to_table('',',') AS v", []string{"v"}, []string{"TEXT"}, nil))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE ss_smoke_dst(v text)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Exec("INSERT INTO ss_smoke_dst SELECT string_to_table($1,',')", 2, "x,x")))
		stringReplaceBoth(t, p, b, c, fixture.Select("SELECT v FROM ss_smoke_dst", []string{"v"}, []string{"TEXT"}, [][]any{{"x"}, {"x"}}))
		stringSplitStoredSetup(t, p, b, c)
		for _, s := range stringSplitStoredWants() {
			stringReplaceBoth(t, p, b, c, s)
		}
		d := fixture.ControlsReferenceDiagnostic("0A000", "set-returning functions are not allowed in WHERE")
		d.Position = "16"
		stringReplaceFailure(t, p, b, c, "SELECT 1 WHERE string_to_table('a', ',') = 'a'", d, true)
		if !b.PG {
			var dump strings.Builder
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
				t.Fatal(err)
			}
			for _, s := range stringSplitStoredWants() {
				stringReplaceBoth(t, p, b, c, s)
			}
		}
	})
}
