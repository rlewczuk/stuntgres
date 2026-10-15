package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSQLJSONObjectAggregateCorrelatedOwnership(t *testing.T) {
	sqlJSONObjectAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE sjo_owner(g int,k text,v int)", 0),
			fixture.Exec("INSERT INTO sjo_owner VALUES(1,'a',1),(1,'b',2),(2,'c',3)", 3),
		} {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		}
		for _, tc := range []struct {
			name, q string
			rows    [][]any
		}{
			{"outer-owned", "SELECT (SELECT json_objectagg(o.k:o.v WITH UNIQUE RETURNING jsonb FORMAT JSON)) AS v FROM sjo_owner o", [][]any{{`{"a": 1, "b": 2, "c": 3}`}}},
			{"outer-owned-empty", "SELECT (SELECT json_objectagg(o.k:o.v WITH UNIQUE RETURNING jsonb FORMAT JSON)) AS v FROM sjo_owner o WHERE FALSE", [][]any{{nil}}},
			{"outer-owned-grouped", "SELECT (SELECT json_objectagg(o.k:o.v WITH UNIQUE RETURNING jsonb FORMAT JSON)) AS v FROM sjo_owner o GROUP BY o.g ORDER BY o.g", [][]any{{`{"a": 1, "b": 2}`}, {`{"c": 3}`}}},
			{"inner-owned", "SELECT (SELECT json_objectagg(i.k:i.v WITH UNIQUE RETURNING jsonb FORMAT JSON) FROM sjo_owner i WHERE i.g=o.g) AS v FROM sjo_owner o WHERE o.k<>'b' ORDER BY o.g", [][]any{{`{"a": 1, "b": 2}`}, {`{"c": 3}`}}},
			{"outer-key-inner-value", "SELECT (SELECT json_objectagg(o.k:i.v WITH UNIQUE RETURNING jsonb FORMAT JSON) FROM sjo_owner i WHERE i.k='c') AS v FROM sjo_owner o WHERE o.k<>'b' ORDER BY o.g", [][]any{{`{"a": 3}`}, {`{"c": 3}`}}},
			{"inner-key-outer-value", "SELECT (SELECT json_objectagg(i.k:o.v WITH UNIQUE RETURNING jsonb FORMAT JSON) FROM sjo_owner i WHERE i.k='c') AS v FROM sjo_owner o WHERE o.k<>'b' ORDER BY o.g", [][]any{{`{"c": 1}`}, {`{"c": 3}`}}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				s := fixture.Select(tc.q, []string{"v"}, []string{"JSONB"}, tc.rows)
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
					jsonArrayAggregateMetadata(t, rows, b.PG, []string{"v"}, []string{"JSONB"}, nil)
				}
				jsonArrayAggregateRoutes(t, p, b, s)
			})
		}
	})
}

func TestReferenceSQLJSONObjectAggregateConsumerAtomicity(t *testing.T) {
	sqlJSONObjectAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE sjo_late_format(id int,g int,k text,v text)", 0),
			fixture.Exec("INSERT INTO sjo_late_format VALUES(1,1,'a','1'),(2,2,'b','2'),(3,2,'b','no')", 3),
			fixture.Exec("CREATE TABLE sjo_late_return(g int,k text,v int)", 0),
			fixture.Exec("INSERT INTO sjo_late_return VALUES(1,'a',1),(2,'b',12345)", 2),
			fixture.Exec("CREATE TABLE sjo_review_destination(g int,v text)", 0),
			fixture.Exec("INSERT INTO sjo_review_destination VALUES(9,'sentinel')", 1),
		} {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		}
		formatError := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type json")
		formatError.Detail = `Token "no" is invalid.`
		formatError.Context = "JSON data, line 1: no"
		for _, tc := range []struct {
			name, q, repair, rebreak string
			diagnostic               fixture.Diagnostic
			recovered                [][]any
		}{
			{"late-format", "INSERT INTO sjo_review_destination SELECT g,json_objectagg(k:v FORMAT JSON RETURNING text) FROM(SELECT * FROM sjo_late_format ORDER BY id) s GROUP BY g ORDER BY g", "UPDATE sjo_late_format SET v='2' WHERE id=3", "UPDATE sjo_late_format SET v='no' WHERE id=3", formatError, [][]any{{int64(1), `{ "a" : 1 }`}, {int64(2), `{ "b" : 2, "b" : 2 }`}, {int64(9), "sentinel"}}},
			{"late-returning", "INSERT INTO sjo_review_destination SELECT g,json_objectagg(k:v RETURNING varchar(11)) FROM sjo_late_return GROUP BY g ORDER BY g", "UPDATE sjo_late_return SET v=2 WHERE g=2", "UPDATE sjo_late_return SET v=12345 WHERE g=2", fixture.ControlsReferenceDiagnostic("22001", "value too long for type character varying(11)"), [][]any{{int64(1), `{ "a" : 1 }`}, {int64(2), `{ "b" : 2 }`}, {int64(9), "sentinel"}}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					t.Run(map[bool]string{false: "direct", true: "same-held"}[prepared], func(t *testing.T) {
						conn, err := b.DB.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer conn.Close()
						var executor fixture.Executor = conn
						if prepared {
							stmt, err := conn.PrepareContext(p.Ctx, tc.q)
							if err != nil {
								t.Fatal(err)
							}
							defer stmt.Close()
							executor = fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: tc.q}
						}
						checkDestination := func(rows [][]any) {
							t.Helper()
							fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT g,v FROM sjo_review_destination ORDER BY g", []string{"g", "v"}, []string{"INT4", "TEXT"}, rows))
						}
						for _, query := range []bool{true, false} {
							failure := fixture.ErrorDiagnostic(tc.q, tc.diagnostic)
							failure.SelectRows = query
							failure.ErrorStage = "execute"
							fixture.RunStep(t, p.Ctx, executor, b.Recorder, failure)
							checkDestination([][]any{{int64(9), "sentinel"}})
						}
						fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec(tc.repair, 1))
						fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Exec(tc.q, 2))
						checkDestination(tc.recovered)
						fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("DELETE FROM sjo_review_destination WHERE g<>9", 2))
						fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec(tc.rebreak, 1))
					})
				}
			})
		}
	})
}
