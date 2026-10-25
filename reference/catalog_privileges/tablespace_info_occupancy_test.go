package reference_test

import (
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTablespaceInfoOccupancy(t *testing.T) {
	p := fixture.NewScenario(t)
	tablespaceInfoBackends(t, p, func(t *testing.T, b fixture.Backend) {
		oid := tablespaceInfoCurrent(t, p, b.DB)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		check := func() {
			want := [][]any{{tablespaceInfoOID(b, oid)}}
			if b.PG {
				s := fixture.Select("SELECT d.datname FROM pg_tablespace_databases(1663) AS t(v) JOIN pg_database d ON d.oid=t.v", []string{"datname"}, []string{"NAME"}, [][]any{{"postgres"}, {"template0"}, {"template1"}, {"stuntgres_reference"}})
				s.Bag = true
				run(s)
			} else {
				run(tablespaceInfoSelect("SELECT pg_tablespace_databases(1663) AS v", "OID", want))
			}
			for _, prepared := range []bool{false, true} {
				s := tablespaceInfoSelect("SELECT v FROM pg_tablespace_databases(1663) AS t(v) WHERE v=(SELECT oid FROM pg_database WHERE datname=current_database())", "OID", want)
				s.Prepared = prepared
				run(s)
			}
		}
		run(fixture.Select("SELECT relkind,relisshared FROM pg_class WHERE oid=1259", []string{"relkind", "relisshared"}, []string{"CHAR", "BOOL"}, [][]any{{"r", false}}))
		check()
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE tsi_occupancy(a integer)", 0),
			fixture.Exec("INSERT INTO tsi_occupancy VALUES(1),(2)", 2),
			fixture.Exec("DELETE FROM tsi_occupancy", 2),
			fixture.Exec("CREATE TABLE tsi_occupancy_two(a integer)", 0),
			fixture.Exec("DROP TABLE tsi_occupancy", 0),
			fixture.Exec("DROP TABLE tsi_occupancy_two", 0),
		} {
			run(s)
			check()
		}
		if !b.PG {
			connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "-other")
			if err != nil {
				t.Fatal(err)
			}
			other := sql.OpenDB(connector)
			defer other.Close()
			if _, err := other.ExecContext(p.Ctx, "CREATE TABLE tsi_other(a integer)"); err != nil {
				t.Fatal(err)
			}
			check()
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			check()
		}
	})
}
