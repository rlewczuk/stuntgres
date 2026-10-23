package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

const cubeInheritedBindingSchema = "cube_inherited_bindings"

// Observe the emitted single-DDL protocol, not executable child/default trees.
// The names passed here are exact quoted dump declaration identities.
func cubeComparisonDeclarationBound(t *testing.T, text, kind, name string) bool {
	t.Helper()
	active, found, bound := false, 0, false
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "-- stuntgres:comparison-binding ") {
			active = line != "-- stuntgres:comparison-binding end"
		}
		if strings.HasPrefix(line, kind+" "+name) {
			found++
			bound = active
		}
	}
	if found != 1 {
		t.Fatalf("expected one %s declaration of %s, found %d", kind, name, found)
	}
	return bound
}

func cubeInheritedBindingChecks(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, extended bool) {
	t.Helper()
	rows := [][]any{{int64(1), nil}, {int64(2), "(2)"}}
	if extended {
		rows = append(rows, []any{int64(3), nil}, []any{int64(4), "(2)"})
	}
	for _, s := range []fixture.Step{
		cubeSelect(b, "SELECT id,d FROM ONLY "+cubeInheritedBindingSchema+".child ORDER BY id", []string{"id", "d"}, rows),
		cubeSelect(b, "SELECT id,g FROM ONLY "+cubeInheritedBindingSchema+".child ORDER BY id", []string{"id", "g"}, rows),
		fixture.Select("SELECT attname,attislocal,attinhcount,attgenerated AS generated_kind,atthasdef FROM pg_attribute WHERE attrelid='"+cubeInheritedBindingSchema+".child'::regclass::oid AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname", "attislocal", "attinhcount", "generated_kind", "atthasdef"}, []string{"NAME", "BOOL", "INT2", "CHAR", "BOOL"}, [][]any{{"id", false, int64(1), "", false}, {"d", false, int64(1), "", true}, {"g", false, int64(1), "s", true}}),
		fixture.Select("SELECT inhparent='"+cubeInheritedBindingSchema+".parent'::regclass::oid AS parent_identity,inhseqno FROM pg_inherits WHERE inhrelid='"+cubeInheritedBindingSchema+".child'::regclass::oid", []string{"parent_identity", "inhseqno"}, []string{"BOOL", "INT4"}, [][]any{{true, int64(1)}}),
	} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
	}
}

func cubeInheritedBindingInsertAfterRestore(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO "+cubeInheritedBindingSchema+".child(id) VALUES(3)", 1))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Exec("INSERT INTO "+cubeInheritedBindingSchema+".child(id,d) VALUES($1,$2)", 1, 4, "2")))
	cubeInheritedBindingChecks(t, p, b, c, true)
}

func TestReferenceCubeComparisonInheritedBindings(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, cubeInheritedBindingSchema)
	cubeComparisonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		phase := "setup"
		run := func(s fixture.Step) {
			if !t.Run(phase+"/"+s.Query, func(t *testing.T) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }) {
				t.FailNow()
			}
		}
		for _, q := range []string{
			"CREATE SCHEMA " + cubeInheritedBindingSchema,
			"CREATE EXTENSION cube SCHEMA " + cubeInheritedBindingSchema,
			"SET search_path=" + cubeInheritedBindingSchema + ",pg_catalog",
			"CREATE TABLE " + cubeInheritedBindingSchema + ".parent(id integer NOT NULL,d cube DEFAULT NULLIF('1'::cube,'1'::cube),g cube GENERATED ALWAYS AS (NULLIF(d,'1'::cube)) STORED)",
			"CREATE TABLE " + cubeInheritedBindingSchema + ".child() INHERITS(" + cubeInheritedBindingSchema + ".parent)",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO "+cubeInheritedBindingSchema+".child(id) VALUES(1)", 1))
		run(fixture.Prepared(fixture.Exec("INSERT INTO "+cubeInheritedBindingSchema+".child(id,d) VALUES($1,$2)", 1, 2, "2")))
		cubeInheritedBindingChecks(t, p, b, c, false)
		if b.PG {
			return
		}
		phase = "native-control"
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatalf("inherited comparison Dump: %#v", err)
		}
		text := dump.String()
		if !cubeComparisonDeclarationBound(t, text, "CREATE TABLE", `"cube_inherited_bindings"."parent"`) {
			t.Fatal("parent comparison declarations lack their binding window")
		}
		if cubeComparisonDeclarationBound(t, text, "CREATE TABLE", `"cube_inherited_bindings"."child"`) {
			t.Fatal("unchanged inherited child acquired a nondeclaring binding window")
		}
		run(fixture.Exec("SET search_path=public", 0))
		run(fixture.Exec("SET extra_float_digits=2", 0))
		settings := cubeComparisonSessionSettings(t, p, c)
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(text)); err != nil {
			t.Fatalf("inherited comparison Restore: %#v", err)
		}
		if got := cubeComparisonSessionSettings(t, p, c); got != settings {
			t.Fatal("inherited Restore changed caller settings", got, settings)
		}
		cubeInheritedBindingChecks(t, p, b, c, false)
		cubeInheritedBindingInsertAfterRestore(t, p, b, c)
		target := fixture.NewFixture(t, false, true)
		destination, err := target.Memory.Conn(target.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer destination.Close()
		if err := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(text)); err != nil {
			t.Fatalf("fresh inherited comparison Restore: %#v", err)
		}
		native := fixture.Backend{Recorder: target.MemoryRecord}
		cubeInheritedBindingChecks(t, target, native, destination, false)
		cubeInheritedBindingInsertAfterRestore(t, target, native, destination)
		if p.Postgres != nil {
			pgc, err := p.Postgres.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer pgc.Close()
			cleanup := "DROP SCHEMA IF EXISTS " + cubeInheritedBindingSchema + " CASCADE"
			if _, err := pgc.ExecContext(p.Ctx, cleanup); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if _, err := pgc.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION; "+cleanup); err != nil {
					t.Error(err)
				}
			}()
			if _, err := pgc.ExecContext(p.Ctx, text); err != nil {
				t.Fatalf("actual inherited comparison dump PostgreSQL replay: %#v", err)
			}
			if _, err := pgc.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
				t.Fatal(err)
			}
			pg := fixture.Backend{PG: true, Recorder: p.PostgresRecord}
			cubeInheritedBindingChecks(t, p, pg, pgc, false)
			cubeInheritedBindingInsertAfterRestore(t, p, pg, pgc)
		}
	})
}
