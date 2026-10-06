//go:build reference

package reference_test

import (
	"database/sql"
	"reflect"
	"testing"
)

func referenceGenericRenameUtility(t *testing.T, p *referencePair, c *sql.Conn, q string, prepared bool) error {
	t.Helper()
	var result sql.Result
	var err error
	if prepared {
		stmt, prepareErr := c.PrepareContext(p.ctx, q)
		if prepareErr != nil {
			t.Fatal("utility Prepare", q, prepareErr)
		}
		defer stmt.Close()
		result, err = stmt.ExecContext(p.ctx)
	} else {
		result, err = c.ExecContext(p.ctx, q)
	}
	if err == nil {
		if n, countErr := result.RowsAffected(); countErr != nil || n != 0 {
			t.Fatalf("utility count=%d err=%v", n, countErr)
		}
	}
	return err
}

// log_cnt is a physical implementation field: require INT8/nonnegative and
// unchanged across rejected utilities, not parity with PostgreSQL WAL logging.
func referenceGenericRenameState(t *testing.T, p *referencePair, c *sql.Conn, name string, value int64, called bool) int64 {
	t.Helper()
	rows, err := c.QueryContext(p.ctx, "SELECT * FROM "+name)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, []string{"last_value", "log_cnt", "is_called"}) {
		t.Fatal(columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 3 {
		t.Fatal(types, err)
	}
	for i, want := range []string{"INT8", "INT8", "BOOL"} {
		if types[i].DatabaseTypeName() != want {
			t.Fatal("type", i, types[i].DatabaseTypeName(), want)
		}
	}
	if !rows.Next() {
		t.Fatal("missing sequence row", rows.Err())
	}
	var got, log int64
	var isCalled bool
	if err = rows.Scan(&got, &log, &isCalled); err != nil || got != value || isCalled != called || log < 0 {
		t.Fatal("sequence state", got, log, isCalled, err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("sequence row count", rows.Err())
	}
	return log
}

func TestReferenceSequenceGenericRelationRename(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, query := range []string{"SELECT nextval('s')", "SELECT nextval('s'::regclass)"} {
			t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared]+"/"+query, func(t *testing.T) {
				p := newReferencePair(t)
				for _, backend := range []struct {
					name string
					db   *sql.DB
				}{{"native", p.memory}, {"postgres", p.postgres}} {
					t.Run(backend.name, func(t *testing.T) {
						c, err := backend.db.Conn(p.ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer c.Close()
						sequenceProbeExec(t, p.ctx, c, "CREATE SEQUENCE s START 14 CACHE 1")
						held, err := c.PrepareContext(p.ctx, query)
						if err != nil {
							t.Fatal(err)
						}
						defer held.Close()
						log := referenceGenericRenameState(t, p, c, "s", 14, false)
						if err = referenceGenericRenameUtility(t, p, c, "ALTER TABLE s RENAME TO identity", prepared); err != nil {
							t.Fatal(err)
						}
						var value int64
						err = held.QueryRowContext(p.ctx).Scan(&value)
						want := controlsReferenceDiagnostic("42P01", `relation "s" does not exist`)
						want.Position = "16"
						referenceAssertDiagnostic(t, err, &want)
						if after := referenceGenericRenameState(t, p, c, "identity", 14, false); after != log {
							t.Fatal("rename/failed held query changed log_cnt", log, after)
						}
						parameter, err := c.PrepareContext(p.ctx, "SELECT nextval($1)")
						if err != nil {
							t.Fatal(err)
						}
						defer parameter.Close()
						rows, err := parameter.QueryContext(p.ctx, "identity")
						if err != nil {
							t.Fatal(err)
						}
						referenceSequenceRows(t, rows, "nextval", 14)
						sequenceProbeValue(t, p.ctx, c, "SELECT currval('identity')", 14)
						sequenceProbeValue(t, p.ctx, c, "SELECT lastval()", 14)
						sequenceProbeValue(t, p.ctx, c, "SELECT nextval('identity')", 15)
					})
				}
			})
		}
	}
}

func TestReferenceSequenceGenericColumnRename(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, keyword := range []string{"COLUMN ", ""} {
			for _, source := range []string{"last_value", "missing", "ctid"} {
				q := "ALTER TABLE s RENAME " + keyword + source + " TO renamed"
				t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared]+"/"+q, func(t *testing.T) {
					p := newReferencePair(t)
					for _, backend := range []struct {
						name string
						db   *sql.DB
					}{{"native", p.memory}, {"postgres", p.postgres}} {
						t.Run(backend.name, func(t *testing.T) {
							c, err := backend.db.Conn(p.ctx)
							if err != nil {
								t.Fatal(err)
							}
							defer c.Close()
							sequenceProbeExec(t, p.ctx, c, "CREATE SEQUENCE s START 14 CACHE 1")
							sequenceProbeValue(t, p.ctx, c, "SELECT nextval('s')", 14)
							log := referenceGenericRenameState(t, p, c, "s", 14, true)
							err = referenceGenericRenameUtility(t, p, c, q, prepared)
							want := controlsReferenceDiagnostic("42809", `cannot rename columns of relation "s"`)
							want.Detail = "This operation is not supported for sequences."
							referenceAssertDiagnostic(t, err, &want)
							if after := referenceGenericRenameState(t, p, c, "s", 14, true); after != log {
								t.Fatal("rejected rename changed log_cnt", log, after)
							}
							sequenceProbeValue(t, p.ctx, c, "SELECT currval('s')", 14)
							sequenceProbeValue(t, p.ctx, c, "SELECT lastval()", 14)
							sequenceProbeValue(t, p.ctx, c, "SELECT nextval('s')", 15)
						})
					}
				})
			}
		}
	}
}
