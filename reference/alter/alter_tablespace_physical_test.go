package reference_test

import (
	"database/sql"
	"io"
	"stuntgres/reference/fixture"
	"testing"
)

// Only real PostgreSQL has distinct physical spaces. Native independently proves
// its builtin identities and explicit missing-target boundary, not movement.
func atsPhysical(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) bool {
	t.Helper()
	if !b.PG {
		atsRun(t, p, b, c, fixture.Select("SELECT spcname FROM pg_tablespace ORDER BY oid", []string{"spcname"}, []string{"NAME"}, [][]any{{"pg_default"}, {"pg_global"}}))
		atsError(t, p, b, c, "ALTER TABLE ALL IN TABLESPACE ats_src SET TABLESPACE ats_dst", atsDiagnostic("42704", `tablespace "ats_src" does not exist`))
		return false
	}
	script := "mkdir /tmp/ats_src /tmp/ats_dst && chown postgres:postgres /tmp/ats_src /tmp/ats_dst"
	code, out, err := fixture.Container.Exec(p.Ctx, []string{"sh", "-c", script})
	if err != nil {
		t.Fatal(err)
	}
	text, err := io.ReadAll(out)
	if err != nil || code != 0 {
		t.Fatal(code, string(text), err)
	}
	// Registered before spaces are created so partial fixture failure still cleans.
	deferOnCleanup := func() {
		for _, q := range []string{"ROLLBACK", "RESET ROLE", "DROP TABLE IF EXISTS ats_physical_temp", "DROP TABLE IF EXISTS information_schema.ats_info CASCADE", "DROP TABLE IF EXISTS ats_selected,ats_excluded,ats_parent,ats_existing_child,ats_blocked,ats_second CASCADE", "DROP MATERIALIZED VIEW IF EXISTS ats_physical_mv", "DROP TABLESPACE IF EXISTS ats_src", "DROP TABLESPACE IF EXISTS ats_dst"} {
			if _, err := c.ExecContext(p.Ctx, q); err != nil {
				t.Error(q, err)
			}
		}
		code, out, err := fixture.Container.Exec(p.Ctx, []string{"sh", "-c", "rmdir /tmp/ats_src /tmp/ats_dst"})
		if err != nil {
			t.Error(err)
			return
		}
		text, err := io.ReadAll(out)
		if err != nil || code != 0 {
			t.Error(code, string(text), err)
		}
	}
	// Child cleanup runs before scenario returns its physical connection.
	t.Cleanup(deferOnCleanup)
	atsExec(t, p, b, c, "CREATE TABLESPACE ats_src LOCATION '/tmp/ats_src'")
	atsExec(t, p, b, c, "CREATE TABLESPACE ats_dst LOCATION '/tmp/ats_dst'")
	return true
}

func TestReferenceAlterTablespacePhysicalSelection(t *testing.T) {
	alterTablespaceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		if !atsPhysical(t, p, b, c) {
			return
		}
		atsRoles(t, p, b, c)
		atsExec(t, p, b, c, "GRANT CREATE ON TABLESPACE ats_src,ats_dst TO ats_owner,ats_other")
		atsExec(t, p, b, c, "SET ROLE ats_owner")
		atsExec(t, p, b, c, "CREATE TABLE ats_selected(id INT) TABLESPACE ats_src")
		atsRun(t, p, b, c, fixture.Exec("INSERT INTO ats_selected VALUES(7)", 1))
		atsExec(t, p, b, c, "CREATE INDEX ats_physical_idx ON ats_selected(id) TABLESPACE ats_src")
		atsExec(t, p, b, c, "CREATE TABLE ats_parent(id INT) PARTITION BY RANGE(id) TABLESPACE ats_src")
		atsExec(t, p, b, c, "CREATE TABLE ats_existing_child PARTITION OF ats_parent FOR VALUES FROM(0) TO(10) TABLESPACE pg_default")
		atsRun(t, p, b, c, fixture.Exec("INSERT INTO ats_existing_child VALUES(3)", 1))
		atsRun(t, p, b, c, fixture.Exec("CREATE MATERIALIZED VIEW ats_physical_mv TABLESPACE ats_src AS SELECT id FROM ats_selected", 1))
		atsExec(t, p, b, c, "CREATE TEMP TABLE ats_physical_temp(id INT) TABLESPACE ats_src")
		atsExec(t, p, b, c, "RESET ROLE")
		atsExec(t, p, b, c, "SET ROLE ats_other")
		atsExec(t, p, b, c, "CREATE TABLE ats_excluded(id INT) TABLESPACE ats_src")
		atsRun(t, p, b, c, fixture.Exec("INSERT INTO ats_excluded VALUES(8)", 1))
		atsExec(t, p, b, c, "RESET ROLE")
		atsExec(t, p, b, c, "CREATE TABLE information_schema.ats_info(id INT) TABLESPACE ats_src")
		var src, dst int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_tablespace WHERE spcname='ats_src'").Scan(&src); err != nil {
			t.Fatal(err)
		}
		if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_tablespace WHERE spcname='ats_dst'").Scan(&dst); err != nil {
			t.Fatal(err)
		}
		atsExec(t, p, b, c, "SET ROLE ats_member")
		// Membership confers ALTER authority, but OWNED BY remains exact owner OID.
		atsExec(t, p, b, c, "ALTER TABLE ALL IN TABLESPACE ats_src OWNED BY ats_member SET TABLESPACE ats_dst")
		atsPlacement(t, p, b, c, "ats_selected", src)
		atsError(t, p, b, c, "ALTER TABLE ALL IN TABLESPACE ats_src OWNED BY ats_other SET TABLESPACE ats_dst", atsDiagnostic("42501", "must be owner of table ats_excluded"))
		atsExec(t, p, b, c, "ALTER TABLE ALL IN TABLESPACE ats_src OWNED BY ats_owner,ats_owner SET TABLESPACE ats_dst")
		atsExec(t, p, b, c, "RESET ROLE")
		for _, tc := range []struct {
			name  string
			space int64
		}{{"ats_selected", dst}, {"ats_parent", dst}, {"ats_existing_child", 0}, {"ats_excluded", src}, {"ats_physical_idx", src}, {"ats_physical_mv", src}, {"ats_physical_temp", src}, {"information_schema.ats_info", src}} {
			atsPlacement(t, p, b, c, tc.name, tc.space)
		}
		atsRun(t, p, b, c, fixture.Select("SELECT relname FROM pg_class WHERE relnamespace='public'::regnamespace AND reltablespace=(SELECT oid FROM pg_tablespace WHERE spcname='ats_dst') ORDER BY relname", []string{"relname"}, []string{"NAME"}, [][]any{{"ats_parent"}, {"ats_selected"}}))
		atsRun(t, p, b, c, fixture.Select("SELECT id FROM ats_selected", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		atsExec(t, p, b, c, "CREATE TABLE ats_future_child PARTITION OF ats_parent FOR VALUES FROM(10) TO(20)")
		atsPlacement(t, p, b, c, "ats_future_child", dst)
		atsRun(t, p, b, c, fixture.Exec("INSERT INTO ats_future_child VALUES(12)", 1))
		for _, phase := range []string{"BEGIN", "ROLLBACK"} {
			atsExec(t, p, b, c, phase)
			space := dst
			if phase == "BEGIN" {
				atsExec(t, p, b, c, "ALTER TABLE ALL IN TABLESPACE ats_dst OWNED BY ats_owner SET TABLESPACE ats_src")
				space = src
			}
			// The future child is session-owned, so OWNED BY excludes it.
			for _, name := range []string{"ats_selected", "ats_parent"} {
				atsPlacement(t, p, b, c, name, space)
			}
			atsPlacement(t, p, b, c, "ats_future_child", dst)
			for _, tc := range []struct {
				name  string
				space int64
			}{{"ats_existing_child", 0}, {"ats_excluded", src}, {"ats_physical_idx", src}, {"ats_physical_mv", src}, {"ats_physical_temp", src}, {"information_schema.ats_info", src}} {
				atsPlacement(t, p, b, c, tc.name, tc.space)
			}
			atsRun(t, p, b, c, fixture.Select("SELECT id FROM ats_selected", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
			atsRun(t, p, b, c, fixture.Select("SELECT id FROM ats_parent ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(12)}}))
			atsRun(t, p, b, c, fixture.Select("SELECT id FROM ats_excluded", []string{"id"}, []string{"INT4"}, [][]any{{int64(8)}}))
		}
		atsExec(t, p, b, c, "ALTER TABLE ALL IN TABLESPACE ats_src SET TABLESPACE ats_dst")
		atsPlacement(t, p, b, c, "information_schema.ats_info", dst)
		atsPlacement(t, p, b, c, "ats_physical_idx", src)
		atsPlacement(t, p, b, c, "ats_physical_mv", src)
		atsPlacement(t, p, b, c, "ats_physical_temp", src)
		atsExec(t, p, b, c, "SET ROLE ats_reader")
		atsError(t, p, b, c, "ALTER TABLE ALL IN TABLESPACE ats_src OWNED BY ats_reader SET TABLESPACE ats_dst", atsDiagnostic("42501", "permission denied for tablespace ats_dst"))
		atsExec(t, p, b, c, "RESET ROLE")
		var notices []fixture.Diagnostic
		_, nc, nr := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		for _, source := range []string{"ats_src", "pg_default"} {
			notices = nil
			fixture.RunStep(t, p.Ctx, nc, nr, fixture.Exec("ALTER TABLE ALL IN TABLESPACE "+source+" OWNED BY ats_reader SET TABLESPACE ats_dst", 0))
			name := source
			if source == "pg_default" {
				name = "(database default)"
			}
			want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "P0002", Message: "no matching relations in tablespace \"" + name + "\" found"}
			if len(notices) != 1 || notices[0] != want {
				t.Fatalf("empty notice %+v want %+v", notices, want)
			}
		}
		// Explicitly destroy session TEMP before dropping its physical tablespace.
		atsExec(t, p, b, c, "DROP TABLE ats_physical_temp")
	})
}

func TestReferenceAlterTablespacePhysicalHeldRoles(t *testing.T) {
	alterTablespaceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		if !atsPhysical(t, p, b, c) {
			return
		}
		atsRoles(t, p, b, c)
		atsExec(t, p, b, c, "GRANT CREATE ON TABLESPACE ats_src,ats_dst TO ats_owner,ats_other")
		atsExec(t, p, b, c, "SET ROLE ats_owner")
		atsExec(t, p, b, c, "CREATE TABLE ats_selected(id INT) TABLESPACE ats_src")
		atsRun(t, p, b, c, fixture.Exec("INSERT INTO ats_selected VALUES(7)", 1))
		atsExec(t, p, b, c, "RESET ROLE")
		atsExec(t, p, b, c, "SET ROLE ats_other")
		atsExec(t, p, b, c, "CREATE TABLE ats_excluded(id INT) TABLESPACE ats_src")
		atsRun(t, p, b, c, fixture.Exec("INSERT INTO ats_excluded VALUES(8)", 1))
		atsExec(t, p, b, c, "RESET ROLE")
		atsExec(t, p, b, c, "CREATE TABLE ats_second(id INT) TABLESPACE ats_src")
		atsRun(t, p, b, c, fixture.Exec("INSERT INTO ats_second VALUES(9)", 1))
		placementBag := func(selected [][]any) {
			atsRun(t, p, b, c, fixture.Select("SELECT c.relname,s.spcname FROM pg_class c JOIN pg_tablespace s ON s.oid=c.reltablespace WHERE c.relnamespace='public'::regnamespace AND c.relname IN ('ats_selected','ats_excluded','ats_second') ORDER BY c.relname", []string{"relname", "spcname"}, []string{"NAME", "NAME"}, selected))
			atsRun(t, p, b, c, fixture.Select("SELECT 'ats_selected'::text AS name,id FROM ats_selected UNION ALL SELECT 'ats_excluded'::text,id FROM ats_excluded UNION ALL SELECT 'ats_second'::text,id FROM ats_second ORDER BY name", []string{"name", "id"}, []string{"TEXT", "INT4"}, [][]any{{"ats_excluded", int64(8)}, {"ats_second", int64(9)}, {"ats_selected", int64(7)}}))
		}
		original := [][]any{{"ats_excluded", "ats_src"}, {"ats_second", "ats_src"}, {"ats_selected", "ats_src"}}
		for _, identity := range []string{"CURRENT_USER", "CURRENT_ROLE", "SESSION_USER"} {
			placementBag(original)
			atsExec(t, p, b, c, "SET ROLE ats_owner")
			q := "ALTER TABLE ALL IN TABLESPACE ats_src OWNED BY " + identity + " SET TABLESPACE ats_dst"
			held, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			executor := fixture.HeldStatement{Executor: c, Statement: held, SQL: q}
			atsExec(t, p, b, c, "RESET ROLE")
			atsExec(t, p, b, c, "SET ROLE ats_other")
			if identity == "SESSION_USER" {
				// Session identity selects the administrative owner's table, not
				// either the prepare-time owner or the current role's table.
				atsRun(t, p, b, executor, fixture.ErrorDiagnostic(q, atsDiagnostic("42501", "must be owner of table ats_second")))
			} else {
				atsRun(t, p, b, executor, fixture.Exec(q, 0))
			}
			atsExec(t, p, b, c, "RESET ROLE")
			if identity == "SESSION_USER" {
				placementBag(original)
				atsRun(t, p, b, executor, fixture.Exec(q, 0))
				placementBag([][]any{{"ats_excluded", "ats_src"}, {"ats_second", "ats_dst"}, {"ats_selected", "ats_src"}})
			} else {
				placementBag([][]any{{"ats_excluded", "ats_dst"}, {"ats_second", "ats_src"}, {"ats_selected", "ats_src"}})
			}
			atsExec(t, p, b, c, "ALTER TABLE ALL IN TABLESPACE ats_dst SET TABLESPACE ats_src")
			placementBag(original)
		}
	})
}
