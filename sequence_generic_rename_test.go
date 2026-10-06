package stuntgres_test

import (
	"context"
	"database/sql"
	"stuntgres"
	"testing"
)

func sequenceGenericRenameUtility(t *testing.T, c *sql.Conn, q string, prepared bool) error {
	t.Helper()
	ctx := context.Background()
	var result sql.Result
	var err error
	if prepared {
		stmt, prepareErr := c.PrepareContext(ctx, q)
		if prepareErr != nil {
			t.Fatal("utility Prepare", q, prepareErr)
		}
		defer stmt.Close()
		result, err = stmt.ExecContext(ctx)
	} else {
		result, err = c.ExecContext(ctx, q)
	}
	if err == nil {
		if n, countErr := result.RowsAffected(); countErr != nil || n != 0 {
			t.Fatalf("utility count=%d err=%v", n, countErr)
		}
	}
	return err
}

func TestSequenceGenericRelationRename(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, query := range []string{"SELECT nextval('s')", "SELECT nextval('s'::regclass)"} {
			t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared]+"/"+query, func(t *testing.T) {
				ctx := context.Background()
				c := sequenceConn(t, openLocal(t, stuntgres.NewDriver(), t.Name()))
				sequenceExec(t, c, "CREATE SEQUENCE s START 14 CACHE 1")
				held, err := c.PrepareContext(ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				rows, err := c.QueryContext(ctx, "SELECT * FROM s")
				queryCoreRows(t, rows, err, []string{"last_value", "log_cnt", "is_called"}, []string{"INT8", "INT8", "BOOL"}, [][]any{{int64(14), int64(0), false}}, false)
				if err = sequenceGenericRenameUtility(t, c, "ALTER TABLE s RENAME TO identity", prepared); err != nil {
					t.Fatal(err)
				}
				var value int64
				err = held.QueryRowContext(ctx).Scan(&value)
				typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42P01", Message: `relation "s" does not exist`, Position: "16"})
				rows, err = c.QueryContext(ctx, "SELECT * FROM identity")
				queryCoreRows(t, rows, err, []string{"last_value", "log_cnt", "is_called"}, []string{"INT8", "INT8", "BOOL"}, [][]any{{int64(14), int64(0), false}}, false)
				parameter, err := c.PrepareContext(ctx, "SELECT nextval($1)")
				if err != nil {
					t.Fatal(err)
				}
				defer parameter.Close()
				rows, err = parameter.QueryContext(ctx, "identity")
				queryCoreRows(t, rows, err, []string{"nextval"}, []string{"INT8"}, [][]any{{int64(14)}}, false)
				sequenceValue(t, c, "SELECT currval('identity')", 14)
				sequenceValue(t, c, "SELECT lastval()", 14)
				sequenceValue(t, c, "SELECT nextval('identity')", 15)
			})
		}
	}
}

func TestSequenceGenericColumnRename(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, columnKeyword := range []string{"COLUMN ", ""} {
			for _, source := range []string{"last_value", "missing", "ctid"} {
				q := "ALTER TABLE s RENAME " + columnKeyword + source + " TO renamed"
				t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared]+"/"+q, func(t *testing.T) {
					c := sequenceConn(t, openLocal(t, stuntgres.NewDriver(), t.Name()))
					sequenceExec(t, c, "CREATE SEQUENCE s START 14 CACHE 1")
					sequenceValue(t, c, "SELECT nextval('s')", 14)
					err := sequenceGenericRenameUtility(t, c, q, prepared)
					typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42809", Message: `cannot rename columns of relation "s"`, Detail: "This operation is not supported for sequences."})
					rows, err := c.QueryContext(context.Background(), "SELECT * FROM s")
					queryCoreRows(t, rows, err, []string{"last_value", "log_cnt", "is_called"}, []string{"INT8", "INT8", "BOOL"}, [][]any{{int64(14), int64(0), true}}, false)
					sequenceValue(t, c, "SELECT currval('s')", 14)
					sequenceValue(t, c, "SELECT lastval()", 14)
					sequenceValue(t, c, "SELECT nextval('s')", 15)
				})
			}
		}
	}
}
