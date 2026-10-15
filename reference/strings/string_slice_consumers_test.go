package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStringSliceConsumers(t *testing.T) {
	stringSliceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE slice_consumers(id int,s text,n int)", 0),
			fixture.Exec("INSERT INTO slice_consumers VALUES(1,'ax',1),(2,'ay',1),(3,'bz',1)", 3),
			fixture.Exec("CREATE TABLE slice_copy(id int,s text)", 0),
			fixture.Exec("INSERT INTO slice_copy SELECT id,substr(s,1,n) FROM slice_consumers", 3),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		for _, q := range []string{
			"SELECT left(s,n) AS v,count(*) AS n FROM slice_consumers GROUP BY left(s,n) ORDER BY v",
			"SELECT substring(s FROM 1 FOR n) AS v,count(*) AS n FROM slice_consumers GROUP BY substring(s,1,n) ORDER BY v",
		} {
			stringPadBoth(t, p, b, c, fixture.Select(q, []string{"v", "n"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(2)}, {"b", int64(1)}}))
		}
		stringPadBoth(t, p, b, c, fixture.Select("SELECT a.id,CASE WHEN left(a.s,1)='a' THEN substring(a.s,2) ELSE right(a.s,1) END AS v FROM slice_consumers a LEFT JOIN slice_copy b ON b.id=a.id AND right(b.s,1)=left(a.s,1) WHERE coalesce(substr(a.s,1,1),'') <> '' ORDER BY right(a.s,1)", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "x"}, {int64(2), "y"}, {int64(3), "z"}}))
		stringPadBoth(t, p, b, c, fixture.Select("SELECT min(left(s,1)) AS lo,max(right(s,1)) AS hi FROM slice_consumers", []string{"lo", "hi"}, []string{"TEXT", "TEXT"}, [][]any{{"a", "z"}}))
		for _, prepared := range []bool{false, true} {
			update := fixture.Select("UPDATE slice_copy SET s=left(s,1)||right('XY',1) WHERE id=1 RETURNING id,substr(s,2) AS v", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "Y"}})
			update.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, update)
		}
		for _, prepared := range []bool{false, true} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO slice_copy VALUES(9,'delete')", 1))
			s := fixture.Select("DELETE FROM slice_copy WHERE id=9 RETURNING substring(s FROM 2 FOR 2) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"el"}})
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
	})
}

func TestReferenceStringSliceAtomicity(t *testing.T) {
	stringSliceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE slice_atomic(id int,s text,n int)", 0),
			fixture.Exec("INSERT INTO slice_atomic VALUES(1,'abc',2),(2,'def',-1)", 2),
			fixture.Exec("CREATE TABLE slice_target(id int,s text)", 0),
			fixture.Exec("INSERT INTO slice_target VALUES(0,'sentinel')", 1),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		state := fixture.Select("SELECT id,s,n FROM slice_atomic ORDER BY id", []string{"id", "s", "n"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(1), "abc", int64(2)}, {int64(2), "def", int64(-1)}})
		target := fixture.Select("SELECT id,s FROM slice_target ORDER BY id", []string{"id", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(0), "sentinel"}})
		for _, q := range []string{
			"INSERT INTO slice_target SELECT id,substring(s,1,n) FROM slice_atomic ORDER BY id",
			"UPDATE slice_atomic SET s=substring(s,1,n) RETURNING id,s",
			"DELETE FROM slice_atomic RETURNING id,substring(s,1,n)",
		} {
			for _, prepared := range []bool{false, true} {
				t.Run(q+fixture.Position(map[bool]int{false: 0, true: 1}[prepared]), func(t *testing.T) {
					s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("22011", "negative substring length not allowed"))
					s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					stringPadBoth(t, p, b, c, state)
					stringPadBoth(t, p, b, c, target)
				})
			}
		}
		q := "UPDATE slice_atomic SET s=substring(s,1,$1::int) RETURNING id,s"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("22011", "negative substring length not allowed"))
		s.Args, s.SelectRows, s.ErrorStage = []any{-1}, true, "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		stringPadBoth(t, p, b, c, state)
		safe := fixture.Select(q, []string{"id", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "ab"}, {int64(2), "de"}}, 2)
		safe.Bag = true
		fixture.RunStep(t, p.Ctx, held, b.Recorder, safe)
		stringPadBoth(t, p, b, c, fixture.Select("SELECT id,s,n FROM slice_atomic ORDER BY id", state.Columns, state.Types, [][]any{{int64(1), "ab", int64(2)}, {int64(2), "de", int64(-1)}}))
	})
}

func TestReferenceStringSliceAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"slice_reader"})
	stringSliceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE USER slice_reader", 0),
			fixture.Exec("CREATE TABLE source(id int,s text,n int,k int,p text,e text)", 0),
			fixture.Exec("INSERT INTO source VALUES(1,'abc',1,2,'b','#')", 1),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		if b.PG {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("GRANT USAGE ON SCHEMA public TO slice_reader", 0))
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("GRANT SELECT(id) ON source TO slice_reader", 0))
		queries := []string{
			"SELECT left(s,1) AS v FROM source", "SELECT right('',n) AS v FROM source WHERE FALSE",
			"SELECT substring('abc',n,1) AS v FROM source WHERE FALSE", "SELECT substr('abc',1,k) AS v FROM source WHERE FALSE",
			"SELECT substring('abc',p) AS v FROM source WHERE FALSE", "SELECT substring('abc','%',e) AS v FROM source WHERE FALSE",
		}
		for _, q := range queries {
			t.Run(q, func(t *testing.T) {
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET SESSION AUTHORIZATION slice_reader", 0))
				s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table source"))
				s.SelectRows, s.ErrorStage = true, "execute"
				stringPadBoth(t, p, b, c, s)
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			})
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM source", 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET SESSION AUTHORIZATION slice_reader", 0))
		for _, q := range queries {
			s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table source"))
			s.SelectRows, s.ErrorStage = true, "execute"
			stringPadBoth(t, p, b, c, s)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		stringPadBoth(t, p, b, c, fixture.Select(queries[0], []string{"v"}, []string{"TEXT"}, nil))
	})
}

func TestReferenceStringSliceCancellation(t *testing.T) {
	stringSliceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE slice_cancel(s text)", 0))
		readSQL := "SELECT left($1,2) AS l,right($1,2) AS r,substr($1,2,2) AS s"
		writeSQL := "INSERT INTO slice_cancel VALUES(substring($1,2,2))"
		read, err := b.DB.PrepareContext(p.Ctx, readSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer read.Close()
		write, err := b.DB.PrepareContext(p.Ctx, writeSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer write.Close()
		// Adapter-boundary proof only. In-flight provider polling is a separate unit proof.
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		for _, query := range []func() (*sql.Rows, error){
			func() (*sql.Rows, error) { return b.DB.QueryContext(ctx, readSQL, "Aé🙂Z") },
			func() (*sql.Rows, error) { return read.QueryContext(ctx, "Aé🙂Z") },
		} {
			rows, err := query()
			if rows != nil {
				rows.Close()
				t.Fatal("canceled slicing query exposed rows")
			}
			fixture.LexicalOrdinary(t, err, context.Canceled)
		}
		for _, exec := range []func() (sql.Result, error){
			func() (sql.Result, error) { return b.DB.ExecContext(ctx, writeSQL, "Aé🙂Z") },
			func() (sql.Result, error) { return write.ExecContext(ctx, "Aé🙂Z") },
		} {
			result, err := exec()
			if result != nil {
				t.Fatal("canceled slicing write exposed a result")
			}
			fixture.LexicalOrdinary(t, err, context.Canceled)
		}
		stringPadBoth(t, p, b, c, fixture.Select("SELECT s FROM slice_cancel", []string{"s"}, []string{"TEXT"}, nil))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: read, SQL: readSQL}, b.Recorder, fixture.Select(readSQL, []string{"l", "r", "s"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"Aé", "🙂Z", "é🙂"}}, "Aé🙂Z"))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: write, SQL: writeSQL}, b.Recorder, fixture.Exec(writeSQL, 1, "Aé🙂Z"))
		stringPadBoth(t, p, b, c, fixture.Select("SELECT s FROM slice_cancel", []string{"s"}, []string{"TEXT"}, [][]any{{"é🙂"}}))
	})
}
