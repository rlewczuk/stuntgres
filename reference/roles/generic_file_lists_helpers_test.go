package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

type fileListSignature struct {
	name, signature, args string
	oid                   int64
	record                bool
}

var fileListSignatures = []fileListSignature{
	{"pg_ls_dir", "pg_ls_dir(text)", "NULL::text", 2625, false},
	{"pg_ls_dir", "pg_ls_dir(text,boolean,boolean)", "NULL::text,false,false", 3297, false},
	{"pg_ls_logdir", "pg_ls_logdir()", "", 3353, true},
	{"pg_ls_waldir", "pg_ls_waldir()", "", 3354, true},
	{"pg_ls_logicalmapdir", "pg_ls_logicalmapdir()", "", 6271, true},
	{"pg_ls_logicalsnapdir", "pg_ls_logicalsnapdir()", "", 6270, true},
	{"pg_ls_replslotdir", "pg_ls_replslotdir(text)", "NULL::text", 6272, true},
	{"pg_ls_summariesdir", "pg_ls_summariesdir()", "", 6400, true},
	{"pg_ls_archive_statusdir", "pg_ls_archive_statusdir()", "", 5031, true},
}

func fileListCases(t *testing.T, body func(*fixture.RoleMembershipScenario, *sql.DB)) {
	t.Helper()
	predefinedCases(t, []string{"gfl_actor", "gfl_mid", "gfl_set", "gfl_delegate"}, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
		if s.Backend.PG {
			originals := make(map[int64]sql.NullString, len(fileListSignatures))
			for _, sig := range fileListSignatures {
				var raw sql.NullString
				if err := s.C.QueryRowContext(s.P.Ctx, "SELECT proacl::text FROM pg_proc WHERE oid=$1", sig.oid).Scan(&raw); err != nil {
					s.T.Fatal(err)
				}
				originals[sig.oid] = raw
			}
			defer func() {
				if _, err := s.C.ExecContext(s.P.Ctx, "RESET ROLE"); err != nil {
					s.T.Error(err)
				}
				// Remove shared routine grant dependencies before restoring raw ACLs.
				if _, err := s.C.ExecContext(s.P.Ctx, "DROP OWNED BY gfl_actor,gfl_mid,gfl_set,gfl_delegate CASCADE"); err != nil {
					s.T.Error(err)
				}
				for oid, raw := range originals {
					var value any
					if raw.Valid {
						value = raw.String
					}
					if _, err := s.C.ExecContext(s.P.Ctx, "UPDATE pg_catalog.pg_proc SET proacl=$1::aclitem[] WHERE oid=$2", value, oid); err != nil {
						s.T.Error(err)
					}
				}
			}()
		}
		body(s, db)
	})
}
func fileListColumns(record bool) ([]string, []string) {
	if record {
		return []string{"name", "size", "modification"}, []string{"TEXT", "INT8", "TIMESTAMPTZ"}
	}
	return []string{"pg_ls_dir"}, []string{"TEXT"}
}
func fileListEmpty(s *fixture.RoleMembershipScenario, call string, record bool) {
	s.T.Helper()
	columns, types := fileListColumns(record)
	predefinedSelect(s, "SELECT * FROM "+call, columns, types, nil)
}
func fileListUnsupported(s *fixture.RoleMembershipScenario, q, name string) {
	s.T.Helper()
	predefinedFailure(s, q, fixture.Diagnostic{Code: "0A000", Message: name + " physical directory listing is unsupported in embedded mode"}, true, "execute")
}
func fileListMissing(s *fixture.RoleMembershipScenario, q, name string, missingOK bool) {
	s.T.Helper()
	if !s.Backend.PG {
		fileListUnsupported(s, q, "pg_ls_dir")
		return
	}
	if missingOK {
		predefinedSelect(s, q, []string{"pg_ls_dir"}, []string{"TEXT"}, nil)
		return
	}
	predefinedFailure(s, q, fixture.Diagnostic{Code: "58P01", Message: fmt.Sprintf("could not open directory \"%s\": No such file or directory", name)}, true, "execute")
}
func fileListDenied(s *fixture.RoleMembershipScenario, call, name string) {
	s.T.Helper()
	predefinedDenied(s, "SELECT * FROM "+call, "permission denied for function "+name, true)
}
func fileListLiteral(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }
func fileListStockRows(s *fixture.RoleMembershipScenario, name string) {
	s.T.Helper()
	q := "SELECT * FROM " + name + "()"
	if !s.Backend.PG {
		fileListUnsupported(s, q, name)
		return
	}
	// log_directory is fixture-owned; an absent configured directory is a
	// separately specified filesystem outcome, not an allowed arbitrary error.
	var log string
	if name == "pg_ls_logdir" {
		if err := s.Backend.DB.QueryRowContext(s.P.Ctx, "SHOW log_directory").Scan(&log); err != nil {
			s.T.Fatal(err)
		}
	}
	rows, err := s.C.QueryContext(s.P.Ctx, q)
	if err != nil {
		if name != "pg_ls_logdir" {
			s.T.Fatal(err)
		}
		fixture.AssertControlDiagnostic(s.T, err, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "58P01", Message: fmt.Sprintf("could not open directory %q: No such file or directory", log)})
		return
	}
	defer rows.Close()
	columns, err := rows.ColumnTypes()
	if err != nil || len(columns) != 3 {
		s.T.Fatal("record metadata", columns, err)
	}
	for i, want := range []string{"TEXT", "INT8", "TIMESTAMPTZ"} {
		if columns[i].DatabaseTypeName() != want {
			s.T.Fatal(columns[i].DatabaseTypeName(), want)
		}
	}
	for rows.Next() {
		var name string
		var size int64
		var modification any
		if err := rows.Scan(&name, &size, &modification); err != nil {
			s.T.Fatal(err)
		}
		if name == "" || strings.HasPrefix(name, ".") || size < 0 || modification == nil {
			s.T.Fatal("invalid regular file record", name, size, modification)
		}
	}
	if err := rows.Err(); err != nil {
		s.T.Fatal(err)
	}
}
