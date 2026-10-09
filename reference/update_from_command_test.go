package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceUpdateFromCommandMetadata(t *testing.T) {
	updateFromReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := updateFromRun(t, p, b, c)
		updateFromSeed(run)
		q := "UPDATE t x SET v=s.n FROM s WHERE x.id=s.k"
		for _, prepared := range []bool{false, true} {
			var result sql.Result
			var err error
			if prepared {
				stmt, prepareErr := c.PrepareContext(p.ctx, q)
				if prepareErr != nil {
					t.Fatal(prepareErr)
				}
				result, err = stmt.ExecContext(p.ctx)
				stmt.Close()
			} else {
				result, err = c.ExecContext(p.ctx, q)
			}
			if err != nil {
				t.Fatal(err)
			}
			referenceLockAffected(t, result, 2)
			if _, err := result.LastInsertId(); err == nil {
				t.Fatal("UPDATE reports a LastInsertId")
			}
		}
		rows, err := c.QueryContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		cols, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		if len(cols) != 0 {
			t.Fatal("non-RETURNING command has result schema", cols)
		}
		if rows.Next() {
			t.Fatal("non-RETURNING command returned a row")
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		run(referenceSelect("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(101)}, {int64(2), int64(20)}, {int64(3), int64(303)}, {int64(4), int64(40)}}))
	})
}
