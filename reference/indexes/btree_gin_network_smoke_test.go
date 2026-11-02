package reference_test

import (
	"bytes"
	"context"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceBtreeGINNetworkFeatureSmoke(t *testing.T) {
	var emitted string
	classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, run := classConn(t, p, b)
		compoundCleanup(t, p, b, conn)
		seed := func() {
			run(fixture.Exec("CREATE EXTENSION btree_gin VERSION '1.3'", 0))
			for _, tc := range networkCases {
				table := "network_smoke_" + tc.typ
				run(fixture.Exec("CREATE TABLE "+table+"(id integer NOT NULL,v "+tc.typ+")", 0))
				run(fixture.Exec("CREATE INDEX "+table+"_i ON "+table+" USING gin(v public."+tc.typ+"_ops)", 0))
				networkSeed(run, table, tc)
			}
			run(fixture.Exec("CREATE INDEX network_smoke_cidr_inet ON network_smoke_cidr USING gin(v inet_ops)", 0))
			run(fixture.Exec("CREATE TABLE network_smoke_int(id integer,v integer)", 0))
			run(fixture.Exec("CREATE INDEX network_smoke_int_i ON network_smoke_int USING gin(v oid_ops)", 0))
			run(fixture.Exec("INSERT INTO network_smoke_int VALUES(1,0),(2,2147483647),(3,-2147483648),(4,-1),(5,NULL)", 5))
		}
		check := func() {
			for _, tc := range networkCases {
				table := "network_smoke_" + tc.typ
				for _, pred := range []struct {
					op  string
					ids []int64
				}{{"=", []int64{2, 3}}, {">=", []int64{2, 3, 4}}, {"<>", []int64{1, 4}}} {
					q := "SELECT id FROM " + table + " WHERE v" + pred.op + "$1::" + tc.typ + " ORDER BY id"
					held := classHeld(t, p, conn, q)
					classRoutes(t, p, b, conn, ginIDs(q, pred.ids, tc.boundary))
					fixture.RunStep(t, p.Ctx, held, b.Recorder, ginIDs(q, pred.ids, tc.boundary))
					fixture.RunStep(t, p.Ctx, held, b.Recorder, ginIDs(q, nil, nil))
				}
				classRoutes(t, p, b, conn, networkValueStep(b, "SELECT id,v FROM "+table+" ORDER BY id", tc, networkValues(tc)))
				classRoutes(t, p, b, conn, networkValueStep(b, "SELECT id,v FROM "+table+" WHERE false", tc, nil))
			}
			run(ginIDs("SELECT id FROM network_smoke_int WHERE v<0 ORDER BY id", []int64{3, 4}))
		}
		seed()
		check()
		d := fixture.ControlsReferenceDiagnostic("42804", `operator class "cidr_ops" does not accept data type inet`)
		ginUtilityError(t, p, b, conn, "CREATE INDEX network_smoke_bad ON network_smoke_inet USING gin(v cidr_ops)", d)
		compoundIndexAbsent(run, "network_smoke_inet", "network_smoke_bad", 1)
		for _, tc := range networkCases {
			table := "network_smoke_" + tc.typ
			run(fixture.Prepared(fixture.Exec("INSERT INTO "+table+" VALUES($1,$2::"+tc.typ+")", 1, 6, tc.boundary)))
			run(ginIDs("SELECT id FROM "+table+" WHERE v=$1::"+tc.typ+" ORDER BY id", []int64{2, 3, 6}, tc.boundary))
			run(fixture.Prepared(fixture.Exec("UPDATE "+table+" SET v=$1::"+tc.typ+" WHERE id=6", 1, tc.lower)))
			run(ginIDs("SELECT id FROM "+table+" WHERE v=$1::"+tc.typ+" ORDER BY id", []int64{1, 6}, tc.lower))
			run(fixture.Exec("DELETE FROM "+table+" WHERE id=6", 1))
		}
		if b.PG {
			for _, tc := range networkCases {
				run(fixture.Exec("DROP TABLE network_smoke_"+tc.typ, 0))
			}
			run(fixture.Exec("DROP TABLE network_smoke_int", 0))
			run(fixture.Exec("DROP EXTENSION btree_gin CASCADE", 0))
			if emitted != "" {
				if _, err := conn.ExecContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder), emitted); err != nil {
					t.Fatalf("smoke PG replay: %v\n%s", err, emitted)
				}
			} else {
				seed()
			}
		} else {
			retained := networkRetain(t, p, b, conn, "network_smoke_")
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			emitted = dump.String()
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			run(amCount("SELECT count(*) FROM pg_extension WHERE extname='btree_gin'", 0))
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			networkCheckRetained(t, retained)
		}
		check()
		networkGraph(t, p, b, conn, "public")
		run(fixture.Prepared(fixture.Exec("INSERT INTO network_smoke_inet VALUES($1,$2::inet)", 1, 6, networkCases[0].boundary)))
		run(ginIDs("SELECT id FROM network_smoke_inet WHERE v=$1::inet ORDER BY id", []int64{2, 3, 6}, networkCases[0].boundary))
		run(fixture.Exec("DELETE FROM network_smoke_inet WHERE id=6", 1))
		check()
	})
}
