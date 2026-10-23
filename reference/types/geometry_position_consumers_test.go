package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func geometryPositionCreateConsumers(run func(fixture.Step)) {
	run(fixture.Exec("CREATE TABLE position_store(id integer NOT NULL,b box,p polygon,left_box boolean GENERATED ALWAYS AS (b << box '(3,3),(2,2)') STORED,left_poly boolean GENERATED ALWAYS AS (p << polygon '((2,2))') STORED,CONSTRAINT position_ok CHECK(b &< box '(3,3),(2,2)'))", 0))
	run(fixture.Exec("CREATE VIEW position_view AS SELECT id,left_box,left_poly,b <^ box '(3,3),(2,2)' AS touch,NULL::polygon &<| p AS absent FROM position_store", 0))
	// Constant typed NULL operands must survive capture and replay on either
	// formal, not just arrive from nullable rows at runtime.
	var expressions []string
	for _, shape := range geometryPositionShapes[1:3] {
		for i, direction := range geometryPositionDirections {
			if !geometryPositionAdmitted(shape.typ, direction.op) {
				continue
			}
			for mask := 1; mask < 4; mask++ {
				a, b := shape.typ+" "+boxQuote(shape.a), shape.typ+" "+boxQuote(shape.b)
				if mask&1 != 0 {
					a = "NULL::" + shape.typ
				}
				if mask&2 != 0 {
					b = "NULL::" + shape.typ
				}
				expressions = append(expressions, fmt.Sprintf("%s %s %s AS %s_%d_%d", a, direction.op, b, shape.typ, i, mask))
			}
		}
	}
	run(fixture.Exec("CREATE VIEW position_constants AS SELECT "+strings.Join(expressions, ","), 0))
	run(fixture.Exec("INSERT INTO position_store(id,b,p) VALUES(1,'(1,1),(0,0)','((0,0))'),(2,NULL,NULL)", 2))
}

func geometryPositionConsumerChecks(t *testing.T, run func(fixture.Step)) {
	t.Helper()
	names := []string{"id", "left_box", "left_poly", "touch", "absent"}
	types := []string{"INT4", "BOOL", "BOOL", "BOOL", "BOOL"}
	for _, prepared := range []bool{false, true} {
		s := fixture.Select("SELECT id,left_box,left_poly,touch,absent FROM position_view ORDER BY id", names, types, [][]any{{int64(1), true, true, true, nil}, {int64(2), nil, nil, nil, nil}})
		s.Prepared = prepared
		run(s)
		s = fixture.Select("SELECT id FROM position_store WHERE left_box ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}})
		s.Prepared = prepared
		run(s)
	}
	var columns, boolTypes []string
	var nulls []any
	for _, shape := range geometryPositionShapes[1:3] {
		for i, direction := range geometryPositionDirections {
			if !geometryPositionAdmitted(shape.typ, direction.op) {
				continue
			}
			for mask := 1; mask < 4; mask++ {
				columns = append(columns, fmt.Sprintf("%s_%d_%d", shape.typ, i, mask))
				boolTypes = append(boolTypes, "BOOL")
				nulls = append(nulls, nil)
			}
		}
	}
	s := fixture.Select("SELECT * FROM position_constants", columns, boolTypes, [][]any{nulls})
	run(s)
	run(fixture.Prepared(s))
	// Roll back the post-replay probe so each replay target has identical data.
	run(fixture.Exec("BEGIN", 0))
	run(fixture.Prepared(fixture.Exec("INSERT INTO position_store(id,b,p) VALUES($1,$2,$3)", 1, 3, "(1,1),(0,0)", "((0,0))")))
	s = fixture.Select("SELECT id,left_box,left_poly,touch,absent FROM position_view WHERE id=3", names, types, [][]any{{int64(3), true, true, true, nil}})
	run(s)
	run(fixture.Prepared(s))
	run(fixture.Exec("ROLLBACK", 0))
	d := fixture.ControlsReferenceDiagnostic("23514", "new row for relation \"position_store\" violates check constraint \"position_ok\"")
	d.Schema, d.Table, d.Constraint = "public", "position_store", "position_ok"
	d.Detail = "Failing row contains (5, (5,5),(4,4), ((0,0)), f, t)."
	for _, prepared := range []bool{false, true} {
		for _, q := range []string{
			"INSERT INTO position_store(id,b,p) VALUES(5,'(5,5),(4,4)','((0,0))')",
			"INSERT INTO position_store(id,b,p) VALUES(4,'(1,1),(0,0)','((0,0))'),(5,'(5,5),(4,4)','((0,0))')",
		} {
			s := fixture.ErrorDiagnostic(q, d)
			s.Prepared, s.ErrorStage = prepared, "execute"
			run(s)
			run(fixture.Select("SELECT id FROM position_store ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		}
	}
}

func geometryPositionConsumerCycle(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, backend.Recorder, s) }
	geometryPositionCreateConsumers(run)
	geometryPositionConsumerChecks(t, run)
	if backend.PG {
		return
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	old, err := c.QueryContext(p.Ctx, "SELECT left_box,left_poly FROM position_view WHERE id=1")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	metadata, err := old.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(metadata) != 2 || metadata[0].Name() != "left_box" || metadata[1].Name() != "left_poly" || metadata[0].DatabaseTypeName() != "BOOL" || metadata[1].DatabaseTypeName() != "BOOL" {
		t.Fatalf("retained metadata: %v", metadata)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	geometryPositionConsumerChecks(t, run)
	if !old.Next() {
		t.Fatalf("retained row: %v", old.Err())
	}
	var a, b bool
	if err := old.Scan(&a, &b); err != nil {
		t.Fatal(err)
	}
	if !a || !b || old.Next() || old.Err() != nil {
		t.Fatalf("retained values %t,%t: %v", a, b, old.Err())
	}
	old.Close()
	fresh := fixture.NewFixture(t, false, true)
	if err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	geometryPositionConsumerChecks(t, func(s fixture.Step) { fixture.RunStep(t, fresh.Ctx, fresh.Memory, fresh.MemoryRecord, s) })
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatalf("emitted SQL replay: %v", err)
		}
		geometryPositionConsumerChecks(t, func(s fixture.Step) { fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, s) })
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
	}
}

func geometryPositionRoutineAuthority(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, backend.Recorder, s) }
	run(fixture.Exec("CREATE ROLE position_reader", 0))
	run(fixture.Exec("GRANT USAGE ON SCHEMA public TO position_reader", 0))
	const signature = "pg_catalog.box_left(box,box)"
	const q = "SELECT $1::box << $2::box AS v"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	defer func() {
		for _, q := range []string{"RESET ROLE", "GRANT EXECUTE ON FUNCTION " + signature + " TO PUBLIC", "REVOKE EXECUTE ON FUNCTION " + signature + " FROM position_reader"} {
			if _, err := c.ExecContext(p.Ctx, q); err != nil {
				t.Error(err)
			}
		}
	}()
	run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+signature+" FROM PUBLIC", 0))
	run(fixture.Exec("SET ROLE position_reader", 0))
	d := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for function box_left"), "(1,1),(0,0)", "(4,4),(3,3)")
	d.SelectRows, d.ErrorStage = true, "execute"
	fixture.RunStep(t, p.Ctx, held, backend.Recorder, d)
	run(d)
	run(fixture.Exec("RESET ROLE", 0))
	run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+signature+" TO position_reader", 0))
	run(fixture.Exec("SET ROLE position_reader", 0))
	fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{true}}, "(1,1),(0,0)", "(4,4),(3,3)"))
	run(fixture.Exec("RESET ROLE", 0))
}

func TestReferenceGeometryPositionConsumers(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"position_reader"})
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			geometryPositionConsumerCycle(t, p, backend, c)
			geometryPositionRoutineAuthority(t, p, backend, c)
		})
	}
}
