package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func regoperatorPersistentRows(pg bool) fixture.Step {
	s := fixture.Select("SELECT id,r,s,r::oid AS ro,s::oid AS so FROM regop_data ORDER BY id", []string{"id", "r", "s", "ro", "so"}, []string{"INT4", "REGOPER", "REGOPERATOR", "OID", "OID"}, [][]any{
		{int64(1), "||/", "||/(NONE,double precision)", int64(597), int64(597)},
		{int64(2), "pg_catalog.+", "+(integer,integer)", int64(551), int64(551)},
		{int64(3), "pg_catalog.-", "-(NONE,integer)", int64(558), int64(558)},
		{int64(4), "0", "0", int64(0), int64(0)},
		{int64(5), "4294967295", "4294967295", int64(4294967295), int64(4294967295)},
		{int64(6), nil, nil, nil, nil},
	})
	if pg {
		for _, row := range s.Rows {
			for _, i := range []int{3, 4} {
				if row[i] != nil {
					row[i] = fmt.Sprint(row[i])
				}
			}
		}
	}
	s.CheckRaw = func(t *testing.T, col, row int, v any) bool {
		if col == 1 || col == 2 {
			fixture.RegtypeRaw(t, pg, v)
			return true
		}
		if col == 3 || col == 4 {
			fixture.OidReferenceValue(t, pg, "OID", v)
			return true
		}
		return false
	}
	return s
}
func regoperatorPersistentSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	for _, q := range []string{"CREATE TABLE regop_data(id integer,r regoper,s regoperator)", "CREATE DOMAIN regop_identifier AS regoperator", "CREATE TABLE regop_expressions(id integer,r regoper DEFAULT '551'::regoper,s regop_identifier DEFAULT '558'::regoperator,CHECK(r='551'::regoper))", "CREATE VIEW regop_view AS SELECT '551'::regoper AS r,'558'::regoperator AS s"} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO regop_data VALUES(1,'597','597'),(2,'551','551'),(3,'558','558'),(4,'0','0'),(5,'4294967295','4294967295'),(6,NULL,NULL)", 6))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO regop_expressions(id) VALUES(1)", 1))
	update := fixture.Select("UPDATE regop_data SET r='551',s='551' WHERE id=2 RETURNING r,s", []string{"r", "s"}, []string{"REGOPER", "REGOPERATOR"}, [][]any{{"pg_catalog.+", "+(integer,integer)"}})
	update.CheckRaw = func(t *testing.T, col, row int, v any) bool {
		fixture.RegtypeRaw(t, b.PG, v)
		return true
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, update)
	// A bad later tuple cannot publish the valid first tuple.
	q := "INSERT INTO regop_data VALUES(7,'597','597'),(8,'597','++(int4,int4)')"
	s := regoperatorError(q, "42883", "operator does not exist: ++(int4,int4)", "", "", "")
	_, e := c.ExecContext(p.Ctx, q)
	if e == nil {
		t.Fatal("bad later tuple succeeded")
	}
	want := *s.Diagnostic
	want.Position = "55"
	fixture.AssertDiagnostic(t, e, &want)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, regoperatorPersistentRows(b.PG))
}
func regoperatorPersistentCheck(t *testing.T, p *fixture.Pair, db fixture.Executor, r *fixture.Recorder) {
	pg := r.RawBackend == fixture.RawPostgres
	fixture.RunStep(t, p.Ctx, db, r, regoperatorPersistentRows(pg))
	fixture.RunStep(t, p.Ctx, db, r, fixture.Prepared(regoperatorPersistentRows(pg)))
	for _, q := range []string{"SELECT r,s FROM regop_view", "SELECT r,s::regoperator AS s FROM regop_expressions"} {
		s := fixture.Select(q, []string{"r", "s"}, []string{"REGOPER", "REGOPERATOR"}, [][]any{{"pg_catalog.+", "-(NONE,integer)"}})
		s.CheckRaw = func(t *testing.T, col, row int, v any) bool {
			fixture.RegtypeRaw(t, pg, v)
			return true
		}
		fixture.RunStep(t, p.Ctx, db, r, s)
	}
	// Read the stored domain itself, not just a cast that hides its carrier.
	for _, suffix := range []string{"", " WHERE false"} {
		s := fixture.Select("SELECT r,s FROM regop_expressions"+suffix, []string{"r", "s"}, []string{"REGOPER", "REGOPERATOR"}, [][]any{{"pg_catalog.+", "-(NONE,integer)"}})
		if suffix != "" {
			s.Rows = nil
		}
		s.CheckRaw = func(t *testing.T, col, row int, v any) bool {
			fixture.RegtypeRaw(t, pg, v)
			return true
		}
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
			fixture.RegtypeCursorMetadata(t, rows, pg, s.Columns, s.Types, []bool{true, true})
		}
		fixture.RunStep(t, p.Ctx, db, r, s)
		fixture.RunStep(t, p.Ctx, db, r, fixture.Prepared(s))
	}
	null := fixture.Select("SELECT NULL::regop_identifier AS s", []string{"s"}, []string{"REGOPERATOR"}, [][]any{{nil}})
	null.CheckRaw = func(t *testing.T, col, row int, v any) bool {
		fixture.RegtypeRaw(t, pg, v)
		return true
	}
	null.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		fixture.RegtypeCursorMetadata(t, rows, pg, null.Columns, null.Types, nil)
	}
	fixture.RunStep(t, p.Ctx, db, r, null)
	fixture.RunStep(t, p.Ctx, db, r, fixture.Prepared(null))
}
func TestReferenceRegOperatorPersistence(t *testing.T) {
	p := fixture.NewScenario(t)
	var emitted []byte
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, e := b.DB.Conn(p.Ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer c.Close()
			regoperatorPersistentSetup(t, p, b, c)
			regoperatorPersistentCheck(t, p, c, b.Recorder)
			if b.PG {
				if emitted == nil {
					t.Fatal("actual native dump bytes required")
				}
				for _, q := range []string{"DROP VIEW regop_view", "DROP TABLE regop_expressions", "DROP TABLE regop_data", "DROP DOMAIN regop_identifier"} {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
				}
				if _, e := c.ExecContext(p.Ctx, string(emitted)); e != nil {
					t.Fatal("native emitted stream replay", e)
				}
				regoperatorPersistentCheck(t, p, c, b.Recorder)
				return
			}
			var dump bytes.Buffer
			if e := stuntgres.Dump(p.Ctx, p.Native, &dump); e != nil {
				t.Fatal(e)
			}
			emitted = bytes.Clone(dump.Bytes())
			retained, e := c.QueryContext(p.Ctx, "SELECT r,s FROM regop_data ORDER BY id")
			if e != nil {
				t.Fatal(e)
			}
			defer retained.Close()
			fixture.RegtypeCursorMetadata(t, retained, false, []string{"r", "s"}, []string{"REGOPER", "REGOPERATOR"}, []bool{true, true})
			if e := stuntgres.Reset(p.Ctx, p.Native); e != nil {
				t.Fatal(e)
			}
			if e := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); e != nil {
				t.Fatal(e)
			}
			regoperatorPersistentCheck(t, p, c, b.Recorder)
			fixture.RegtypeAssertRows(t, retained, [][]any{{"||/", "||/(NONE,double precision)"}, {"pg_catalog.+", "+(integer,integer)"}, {"pg_catalog.-", "-(NONE,integer)"}, {"0", "0"}, {"4294967295", "4294967295"}, {nil, nil}}, false)
			var redump bytes.Buffer
			if e := stuntgres.Dump(p.Ctx, p.Native, &redump); e != nil {
				t.Fatal(e)
			}
			if !bytes.Equal(emitted, redump.Bytes()) {
				t.Fatal("redump changed operator identities")
			}
			fresh := fixture.NewFixture(t, false, true)
			fixture.RunStep(t, fresh.Ctx, fresh.Memory, fresh.MemoryRecord, fixture.Exec("CREATE TABLE unrelated(v integer)", 0))
			if e := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(emitted)); e != nil {
				t.Fatal(e)
			}
			regoperatorPersistentCheck(t, fresh, fresh.Memory, fresh.MemoryRecord)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE regop_sentinel(v integer)", 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO regop_sentinel VALUES(42)", 1))
			for _, bad := range []string{string(emitted[:len(emitted)/2]), "CREATE TABLE doomed(r regoperator); INSERT INTO doomed VALUES('++(int4,int4)');"} {
				if e := stuntgres.Restore(p.Ctx, p.Native, bytes.NewBufferString(bad)); e == nil {
					t.Fatal("bad staged Restore succeeded")
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM regop_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
				regoperatorPersistentCheck(t, p, c, b.Recorder)
			}
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			if e := stuntgres.Restore(ctx, p.Native, bytes.NewReader(emitted)); !errors.Is(e, context.Canceled) {
				t.Fatal("canceled Restore", e)
			}
		})
	}
}
func TestReferenceRegOperatorFeatureSmoke(t *testing.T) {
	TestReferenceRegOperatorValuesMetadata(t)
	TestReferenceRegOperatorSignatures(t)
	TestReferenceRegOperatorNamespaceAuthorization(t)
	TestReferenceRegOperatorPreparedLifetime(t)
	TestReferenceRegOperatorPersistence(t)
}
