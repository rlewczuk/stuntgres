package reference_test

import (
	"bytes"
	"database/sql"
	"math"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceMathHyperbolicConsumerAtomicity(t *testing.T) {
	p := fixture.NewScenario(t)
	referenceHyperbolicBackends(t, p, func(t *testing.T, backend fixture.Backend) {
		run := func(s fixture.Step) {
			fixture.RunStep(t, p.Ctx, referenceArithmeticExecutor{backend.DB, t}, backend.Recorder, s)
		}
		run(fixture.Exec("CREATE TABLE hyperbolic_source(id INT,d DOUBLE PRECISION)", 0))
		run(fixture.Exec("CREATE TABLE hyperbolic_target(id INT,d DOUBLE PRECISION)", 0))
		run(fixture.Exec("INSERT INTO hyperbolic_target VALUES(99,7)", 1))
		for _, prepared := range []bool{false, true} {
			run(fixture.Exec("DELETE FROM hyperbolic_source", map[bool]int64{false: 0, true: 2}[prepared]))
			run(fixture.Exec("INSERT INTO hyperbolic_source VALUES(1,2),(2,0)", 2))
			for _, query := range []string{"INSERT INTO hyperbolic_target SELECT id,acosh(d) FROM hyperbolic_source ORDER BY id", "UPDATE hyperbolic_source SET d=acosh(d)"} {
				failure := referenceHyperbolicDomain(query)
				failure.SelectRows, failure.Prepared = false, prepared
				run(failure)
				run(fixture.Select("SELECT id,d FROM hyperbolic_target ORDER BY id", []string{"id", "d"}, []string{"INT4", "FLOAT8"}, [][]any{{int64(99), float64(7)}}))
				run(fixture.Select("SELECT id,d FROM hyperbolic_source ORDER BY id", []string{"id", "d"}, []string{"INT4", "FLOAT8"}, [][]any{{int64(1), float64(2)}, {int64(2), float64(0)}}))
			}
			run(fixture.Exec("UPDATE hyperbolic_source SET d=1 WHERE id=2", 1))
			insert := fixture.Exec("INSERT INTO hyperbolic_target SELECT id,acosh(d) FROM hyperbolic_source ORDER BY id", 2)
			insert.Prepared = prepared
			run(insert)
			update := fixture.Exec("UPDATE hyperbolic_source SET d=acosh(d)", 2)
			update.Prepared = prepared
			run(update)
			for _, table := range []string{"hyperbolic_source", "hyperbolic_target"} {
				referenceHyperbolicFloats(t, p, backend, []referenceScalarFloatCase{{table, "SELECT d AS result FROM " + table + " WHERE id=1", "FLOAT8", 1.3169578969248166, 2}, {table + "_zero", "SELECT d AS result FROM " + table + " WHERE id=2", "FLOAT8", 0, 0}})
			}
			run(fixture.Exec("DELETE FROM hyperbolic_target WHERE id < 99", 2))
		}
	})
}

func TestReferenceMathHyperbolicAuthorization(t *testing.T) {
	const reader = "hyperbolic_column_reader"
	fixture.RoleReferenceCleanup(t, []string{reader})
	p := fixture.NewScenario(t)
	referenceHyperbolicBackends(t, p, func(t *testing.T, backend fixture.Backend) {
		conn, err := backend.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		defer conn.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
		if backend.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO PUBLIC", 0))
		}
		for _, query := range []string{"CREATE ROLE " + reader, "CREATE TABLE hyperbolic_acl(secret DOUBLE PRECISION,visible INT)", "GRANT SELECT(visible) ON hyperbolic_acl TO " + reader} {
			run(fixture.Exec(query, 0))
		}
		denied := func(query string) fixture.Step {
			s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for table hyperbolic_acl"})
			s.SelectRows, s.ErrorStage = true, "execute"
			return s
		}
		run(fixture.Exec("SET SESSION AUTHORIZATION "+reader, 0))
		for _, query := range []string{"SELECT sinh(secret) FROM hyperbolic_acl", "SELECT acosh(secret) FROM hyperbolic_acl"} {
			run(denied(query))
			run(fixture.Prepared(denied(query)))
		}
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("INSERT INTO hyperbolic_acl VALUES(0,1)", 1))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+reader, 0))
		// The invalid acosh argument must not leak ahead of the column ACL.
		run(denied("SELECT acosh(secret) FROM hyperbolic_acl"))
		run(fixture.Prepared(denied("SELECT acosh(secret) FROM hyperbolic_acl")))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT SELECT(secret) ON hyperbolic_acl TO "+reader, 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+reader, 0))
		const query = "SELECT sinh(secret) FROM hyperbolic_acl"
		stmt, err := conn.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{conn, stmt, query}
		value := fixture.Select(query, []string{"sinh"}, []string{"FLOAT8"}, [][]any{{float64(0)}})
		fixture.RunStep(t, p.Ctx, held, backend.Recorder, value)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("REVOKE SELECT(secret) ON hyperbolic_acl FROM "+reader, 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+reader, 0))
		fixture.RunStep(t, p.Ctx, held, backend.Recorder, denied(query))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT SELECT(secret) ON hyperbolic_acl TO "+reader, 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+reader, 0))
		fixture.RunStep(t, p.Ctx, held, backend.Recorder, value)
	})
}

func referenceHyperbolicView(t *testing.T, p *fixture.Pair, backend fixture.Backend) {
	t.Helper()
	for _, name := range referenceHyperbolicNames {
		want := float64(0)
		if name == "cosh" {
			want = 1
		}
		for _, tc := range []struct {
			id   string
			want float64
		}{{"1", want}, {"2", want}, {"3", math.NaN()}} {
			if tc.id == "2" && name != "cosh" && name != "acosh" {
				tc.want = math.Copysign(0, -1)
			}
			referenceHyperbolicFloats(t, p, backend, []referenceScalarFloatCase{{name + tc.id, "SELECT " + name + " AS result FROM hyperbolic_view WHERE id=" + tc.id, "FLOAT8", tc.want, 0}})
		}
		null := fixture.Select("SELECT "+name+" FROM hyperbolic_view WHERE id=4", []string{name}, []string{"FLOAT8"}, [][]any{{nil}})
		null.CheckMetadata = func(t *testing.T, rows *sql.Rows) { referenceMathTrigMetadata(t, rows, backend.PG, true) }
		referenceMathTrigStep(t, p, backend, null)
	}
}

func TestReferenceMathHyperbolicControls(t *testing.T) {
	p := fixture.NewScenario(t)
	var emitted []byte
	referenceHyperbolicBackends(t, p, func(t *testing.T, backend fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
		run(fixture.Exec("CREATE TABLE hyperbolic_stored(id INT,d DOUBLE PRECISION,a DOUBLE PRECISION)", 0))
		run(fixture.Exec("INSERT INTO hyperbolic_stored VALUES(1,0,1),(2,'-0',1),(3,'NaN','NaN'),(4,NULL,NULL)", 4))
		run(fixture.Exec("CREATE VIEW hyperbolic_view AS SELECT id,sinh(d) AS sinh,cosh(d) AS cosh,tanh(d) AS tanh,asinh(d) AS asinh,acosh(a) AS acosh,atanh(d) AS atanh FROM hyperbolic_stored", 0))
		referenceHyperbolicView(t, p, backend)
		// Native controls have no PostgreSQL API equivalent. The shared SQL view
		// assertions also validate actual native-emitted SQL on PostgreSQL below.
		if !backend.PG {
			const query = "SELECT sinh FROM hyperbolic_view WHERE id=1"
			stmt, err := backend.DB.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			retained, err := p.Native.QueryContext(p.Ctx, "SELECT sinh FROM hyperbolic_view ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			emitted = bytes.Clone(dump.Bytes())
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
			referenceHyperbolicView(t, p, backend)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{backend.DB, stmt, query}, backend.Recorder, fixture.Select(query, []string{"sinh"}, []string{"FLOAT8"}, [][]any{{float64(0)}}))
			for _, want := range []any{float64(0), math.Copysign(0, -1), math.NaN(), nil} {
				if !retained.Next() {
					t.Fatal("retained row missing", retained.Err())
				}
				var got any
				if err := retained.Scan(&got); err != nil {
					t.Fatal(err)
				}
				if want == nil {
					if got != nil {
						t.Fatal("retained NULL", got)
					}
				} else if value, ok := got.(float64); !ok || !referenceScalarFloatMatches(value, want.(float64), 0) {
					t.Fatalf("retained %v want %v", got, want)
				}
			}
			if retained.Next() || retained.Err() != nil {
				t.Fatal("retained end", retained.Err())
			}
			if err := retained.Close(); err != nil {
				t.Fatal(err)
			}
		}
		// A source mutation proves symbolic calculation, not cached view cells.
		run(fixture.Exec("UPDATE hyperbolic_stored SET d=1,a=2 WHERE id=1", 1))
		referenceHyperbolicFloats(t, p, backend, []referenceScalarFloatCase{{"view_recalculated", "SELECT sinh AS result FROM hyperbolic_view WHERE id=1", "FLOAT8", 1.1752011936438014, 2}, {"view_acosh", "SELECT acosh AS result FROM hyperbolic_view WHERE id=1", "FLOAT8", 1.3169578969248166, 2}})
	})
	if p.Postgres != nil && emitted != nil {
		backend := fixture.Backend{Name: "postgres_replay", DB: p.Postgres, Recorder: p.PostgresRecord, PG: true}
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("DROP VIEW hyperbolic_view", 0))
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("DROP TABLE hyperbolic_stored", 0))
		if _, err := backend.DB.ExecContext(p.Ctx, string(emitted)); err != nil {
			t.Fatal("actual native Dump replay", err)
		}
		referenceHyperbolicView(t, p, backend)
	}
}
