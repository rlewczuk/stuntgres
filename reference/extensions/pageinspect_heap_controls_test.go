package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func heapViews(pg bool) []fixture.Step {
	return []fixture.Step{
		fixture.Select("SELECT * FROM heap_view ORDER BY lp", heapNames, heapTypes, [][]any{heapRow(pg)}),
		fixture.Select("SELECT * FROM heap_flags_view", heapFlagsNames, heapFlagsTypes, [][]any{{"{HEAP_HASNULL,HEAP_HASOID_OLD,HEAP_XMIN_COMMITTED,HEAP_XMIN_INVALID,HEAP_ONLY_TUPLE}", "{HEAP_XMIN_FROZEN}"}}),
	}
}

func heapControls(t *testing.T, smoke bool) {
	fixture.RoleReferenceCleanup(t, []string{"heap_owner", "heap_reader"})
	heapCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{"CREATE ROLE heap_owner SUPERUSER", "CREATE SCHEMA heap_moved", "CREATE TABLE heap_images(id integer,raw bytea,a integer,z integer)"} {
			run(fixture.Exec(q, 0))
		}
		input := heapImage()
		run(fixture.Exec("INSERT INTO heap_images VALUES(1,$1,777,32777)", 1, input))
		clear(input)
		for _, q := range []string{
			"CREATE VIEW heap_view AS SELECT h.* FROM heap_images CROSS JOIN LATERAL heap_part.heap_page_items(raw) AS h",
			"CREATE VIEW heap_flags_view AS SELECT (heap_part.heap_tuple_infomask_flags(a,z)).* FROM heap_images",
		} {
			run(fixture.Exec(q, 0))
		}
		views := heapViews(b.PG)
		held := make([]fixture.HeldStatement, len(views))
		for i, s := range views {
			st, err := c.PrepareContext(p.Ctx, s.Query)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			held[i] = fixture.HeldStatement{Executor: c, Statement: st, SQL: s.Query}
		}
		checkHeld := func(heapRows [][]any, flags []any) {
			h, f := views[0], views[1]
			h.Rows, f.Rows = heapRows, [][]any{flags}
			for i, s := range []fixture.Step{h, f} {
				heapBoth(t, p, b, c, heapMetadata(s, b.PG, true))
				fixture.RunStep(t, p.Ctx, held[i], b.Recorder, heapMetadata(s, b.PG, true))
			}
		}
		full := func() { checkHeld([][]any{heapRow(b.PG)}, heapViews(b.PG)[1].Rows[0]) }
		changes := func() {
			run(fixture.Exec("UPDATE heap_images SET raw=$1,a=0,z=0", 1, make([]byte, 24)))
			checkHeld(nil, []any{"{}", "{}"})
			run(fixture.Exec("UPDATE heap_images SET raw=NULL,a=NULL,z=NULL", 1))
			checkHeld(nil, []any{nil, nil})
			// A valid tuple with no data is different from no source row.
			empty, row := heapImage()[:64], heapRow(b.PG)
			heapPointer(empty, 0, 32, 1, 32)
			row[3], row[13] = int64(32), ""
			run(fixture.Exec("UPDATE heap_images SET raw=$1,a=1,z=0", 1, empty))
			checkHeld([][]any{row}, []any{"{HEAP_HASNULL}", "{}"})
			run(fixture.Exec("UPDATE heap_images SET raw=$1,a=777,z=32777", 1, heapImage()))
			full()
		}
		full()
		changes()
		if smoke {
			heapBoth(t, p, b, c, heapMetadata(heapStep(heapSource, heapImage(), heapRow(b.PG)), b.PG, false))
			heapBoth(t, p, b, c, heapMetadata(heapFlagsStep(heapFlagsSource, int64(777), int64(32777), heapViews(b.PG)[1].Rows[0][0], "{HEAP_XMIN_FROZEN}"), b.PG, false))
			heapBoth(t, p, b, c, heapStep(heapSource, nil))
			heapBoth(t, p, b, c, heapFlagsStep(heapFlagsSource, nil, int64(0), nil, nil))
			heapBoth(t, p, b, c, heapError(heapSource, "22023", "input page too small (3 bytes)", make([]byte, 3)))
			heapAuthority(t, p, b, c)
		} else {
			run(fixture.Exec("CREATE ROLE heap_reader", 0))
		}
		for _, q := range []string{"GRANT USAGE ON SCHEMA public,heap_part,heap_moved TO heap_reader", "GRANT SELECT ON heap_view,heap_flags_view TO heap_reader"} {
			run(fixture.Exec(q, 0))
		}
		ids := make([]int64, 2)
		for i, tc := range []struct{ name, args, moved string }{{"heap_page_items", "bytea", "decoded"}, {"heap_tuple_infomask_flags", "integer,integer", "decoded_flags"}} {
			if err := c.QueryRowContext(p.Ctx, "SELECT 'heap_part."+tc.name+"("+tc.args+")'::regprocedure::bigint").Scan(&ids[i]); err != nil || ids[i] <= 0 {
				t.Fatal("provider identity", ids[i], err)
			}
			for _, q := range []string{"ALTER FUNCTION heap_part." + tc.name + "(" + tc.args + ") OWNER TO heap_owner", "ALTER FUNCTION heap_part." + tc.name + "(" + tc.args + ") RENAME TO " + tc.moved, "ALTER FUNCTION heap_part." + tc.moved + "(" + tc.args + ") SET SCHEMA heap_moved"} {
				run(fixture.Exec(q, 0))
			}
			heapBoth(t, p, b, c, fixture.Select("SELECT 'heap_moved."+tc.moved+"("+tc.args+")'::regprocedure::bigint AS id", []string{"id"}, []string{"INT8"}, [][]any{{ids[i]}}))
		}
		if ids[0] == ids[1] {
			t.Fatal("distinct routine identities collapsed", ids)
		}
		full()
		// Lawful detach and foreign reassignment preserve released identity and
		// executable stored references, not just lookup names. Reattach before Dump.
		run(fixture.Exec("CREATE EXTENSION \"uuid-ossp\"", 0))
		for i, signature := range []string{"heap_moved.decoded(bytea)", "heap_moved.decoded_flags(integer,integer)"} {
			run(fixture.Exec("ALTER EXTENSION pageinspect DROP FUNCTION "+signature, 0))
			full()
			memberCount := func(want int64, extension string) {
				heapBoth(t, p, b, c, fixture.Select(fmt.Sprintf("SELECT count(*) FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid WHERE d.classid=1255 AND d.objid=%d AND d.refclassid=3079 AND d.deptype='e' AND e.extname='%s'", ids[i], extension), []string{"count"}, []string{"INT8"}, [][]any{{want}}))
			}
			memberCount(0, "pageinspect")
			run(fixture.Exec("ALTER EXTENSION \"uuid-ossp\" ADD FUNCTION "+signature, 0))
			memberCount(1, "uuid-ossp")
			full()
			run(fixture.Exec("ALTER EXTENSION \"uuid-ossp\" DROP FUNCTION "+signature, 0))
			run(fixture.Exec("ALTER EXTENSION pageinspect ADD FUNCTION "+signature, 0))
			memberCount(1, "pageinspect")
		}
		run(fixture.Exec("DROP EXTENSION \"uuid-ossp\"", 0))
		check := func(conn *sql.Conn, recorder *fixture.Recorder, pg bool) {
			execute := func(s fixture.Step) {
				fixture.RunStep(t, p.Ctx, conn, recorder, s)
				fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Prepared(s))
			}
			execute(heapDeclaration(false, "decoded", "heap_moved", "heap_owner"))
			execute(heapDeclaration(true, "decoded_flags", "heap_moved", "heap_owner"))
			execute(heapIdentities("heap_moved", "decoded", "decoded_flags"))
			for _, s := range heapViews(pg) {
				execute(heapMetadata(s, pg, true))
				s.Query += " WHERE false"
				// heap_view already has ORDER BY; insert predicate before it.
				s.Query = strings.ReplaceAll(s.Query, " ORDER BY lp WHERE false", " WHERE false ORDER BY lp")
				s.Rows = nil
				execute(heapMetadata(s, pg, true))
			}
			execute(fixture.Select("SELECT raw,a,z FROM heap_images ORDER BY id", []string{"raw", "a", "z"}, []string{"BYTEA", "INT4", "INT4"}, [][]any{{string(heapImage()), int64(777), int64(32777)}}))
			execute(heapMetadata(heapStep("SELECT * FROM heap_moved.decoded(page=>$1)", heapImage(), heapRow(pg)), pg, false))
			execute(heapMetadata(heapFlagsStep("SELECT * FROM heap_moved.decoded_flags(t_infomask2=>$2,t_infomask=>$1)", int64(777), int64(32777), heapViews(pg)[1].Rows[0][0], "{HEAP_XMIN_FROZEN}"), pg, false))
			principal := "postgres"
			if pg {
				principal = "stuntgres"
			}
			execute(fixture.Select("SELECT n.nspname::text AS target,r.rolname::text AS owner FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace JOIN pg_roles r ON r.oid=e.extowner WHERE e.extname='pageinspect'", []string{"target", "owner"}, []string{"TEXT", "TEXT"}, [][]any{{"heap_part", principal}}))
		}
		checkOriginal := func() { check(c, b.Recorder, b.PG); full() }
		checkOriginal()
		changes()
		run(fixture.Exec("SET SESSION AUTHORIZATION heap_reader", 0))
		for i, s := range views {
			denied := heapError(s.Query, "42501", "must be superuser to use raw page functions")
			heapBoth(t, p, b, c, denied)
			fixture.RunStep(t, p.Ctx, held[i], b.Recorder, denied)
		}
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("UPDATE heap_images SET raw=NULL,a=NULL,z=NULL", 1))
		run(fixture.Exec("SET SESSION AUTHORIZATION heap_reader", 0))
		checkHeld(nil, []any{nil, nil})
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("UPDATE heap_images SET raw=$1,a=777,z=32777", 1, heapImage()))
		checkOriginal()
		if b.PG {
			return
		}
		dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
		other, destination, recorder := pgcryptoControlDestination(t, p, "heap_restore")
		for _, q := range []string{"CREATE ROLE heap_owner SUPERUSER", "CREATE ROLE heap_reader"} {
			if _, err := other.ExecContext(p.Ctx, q); err != nil {
				t.Fatal(err)
			}
		}
		if err := stuntgres.Restore(p.Ctx, other, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		check(destination, recorder, false)
		if fixture.ExtensionDump(t, p.Ctx, other) != dump {
			t.Fatal("symbolic redump changed")
		}
		// Replay consumers must recompute against their own stored inputs.
		fixture.RunStep(t, p.Ctx, destination, recorder, fixture.Exec("UPDATE heap_images SET raw=NULL,a=NULL,z=NULL", 1))
		for _, s := range heapViews(false) {
			if len(s.Columns) == 14 {
				s.Rows = nil
			} else {
				s.Rows = [][]any{{nil, nil}}
			}
			fixture.RunStep(t, p.Ctx, destination, recorder, heapMetadata(s, false, true))
		}
		emptyRestored, emptyRestoredRow := heapImage()[:64], heapRow(false)
		heapPointer(emptyRestored, 0, 32, 1, 32)
		emptyRestoredRow[3], emptyRestoredRow[13] = int64(32), ""
		fixture.RunStep(t, p.Ctx, destination, recorder, fixture.Exec("UPDATE heap_images SET raw=$1,a=0,z=0", 1, emptyRestored))
		restoredViews := heapViews(false)
		restoredViews[0].Rows, restoredViews[1].Rows = [][]any{emptyRestoredRow}, [][]any{{"{}", "{}"}}
		for _, s := range restoredViews {
			fixture.RunStep(t, p.Ctx, destination, recorder, heapMetadata(s, false, true))
		}
		fixture.RunStep(t, p.Ctx, destination, recorder, fixture.Exec("UPDATE heap_images SET raw=$1,a=777,z=32777", 1, heapImage()))
		check(destination, recorder, false)
		retained := make([]*sql.Rows, len(views))
		descriptors := make([][]*sql.ColumnType, len(views))
		for i, s := range views {
			rows, err := c.QueryContext(p.Ctx, s.Query)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			retained[i] = rows
			descriptors[i], err = rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
		}
		run(fixture.Exec("UPDATE heap_images SET raw=NULL,a=0,z=0", 1))
		checkHeld(nil, []any{"{}", "{}"})
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		run(fixture.Select("SELECT count(*) FROM pg_extension WHERE extname='pageinspect'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Select("SELECT count(*) FROM pg_proc WHERE prosrc IN ('heap_page_items','heap_tuple_infomask_flags')", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		// Rows opened before UPDATE, Reset and Restore retain the old eager values
		// and descriptors. Exported data may mutate without changing restored rows.
		for i, rows := range retained {
			s := views[i]
			heapCheckColumns(t, descriptors[i], s.Columns, s.Types, false, true)
			if !rows.Next() {
				t.Fatal("retained row absent", rows.Err())
			}
			values, dest := make([]any, len(s.Columns)), make([]any, len(s.Columns))
			for j := range values {
				dest[j] = &values[j]
			}
			if err := rows.Scan(dest...); err != nil {
				t.Fatal(err)
			}
			for j, v := range values {
				if !reflect.DeepEqual(fixture.Normalize(v), s.Rows[0][j]) {
					t.Fatal("retained row changed", j, v, s.Rows[0][j])
				}
			}
			if i == 0 {
				clear(values[13].([]byte))
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("retained cardinality", rows.Err())
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			heapCheckColumns(t, descriptors[i], s.Columns, s.Types, false, true)
		}
		checkOriginal()
		changes()
		checkOriginal()
		before := fixture.ExtensionDump(t, p.Ctx, p.Native)
		bad := dump + "\nINSERT INTO heap_images VALUES(2,'\\x'::bytea,0,0);\nINSERT INTO heap_images VALUES(3,NULL,1/0,0);\n"
		diagnostic := fixture.ControlsReferenceDiagnostic("22012", "division by zero")
		fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)), &diagnostic)
		if fixture.ExtensionDump(t, p.Ctx, p.Native) != before {
			t.Fatal("failed Restore published staged catalog/prefix mutation")
		}
		checkOriginal()
	})
}

func TestReferencePageinspectHeapControls(t *testing.T) { heapControls(t, false) }
