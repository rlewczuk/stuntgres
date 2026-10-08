//go:build reference

package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
)

func TestReferenceIdentityPublicationFailure(t *testing.T) {
	identityReferenceCase(t, func(t *testing.T, db *sql.DB) {
		identityRefExec(t, db, `CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY(MAXVALUE 2 CACHE 1),p INT NOT NULL)`, 0)
		_, err := db.Exec(`INSERT INTO i VALUES(DEFAULT,1),(DEFAULT,NULL)`)
		identityRefState(t, err, "23502")
		identityRefRows(t, db, `SELECT * FROM i`, nil)
		identityRefRows(t, db, `SELECT last_value FROM i_id_seq`, [][]int64{{2}})
		_, err = db.Exec(`INSERT INTO i(p) VALUES(3)`)
		identityRefState(t, err, "2200H")
		identityRefRows(t, db, `SELECT * FROM i`, nil)
	})
}

func TestReferenceIdentityEarlyRowFailureStopsDemand(t *testing.T) {
	identityReferenceCase(t, func(t *testing.T, db *sql.DB) {
		for _, tc := range []struct{ name, options, later string }{
			{"exhaustion", "MINVALUE 0 MAXVALUE 1 START 1 CACHE 1", "$2"},
			{"later_identity", "MINVALUE 0 MAXVALUE 10 START 1 CACHE 1", "$2"},
			{"later_side_producer", "MINVALUE 0 MAXVALUE 10 START 1 CACHE 1", "nextval('side')"},
		} {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/prepared=%t", tc.name, prepared), func(t *testing.T) {
					ctx := t.Context()
					c, err := db.Conn(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					sequenceProbeExec(t, ctx, c, `CREATE TABLE early(id INT GENERATED ALWAYS AS IDENTITY(`+tc.options+`),p INT NOT NULL)`)
					sequenceProbeExec(t, ctx, c, `CREATE SEQUENCE side START 50 CACHE 1`)
					defer sequenceProbeExec(t, ctx, c, `DROP TABLE early`)
					defer sequenceProbeExec(t, ctx, c, `DROP SEQUENCE side`)
					query := `INSERT INTO early VALUES(DEFAULT,$1),(DEFAULT,` + tc.later + `)`
					var held *sql.Stmt
					if prepared {
						held, err = c.PrepareContext(ctx, query)
						if err != nil {
							t.Fatal("Prepare demanded a row", err)
						}
						defer held.Close()
					}
					var last int64
					var called bool
					if err = c.QueryRowContext(ctx, `SELECT last_value,is_called FROM early_id_seq`).Scan(&last, &called); err != nil || last != 1 || called {
						t.Fatal(last, called, err)
					}
					var result sql.Result
					args := []any{nil, 8}
					if tc.name == "later_side_producer" {
						args = args[:1]
					}
					if prepared {
						result, err = held.ExecContext(ctx, args...)
					} else {
						result, err = c.ExecContext(ctx, query, args...)
					}
					if result != nil {
						t.Fatal("failed batch returned a result", result)
					}
					want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "p" of relation "early" violates not-null constraint`, Detail: "Failing row contains (1, null).", Schema: "public", Table: "early", Column: "p"}
					referenceAssertDiagnostic(t, err, &want)
					identityRefRows(t, db, `SELECT * FROM early`, nil)
					if err = c.QueryRowContext(ctx, `SELECT last_value,is_called FROM early_id_seq`).Scan(&last, &called); err != nil || last != 1 || !called {
						t.Fatal(last, called, err)
					}
					if err = c.QueryRowContext(ctx, `SELECT last_value,is_called FROM side`).Scan(&last, &called); err != nil || last != 50 || called {
						t.Fatal(last, called, err)
					}
					sequenceProbeValue(t, ctx, c, `SELECT currval('early_id_seq')`, 1)
					sequenceProbeValue(t, ctx, c, `SELECT lastval()`, 1)
					want = controlsReferenceDiagnostic("55000", `currval of sequence "side" is not yet defined in this session`)
					referenceAssertDiagnostic(t, c.QueryRowContext(ctx, `SELECT currval('side')`).Scan(&last), &want)
					sequenceProbeValue(t, ctx, c, `SELECT lastval()`, 1)
					firstID := int64(2)
					if tc.name == "exhaustion" {
						sequenceProbeExec(t, ctx, c, `ALTER TABLE early ALTER id RESTART WITH 0`)
						firstID = 0
					}
					args[0] = 7
					if prepared {
						result, err = held.ExecContext(ctx, args...)
					} else {
						result, err = c.ExecContext(ctx, query, args...)
					}
					if err != nil {
						t.Fatal("healthy reuse", err)
					}
					if n, err := result.RowsAffected(); err != nil || n != 2 {
						t.Fatal(n, err)
					}
					payload := int64(8)
					if tc.name == "later_side_producer" {
						payload = 50
					}
					identityRefRows(t, db, `SELECT * FROM early ORDER BY id`, [][]int64{{firstID, 7}, {firstID + 1, payload}})
					if err = c.QueryRowContext(ctx, `SELECT last_value,is_called FROM early_id_seq`).Scan(&last, &called); err != nil || last != firstID+1 || !called {
						t.Fatal(last, called, err)
					}
					sequenceProbeValue(t, ctx, c, `SELECT currval('early_id_seq')`, firstID+1)
					sequenceProbeValue(t, ctx, c, `SELECT lastval()`, firstID+1)
				})
			}
		}
	})
}

func TestReferenceIdentitySessionsCacheAndNoDemand(t *testing.T) {
	identityReferenceCase(t, func(t *testing.T, db *sql.DB) {
		ctx := t.Context()
		a, err := db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer a.Close()
		b, err := db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer b.Close()
		sequenceProbeExec(t, ctx, a, `CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY(CACHE 2))`)
		held, err := a.PrepareContext(t.Context(), `INSERT INTO i DEFAULT VALUES`)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		identityRefRows(t, db, `SELECT last_value FROM i_id_seq`, [][]int64{{1}})
		identityRefConnExec(t, ctx, a, `INSERT INTO i OVERRIDING SYSTEM VALUE VALUES(99)`, 1)
		identityRefConnExec(t, ctx, a, `INSERT INTO i OVERRIDING SYSTEM VALUE SELECT id FROM i WHERE false`, 0)
		identityRefRows(t, db, `SELECT last_value FROM i_id_seq`, [][]int64{{1}})
		if _, err = held.Exec(); err != nil {
			t.Fatal(err)
		}
		sequenceProbeValue(t, ctx, a, `SELECT currval('i_id_seq')`, 1)
		identityRefConnExec(t, ctx, b, `INSERT INTO i DEFAULT VALUES`, 1)
		sequenceProbeValue(t, ctx, b, `SELECT currval('i_id_seq')`, 3)
		if _, err = held.Exec(); err != nil {
			t.Fatal(err)
		}
		sequenceProbeValue(t, ctx, a, `SELECT lastval()`, 2)
		identityRefRows(t, db, `SELECT last_value FROM i_id_seq`, [][]int64{{4}})
		sequenceProbeExec(t, ctx, a, `BEGIN READ ONLY`)
		_, err = a.ExecContext(ctx, `INSERT INTO i DEFAULT VALUES`)
		identityRefState(t, err, "25006")
		sequenceProbeExec(t, ctx, a, `ROLLBACK`)
		identityRefRows(t, db, `SELECT last_value FROM i_id_seq`, [][]int64{{4}})
	})
}

func identityRefConnExec(t *testing.T, ctx context.Context, c *sql.Conn, q string, count int64) {
	t.Helper()
	r, err := c.ExecContext(ctx, q)
	if err != nil {
		t.Fatal(q, err)
	}
	if n, err := r.RowsAffected(); err != nil || n != count {
		t.Fatal(q, n, err)
	}
}
