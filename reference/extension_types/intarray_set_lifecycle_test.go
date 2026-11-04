package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func intarraySetAtomicity(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	original := intarraySelect(b, "SELECT id,a,b,d FROM ia_set_rows ORDER BY id", []string{"id", "a", "b", "d"}, []string{"INT4", "_INT4", "_INT4", "_INT4"}, []bool{false, true, true, true}, [][]any{
		{int64(1), "{3,1,3,2}", "{2,4,2}", "{1,2,3}"}, {int64(2), "{}", "{8,8}", "{1,2,3}"}, {int64(3), nil, nil, "{1,2,3}"},
	})
	for _, q := range []string{
		"UPDATE ia_set_rows SET a=ia_provider._int_union(a,(CASE WHEN id=1 THEN $1::text ELSE $2::text END)::integer[]) WHERE id<3",
		"INSERT INTO ia_set_rows(id,a,b) SELECT id+10,ia_provider._int_union(a,(CASE WHEN id=1 THEN $1::text ELSE $2::text END)::integer[]),b FROM ia_set_rows WHERE id<3",
	} {
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, args := range [][]any{{"{NULL}", "{9}"}, {"{9}", "{NULL}"}} {
			for _, prepared := range []bool{false, true} {
				intarrayError(t, p, b, c, q, prepared, "execute", intarraySetNullError(), args...)
				run(original)
			}
			s := fixture.ErrorDiagnostic(q, intarraySetNullError(), args...)
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			run(original)
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 2, "{9}", "{7}"))
		if q[0] == 'U' {
			run(intarraySelect(b, "SELECT id,a FROM ia_set_rows WHERE id<3 ORDER BY id", []string{"id", "a"}, []string{"INT4", "_INT4"}, []bool{false, true}, [][]any{{int64(1), "{1,2,3,9}"}, {int64(2), "{7}"}}))
			run(fixture.Exec("UPDATE ia_set_rows SET a=CASE WHEN id=1 THEN '{3,1,3,2}'::integer[] ELSE '{}'::integer[] END WHERE id<3", 2))
		} else {
			run(intarraySelect(b, "SELECT id,a FROM ia_set_rows WHERE id>10 ORDER BY id", []string{"id", "a"}, []string{"INT4", "_INT4"}, []bool{false, true}, [][]any{{int64(11), "{1,2,3,9}"}, {int64(12), "{7}"}}))
			run(fixture.Exec("DELETE FROM ia_set_rows WHERE id>10", 2))
		}
		if err := stmt.Close(); err != nil {
			t.Fatal(err)
		}
		run(original)
	}
	// The CHECK's body, not merely the projected operator, rejects NULL elements.
	for _, prepared := range []bool{false, true} {
		intarrayError(t, p, b, c, "INSERT INTO ia_set_rows(id,a) VALUES (10,'{9}'),(11,'{NULL}')", prepared, "execute", intarraySetNullError())
		run(original)
	}
}

func intarraySetPresentation(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	var held []fixture.HeldStatement
	for _, column := range []string{"overlap", "united"} {
		q := "SELECT " + column + " AS v FROM ia_set_view WHERE id=$1::integer"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held = append(held, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q})
	}
	check := func() {
		intarrayBoth(t, p, b, c, intarraySetStoredCheck(b))
		fixture.RunStep(t, p.Ctx, held[0], b.Recorder, intarraySelect(b, held[0].SQL, []string{"v"}, []string{"BOOL"}, []bool{true}, [][]any{{true}}, int64(1)))
		fixture.RunStep(t, p.Ctx, held[1], b.Recorder, intarraySelect(b, held[1].SQL, []string{"v"}, []string{"_INT4"}, []bool{true}, [][]any{{"{1,2,3,4}"}}, int64(1)))
	}
	check()
	// A separate connection keeps the original result/ColumnTypes available
	// while presentation DDL runs on the mutation connection.
	readerConn, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer readerConn.Close()
	retained := intarraySetRetain(t, p, b, readerConn)
	defer retained.rows.Close()
	run(fixture.Exec("CREATE SCHEMA ia_collision", 0))
	for _, q := range []string{
		"ALTER FUNCTION ia_provider._int_union(integer[],integer[]) RENAME TO renamed_union",
		"ALTER OPERATOR ia_provider.| (integer[],integer[]) SET SCHEMA ia_collision",
	} {
		run(fixture.Exec(q, 0))
		check()
	}
	intarrayBoth(t, p, b, c, intarraySetResult(b, "SELECT ia_provider.renamed_union('{3,1,3}'::integer[],'{2}'::integer[]) AS v", "_INT4", "{1,2,3}"))
	run(fixture.Exec("ALTER OPERATOR ia_collision.| (integer[],integer[]) SET SCHEMA ia_provider", 0))
	run(fixture.Exec("ALTER EXTENSION intarray SET SCHEMA ia_collision", 0))
	check()
	intarraySetConsume(t, b, retained)
	// The stored DEFAULT/CHECK remain executable after genuine member relocation.
	run(fixture.Exec("INSERT INTO ia_set_rows(id,a,b) VALUES (4,'{5,5}','{}')", 1))
	intarrayBoth(t, p, b, c, intarraySelect(b, "SELECT id,d FROM ia_set_rows WHERE id=4", []string{"id", "d"}, []string{"INT4", "_INT4"}, []bool{false, true}, [][]any{{int64(4), "{1,2,3}"}}))
	run(fixture.Exec("DELETE FROM ia_set_rows WHERE id=4", 1))
	run(fixture.Exec("ALTER EXTENSION intarray SET SCHEMA ia_provider", 0))
	run(fixture.Exec("ALTER FUNCTION ia_provider.renamed_union(integer[],integer[]) RENAME TO _int_union", 0))
	check()
}

func TestReferenceIntarraySetLifecycle(t *testing.T) {
	t.Run("stored_and_presentation", func(t *testing.T) {
		intarrayScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			intarraySetSeed(t, p, b, c)
			intarraySetAtomicity(t, p, b, c)
			intarraySetPresentation(t, p, b, c)
		})
	})
	t.Run("detached_original", func(t *testing.T) {
		intarrayScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			intarrayInstall(t, p, b, c)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			var originalID int64
			if err := c.QueryRowContext(p.Ctx, "SELECT 'ia_provider._int_union(integer[],integer[])'::regprocedure::oid").Scan(&originalID); err != nil || originalID <= 0 {
				t.Fatalf("original identity %d: %v", originalID, err)
			}
			for _, q := range []string{"BEGIN", "DROP EXTENSION intarray CASCADE", "ROLLBACK"} {
				run(fixture.Exec(q, 0))
			}
			intarrayBoth(t, p, b, c, fixture.Select("SELECT 'ia_provider._int_union(integer[],integer[])'::regprocedure::oid=$1::oid AS original", []string{"original"}, []string{"BOOL"}, [][]any{{true}}, originalID))
			intarrayBoth(t, p, b, c, intarraySetResult(b, "SELECT ia_provider._int_union('{3,1,3}'::integer[],'{2}'::integer[]) AS v", "_INT4", "{1,2,3}"))
			for _, q := range []string{
				"CREATE SCHEMA ia_collision",
				"CREATE TABLE ia_detached(a integer[],b integer[])",
				"INSERT INTO ia_detached VALUES ('{3,1,3}','{2}')",
				"CREATE VIEW ia_detached_view AS SELECT a OPERATOR(ia_provider.|) b AS v FROM ia_detached",
				"ALTER EXTENSION intarray DROP OPERATOR ia_provider.| (integer[],integer[])",
				"ALTER EXTENSION intarray DROP FUNCTION ia_provider._int_union(integer[],integer[])",
				"ALTER FUNCTION ia_provider._int_union(integer[],integer[]) RENAME TO original_union",
				"ALTER FUNCTION ia_provider.original_union(integer[],integer[]) SET SCHEMA ia_collision",
				"ALTER OPERATOR ia_provider.| (integer[],integer[]) SET SCHEMA ia_collision",
				"DROP EXTENSION intarray CASCADE",
				"CREATE EXTENSION intarray SCHEMA ia_provider",
			} {
				count := int64(0)
				if q == "INSERT INTO ia_detached VALUES ('{3,1,3}','{2}')" {
					count = 1
				}
				run(fixture.Exec(q, count))
			}
			intarrayBoth(t, p, b, c, intarraySelect(b, "SELECT v FROM ia_detached_view", []string{"v"}, []string{"_INT4"}, []bool{true}, [][]any{{"{1,2,3}"}}))
			for _, q := range []string{"SELECT ia_collision.original_union('{3,1,3}'::integer[],'{2}'::integer[]) AS v", "SELECT '{3,1,3}'::integer[] OPERATOR(ia_collision.|) '{2}'::integer[] AS v", "SELECT ia_provider._int_union('{3,1,3}'::integer[],'{2}'::integer[]) AS v"} {
				intarrayBoth(t, p, b, c, intarraySetResult(b, q, "_INT4", "{1,2,3}"))
			}
			intarrayBoth(t, p, b, c, fixture.Select("SELECT 'ia_collision.original_union(integer[],integer[])'::regprocedure::oid <> 'ia_provider._int_union(integer[],integer[])'::regprocedure::oid AS distinct_origins", []string{"distinct_origins"}, []string{"BOOL"}, [][]any{{true}}))
		})
	})
}
