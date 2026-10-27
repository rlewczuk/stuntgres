package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func fileListControlRoundtrip(s *fixture.RoleMembershipScenario) {
	s.T.Helper()
	var originalRaw string
	if err := s.C.QueryRowContext(s.P.Ctx, "SELECT proacl::text FROM pg_proc WHERE oid=3353").Scan(&originalRaw); err != nil {
		s.T.Fatal(err)
	}
	s.Exec("SET ROLE gfl_actor")
	held := predefinedHeld(s, "SELECT * FROM pg_ls_dir($1::text,false,false)")
	fileListHeldEmpty(s, held, false, nil)
	s.Exec("RESET ROLE")
	var snapshot bytes.Buffer
	if err := stuntgres.Dump(s.P.Ctx, s.P.Native, &snapshot); err != nil {
		s.T.Fatal(err)
	}
	s.Exec("SET application_name='gfl_live_peer'")
	if err := stuntgres.Reset(s.P.Ctx, s.P.Native); err != nil {
		s.T.Fatal(err)
	}
	if err := stuntgres.Restore(s.P.Ctx, s.P.Native, bytes.NewReader(snapshot.Bytes())); err != nil {
		s.T.Fatal(err)
	}
	predefinedSelect(s, "SELECT current_setting('application_name') AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"gfl_live_peer"}})
	predefinedSelect(s, "SELECT id FROM gfl_sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})
	predefinedSelect(s, "SELECT proacl::text AS acl FROM pg_proc WHERE oid=3353", []string{"acl"}, []string{"TEXT"}, [][]any{{originalRaw}})
	s.Exec("SET ROLE gfl_actor")
	fileListEmpty(s, "pg_ls_dir(NULL::text,false,false)", false)
	fileListHeldEmpty(s, held, false, nil)
	fileListUnsupported(s, "SELECT * FROM pg_ls_dir('gfl_missing_497356',true,false)", "pg_ls_dir")
	s.Exec("RESET ROLE")
	// A distinct Driver remaps principal identities; roles are prerequisites,
	// deliberately not silently created by portable catalog Restore.
	driver := &stuntgres.Driver{}
	connector, err := driver.OpenConnector("gfl_fresh_controls")
	if err != nil {
		s.T.Fatal(err)
	}
	fresh := sql.OpenDB(connector)
	defer fresh.Close()
	if _, err := fresh.ExecContext(s.P.Ctx, "CREATE TABLE gfl_destination_sentinel(id INT); INSERT INTO gfl_destination_sentinel VALUES(91)"); err != nil {
		s.T.Fatal(err)
	}
	if _, err := fresh.ExecContext(s.P.Ctx, "CREATE ROLE gfl_destination_reader; GRANT EXECUTE ON FUNCTION pg_ls_dir(text,boolean,boolean) TO gfl_destination_reader"); err != nil {
		s.T.Fatal(err)
	}
	if err := stuntgres.Restore(s.P.Ctx, fresh, bytes.NewReader(snapshot.Bytes())); err == nil {
		s.T.Fatal("missing principals accepted")
	}
	var sentinel int
	if err := fresh.QueryRowContext(s.P.Ctx, "SELECT id FROM gfl_destination_sentinel").Scan(&sentinel); err != nil || sentinel != 91 {
		s.T.Fatal("failed restore publication", sentinel, err)
	}
	var allowed bool
	if err := fresh.QueryRowContext(s.P.Ctx, "SELECT has_function_privilege('gfl_destination_reader','pg_ls_dir(text,boolean,boolean)','EXECUTE')").Scan(&allowed); err != nil || !allowed {
		s.T.Fatal("failed restore changed destination permissions", allowed, err)
	}
	for _, role := range []string{"gfl_delegate", "gfl_set", "gfl_mid", "gfl_actor"} {
		if _, err := fresh.ExecContext(s.P.Ctx, "CREATE ROLE "+role+" LOGIN"); err != nil {
			s.T.Fatal(err)
		}
	}
	if err := stuntgres.Restore(s.P.Ctx, fresh, bytes.NewReader(snapshot.Bytes())); err != nil {
		s.T.Fatal(err)
	}
	var raw string
	if err := fresh.QueryRowContext(s.P.Ctx, "SELECT proacl::text FROM pg_proc WHERE oid=3353").Scan(&raw); err != nil || raw != originalRaw {
		s.T.Fatal("fresh raw ACL", raw, originalRaw, err)
	}
	c, err := fresh.Conn(s.P.Ctx)
	if err != nil {
		s.T.Fatal(err)
	}
	defer c.Close()
	if _, err := c.ExecContext(s.P.Ctx, "SET ROLE gfl_actor"); err != nil {
		s.T.Fatal(err)
	}
	stmt, err := c.PrepareContext(s.P.Ctx, "SELECT * FROM pg_ls_dir($1::text,false,false)")
	if err != nil {
		s.T.Fatal(err)
	}
	defer stmt.Close()
	rows, err := stmt.QueryContext(s.P.Ctx, nil)
	if err != nil {
		s.T.Fatal(err)
	}
	columns, err := rows.ColumnTypes()
	if err != nil || len(columns) != 1 || columns[0].Name() != "pg_ls_dir" || columns[0].DatabaseTypeName() != "TEXT" || rows.Next() {
		rows.Close()
		s.T.Fatal("restored typed strict empty", columns, err)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		s.T.Fatal(err)
	}
	rows.Close()
	rows, err = stmt.QueryContext(s.P.Ctx, "gfl_missing_497356")
	if rows != nil {
		rows.Close()
	}
	fixture.AssertControlDiagnostic(s.T, err, fixture.ControlsReferenceDiagnostic("0A000", "pg_ls_dir physical directory listing is unsupported in embedded mode"))
	canceled, cancel := context.WithCancel(s.P.Ctx)
	cancel()
	if rows, err := stmt.QueryContext(canceled, nil); rows != nil || !errors.Is(err, context.Canceled) {
		if rows != nil {
			rows.Close()
		}
		s.T.Fatal("native pre-cancellation", rows, err)
	}
}
func TestReferenceGenericFileListsControls(t *testing.T) {
	fileListCases(t, func(s *fixture.RoleMembershipScenario, _ *sql.DB) {
		s.Exec("CREATE TABLE gfl_sentinel(id INT)")
		s.Run(fixture.Exec("INSERT INTO gfl_sentinel VALUES(7)", 1))
		s.Exec("GRANT EXECUTE ON FUNCTION pg_ls_dir(text,boolean,boolean) TO gfl_actor")
		s.Exec("GRANT EXECUTE ON FUNCTION pg_ls_dir(text) TO gfl_delegate WITH GRANT OPTION")
		s.Exec("SET ROLE gfl_delegate")
		s.Exec("GRANT EXECUTE ON FUNCTION pg_ls_dir(text) TO gfl_actor")
		s.Exec("RESET ROLE")
		s.Exec("REVOKE EXECUTE ON FUNCTION pg_ls_logdir() FROM pg_monitor")
		if s.Backend.PG {
			s.Run(fixture.Exec("UPDATE pg_catalog.pg_proc SET proacl='{}'::aclitem[] WHERE oid=3353", 1))
		} else {
			// Raw catalog repair belongs to Restore, not arbitrary live catalog DML.
			// Retain the emitted locator and the rest of the genuine dump unchanged.
			var snapshot bytes.Buffer
			if err := stuntgres.Dump(s.P.Ctx, s.P.Native, &snapshot); err != nil {
				s.T.Fatal(err)
			}
			lines := strings.Split(snapshot.String(), "\n")
			repairs := 0
			for i, line := range lines {
				if !strings.HasPrefix(line, "UPDATE pg_catalog.pg_proc SET proacl=") || !strings.Contains(line, "pg_ls_logdir") {
					continue
				}
				_, locator, ok := strings.Cut(line, " WHERE ")
				if !ok {
					s.T.Fatal("missing emitted ACL locator", line)
				}
				lines[i] = `UPDATE pg_catalog.pg_proc SET proacl=pg_catalog.acldefault('c'::pg_catalog."char",0::pg_catalog.oid)::pg_catalog.aclitem[] WHERE ` + locator
				repairs++
			}
			if repairs != 1 {
				s.T.Fatal("missing emitted logdir ACL repair", repairs)
			}
			if err := stuntgres.Restore(s.P.Ctx, s.P.Native, strings.NewReader(strings.Join(lines, "\n"))); err != nil {
				s.T.Fatal(err)
			}
		}
		predefinedSelect(s, "SELECT proacl::text AS acl FROM pg_proc WHERE oid=3353", []string{"acl"}, []string{"TEXT"}, [][]any{{"{}"}})
		s.Exec("SET ROLE gfl_actor")
		fileListEmpty(s, "pg_ls_dir(NULL::text)", false)
		fileListEmpty(s, "pg_ls_dir(NULL::text,false,false)", false)
		s.Exec("RESET ROLE")
		if !s.Backend.PG {
			fileListControlRoundtrip(s)
		} else {
			// Replay actual emitted native repairs, never a handwritten ACL surrogate.
			// Catalog DML replay does not maintain PostgreSQL shared dependencies.
			// Remove real grant dependencies before replacing their raw ACL values.
			s.Exec("DROP OWNED BY gfl_actor,gfl_mid,gfl_set,gfl_delegate CASCADE")
			var snapshot bytes.Buffer
			if err := stuntgres.Dump(s.P.Ctx, s.P.Native, &snapshot); err != nil {
				s.T.Fatal(err)
			}
			count := 0
			for line := range strings.SplitSeq(snapshot.String(), "\n") {
				if strings.HasPrefix(line, "UPDATE pg_catalog.pg_proc SET proacl=") && strings.Contains(line, "pg_ls_") {
					if _, err := s.C.ExecContext(s.P.Ctx, line); err != nil {
						s.T.Fatal("emitted ACL replay", err)
					}
					count++
				}
			}
			if count != 3 {
				s.T.Fatal("expected only the two modified dirname ACLs and empty logdir ACL", count)
			}
			s.Exec("SET ROLE gfl_actor")
			fileListEmpty(s, "pg_ls_dir(NULL::text)", false)
			fileListEmpty(s, "pg_ls_dir(NULL::text,false,false)", false)
			s.Exec("RESET ROLE")
		}
		s.Exec("SET ROLE pg_monitor")
		fileListDenied(s, "pg_ls_logdir()", "pg_ls_logdir")
		fileListEmpty(s, "pg_ls_replslotdir(NULL::text)", true)
		s.Exec("RESET ROLE")
	})
}
