package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func referenceGenericRenameUtility(t *testing.T, p *fixture.Pair, c *sql.Conn, q string, prepared bool) error {
	t.Helper()
	var result sql.Result
	var err error
	if prepared {
		stmt, prepareErr := c.PrepareContext(p.Ctx, q)
		if prepareErr != nil {
			t.Fatal("utility Prepare", q, prepareErr)
		}
		defer stmt.Close()
		result, err = stmt.ExecContext(p.Ctx)
	} else {
		result, err = c.ExecContext(p.Ctx, q)
	}
	if err == nil {
		if n, countErr := result.RowsAffected(); countErr != nil || n != 0 {
			t.Fatalf("utility count=%d err=%v", n, countErr)
		}
	}
	return err
}

func TestReferenceSequenceGenericRelationRename(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, query := range []string{"SELECT nextval('s')", "SELECT nextval('s'::regclass)"} {
			t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared]+"/"+query, func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, backend := range p.Backends() {
					t.Run(backend.Name, func(t *testing.T) {
						c, err := backend.DB.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer c.Close()
						fixture.SequenceProbeExec(t, p.Ctx, c, "CREATE SEQUENCE s START 14 CACHE 1")
						held, err := c.PrepareContext(p.Ctx, query)
						if err != nil {
							t.Fatal(err)
						}
						defer held.Close()
						log := fixture.GenericRenameState(t, p, c, "s", 14, false, backend.PG)
						if err = referenceGenericRenameUtility(t, p, c, "ALTER TABLE s RENAME TO identity", prepared); err != nil {
							t.Fatal(err)
						}
						var value int64
						err = held.QueryRowContext(p.Ctx).Scan(&value)
						want := fixture.ControlsReferenceDiagnostic("42P01", `relation "s" does not exist`)
						want.Position = "16"
						fixture.AssertDiagnostic(t, err, &want)
						if after := fixture.GenericRenameState(t, p, c, "identity", 14, false, backend.PG); after != log {
							t.Fatal("rename/failed held query changed log_cnt", log, after)
						}
						parameter, err := c.PrepareContext(p.Ctx, "SELECT nextval($1)")
						if err != nil {
							t.Fatal(err)
						}
						defer parameter.Close()
						fixture.RunStep(t, p.Ctx, fixture.HeldStatement{c, parameter, "SELECT nextval($1)"}, backend.Recorder,
							fixture.SequenceInt64Step(fixture.Select("SELECT nextval($1)", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(14)}}, "identity")))
						fixture.SequenceProbeValue(t, p.Ctx, c, "SELECT currval('identity')", 14)
						fixture.SequenceProbeValue(t, p.Ctx, c, "SELECT lastval()", 14)
						fixture.SequenceProbeValue(t, p.Ctx, c, "SELECT nextval('identity')", 15)
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
					p := fixture.NewScenario(t)
					for _, backend := range p.Backends() {
						t.Run(backend.Name, func(t *testing.T) {
							c, err := backend.DB.Conn(p.Ctx)
							if err != nil {
								t.Fatal(err)
							}
							defer c.Close()
							fixture.SequenceProbeExec(t, p.Ctx, c, "CREATE SEQUENCE s START 14 CACHE 1")
							fixture.SequenceProbeValue(t, p.Ctx, c, "SELECT nextval('s')", 14)
							log := fixture.GenericRenameState(t, p, c, "s", 14, true, backend.PG)
							err = referenceGenericRenameUtility(t, p, c, q, prepared)
							want := fixture.ControlsReferenceDiagnostic("42809", `cannot rename columns of relation "s"`)
							want.Detail = "This operation is not supported for sequences."
							fixture.AssertDiagnostic(t, err, &want)
							if after := fixture.GenericRenameState(t, p, c, "s", 14, true, backend.PG); after != log {
								t.Fatal("rejected rename changed log_cnt", log, after)
							}
							fixture.SequenceProbeValue(t, p.Ctx, c, "SELECT currval('s')", 14)
							fixture.SequenceProbeValue(t, p.Ctx, c, "SELECT lastval()", 14)
							fixture.SequenceProbeValue(t, p.Ctx, c, "SELECT nextval('s')", 15)
						})
					}
				})
			}
		}
	}
}
