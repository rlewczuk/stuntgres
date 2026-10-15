package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceObjectAddressStoredDemand(t *testing.T) {
	objectAddressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		cases := []struct{ name, expr, typ, literal string }{
			{"identify", "(pg_identify_object(1247,23,0)).identity", "TEXT", "'integer'"},
			{"as_address", "(pg_identify_object_as_address(1247,23,0)).object_names", "TEXT[]", "ARRAY['integer']::text[]"},
			{"inverse", "(pg_get_object_address('type',ARRAY['integer'],ARRAY[]::text[])).objsubid", "INT", "0"},
			{"describe", "pg_describe_object(1247,23,0)", "TEXT", "'type integer'"},
		}
		for _, tc := range cases {
			for _, kind := range []string{"default", "check"} {
				table := "addr_demand_" + kind + "_" + tc.name
				definition := "v " + tc.typ + " DEFAULT " + tc.expr
				if kind == "check" {
					definition = "v " + tc.typ + ",CONSTRAINT " + table + "_check CHECK (v = " + tc.expr + ")"
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE "+table+"(id INT,"+definition+")", 0))
			}
		}
		consume := func(t *testing.T, executor fixture.Executor, recorder *fixture.Recorder) {
			for _, tc := range cases {
				for _, kind := range []string{"default", "check"} {
					table := "addr_demand_" + kind + "_" + tc.name
					fixture.RunStep(t, p.Ctx, executor, recorder, fixture.Exec("DELETE FROM "+table, 0))
					for i, prepared := range []bool{false, true} {
						q := "INSERT INTO " + table + "(id) VALUES(1)"
						if kind == "check" {
							q = "INSERT INTO " + table + " VALUES(1," + tc.literal + ")"
						}
						s := fixture.Exec(q, 1)
						if prepared {
							s = fixture.Prepared(s)
						}
						fixture.RunStep(t, p.Ctx, executor, recorder, s)
						if kind == "check" {
							bad, detail := "'wrong'", "wrong"
							if tc.name == "inverse" {
								bad, detail = "1", "1"
							}
							if tc.name == "as_address" {
								bad, detail = "ARRAY['wrong']::text[]", "{wrong}"
							}
							want := fixture.Diagnostic{Code: "23514", Message: `new row for relation "` + table + `" violates check constraint "` + table + `_check"`, Detail: "Failing row contains (1, " + detail + ").", Schema: "public", Table: table, Constraint: table + "_check"}
							failure := fixture.ImplicitError("UPDATE "+table+" SET v="+bad, want)
							failure.ErrorStage = "execute"
							if prepared {
								failure = fixture.Prepared(failure)
							}
							fixture.RunStep(t, p.Ctx, executor, recorder, failure)
							// The failed write must leave all prior rows unchanged.
							fixture.RunStep(t, p.Ctx, executor, recorder, fixture.Select("SELECT count(*) AS n FROM "+table+" WHERE v="+tc.literal, []string{"n"}, []string{"INT8"}, [][]any{{int64(i + 1)}}))
						}
					}
					fixture.RunStep(t, p.Ctx, executor, recorder, fixture.Select("SELECT count(*) AS n FROM "+table+" WHERE v="+tc.literal, []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
					fixture.RunStep(t, p.Ctx, executor, recorder, fixture.Exec("DELETE FROM "+table, 2))
				}
			}
		}
		consume(t, c, b.Recorder)
		if b.PG {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		consume(t, c, b.Recorder)
		driver := stuntgres.NewDriver()
		connector, err := driver.OpenConnector(t.Name() + "/demand-destination")
		if err != nil {
			t.Fatal(err)
		}
		destination := sql.OpenDB(connector)
		defer destination.Close()
		if _, err := destination.ExecContext(p.Ctx, "CREATE TABLE addr_demand_spacer(a INT)"); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, destination, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		recordedDestination, destinationRecorder := fixture.InsertSelectRecordedPool(t, connector)
		consume(t, recordedDestination, destinationRecorder)
	})
}
