package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

const tempAsCoderCreate = `CREATE TEMP TABLE identified_module_files ON COMMIT DROP AS
SELECT DISTINCT f.id
FROM files f
         JOIN template_version_terraform_values tvtv
              ON tvtv.cached_module_files = f.id
WHERE f.created_by = '00000000-0000-0000-0000-000000000000'
  AND f.mimetype = 'application/x-tar'
  AND f.created_at >= '2026-08-31 08:00:00+00'
  AND f.created_at < '2026-08-31 22:00:00+00';`
const tempAsCoderUpdate = `UPDATE template_version_terraform_values
SET cached_module_files = NULL
WHERE cached_module_files IN (SELECT id FROM identified_module_files);`
const tempAsCoderDelete = `DELETE FROM files
    USING identified_module_files c
WHERE files.id = c.id;`

func tempAsUUID(i int) string { return fmt.Sprintf("00000000-0000-0000-0000-%012d", i) }
func tempAsCoderState(s *fixture.TempReferenceSession, c fixture.Executor, migrated bool) {
	s.T.Helper()
	start := 1
	if migrated {
		start = 3
	}
	files := [][]any{}
	for i := start; i <= 7; i++ {
		files = append(files, []any{tempAsUUID(i)})
	}
	tempAsRun(s, c, fixture.Select("SELECT id FROM files ORDER BY id", []string{"id"}, []string{"UUID"}, files))
	refs := [][]any{}
	for i, id := range []int{1, 1, 2, 3, 4, 5, 6} {
		var value any = tempAsUUID(id)
		if migrated && i < 3 {
			value = nil
		}
		refs = append(refs, []any{tempAsUUID(i + 1), value})
	}
	tempAsRun(s, c, fixture.Select("SELECT template_version_id,cached_module_files FROM template_version_terraform_values ORDER BY template_version_id", []string{"template_version_id", "cached_module_files"}, []string{"UUID", "UUID"}, refs))
}
func tempAsCoderTargets(s *fixture.TempReferenceSession, c fixture.Executor) {
	tempAsRun(s, c, fixture.Select("SELECT id FROM identified_module_files ORDER BY id", []string{"id"}, []string{"UUID"}, [][]any{{tempAsUUID(1)}, {tempAsUUID(2)}}))
}
func tempAsCoderMigration(s *fixture.TempReferenceSession) {
	tempAsExec(s, s.C, "CREATE TABLE files(id uuid PRIMARY KEY,created_at timestamptz NOT NULL,created_by uuid NOT NULL,mimetype text NOT NULL)", 0)
	tempAsExec(s, s.C, "CREATE TABLE template_version_terraform_values(template_version_id uuid PRIMARY KEY,cached_module_files uuid,CONSTRAINT template_version_terraform_values_cached_module_files_fkey FOREIGN KEY(cached_module_files) REFERENCES files(id))", 0)
	for i := 1; i <= 7; i++ {
		timestamp, creator, mime := "12:00:00", tempAsUUID(0), "application/x-tar"
		switch i {
		case 1:
			timestamp = "08:00:00"
		case 3:
			timestamp = "22:00:00"
		case 4:
			timestamp = "07:59:59"
		case 5:
			creator = "10000000-0000-0000-0000-000000000000"
		case 6:
			mime = "text/plain"
		}
		tempAsExec(s, s.C, fmt.Sprintf("INSERT INTO files VALUES('%s','2026-08-31 %s+00','%s','%s')", tempAsUUID(i), timestamp, creator, mime), 1)
	}
	for i, id := range []int{1, 1, 2, 3, 4, 5, 6} {
		tempAsExec(s, s.C, fmt.Sprintf("INSERT INTO template_version_terraform_values VALUES('%s','%s')", tempAsUUID(i+1), tempAsUUID(id)), 1)
	}
	tempAsCoderState(s, s.C, false)
	tempAsExec(s, s.C, "BEGIN", 0)
	d := fixture.ControlsReferenceDiagnostic("23503", `update or delete on table "files" violates foreign key constraint "template_version_terraform_values_cached_module_files_fkey" on table "template_version_terraform_values"`)
	d.Detail = `Key (id)=(00000000-0000-0000-0000-000000000001) is still referenced from table "template_version_terraform_values".`
	d.Schema = "public"
	d.Table = "template_version_terraform_values"
	d.Constraint = "template_version_terraform_values_cached_module_files_fkey"
	step := fixture.ErrorDiagnostic("DELETE FROM files WHERE id='"+tempAsUUID(1)+"'", d)
	step.ErrorStage = "execute"
	tempAsRun(s, s.C, step)
	tempAsExec(s, s.C, "ROLLBACK", 0)
	tempAsCoderState(s, s.C, false)
	// Rollback first leaves the exact original seed available for the commit proof.
	var held *sql.Stmt
	if s.Prepared {
		var err error
		held, err = s.C.PrepareContext(s.P.Ctx, tempAsCoderCreate)
		if err != nil {
			s.T.Fatal(err)
		}
		defer held.Close()
		tempAsAbsent(s, "identified_module_files")
	}
	for _, commit := range []bool{false, true} {
		tx, err := s.C.BeginTx(s.P.Ctx, nil)
		if err != nil {
			s.T.Fatal(err)
		}
		func() {
			defer tx.Rollback()
			if held != nil && !commit {
				bound := tx.StmtContext(s.P.Ctx, held)
				defer bound.Close()
				fixture.RunStep(s.T, s.P.Ctx, fixture.HeldStatement{Executor: tx, Statement: bound, SQL: tempAsCoderCreate}, s.Record, fixture.Exec(tempAsCoderCreate, 2))
			} else {
				tempAsExec(s, tx, tempAsCoderCreate, 2)
			}
			tempAsCoderTargets(s, tx)
			s.Missing(s.Peer, "identified_module_files", "id", "16")
			tempAsExec(s, tx, tempAsCoderUpdate, 3)
			tempAsCoderTargets(s, tx)
			tempAsExec(s, tx, tempAsCoderDelete, 2)
			tempAsCoderState(s, tx, true)
			if commit {
				err = tx.Commit()
			} else {
				err = tx.Rollback()
			}
			if err != nil {
				s.T.Fatal(err)
			}
		}()
		tempAsCoderState(s, s.C, commit)
		tempAsAbsent(s, "identified_module_files")
		s.Missing(s.C, "identified_module_files", "id", "16")
	}
}
func TestReferenceCreateTempTableAsCoderMigration(t *testing.T) {
	tempAsScenario(t, tempAsCoderMigration)
}
func TestReferenceCreateTempTableAsFeatureSmoke(t *testing.T) {
	tempAsScenario(t, func(s *fixture.TempReferenceSession) {
		tempAsReproduction(s)
		tempAsNoData(s)
		tempAsLifecycle(s)
		tempAsCoderMigration(s)
	})
}
