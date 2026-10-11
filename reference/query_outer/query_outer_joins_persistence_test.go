package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryOuterJoinsPersistence(t *testing.T) {
	fixture.OuterJoinsBackendScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, backend fixture.Backend) {
		for _, kind := range []string{"LEFT", "RIGHT", "FULL"} {
			for _, form := range []string{"ON", "USING", "NATURAL"} {
				name := "persist_" + strings.ToLower(kind) + "_" + strings.ToLower(form)
				source := "a " + kind + " JOIN b USING(k)"
				projection := "*"
				if form == "ON" {
					source = "a " + kind + " JOIN b ON a.k=b.k"
					projection = "a.k AS ak,a.l,b.k AS bk,b.r"
				}
				if form == "NATURAL" {
					source = "a NATURAL " + kind + " JOIN b"
				}
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("CREATE VIEW "+name+" AS SELECT "+projection+" FROM "+source, 0))
			}
		}
		fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("CREATE TABLE disjoint(z BOOLEAN)", 0))
		fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("CREATE VIEW persist_zero AS SELECT * FROM a NATURAL LEFT JOIN disjoint", 0))
		var dump bytes.Buffer
		if !backend.PG {
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			direct, err := conn.QueryContext(p.Ctx, "SELECT * FROM persist_full_using")
			if err != nil {
				t.Fatal(err)
			}
			defer direct.Close()
			computed, err := conn.QueryContext(p.Ctx, "SELECT k+1 AS next FROM persist_full_using")
			if err != nil {
				t.Fatal(err)
			}
			defer computed.Close()
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			fixture.OuterJoinsHeldRows(t, direct, fixture.Select("", []string{"k", "l", "r"}, []string{"INT8", "TEXT", "TEXT"}, fixture.OuterJoinsMergedRows("FULL")))
			fixture.OuterJoinsHeldRows(t, computed, fixture.Select("", []string{"next"}, []string{"INT8"}, [][]any{{int64(2)}, {int64(2)}, {int64(2)}, {int64(2)}, {int64(3)}, {nil}, {int64(4)}, {nil}}))
		}
		for _, kind := range []string{"LEFT", "RIGHT", "FULL"} {
			for _, form := range []string{"ON", "USING", "NATURAL"} {
				name := "persist_" + strings.ToLower(kind) + "_" + strings.ToLower(form)
				step := fixture.Select("SELECT * FROM "+name, []string{"k", "l", "r"}, []string{"INT8", "TEXT", "TEXT"}, fixture.OuterJoinsMergedRows(kind))
				if form == "ON" {
					step = fixture.Select("SELECT * FROM "+name, []string{"ak", "l", "bk", "r"}, []string{"INT4", "TEXT", "INT8", "TEXT"}, fixture.OuterJoinsOnRows(kind))
				}
				fixture.OuterJoinsCases(t, p, conn, backend.Recorder, step)
			}
		}
		fixture.OuterJoinsCases(t, p, conn, backend.Recorder, fixture.Select("SELECT * FROM persist_zero", []string{"k", "l", "z"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(1), "a", nil}, {int64(1), "a", nil}, {int64(2), "b", nil}, {nil, "n", nil}}))
		var definition string
		if err := conn.QueryRowContext(p.Ctx, "SELECT definition FROM pg_catalog.pg_views WHERE viewname='persist_zero'").Scan(&definition); err != nil {
			t.Fatal(err)
		}
		outerJoinsAssertViewDefinition(t, "no_keys", definition)
		if !backend.PG {
			var again bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || !bytes.Equal(dump.Bytes(), again.Bytes()) {
				t.Fatal("outer join replay changed canonical dump", err)
			}
		}
	})
}
