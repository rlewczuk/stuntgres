package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func stringSplitAdditionalDiagnostics(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	for _, family := range []struct {
		name        string
		args, types []string
	}{
		{"split_part", []string{"'a'", "','", "1"}, []string{"unknown", "unknown", "integer"}},
		{"string_to_array", []string{"'a'", "','", "'x'"}, []string{"unknown", "unknown", "unknown"}},
		{"string_to_table", []string{"'a'", "','", "'x'"}, []string{"unknown", "unknown", "unknown"}},
	} {
		for i := range family.args {
			for _, wrong := range []struct{ sql, typ string }{{"true", "boolean"}, {"'a'::bytea", "bytea"}, {"1::bigint", "bigint"}} {
				args := append([]string(nil), family.args...)
				types := append([]string(nil), family.types...)
				args[i] = wrong.sql
				types[i] = wrong.typ
				q := "SELECT " + family.name + "(" + strings.Join(args, ",") + ")"
				d := fixture.ControlsReferenceDiagnostic("42883", "function "+family.name+"("+strings.Join(types, ", ")+") does not exist")
				d.Position = "8"
				d.Hint = hint
				t.Run(q, func(t *testing.T) { stringReplaceFailure(t, p, b, c, q, d, true) })
			}
		}
	}
	for _, expr := range []string{`split_part(NULL::text COLLATE "C",'a' COLLATE "POSIX",1)`, `string_to_array('' COLLATE "C",',' COLLATE "POSIX")`, `string_to_table(NULL::text COLLATE "C",',' COLLATE "POSIX")`} {
		q := "SELECT " + expr
		d := fixture.ControlsReferenceDiagnostic("42P21", `collation mismatch between explicit collations "C" and "POSIX"`)
		d.Position = fixture.Position(len([]rune(q[:strings.LastIndex(q, "COLLATE")])) + 1)
		stringReplaceFailure(t, p, b, c, q, d, true)
	}
	q := "SELECT string_to_table(v,',') FROM dst FOR UPDATE"
	d := fixture.ControlsReferenceDiagnostic("0A000", "FOR UPDATE is not allowed with set-returning functions in the target list")
	for _, q := range []string{q, "SELECT replace(string_to_table('a,b',','),NULL,'x') FROM dst FOR UPDATE"} {
		stringReplaceFailure(t, p, b, c, q, d, true)
	}
	q = "SELECT * FROM (VALUES(string_to_table('',','))) t(v)"
	d = fixture.ControlsReferenceDiagnostic("0A000", "set-returning functions are not allowed in VALUES")
	d.Position = fixture.Position(strings.Index(q, "string_to_table") + 1)
	stringReplaceFailure(t, p, b, c, q, d, true)
	q = "INSERT INTO dst(v) VALUES(string_to_table('a,b',',')) ORDER BY 1 LIMIT 1"
	d = fixture.ControlsReferenceDiagnostic("0A000", "set-returning functions are not allowed in VALUES")
	d.Position = fixture.Position(strings.Index(q, "string_to_table") + 1)
	stringReplaceFailure(t, p, b, c, q, d, true)
	q = "SELECT count(*) OVER(ROWS string_to_table('',',')::int PRECEDING) FROM dst"
	d = fixture.ControlsReferenceDiagnostic("0A000", "set-returning functions are not allowed in window definitions")
	d.Position = fixture.Position(strings.Index(q, "string_to_table") + 1)
	stringReplaceFailure(t, p, b, c, q, d, true)
	q = "SELECT * FROM string_to_table(s.v,',') t(v), (VALUES('a')) s(v)"
	d = fixture.ControlsReferenceDiagnostic("42P01", `missing FROM-clause entry for table "s"`)
	d.Position = fixture.Position(strings.Index(q, "s.v") + 1)
	stringReplaceFailure(t, p, b, c, q, d, true)
	for _, q := range []string{"UPDATE dst SET v=t.x FROM string_to_table(dst.v,',') t(x)", "DELETE FROM dst USING string_to_table(dst.v,',') t(x) WHERE dst.v=t.x"} {
		d := fixture.ControlsReferenceDiagnostic("42P10", `invalid reference to FROM-clause entry for table "dst"`)
		d.Hint = `There is an entry for table "dst", but it cannot be referenced from this part of the query.`
		d.Position = fixture.Position(strings.Index(q, "dst.v") + 1)
		stringReplaceFailure(t, p, b, c, q, d, true)
	}
	// Full-input parsing cannot publish a valid INSERT prefix before the suffix error.
	q = "INSERT INTO dst VALUES(split_part('a,b',',',1)) +"
	d = fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "+"`)
	d.Position = fixture.Position(strings.Index(q, "+") + 1)
	stringReplaceFailure(t, p, b, c, q, d, true)
	// Reached scalar functions remain legal in window partition/order stages.
	stringReplaceBoth(t, p, b, c, fixture.Select("SELECT v,row_number() OVER(PARTITION BY v ORDER BY v) AS n FROM string_to_table('a,a,b',',') t(v) ORDER BY v,n", []string{"v", "n"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(1)}, {"a", int64(2)}, {"b", int64(1)}}))
}

func stringSplitStoredDependency(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE TABLE ss_dependency(s text)", 0), fixture.Exec("INSERT INTO ss_dependency VALUES('a,b')", 1),
		fixture.Exec("CREATE VIEW ss_dependency_view AS SELECT string_to_table(s,',') AS v FROM ss_dependency", 0),
	} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
	d := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop table ss_dependency because other objects depend on it")
	d.Detail = "view ss_dependency_view depends on table ss_dependency"
	d.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
	stringReplaceFailure(t, p, b, c, "DROP TABLE ss_dependency", d, false)
	stringReplaceBoth(t, p, b, c, fixture.Select("SELECT v FROM ss_dependency_view ORDER BY v", []string{"v"}, []string{"TEXT"}, [][]any{{"a"}, {"b"}}))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP TABLE ss_dependency CASCADE", 0))
	stringReplaceBoth(t, p, b, c, fixture.Select("SELECT relname FROM pg_catalog.pg_class WHERE relname='ss_dependency_view'", []string{"relname"}, []string{"NAME"}, nil))
}

func stringSplitReadOnlyViews(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE VIEW ss_folded_view AS SELECT replace(string_to_table('a,b',','),NULL,'x') AS v FROM ss_stored", 0),
		fixture.Exec("CREATE VIEW ss_function_only_view AS SELECT v FROM string_to_table('a,b',',') t(v)", 0),
		fixture.Exec("CREATE VIEW ss_function_target_view AS SELECT string_to_table('a,b',',') AS v FROM string_to_table('x',',')", 0),
		fixture.Exec("CREATE VIEW ss_source_free_target_view AS SELECT string_to_table('a,b',',') AS v", 0),
		fixture.Exec("CREATE VIEW ss_derived_target_view AS SELECT string_to_table('a,b',',') AS v FROM (SELECT v FROM string_to_table('x',',') t(v)) AS src", 0),
		fixture.Exec("CREATE VIEW ss_nested_target_view AS SELECT string_to_table('a,b',',') AS v FROM ss_function_only_view", 0),
	} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
	s := fixture.Select("SELECT v FROM ss_folded_view", []string{"v"}, []string{"TEXT"}, [][]any{{nil}, {nil}})
	s.Bag = true
	stringReplaceBoth(t, p, b, c, s)
	for _, view := range []string{"ss_target_view", "ss_folded_view", "ss_derived_target_view", "ss_nested_target_view"} {
		t.Run("inherited_lock_"+view, func(t *testing.T) {
			stringReplaceFailure(t, p, b, c, "SELECT * FROM "+view+" FOR UPDATE", fixture.ControlsReferenceDiagnostic("0A000", "FOR UPDATE is not allowed with set-returning functions in the target list"), false)
		})
	}
	stringReplaceBoth(t, p, b, c, fixture.Select("SELECT v FROM ss_function_target_view ORDER BY v FOR UPDATE", []string{"v"}, []string{"TEXT"}, [][]any{{"a"}, {"b"}}))
	stringReplaceBoth(t, p, b, c, fixture.Select("SELECT v FROM ss_source_free_target_view ORDER BY v FOR UPDATE", []string{"v"}, []string{"TEXT"}, [][]any{{"a"}, {"b"}}))
	stringReplaceBoth(t, p, b, c, fixture.Select("SELECT v FROM ss_function_only_view ORDER BY v FOR UPDATE", []string{"v"}, []string{"TEXT"}, [][]any{{"a"}, {"b"}}))
	stringReplaceBoth(t, p, b, c, fixture.Select("SELECT id,a,b,n FROM ss_from_view ORDER BY id,n FOR UPDATE", []string{"id", "a", "b", "n"}, []string{"INT4", "TEXT", "TEXT", "INT8"}, [][]any{{int64(1), "b", "x", int64(1)}, {int64(2), "c", "x", int64(1)}, {int64(2), "d", nil, int64(2)}}))
	for _, view := range []struct{ name, detail string }{
		{"ss_target_view", "Views that return set-returning functions are not automatically updatable."},
		{"ss_folded_view", "Views that return set-returning functions are not automatically updatable."},
		{"ss_function_target_view", "Views that return set-returning functions are not automatically updatable."},
		{"ss_function_only_view", "Views that do not select from a single table or view are not automatically updatable."},
	} {
		for _, op := range []struct{ verb, gerund, command, q string }{
			{"insert into", "inserting into", "INSERT", "INSERT INTO " + view.name + "(v) SELECT 'z' WHERE FALSE"},
			{"update", "updating", "UPDATE", "UPDATE " + view.name + " SET v='z' WHERE FALSE"},
			{"delete from", "deleting from", "DELETE", "DELETE FROM " + view.name + " WHERE FALSE"},
		} {
			d := fixture.ControlsReferenceDiagnostic("55000", "cannot "+op.verb+" view \""+view.name+"\"")
			d.Detail = view.detail
			d.Hint = "To enable " + op.gerund + " the view, provide an INSTEAD OF " + op.command + " trigger or an unconditional ON " + op.command + " DO INSTEAD rule."
			stringReplaceFailure(t, p, b, c, op.q, d, true)
		}
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP VIEW ss_folded_view,ss_function_only_view,ss_function_target_view,ss_source_free_target_view,ss_derived_target_view,ss_nested_target_view", 0))
}
