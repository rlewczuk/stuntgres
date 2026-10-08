package stuntgres_test

import (
	"context"
	"database/sql"
	"fmt"
	"stuntgres"
	"testing"
)

func TestIdentityPublicationFailureConsumesDemandedValues(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	tableCoreExec(t, db, false, `CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY(MAXVALUE 2 CACHE 1),p INT NOT NULL)`, 0)
	_, err := db.Exec(`INSERT INTO i VALUES(DEFAULT,1),(DEFAULT,NULL)`)
	identityState(t, err, "23502")
	wantTuples(t, db, `SELECT * FROM i`, [][]any{})
	wantTuples(t, db, `SELECT last_value,is_called FROM i_id_seq`, [][]any{{int64(2), true}})
	_, err = db.Exec(`INSERT INTO i(p) VALUES(3)`)
	identityState(t, err, "2200H")
	wantTuples(t, db, `SELECT * FROM i`, [][]any{})
}

// Failure demand stops at the first constrained row, not at batch publication.
func TestIdentityEarlyRowFailureStopsDemand(t *testing.T) {
	for _, tc := range []struct {
		name, options, later string
	}{
		{"exhaustion", "MINVALUE 0 MAXVALUE 1 START 1 CACHE 1", "$2"},
		{"later_identity", "MINVALUE 0 MAXVALUE 10 START 1 CACHE 1", "$2"},
		{"later_side_producer", "MINVALUE 0 MAXVALUE 10 START 1 CACHE 1", "nextval('side')"},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared=%t", tc.name, prepared), func(t *testing.T) {
				db := openLocal(t, stuntgres.NewDriver(), t.Name())
				c := sequenceConn(t, db)
				ctx := t.Context()
				sequenceExec(t, c, `CREATE TABLE early(id INT GENERATED ALWAYS AS IDENTITY(`+tc.options+`),p INT NOT NULL)`)
				sequenceExec(t, c, `CREATE SEQUENCE side START 50 CACHE 1`)
				query := `INSERT INTO early VALUES(DEFAULT,$1),(DEFAULT,` + tc.later + `)`
				var held *sql.Stmt
				var err error
				if prepared {
					held, err = c.PrepareContext(ctx, query)
					if err != nil {
						t.Fatal("Prepare demanded a row", err)
					}
					defer held.Close()
				}
				wantTuples(t, db, `SELECT last_value,is_called FROM early_id_seq`, [][]any{{int64(1), false}})
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
				typeCoreDiagnostic(t, err, stuntgres.Error{Code: "23502", Message: `null value in column "p" of relation "early" violates not-null constraint`, Detail: "Failing row contains (1, null).", Schema: "public", Table: "early", Column: "p"})
				wantTuples(t, db, `SELECT * FROM early`, [][]any{})
				wantTuples(t, db, `SELECT last_value,is_called FROM early_id_seq`, [][]any{{int64(1), true}})
				wantTuples(t, db, `SELECT last_value,is_called FROM side`, [][]any{{int64(50), false}})
				sequenceValue(t, c, `SELECT currval('early_id_seq')`, 1)
				sequenceValue(t, c, `SELECT lastval()`, 1)
				var memory int64
				typeCoreDiagnostic(t, c.QueryRowContext(ctx, `SELECT currval('side')`).Scan(&memory), stuntgres.Error{Code: "55000", Message: `currval of sequence "side" is not yet defined in this session`})
				sequenceValue(t, c, `SELECT lastval()`, 1)
				firstID := int64(2)
				if tc.name == "exhaustion" {
					sequenceExec(t, c, `ALTER TABLE early ALTER id RESTART WITH 0`)
					firstID = 0
				}
				// Reuse the same held statement after failure; no stale staged row.
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
				wantTuples(t, db, `SELECT * FROM early ORDER BY id`, [][]any{{firstID, int64(7)}, {firstID + 1, payload}})
				wantTuples(t, db, `SELECT last_value,is_called FROM early_id_seq`, [][]any{{firstID + 1, true}})
				sequenceValue(t, c, `SELECT currval('early_id_seq')`, firstID+1)
				sequenceValue(t, c, `SELECT lastval()`, firstID+1)
			})
		}
	}
}

func TestIdentitySessionsCacheAndNoDemand(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	a, b := sequenceConn(t, db), sequenceConn(t, db)
	ctx := context.Background()
	sequenceExec(t, a, `CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY(CACHE 2))`)
	held, err := a.PrepareContext(ctx, `INSERT INTO i DEFAULT VALUES`)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	wantTuples(t, db, `SELECT last_value,is_called FROM i_id_seq`, [][]any{{int64(1), false}})
	identityConnExec(t, ctx, a, `INSERT INTO i OVERRIDING SYSTEM VALUE VALUES(99)`, 1)
	identityConnExec(t, ctx, a, `INSERT INTO i OVERRIDING SYSTEM VALUE SELECT id FROM i WHERE false`, 0)
	wantTuples(t, db, `SELECT last_value,is_called FROM i_id_seq`, [][]any{{int64(1), false}})
	if _, err = held.ExecContext(ctx); err != nil {
		t.Fatal(err)
	}
	sequenceValue(t, a, `SELECT currval('i_id_seq')`, 1)
	identityConnExec(t, ctx, b, `INSERT INTO i DEFAULT VALUES`, 1)
	sequenceValue(t, b, `SELECT currval('i_id_seq')`, 3)
	if _, err = held.ExecContext(ctx); err != nil {
		t.Fatal(err)
	}
	sequenceValue(t, a, `SELECT lastval()`, 2)
	wantTuples(t, db, `SELECT last_value,is_called FROM i_id_seq`, [][]any{{int64(4), true}})
	sequenceExec(t, a, `BEGIN READ ONLY`)
	_, err = a.ExecContext(ctx, `INSERT INTO i DEFAULT VALUES`)
	identityState(t, err, "25006")
	sequenceExec(t, a, `ROLLBACK`)
	wantTuples(t, db, `SELECT last_value,is_called FROM i_id_seq`, [][]any{{int64(4), true}})
}

func identityConnExec(t *testing.T, ctx context.Context, c *sql.Conn, q string, count int64) {
	t.Helper()
	r, err := c.ExecContext(ctx, q)
	if err != nil {
		t.Fatal(q, err)
	}
	if n, err := r.RowsAffected(); err != nil || n != count {
		t.Fatal(q, n, err)
	}
}
