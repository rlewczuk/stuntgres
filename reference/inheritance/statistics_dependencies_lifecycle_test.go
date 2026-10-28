package reference_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStatisticsDependenciesLifecycle(t *testing.T) {
	withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
		for _, q := range []string{"CREATE TABLE dep_p(a INT,b INT)", "CREATE TABLE dep_c() INHERITS(dep_p)", "CREATE STATISTICS dep_s(dependencies) ON a,b FROM dep_p"} {
			s.run(fixture.Exec(q, 0))
		}
		s.run(fixture.Exec("INSERT INTO dep_p VALUES(1,10),(1,10),(2,20)", 3))
		s.run(fixture.Exec("INSERT INTO dep_c VALUES(1,20)", 1))
		s.run(fixture.Exec("ANALYZE dep_p", 0))
		s.run(dependenciesRows(s.pg, []any{"dep_s", false, dependenciesFull}, []any{"dep_s", true, dependenciesPartial}))
		s.run(fixture.Exec("BEGIN", 0))
		defer s.conn.ExecContext(s.pair.Ctx, "ROLLBACK")
		s.run(fixture.Exec("DELETE FROM dep_c", 1))
		s.run(fixture.Exec("SAVEPOINT dep_save", 0))
		s.run(fixture.Exec("ANALYZE dep_p", 0))
		s.run(dependenciesRows(s.pg, []any{"dep_s", false, dependenciesFull}, []any{"dep_s", true, dependenciesFull}))
		// Another real session sees the committed immutable fact, not the private refresh.
		for _, b := range s.pair.Backends() {
			if b.PG == s.pg {
				fixture.RunStep(t, s.pair.Ctx, b.DB, b.Recorder, dependenciesRows(s.pg, []any{"dep_s", false, dependenciesFull}, []any{"dep_s", true, dependenciesPartial}))
			}
		}
		s.run(fixture.Exec("ROLLBACK TO dep_save", 0))
		s.run(dependenciesRows(s.pg, []any{"dep_s", false, dependenciesFull}, []any{"dep_s", true, dependenciesPartial}))
		s.run(fixture.Exec("ANALYZE dep_p", 0))
		s.run(fixture.Exec("ROLLBACK", 0))
		s.run(dependenciesRows(s.pg, []any{"dep_s", false, dependenciesFull}, []any{"dep_s", true, dependenciesPartial}))
		s.run(fixture.Exec("BEGIN", 0))
		s.run(fixture.Exec("DELETE FROM dep_c", 1))
		s.run(fixture.Exec("ANALYZE dep_p", 0))
		s.run(fixture.Exec("COMMIT", 0))
		s.run(dependenciesRows(s.pg, []any{"dep_s", false, dependenciesFull}, []any{"dep_s", true, dependenciesFull}))
		for _, q := range []string{"CREATE TABLE dep_part(a INT,b INT) PARTITION BY RANGE(a)", "CREATE TABLE dep_leaf PARTITION OF dep_part FOR VALUES FROM(0) TO(10)", "CREATE STATISTICS dep_part_s(dependencies) ON a,b FROM dep_part"} {
			s.run(fixture.Exec(q, 0))
		}
		s.run(fixture.Exec("INSERT INTO dep_part VALUES(1,10),(2,20)", 2))
		s.run(fixture.Exec("ANALYZE dep_part", 0))
		s.run(dependenciesRows(s.pg, []any{"dep_part_s", true, dependenciesFull}, []any{"dep_s", false, dependenciesFull}, []any{"dep_s", true, dependenciesFull}))
	})
}

func TestReferenceStatisticsDependenciesDiagnostics(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"dep_actor"})
	fixture.SchemaReferenceCleanup(t, "dep_hidden")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			for _, q := range []string{"CREATE ROLE dep_actor", "GRANT USAGE ON SCHEMA public TO dep_actor", "CREATE TABLE dep_acl(a INT,b INT)", "CREATE STATISTICS dep_s(dependencies) ON a,b FROM dep_acl", "CREATE SCHEMA dep_hidden", "CREATE TABLE dep_hidden.h(a INT,b INT)"} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Exec("INSERT INTO dep_acl VALUES(1,10),(1,10),(2,20)", 3))
			run(fixture.Exec("ANALYZE dep_acl", 0))
			run(fixture.Exec("INSERT INTO dep_acl VALUES(1,20)", 1))
			var notices []fixture.Diagnostic
			_, actor, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			arun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, actor, rec, s) }
			arun(fixture.Exec("SET ROLE dep_actor", 0))
			defer actor.ExecContext(p.Ctx, "RESET ROLE")
			for _, prepared := range []bool{false, true} {
				notices = nil
				step := fixture.Exec("ANALYZE dep_acl", 0)
				step.Prepared = prepared
				arun(step)
				want := fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000", Message: `permission denied to analyze "dep_acl", skipping it`}
				if !reflect.DeepEqual(notices, []fixture.Diagnostic{want}) {
					t.Fatal(notices)
				}
				step = fixture.ErrorDiagnostic("ANALYZE dep_hidden.h", fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema dep_hidden"))
				step.Prepared = prepared
				arun(step)
				run(dependenciesRows(b.PG, []any{"dep_s", false, dependenciesFull}))
			}
			run(fixture.Exec("GRANT MAINTAIN ON dep_acl TO dep_actor", 0))
			arun(fixture.Prepared(fixture.Exec("ANALYZE dep_acl", 0)))
			run(dependenciesRows(b.PG, []any{"dep_s", false, dependenciesPartial}))
		})
	}
}

func TestReferenceStatisticsDependenciesControls(t *testing.T) {
	p := fixture.NewScenario(t)
	native := p.Backends()[0]
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, native.DB, native.Recorder, s) }
	for _, q := range []string{"CREATE TABLE dep_t(a INT,hole INT,b INT)", "ALTER TABLE dep_t DROP COLUMN hole", "CREATE STATISTICS dep_s(ndistinct,dependencies) ON a,b FROM dep_t", "CREATE STATISTICS dep_e(dependencies) ON (a+0),(b+0) FROM dep_t", "CREATE TABLE dep_z(a INT,b INT)", "CREATE STATISTICS dep_z_s(dependencies) ON a,b FROM dep_z"} {
		run(fixture.Exec(q, 0))
	}
	run(fixture.Exec("INSERT INTO dep_t VALUES(1,10),(1,10),(2,20),(1,20)", 4))
	run(fixture.Exec("INSERT INTO dep_z VALUES(1,10),(1,20),(2,10),(2,20)", 4))
	run(fixture.Exec("ANALYZE dep_t,dep_z", 0))
	// Rebuild the expression-only fact with b absent from scalar selection.
	// Its captured snapshot must still retain b's actual usage authority.
	run(fixture.Exec("ANALYZE dep_t(a)", 0))
	run(fixture.Exec("DELETE FROM dep_t WHERE a=1 AND b=20", 1))
	run(fixture.Exec("DELETE FROM dep_z WHERE (a=1 AND b=20) OR (a=2 AND b=10)", 2))
	want := func(pg bool) fixture.Step {
		return dependenciesRows(pg, []any{"dep_e", false, `{"-1 => -2": 0.250000, "-2 => -1": 0.500000}`}, []any{"dep_s", false, `{"1 => 3": 0.250000, "3 => 1": 0.500000}`}, []any{"dep_z_s", false, nil})
	}
	run(want(false))
	held, err := p.Native.QueryContext(p.Ctx, dependenciesQuery)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	metadata, err := held.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if metadata[2].DatabaseTypeName() != "PG_DEPENDENCIES" {
		t.Fatal(metadata[2])
	}
	var saved bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &saved); err != nil {
		t.Fatal(err)
	}
	// A duplicate actual captured state record must reject the entire import.
	var line string
	for _, v := range strings.Split(saved.String(), "\n") {
		if strings.HasPrefix(v, "-- stuntgres-ndistinct-v1 ") {
			line = v
			break
		}
	}
	if line == "" {
		t.Fatal("missing captured state")
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(saved.String()+line+"\n")); err == nil {
		t.Fatal("duplicate captured state accepted")
	}
	run(want(false))
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(line, "-- stuntgres-ndistinct-v1 "))
	if err != nil {
		t.Fatal(err)
	}
	corrupt := strings.Replace(string(raw), `"degree":0.25`, `"degree":0`, 1)
	if corrupt == string(raw) {
		t.Fatal("missing actual fractional construction state")
	}
	corruptLine := "-- stuntgres-ndistinct-v1 " + base64.StdEncoding.EncodeToString([]byte(corrupt))
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(strings.Replace(saved.String(), line, corruptLine, 1))); err == nil {
		t.Fatal("zero-degree corrupt state accepted")
	}
	run(want(false))
	canceled, cancel := context.WithCancel(p.Ctx)
	cancel()
	if err := stuntgres.Restore(canceled, p.Native, bytes.NewReader(saved.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	run(want(false))
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if !held.Next() {
		t.Fatal(held.Err())
	}
	var name string
	var inherited bool
	var value string
	if err := held.Scan(&name, &inherited, &value); err != nil || name != "dep_e" || inherited || value != `{"-1 => -2": 0.250000, "-2 => -1": 0.500000}` {
		t.Fatal(name, inherited, value, err)
	}
	held.Close()
	if metadata[2].DatabaseTypeName() != "PG_DEPENDENCIES" {
		t.Fatal("held metadata changed", metadata[2])
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(saved.Bytes())); err != nil {
		t.Fatal(err)
	}
	run(want(false))
	run(ndistinctRows(false, []any{"dep_e", false, nil}, []any{"dep_s", false, `{"1, 3": 3}`}, []any{"dep_z_s", false, nil}))
	var again bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(saved.Bytes(), again.Bytes()) {
		t.Fatal("redump changed captured fractions or NULL")
	}
	run(fixture.Exec("ANALYZE dep_t,dep_z", 0))
	run(dependenciesRows(false, []any{"dep_e", false, `{"-1 => -2": 1.000000, "-2 => -1": 1.000000}`}, []any{"dep_s", false, `{"1 => 3": 1.000000, "3 => 1": 1.000000}`}, []any{"dep_z_s", false, dependenciesFull}))
	for _, b := range p.Backends() {
		if b.PG {
			if _, err := b.DB.ExecContext(p.Ctx, saved.String()); err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, dependenciesRows(true))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("ANALYZE dep_t,dep_z", 0))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, dependenciesRows(true, []any{"dep_e", false, `{"-1 => -2": 1.000000, "-2 => -1": 1.000000}`}, []any{"dep_s", false, `{"1 => 3": 1.000000, "3 => 1": 1.000000}`}, []any{"dep_z_s", false, dependenciesFull}))
		}
	}
}
