package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCompositeExprViewTargetPaths(t *testing.T) {
	fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.CompositeReferenceSetup(t, p, b, c,
			`CREATE TYPE view_array_pair AS (a integer[],t text)`,
			`CREATE TYPE view_array_outer AS (p view_array_pair,n integer)`,
			`CREATE TABLE view_target_base(sentinel integer,k integer,w view_array_outer,idx integer,note text)`,
			`CREATE VIEW view_target AS SELECT idx,k,w FROM view_target_base`,
			`CREATE TABLE view_target_source(k integer,delta integer)`,
		)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO view_target_base VALUES(90,1,NULL,2,'one'),(91,2,NULL,4,'two'),(92,9,NULL,8,'sentinel')`, 3))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO view_target_source VALUES(1,1)`, 1))
		read := func(want [][]any) {
			t.Helper()
			compositeExprBoth(t, p, b, c, `SELECT sentinel,k,idx,((w).p).a::text AS a,(w).p.t,(w).n,note FROM view_target_base ORDER BY k`,
				[]string{"sentinel", "k", "idx", "a", "t", "n", "note"}, []string{"INT4", "INT4", "INT4", "TEXT", "TEXT", "INT4", "TEXT"}, want)
		}
		// Logical idx is slot zero, but physical slot zero is sentinel. Repeated
		// partial writes compose privately while every bound sees the old idx.
		q := `UPDATE view_target SET idx=idx+1,w.p.a[idx]=11,w.p.a[idx+1]=12,w.p.t='direct',w.n=5 WHERE k=1`
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 1))
		read([][]any{{int64(90), int64(1), int64(3), "[2:3]={11,12}", "direct", int64(5), "one"}, {int64(91), int64(2), int64(4), nil, nil, nil, "two"}, {int64(92), int64(9), int64(8), nil, nil, nil, "sentinel"}})
		q = `UPDATE view_target SET idx=idx+1,w.p.a[idx]=$1,w.p.a[idx+1]=$2,w.p.t='prepared' WHERE k=1`
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Exec(q, 1, 21, 22)))
		read([][]any{{int64(90), int64(1), int64(4), "[2:4]={11,21,22}", "prepared", int64(5), "one"}, {int64(91), int64(2), int64(4), nil, nil, nil, "two"}, {int64(92), int64(9), int64(8), nil, nil, nil, "sentinel"}})
		// The source delta also moves from the logical target width to the wider
		// physical target frame. Neither RHS reads a source or composite column.
		q = `UPDATE view_target v SET idx=idx+1,w.p.a[idx+s.delta]=$1,w.p.a[idx+s.delta+1]=$2 FROM view_target_source s WHERE v.k=s.k`
		held, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		for _, x := range []struct {
			first, second int
			idx           int64
			array         string
		}{{31, 32, 5, "[2:6]={11,21,22,31,32}"}, {41, 42, 6, "[2:7]={11,21,22,31,41,42}"}} {
			result, err := held.ExecContext(p.Ctx, x.first, x.second)
			if err != nil {
				t.Fatal(err)
			}
			fixture.LockAffected(t, result, 1)
			read([][]any{{int64(90), int64(1), x.idx, x.array, "prepared", int64(5), "one"}, {int64(91), int64(2), int64(4), nil, nil, nil, "two"}, {int64(92), int64(9), int64(8), nil, nil, nil, "sentinel"}})
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 1, 51, 52))
		unchanged := [][]any{{int64(90), int64(1), int64(7), "[2:8]={11,21,22,31,41,51,52}", "prepared", int64(5), "one"}, {int64(91), int64(2), int64(4), nil, nil, nil, "two"}, {int64(92), int64(9), int64(8), nil, nil, nil, "sentinel"}}
		read(unchanged)
		// A later-row bound failure cannot publish either the scalar idx write
		// or the earlier row's private composite/array write.
		q = `UPDATE view_target SET idx=99,w.p.a[10/(idx-4)]=77 WHERE k<3`
		fixture.CompositeReferenceError(t, p, c, q, "execute", fixture.CompositeReferenceDiagnostic("22012", "division by zero"))
		read(unchanged)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`UPDATE view_target_base SET idx=NULL WHERE k=2`, 1))
		unchanged[1][2] = nil
		q = `UPDATE view_target SET w.p.a[idx]=1/(k-k),w.p.t='bad' WHERE k=2`
		fixture.CompositeReferenceError(t, p, c, q, "execute", fixture.CompositeReferenceDiagnostic("22004", "array subscript in assignment must not be null"))
		read(unchanged)
		// The retained lowered handle remains reusable after both failures.
		result, err := held.ExecContext(p.Ctx, 61, 62)
		if err != nil {
			t.Fatal(err)
		}
		fixture.LockAffected(t, result, 1)
		unchanged[0][2] = int64(8)
		unchanged[0][3] = "[2:9]={11,21,22,31,41,51,61,62}"
		read(unchanged)
	})
}
