package reference_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

// Ordinary tablespaces are real cluster objects, never synthetic native catalog
// entries. Register every teardown before the corresponding creation attempt.
type lifecyclePhysical struct {
	s               *lifecycleCase
	name, directory string
	cleanup         []string
}

func lifecycleOrdinary(t *testing.T, s *lifecycleCase, suffix string) *lifecyclePhysical {
	t.Helper()
	if !s.b.PG {
		t.Fatal("ordinary physical setup is PostgreSQL-only")
	}
	f := &lifecyclePhysical{s: s, name: "ts_lifecycle_" + suffix, directory: fmt.Sprintf("/tmp/stuntgres_ts_lifecycle_%s_%d", suffix, time.Now().UnixNano())}
	// A deferred cleanup, not t.Cleanup, is installed by the caller: borrowers
	// must close and dependencies disappear before dropping cluster objects.
	return f
}

func (f *lifecyclePhysical) create(t *testing.T) {
	t.Helper()
	tablespaceContainer(t, f.s.p, "mkdir "+f.directory+" && chown postgres:postgres "+f.directory)
	f.s.exec(t, tablespaceSQL(f.name, "", f.directory))
}

func (f *lifecyclePhysical) object(t *testing.T, create, drop string) {
	t.Helper()
	f.cleanup = append(f.cleanup, drop)
	f.s.exec(t, create)
}

func (f *lifecyclePhysical) close(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "RESET default_tablespace", "RESET temp_tablespaces"} {
		if _, err := f.s.c.ExecContext(ctx, q); err != nil {
			t.Error("physical session cleanup", q, err)
		}
	}
	f.s.inTx = false
	for i := len(f.cleanup) - 1; i >= 0; i-- {
		if _, err := f.s.c.ExecContext(ctx, f.cleanup[i]); err != nil {
			t.Error("physical object cleanup", f.cleanup[i], err)
		}
	}
	if _, err := f.s.c.ExecContext(ctx, "DROP TABLESPACE IF EXISTS "+f.name); err != nil {
		t.Error("physical tablespace cleanup", err)
		return
	}
	// DROP owns PostgreSQL's version directory; only remove our now-empty parent.
	tablespaceContainer(t, &fixture.Pair{Ctx: ctx}, "if test -d "+f.directory+"; then rmdir "+f.directory+"; fi")
}

func lifecycleDependency(name, detail string) fixture.Diagnostic {
	d := tablespaceDiagnostic("2BP01", "tablespace \""+name+"\" cannot be dropped because some objects depend on it")
	d.Detail = detail
	return d
}

func (f *lifecyclePhysical) blocked(t *testing.T, d fixture.Diagnostic) {
	t.Helper()
	f.s.error(t, "DROP TABLESPACE "+f.name, d, false)
	f.s.count(t, "SELECT count(*) AS n FROM pg_tablespace WHERE spcname=$1", 1, f.name)
}

func lifecycleNativeBoundary(t *testing.T, s *lifecycleCase) {
	t.Helper()
	if s.b.PG {
		t.Fatal("native capability boundary used on PostgreSQL")
	}
	for range 2 {
		s.error(t, tablespaceSQL("ts_lifecycle_unavailable", "", "/tmp/ts_lifecycle_unavailable"), tablespaceDiagnostic("0A000", tablespacePhysicalMessage), false)
		s.count(t, "SELECT count(*) AS n FROM pg_tablespace WHERE spcname='ts_lifecycle_unavailable'", 0)
	}
	s.exec(t, "CREATE TABLE ts_lifecycle_boundary(a integer)")
	// Explicit index placement is an existing capability boundary, even for
	// the database-default OID. It must not be mistaken for a successful no-op.
	createIndex := "CREATE INDEX ts_lifecycle_boundary_placed ON ts_lifecycle_boundary(a) TABLESPACE pg_default"
	createDiagnostic := tablespaceDiagnostic("0A000", "index tablespaces are unsupported")
	createDiagnostic.Position = fixture.Position(strings.Index(createIndex, "TABLESPACE") + 1)
	s.error(t, createIndex, createDiagnostic, true)
	s.count(t, "SELECT count(*) AS n FROM pg_class WHERE relname='ts_lifecycle_boundary_placed'", 0)
	s.exec(t, "CREATE INDEX ts_lifecycle_boundary_idx ON ts_lifecycle_boundary(a)")
	for _, object := range []string{"TABLE ts_lifecycle_boundary"} {
		s.both(t, fixture.Exec("ALTER "+object+" SET TABLESPACE pg_default", 0))
		s.error(t, "ALTER "+object+" SET TABLESPACE ts_lifecycle_unavailable", lifecycleMissing("ts_lifecycle_unavailable"), false)
		s.error(t, "ALTER "+object+" SET TABLESPACE pg_global", tablespaceDiagnostic("22023", "only shared relations can be placed in pg_global tablespace"), false)
	}
	for _, destination := range []string{"pg_default", "ts_lifecycle_unavailable", "pg_global"} {
		q := "ALTER INDEX ts_lifecycle_boundary_idx SET TABLESPACE " + destination
		d := tablespaceDiagnostic("0A000", "index tablespaces are unsupported")
		d.Position = fixture.Position(strings.Index(q, "TABLESPACE") + 1)
		s.error(t, q, d, false)
	}
	s.both(t, fixture.Select("SELECT relname,reltablespace::bigint AS placement FROM pg_class WHERE relname IN ('ts_lifecycle_boundary','ts_lifecycle_boundary_idx') ORDER BY relname", []string{"relname", "placement"}, []string{"NAME", "INT8"}, [][]any{{"ts_lifecycle_boundary", int64(0)}, {"ts_lifecycle_boundary_idx", int64(0)}}))
	s.count(t, "SELECT count(*) AS n FROM pg_shdepend WHERE refclassid=1213 AND deptype='t'", 0)
	// There is no native ordinary destination to exercise a distinct physical
	// move or ordinary DROP. Pinned rejection is not a 2BP01/55000 witness.
}

func lifecyclePhysicalBlockers(t *testing.T, s *lifecycleCase, suffix string) {
	t.Helper()
	f := lifecycleOrdinary(t, s, suffix)
	defer f.close(t)
	f.create(t)
	partition := "ts_lifecycle_" + suffix + "_part"
	heap := "ts_lifecycle_" + suffix + "_heap"
	f.object(t, "CREATE TABLE "+partition+"(a integer) PARTITION BY RANGE(a) TABLESPACE "+f.name, "DROP TABLE IF EXISTS "+partition+" CASCADE")
	dependency := lifecycleDependency(f.name, "tablespace for table "+partition)
	f.blocked(t, dependency)
	f.object(t, "CREATE TABLE "+heap+"(a integer) TABLESPACE "+f.name, "DROP TABLE IF EXISTS "+heap+" CASCADE")
	f.blocked(t, dependency) // dependency wins over genuine files
	s.exec(t, "DROP TABLE "+partition)
	f.blocked(t, tablespaceDiagnostic("55000", "tablespace \""+f.name+"\" is not empty"))
	s.exec(t, "DROP TABLE "+heap)
	s.exec(t, "DROP TABLESPACE "+f.name)
	s.count(t, "SELECT count(*) AS n FROM pg_tablespace WHERE spcname=$1", 0, f.name)
}

func TestReferenceTablespaceLifecycleDependencies(t *testing.T) {
	tablespaceLifecycleScenario(t, nil, func(t *testing.T, s *lifecycleCase) {
		if !s.b.PG {
			t.Run("native-ordinary-unavailable", func(t *testing.T) { lifecycleNativeBoundary(t, s) })
			return
		}
		t.Run("pg-committed-rename-create-precedence", func(t *testing.T) {
			for _, prepared := range []bool{false, true} {
				t.Run(lifecycleRoute(prepared), func(t *testing.T) {
					f := lifecycleOrdinary(t, s, "rename_create_"+lifecycleRoute(prepared))
					defer f.close(t)
					f.create(t)
					var oid int64
					if err := s.c.QueryRowContext(s.p.Ctx, "SELECT oid FROM pg_tablespace WHERE spcname=$1", f.name).Scan(&oid); err != nil || oid <= 0 || oid == 1663 || oid == 1664 {
						t.Fatal("ordinary rename identity", oid, err)
					}
					renamed := f.name + "_renamed"
					f.cleanup = append(f.cleanup, "DROP TABLESPACE IF EXISTS "+renamed)
					step := fixture.Exec("ALTER TABLESPACE "+f.name+" RENAME TO "+renamed, 0)
					step.Prepared = prepared
					s.step(t, step)
					// PG can safely commit this disposable ordinary rename,
					// unlike a builtin rename. CREATE now reaches real validation.
					s.error(t, tablespaceSQL(renamed, "", tablespaceAbsent), tablespaceDiagnostic("42710", "tablespace \""+renamed+"\" already exists"), false)
					s.error(t, tablespaceSQL(renamed, "", "relative"), tablespaceDiagnostic("42P17", "tablespace location must be an absolute path"), false)
					s.count(t, "SELECT count(*) AS n FROM pg_tablespace WHERE spcname=$1", 0, f.name)
					s.both(t, fixture.Select("SELECT oid=$1::oid AS same_identity FROM pg_tablespace WHERE spcname=$2", []string{"same_identity"}, []string{"BOOL"}, [][]any{{true}}, oid, renamed))
				})
			}
		})
		t.Run("pg-dependency-before-physical-nonempty", func(t *testing.T) { lifecyclePhysicalBlockers(t, s, "dep") })
		t.Run("pg-partitioned-index-dependency", func(t *testing.T) {
			f := lifecycleOrdinary(t, s, "partidx")
			defer f.close(t)
			f.create(t)
			f.object(t, "CREATE TABLE ts_lifecycle_index_parent(a integer) PARTITION BY RANGE(a)", "DROP TABLE IF EXISTS ts_lifecycle_index_parent CASCADE")
			f.object(t, "CREATE INDEX ts_lifecycle_partitioned_idx ON ts_lifecycle_index_parent(a) TABLESPACE "+f.name, "DROP INDEX IF EXISTS ts_lifecycle_partitioned_idx")
			f.blocked(t, lifecycleDependency(f.name, "tablespace for index ts_lifecycle_partitioned_idx"))
			s.exec(t, "DROP INDEX ts_lifecycle_partitioned_idx")
			s.exec(t, "DROP TABLESPACE "+f.name)
		})
		t.Run("pg-index-file-only", func(t *testing.T) {
			f := lifecycleOrdinary(t, s, "idxfiles")
			defer f.close(t)
			f.create(t)
			f.object(t, "CREATE TABLE ts_lifecycle_index_heap(a integer)", "DROP TABLE IF EXISTS ts_lifecycle_index_heap CASCADE")
			f.object(t, "CREATE INDEX ts_lifecycle_file_idx ON ts_lifecycle_index_heap(a) TABLESPACE "+f.name, "DROP INDEX IF EXISTS ts_lifecycle_file_idx")
			f.blocked(t, tablespaceDiagnostic("55000", "tablespace \""+f.name+"\" is not empty"))
			s.exec(t, "DROP INDEX ts_lifecycle_file_idx")
			s.exec(t, "DROP TABLESPACE "+f.name)
		})
		t.Run("pg-database-default-physical-occupancy", func(t *testing.T) {
			f := lifecycleOrdinary(t, s, "dbfiles")
			defer f.close(t)
			f.create(t)
			f.object(t, "CREATE DATABASE ts_lifecycle_physical_db TEMPLATE template0 TABLESPACE "+f.name, "DROP DATABASE IF EXISTS ts_lifecycle_physical_db")
			f.blocked(t, tablespaceDiagnostic("55000", "tablespace \""+f.name+"\" is not empty"))
			s.exec(t, "DROP DATABASE ts_lifecycle_physical_db")
			s.exec(t, "DROP TABLESPACE "+f.name)
		})
		t.Run("pg-remote-dependency-count", func(t *testing.T) {
			f := lifecycleOrdinary(t, s, "remote")
			defer f.close(t)
			f.create(t)
			f.object(t, "CREATE DATABASE ts_lifecycle_remote_db TEMPLATE template0", "DROP DATABASE IF EXISTS ts_lifecycle_remote_db")
			remote, raw, rec := fixture.DatabaseScenarioPool(t, s.p, true, "ts_lifecycle_remote_db", "")
			defer raw.Close()
			defer remote.Close()
			fixture.RunStep(t, s.p.Ctx, remote, rec, fixture.Exec("CREATE TABLE ts_lifecycle_remote_one(a integer) PARTITION BY RANGE(a) TABLESPACE "+f.name, 0))
			f.blocked(t, lifecycleDependency(f.name, "1 object in database ts_lifecycle_remote_db"))
			fixture.RunStep(t, s.p.Ctx, remote, rec, fixture.Exec("CREATE TABLE ts_lifecycle_remote_two(a integer) PARTITION BY RANGE(a) TABLESPACE "+f.name, 0))
			f.blocked(t, lifecycleDependency(f.name, "2 objects in database ts_lifecycle_remote_db"))
			fixture.RunStep(t, s.p.Ctx, remote, rec, fixture.Exec("DROP TABLE ts_lifecycle_remote_one,ts_lifecycle_remote_two", 0))
			// Close all database borrowers before DROP DATABASE or tablespace.
			remote.Close()
			raw.Close()
			s.exec(t, "DROP DATABASE ts_lifecycle_remote_db")
			s.exec(t, "DROP TABLESPACE "+f.name)
		})
		t.Run("pg-settings-are-not-dependencies", func(t *testing.T) {
			for _, prepared := range []bool{false, true} {
				t.Run(lifecycleRoute(prepared), func(t *testing.T) {
					f := lifecycleOrdinary(t, s, "settings_"+lifecycleRoute(prepared))
					defer f.close(t)
					f.create(t)
					s.exec(t, "SET default_tablespace='"+f.name+"'")
					s.exec(t, "SET temp_tablespaces='"+f.name+"'")
					step := fixture.Exec("DROP TABLESPACE "+f.name, 0)
					step.Prepared = prepared
					s.step(t, step)
					s.count(t, "SELECT count(*) AS n FROM pg_tablespace WHERE spcname=$1", 0, f.name)
				})
			}
		})
		t.Run("pg-ordinary-temp-oid-cache-and-stale-next-transaction", func(t *testing.T) {
			f := lifecycleOrdinary(t, s, "tempcache")
			defer f.close(t)
			f.create(t)
			var oid int64
			if err := s.c.QueryRowContext(s.p.Ctx, "SELECT oid FROM pg_tablespace WHERE spcname=$1", f.name).Scan(&oid); err != nil || oid <= 0 || oid == 1663 || oid == 1664 {
				t.Fatal("ordinary temp identity", oid, err)
			}
			renamed := f.name + "_renamed"
			f.cleanup = append(f.cleanup, "DROP TABLESPACE IF EXISTS "+renamed)
			s.exec(t, "SET temp_tablespaces='"+f.name+"'")
			s.begin(t)
			f.object(t, "CREATE TEMP TABLE ts_lifecycle_cached_first(a integer)", "DROP TABLE IF EXISTS ts_lifecycle_cached_first")
			s.exec(t, "ALTER TABLESPACE "+f.name+" RENAME TO "+renamed)
			f.object(t, "CREATE TEMP TABLE ts_lifecycle_cached_second(a integer)", "DROP TABLE IF EXISTS ts_lifecycle_cached_second")
			s.both(t, fixture.Select("SELECT relname,reltablespace=$1::oid AS cached FROM pg_class WHERE relname IN ('ts_lifecycle_cached_first','ts_lifecycle_cached_second') ORDER BY relname", []string{"relname", "cached"}, []string{"NAME", "BOOL"}, [][]any{{"ts_lifecycle_cached_first", true}, {"ts_lifecycle_cached_second", true}}, oid))
			s.exec(t, "COMMIT")
			s.inTx = false
			// Only an ordinary, disposable tablespace is committed on PG.
			// Builtin mutation witnesses always end with ROLLBACK.
			f.object(t, "CREATE TEMP TABLE ts_lifecycle_cached_next(a integer)", "DROP TABLE IF EXISTS ts_lifecycle_cached_next")
			s.both(t, fixture.Select("SELECT reltablespace::bigint AS placement FROM pg_class WHERE relname='ts_lifecycle_cached_next'", []string{"placement"}, []string{"INT8"}, [][]any{{int64(0)}}))
			s.both(t, fixture.Select("SELECT current_setting('temp_tablespaces') AS setting", []string{"setting"}, []string{"TEXT"}, [][]any{{f.name}}))
			for _, table := range []string{"ts_lifecycle_cached_first", "ts_lifecycle_cached_second", "ts_lifecycle_cached_next"} {
				s.exec(t, "DROP TABLE "+table)
			}
			s.exec(t, "RESET temp_tablespaces")
			s.step(t, fixture.Prepared(fixture.Exec("DROP TABLESPACE "+renamed, 0)))
		})
		t.Run("pg-real-distinct-relocation", func(t *testing.T) {
			f := lifecycleOrdinary(t, s, "move")
			defer f.close(t)
			f.create(t)
			var destination int64
			if err := s.c.QueryRowContext(s.p.Ctx, "SELECT oid FROM pg_tablespace WHERE spcname=$1", f.name).Scan(&destination); err != nil || destination <= 0 || destination == 1663 || destination == 1664 {
				t.Fatal("ordinary identity", destination, err)
			}
			f.object(t, "CREATE TABLE ts_lifecycle_move_heap(a integer)", "DROP TABLE IF EXISTS ts_lifecycle_move_heap CASCADE")
			f.object(t, "CREATE INDEX ts_lifecycle_move_idx ON ts_lifecycle_move_heap(a)", "DROP INDEX IF EXISTS ts_lifecycle_move_idx")
			for _, prepared := range []bool{false, true} {
				for _, object := range []string{"TABLE ts_lifecycle_move_heap", "INDEX ts_lifecycle_move_idx"} {
					step := fixture.Exec("ALTER "+object+" SET TABLESPACE "+f.name, 0)
					step.Prepared = prepared
					s.step(t, step)
				}
				s.both(t, fixture.Select("SELECT relname,reltablespace=$1::oid AS moved FROM pg_class WHERE relname IN ('ts_lifecycle_move_heap','ts_lifecycle_move_idx') ORDER BY relname", []string{"relname", "moved"}, []string{"NAME", "BOOL"}, [][]any{{"ts_lifecycle_move_heap", true}, {"ts_lifecycle_move_idx", true}}, destination))
				// Reset to the actual default before repeating with a held
				// driver statement: the next route is another distinct move.
				s.exec(t, "ALTER TABLE ts_lifecycle_move_heap SET TABLESPACE pg_default")
				s.exec(t, "ALTER INDEX ts_lifecycle_move_idx SET TABLESPACE pg_default")
			}
			s.exec(t, "DROP INDEX ts_lifecycle_move_idx")
			s.exec(t, "DROP TABLE ts_lifecycle_move_heap")
			s.exec(t, "DROP TABLESPACE "+f.name)
		})
	})
}
