package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTablespaceInfoPhysical(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"tsi_physical_reader"})
	p := fixture.NewScenario(t)
	tablespaceInfoBackends(t, p, func(t *testing.T, b fixture.Backend) {
		const directory = "/tmp/stuntgres_tablespace_info_owned"
		const name = "tsi_physical_space"
		c, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		q := tablespaceSQL(name, "", directory)
		if !b.PG {
			s := fixture.ErrorDiagnostic(q, tablespaceDiagnostic("0A000", tablespacePhysicalMessage))
			s.ErrorStage = "execute"
			run(s)
			run(fixture.Select("SELECT spcname FROM pg_tablespace WHERE spcname='tsi_physical_space'", []string{"spcname"}, []string{"NAME"}, nil))
			s = fixture.ErrorDiagnostic("SELECT pg_tablespace_location('4294967295'::oid)", tablespaceInfoLocationError(false))
			s.SelectRows = true
			run(s)
			return
		}
		tablespaceContainer(t, p, "test ! -e "+directory+" && mkdir "+directory+" && chown postgres:postgres "+directory)
		// Cleanup is registered before CREATE. Restore permissions first even when an
		// assertion fails while the version directory is deliberately inaccessible.
		defer func() {
			tablespaceContainer(t, p, "if test -d "+directory+"/PG_18_202506291; then chmod 700 "+directory+"/PG_18_202506291; fi")
			for _, q := range []string{"RESET ROLE", "DROP VIEW IF EXISTS tsi_physical_view", "DROP TABLE IF EXISTS tsi_physical_a", "DROP TABLE IF EXISTS tsi_physical_b", "DROP TABLESPACE IF EXISTS " + name} {
				if _, err := c.ExecContext(p.Ctx, q); err != nil {
					t.Error("physical cleanup", q, err)
				}
			}
			tablespaceContainer(t, p, "rmdir "+directory)
		}()
		run(fixture.Exec(q, 0))
		var oid int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_tablespace WHERE spcname=$1", name).Scan(&oid); err != nil {
			t.Fatal(err)
		}
		if oid <= 0 || oid == 1663 || oid == 1664 {
			t.Fatal("invalid generated tablespace identity", oid)
		}
		current := tablespaceInfoCurrent(t, p, c)
		location := func() {
			for _, prepared := range []bool{false, true} {
				s := tablespaceInfoSelect("SELECT pg_tablespace_location($1::oid) AS v", "TEXT", [][]any{{directory}}, oid)
				s.Prepared = prepared
				run(s)
			}
		}
		occupancy := func(phase string, present bool) {
			t.Run(phase, func(t *testing.T) {
				var rows [][]any
				if present {
					rows = [][]any{{tablespaceInfoOID(b, current)}}
				}
				s := tablespaceInfoSelect("SELECT v FROM pg_tablespace_databases($1::oid) AS t(v)", "OID", rows, oid)
				s.Bag = true
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			})
		}
		location()
		occupancy("fresh_tablespace", false)
		run(fixture.Exec("CREATE VIEW tsi_physical_view AS SELECT 1 AS a", 0))
		occupancy("fileless_view", false)
		run(fixture.Exec("CREATE TABLE tsi_physical_a(a integer) TABLESPACE "+name, 0))
		occupancy("zero_row_table", true)
		run(fixture.Select("SELECT count(*) AS n FROM tsi_physical_a", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Exec("CREATE TABLE tsi_physical_b(a integer) TABLESPACE "+name, 0))
		occupancy("two_zero_row_tables", true)
		run(fixture.Exec("INSERT INTO tsi_physical_a VALUES(1)", 1))
		occupancy("inserted_row", true)
		run(fixture.Exec("DELETE FROM tsi_physical_a", 1))
		occupancy("deleted_row", true)
		run(fixture.Exec("CREATE ROLE tsi_physical_reader NOLOGIN NOSUPERUSER", 0))
		run(fixture.Exec("REVOKE ALL ON tsi_physical_a,tsi_physical_b FROM PUBLIC", 0))
		run(fixture.Exec("SET ROLE tsi_physical_reader", 0))
		run(fixture.Select("SELECT has_tablespace_privilege(current_user,$1::oid,'CREATE') AS allowed", []string{"allowed"}, []string{"BOOL"}, [][]any{{false}}, oid))
		location()
		occupancy("ordinary_role", true)
		run(fixture.Exec("RESET ROLE", 0))
		tablespaceContainer(t, p, "chmod 000 "+directory+"/PG_18_202506291")
		want := tablespaceDiagnostic("42501", fmt.Sprintf(`could not open directory "pg_tblspc/%d/PG_18_202506291": Permission denied`, oid))
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("SELECT v FROM pg_tablespace_databases($1::oid) AS t(v)", want, oid)
			s.SelectRows = true
			s.Prepared = prepared
			s.ErrorStage = "execute"
			run(s)
		}
		location()
		tablespaceContainer(t, p, "chmod 700 "+directory+"/PG_18_202506291")
		occupancy("permissions_restored", true)
		run(fixture.Exec("DROP TABLE tsi_physical_a", 0))
		occupancy("first_table_dropped", true)
		// Pinned18.6 still reports the directory at this immediate DROP boundary;
		// physical unlink is not the same contract as logical descriptor visibility.
		run(fixture.Exec("DROP TABLE tsi_physical_b", 0))
		occupancy("last_table_dropped", true)
	})
}
