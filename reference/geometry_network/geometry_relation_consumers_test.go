package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func geometryRelationCreateConsumers(run func(fixture.Step)) {
	run(fixture.Exec("CREATE TABLE geom_rel_source(id integer NOT NULL,p point,s lseg,b box,z box,closest point GENERATED ALWAYS AS (p ## s) STORED,common box GENERATED ALWAYS AS (b # z) STORED,distance float8 GENERATED ALWAYS AS (p <-> s) STORED,hit boolean GENERATED ALWAYS AS (s ?# b) STORED,CONSTRAINT geom_rel_inside CHECK(p <@ b))", 0))
	run(fixture.Exec("CREATE VIEW geom_rel_view AS SELECT id,p ## s AS closest,b # z AS common,p <-> s AS distance,s ?# b AS hit FROM geom_rel_source", 0))
	run(fixture.Exec("CREATE VIEW geom_rel_constants AS SELECT lseg '[(0,0),(2,0)]' # lseg '[(1,-1),(1,1)]' AS crossing,point '(1,1)' ## lseg '[(0,0),(2,0)]' AS closest,box '(2,2),(0,0)' # box '(1.5,1.5),(0.5,0.5)' AS common,NULL::point ## lseg '[(0,0),(2,0)]' AS absent,NULL::box # box '(1,1),(0,0)' AS empty_box,NULL::point <-> point '(0,0)' AS empty_distance,NULL::line ?# line '{0,1,0}' AS empty_hit", 0))
	run(fixture.Exec("INSERT INTO geom_rel_source(id,p,s,b,z) VALUES(1,'(1,1)','[(0,0),(2,0)]','(2,2),(0,0)','(1.5,1.5),(0.5,0.5)'),(2,NULL,NULL,NULL,NULL)", 2))
}

func geometryRelationConsumerChecks(t *testing.T, b fixture.Backend, run func(fixture.Step)) {
	t.Helper()
	names := []string{"id", "closest", "common", "distance", "hit"}
	types := []string{"INT4", "POINT", "BOX", "FLOAT8", "BOOL"}
	rows := [][]any{{int64(1), "(1,0)", grI, float64(1), true}, {int64(2), nil, nil, nil, nil}}
	for _, source := range []string{"geom_rel_source", "geom_rel_view"} {
		s := boxSelect(b, "SELECT id,closest,common,distance,hit FROM "+source+" ORDER BY id", names, types, rows)
		run(s)
		run(fixture.Prepared(s))
	}
	s := boxSelect(b, "SELECT * FROM geom_rel_constants", []string{"crossing", "closest", "common", "absent", "empty_box", "empty_distance", "empty_hit"}, []string{"POINT", "POINT", "BOX", "POINT", "BOX", "FLOAT8", "BOOL"}, [][]any{{"(1,0)", "(1,0)", grI, nil, nil, nil, nil}})
	run(s)
	run(fixture.Prepared(s))
	// New rows on every replay target execute the stored definitions again.
	run(fixture.Exec("BEGIN", 0))
	run(fixture.Prepared(fixture.Exec("INSERT INTO geom_rel_source(id,p,s,b,z) VALUES($1,$2,$3,$4,$5)", 1, 7, "(0,1)", grS, grB, grI)))
	s = boxSelect(b, "SELECT id,closest,common,distance,hit FROM geom_rel_view WHERE id=7", names, types, [][]any{{int64(7), "(0,0)", grI, float64(1), true}})
	run(s)
	run(fixture.Prepared(s))
	run(fixture.Exec("ROLLBACK", 0))
	d := fixture.ControlsReferenceDiagnostic("23514", "new row for relation \"geom_rel_source\" violates check constraint \"geom_rel_inside\"")
	d.Schema, d.Table, d.Constraint = "public", "geom_rel_source", "geom_rel_inside"
	d.Detail = "Failing row contains (8, (4,0), [(0,0),(2,0)], (2,2),(0,0), (1.5,1.5),(0.5,0.5), (2,0), (1.5,1.5),(0.5,0.5), 2, t)."
	for _, prepared := range []bool{false, true} {
		errStep := fixture.ErrorDiagnostic("INSERT INTO geom_rel_source(id,p,s,b,z) VALUES(8,'(4,0)','[(0,0),(2,0)]','(2,2),(0,0)','(1.5,1.5),(0.5,0.5)')", d)
		errStep.Prepared, errStep.ErrorStage = prepared, "execute"
		run(errStep)
		q := "INSERT INTO geom_rel_source(id,p,s,b,z) VALUES(9,$1,$2,$3,$4),(10,$5,$6,$3,$4)"
		run(boxError(q, "22003", "value out of range: overflow", "", "", prepared, true, grR, grS, grB, grI, "(-1e308,0)", "[(1e308,0),(1e308,1)]"))
		run(fixture.Select("SELECT id FROM geom_rel_source ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
	}
}

func geometryRelationRoutineAuthority(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec("CREATE ROLE geom_rel_reader", 0))
	run(fixture.Exec("GRANT USAGE ON SCHEMA public TO geom_rel_reader", 0))
	const signature = "pg_catalog.close_ps(point,lseg)"
	q := "SELECT $1::point ## $2::lseg AS v"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	defer func() {
		for _, q := range []string{"RESET ROLE", "GRANT EXECUTE ON FUNCTION " + signature + " TO PUBLIC", "REVOKE EXECUTE ON FUNCTION " + signature + " FROM geom_rel_reader"} {
			if _, err := c.ExecContext(p.Ctx, q); err != nil {
				t.Error(err)
			}
		}
	}()
	run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+signature+" FROM PUBLIC", 0))
	run(fixture.Exec("SET ROLE geom_rel_reader", 0))
	d := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for function close_ps"), grR, grS)
	d.SelectRows, d.ErrorStage = true, "execute"
	fixture.RunStep(t, p.Ctx, held, b.Recorder, d)
	run(d)
	run(fixture.Exec("RESET ROLE", 0))
	run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+signature+" TO geom_rel_reader", 0))
	run(fixture.Exec("SET ROLE geom_rel_reader", 0))
	fixture.RunStep(t, p.Ctx, held, b.Recorder, boxSelect(b, q, []string{"v"}, []string{"POINT"}, [][]any{{"(1,0)"}}, grR, grS))
	run(fixture.Exec("RESET ROLE", 0))
}

func TestReferenceGeometryRelationConsumers(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"geom_rel_reader"})
	geometryRelationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		geometryRelationCreateConsumers(run)
		geometryRelationConsumerChecks(t, b, run)
		if !b.PG {
			// Native controls have no PG counterpart. Execute their emitted SQL
			// on the real server rather than treating text inspection as replay.
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			old, err := c.QueryContext(p.Ctx, "SELECT closest FROM geom_rel_view WHERE id=1")
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			retained := boxSelect(b, "", []string{"closest"}, []string{"POINT"}, [][]any{{"(1,0)"}})
			retained.CheckMetadata(t, old)
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			geometryRelationConsumerChecks(t, b, run)
			retained.CheckMetadata(t, old)
			if !old.Next() {
				t.Fatalf("retained row: %v", old.Err())
			}
			var v any
			if err := old.Scan(&v); err != nil {
				t.Fatal(err)
			}
			retained.CheckRaw(t, 0, 0, v)
			if fixture.Normalize(v) != "(1,0)" {
				t.Fatalf("retained value %v", v)
			}
			if old.Next() || old.Err() != nil {
				t.Fatalf("retained exhaustion: %v", old.Err())
			}
			old.Close()
			target := fixture.NewFixture(t, false, true)
			if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			geometryRelationConsumerChecks(t, b, func(s fixture.Step) { fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, s) })
			if p.Postgres != nil {
				if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
					t.Fatal(err)
				}
				if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
					t.Fatalf("emitted SQL replay: %v", err)
				}
				geometryRelationConsumerChecks(t, fixture.Backend{PG: true}, func(s fixture.Step) { fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, s) })
				if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
					t.Fatal(err)
				}
			}
		}
		t.Run(fmt.Sprintf("%s_authority", b.Name), func(t *testing.T) { geometryRelationRoutineAuthority(t, p, b, c) })
	})
}
