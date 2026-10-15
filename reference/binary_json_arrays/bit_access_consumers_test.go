package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBitAccessAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"bit_access_reader"})
	fixture.SchemaReferenceCleanup(t, "access_private")
	bitAccessScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		defer c.ExecContext(p.Ctx, "RESET ROLE")
		for _, q := range []string{"CREATE ROLE bit_access_reader", "CREATE TABLE t(v VARBIT)", "CREATE SCHEMA access_private", "CREATE TABLE access_private.t(v VARBIT)"} {
			run(fixture.Exec(q, 0))
		}
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO bit_access_reader", 0))
		}
		heldSQL := "SELECT get_bit(v,0) FROM public.t"
		stmt, err := c.PrepareContext(p.Ctx, heldSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: heldSQL}
		for _, seed := range []string{"", "INSERT INTO t VALUES(NULL)", "INSERT INTO t VALUES(B'101')"} {
			if seed != "" {
				run(fixture.Exec(seed, 1))
			}
			run(fixture.Exec("SET ROLE bit_access_reader", 0))
			for _, q := range []string{heldSQL, "SELECT set_bit(v,0,1) FROM public.t WHERE FALSE", "SELECT get_bit(v,0) FROM access_private.t"} {
				d := fixture.ControlsReferenceDiagnostic("42501", "permission denied for table t")
				if strings.Contains(q, "access_private") {
					d.Message = "permission denied for schema access_private"
					d.Position = fixture.Position(strings.Index(q, "access_private") + 1)
				}
				for _, prepared := range []bool{false, true} {
					for _, query := range []bool{false, true} {
						s := fixture.ErrorDiagnostic(q, d)
						s.SelectRows = query
						s.Prepared = prepared
						s.ErrorStage = "execute"
						if prepared && d.Position != "" {
							s.ErrorStage = "prepare"
						}
						run(s)
					}
				}
			}
			s := fixture.ErrorDiagnostic(heldSQL, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table t"))
			s.SelectRows = true
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			run(fixture.Exec("RESET ROLE", 0))
		}
		s := fixture.Select("SELECT get_bit(v,0) AS g FROM t", []string{"g"}, []string{"INT4"}, [][]any{{nil}, {int64(1)}})
		s.Bag = true
		bitStringFunctionsBoth(t, p, b, c, s)
	})
}

func TestReferenceBitAccessAtomicity(t *testing.T) { bitAccessScenario(t, bitAccessAtomicity) }
func bitAccessAtomicity(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE TABLE ba_atomic(id INTEGER,v VARBIT,n INTEGER)", 0),
		fixture.Exec("INSERT INTO ba_atomic VALUES(1,B'100',0),(2,B'010',3)", 2),
		fixture.Exec("CREATE TABLE ba_destination(v VARBIT)", 0),
	} {
		run(s)
	}
	original := fixture.Select("SELECT id,v FROM ba_atomic ORDER BY id", []string{"id", "v"}, []string{"INT4", "VARBIT"}, [][]any{{int64(1), "100"}, {int64(2), "010"}})
	for _, q := range []string{"UPDATE ba_atomic SET v=set_bit(v,n,0)", "INSERT INTO ba_destination SELECT set_bit(v,n,0) FROM ba_atomic ORDER BY id", "INSERT INTO ba_destination SELECT set_bit(v,get_bit(v,n),0) FROM ba_atomic ORDER BY id"} {
		binaryAccessBodyError(t, p, b, c, q, "2202E", "bit index 3 out of valid range (0..2)")
		run(original)
		run(fixture.Select("SELECT v FROM ba_destination", []string{"v"}, []string{"VARBIT"}, nil))
	}
	q := "INSERT INTO ba_destination SELECT set_bit(v,n,0) FROM ba_atomic ORDER BY id"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	h := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("2202E", "bit index 3 out of valid range (0..2)"))
	s.ErrorStage = "execute"
	fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
	run(fixture.Exec("UPDATE ba_atomic SET n=2 WHERE id=2", 1))
	fixture.RunStep(t, p.Ctx, h, b.Recorder, fixture.Exec(q, 2))
	bag := fixture.Select("SELECT v FROM ba_destination", []string{"v"}, []string{"VARBIT"}, [][]any{{"000"}, {"010"}})
	bag.Bag = true
	run(bag)
	s = fixture.Select("UPDATE ba_atomic SET v=set_bit(v,n,0) RETURNING id,v", []string{"id", "v"}, []string{"INT4", "VARBIT"}, [][]any{{int64(1), "000"}, {int64(2), "010"}})
	s.Bag = true
	run(s)
	for _, tc := range []struct{ typ, code, message string }{{"BIT(2)", "22026", "bit string length 3 does not match type bit(2)"}, {"VARBIT(2)", "22001", "bit string too long for type bit varying(2)"}} {
		run(fixture.Exec("CREATE TABLE ba_sized(v "+tc.typ+")", 0))
		binaryAccessBodyError(t, p, b, c, "INSERT INTO ba_sized SELECT set_bit(v,0,1) FROM ba_atomic ORDER BY id", tc.code, tc.message)
		run(fixture.Select("SELECT v FROM ba_sized", []string{"v"}, []string{strings.Split(tc.typ, "(")[0]}, nil))
		run(fixture.Exec("DROP TABLE ba_sized", 0))
	}
	tx, err := c.BeginTx(p.Ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("SAVEPOINT ba_before", 0))
	s = fixture.ErrorDiagnostic("UPDATE ba_atomic SET v=set_bit(v,0,2)", fixture.ControlsReferenceDiagnostic("22023", "new bit must be 0 or 1"))
	s.ErrorStage = "execute"
	fixture.RunStep(t, p.Ctx, tx, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("ROLLBACK TO SAVEPOINT ba_before", 0))
	fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("UPDATE ba_atomic SET v=set_bit(v,0,1) WHERE id=1", 1))
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	run(fixture.Select("SELECT id,v FROM ba_atomic ORDER BY id", []string{"id", "v"}, []string{"INT4", "VARBIT"}, [][]any{{int64(1), "100"}, {int64(2), "010"}}))
}
