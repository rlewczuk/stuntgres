package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAlterTypeAddedColumnFillAtomicity(t *testing.T) {
	addedFillScenarios(t, []string{"rollback", "savepoint"}, func(s *addedFillSession, scenario string) {
		n := s.name
		outside := s.run(s.conn)
		addedFillExec(outside, "CREATE TABLE "+n+" (id integer PRIMARY KEY, payload text NOT NULL DEFAULT '7')", 0)
		addedFillExec(outside, "INSERT INTO "+n+"(id) VALUES (1),(2)", 2)
		baseline := func(r addedFillRun, count int, latency bool) {
			columns, types := []string{"id", "payload", "payload_type"}, []string{"INT4", "TEXT", "TEXT"}
			query := "SELECT id,payload,pg_typeof(payload)::text AS payload_type"
			if latency {
				query += ",latency,pg_typeof(latency)::text AS latency_type"
				columns = append(columns, "latency", "latency_type")
				types = append(types, "INT8", "TEXT")
			}
			rows := make([][]any, count)
			for id := 1; id <= count; id++ {
				row := []any{int64(id), "7", "text"}
				if latency {
					row = append(row, int64(-1), "bigint")
				}
				rows[id-1] = row
			}
			r(fixture.Select(query+" FROM "+n+" ORDER BY id", columns, types, rows))
			addedFillColumn(r, n, "payload", "text")
			if latency {
				addedFillColumn(r, n, "latency", "bigint")
			} else {
				addedFillAbsent(r, n, "latency")
			}
			addedFillAbsent(r, n, "marker")
		}
		baseline(outside, 2, false)
		tx, run := s.begin()
		addedFillExec(run, "ALTER TABLE "+n+" ADD COLUMN latency bigint DEFAULT -1 NOT NULL", 0)
		// Exercise the added default before saving the immutable transaction root.
		addedFillExec(run, "INSERT INTO "+n+"(id) VALUES (9)", 1)
		run(fixture.Select("SELECT payload,latency FROM "+n+" WHERE id=9", []string{"payload", "latency"}, []string{"TEXT", "INT8"}, [][]any{{"7", int64(-1)}}))
		addedFillExec(run, "DELETE FROM "+n+" WHERE id=9", 1)
		baseline(run, 2, true)
		if scenario == "savepoint" {
			addedFillExec(run, "SAVEPOINT before_rewrite", 0)
		}
		addedFillExec(run, "ALTER TABLE "+n+" ALTER COLUMN latency TYPE FLOAT", 0)
		addedFillExec(run, "ALTER TABLE "+n+" ADD COLUMN marker integer DEFAULT 7 NOT NULL", 0)
		addedFillExec(run, "UPDATE "+n+" SET payload='bad' WHERE id=2", 1)
		conversion := "ALTER TABLE " + n + " ALTER COLUMN payload DROP DEFAULT, ALTER COLUMN payload TYPE integer USING payload::integer, ALTER COLUMN payload SET DEFAULT 7"
		failure := fixture.ImplicitError(conversion, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`})
		failure.ErrorStage = "execute"
		run(failure)
		aborted := fixture.ImplicitError("SELECT * FROM "+n, fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
		aborted.SelectRows = true
		aborted.ErrorStage = "execute"
		if s.prepared {
			aborted.ErrorStage = "prepare"
		}
		run(aborted)
		if scenario == "rollback" {
			if err := tx.Rollback(); err != nil {
				s.t.Fatal(err)
			}
			baseline(outside, 2, false)
			addedFillExec(outside, "INSERT INTO "+n+"(id) VALUES (3)", 1)
			baseline(outside, 3, false)
			return
		}
		addedFillExec(run, "ROLLBACK TO before_rewrite", 0)
		baseline(run, 2, true)
		addedFillExec(run, "ROLLBACK TO before_rewrite", 0)
		baseline(run, 2, true)
		addedFillExec(run, "INSERT INTO "+n+"(id) VALUES (3)", 1)
		baseline(run, 3, true)
		addedFillExec(run, "ALTER TABLE "+n+" ALTER COLUMN latency TYPE FLOAT", 0)
		addedFillExec(run, conversion, 0)
		addedFillExec(run, "ALTER TABLE "+n+" ADD COLUMN marker integer DEFAULT 7 NOT NULL", 0)
		converted := func(r addedFillRun, count int) {
			rows := make([][]any, count)
			for id := 1; id <= count; id++ {
				rows[id-1] = []any{int64(id), int64(7), float64(-1), int64(7), "integer", "double precision", "integer"}
			}
			r(fixture.Select("SELECT id,payload,latency,marker,pg_typeof(payload)::text AS payload_type,pg_typeof(latency)::text AS latency_type,pg_typeof(marker)::text AS marker_type FROM "+n+" ORDER BY id", []string{"id", "payload", "latency", "marker", "payload_type", "latency_type", "marker_type"}, []string{"INT4", "INT4", "FLOAT8", "INT4", "TEXT", "TEXT", "TEXT"}, rows))
			addedFillColumn(r, n, "payload", "integer")
			addedFillColumn(r, n, "latency", "double precision")
			addedFillColumn(r, n, "marker", "integer")
		}
		converted(run, 3)
		s.commit(tx)
		converted(outside, 3)
		addedFillExec(outside, "INSERT INTO "+n+"(id) VALUES (4)", 1)
		converted(outside, 4)
	})
}
