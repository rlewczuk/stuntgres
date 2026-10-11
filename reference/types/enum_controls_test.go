package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strconv"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceEnumControls(t *testing.T) {
	fixture.EnumReferenceScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn) {
		defer func() {
			for _, schema := range []string{"enum_saved", "enum_kept"} {
				if _, err := conn.ExecContext(p.Ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE"); err != nil {
					t.Error(err)
				}
			}
		}()
		fixture.EnumReferenceSetup(t, p, backend, conn)
		for _, query := range []string{
			`CREATE SCHEMA IF NOT EXISTS enum_saved`,
			`CREATE TYPE enum_saved."Odd Type" AS ENUM (' z ','','O''K','back\slash')`,
			`CREATE DOMAIN enum_saved.wrapped AS enum_saved."Odd Type"`,
			`CREATE TABLE enum_saved.saved(id integer,v enum_saved.wrapped DEFAULT 'O''K'::enum_saved."Odd Type", CHECK (v::enum_saved."Odd Type" >= ''::enum_saved."Odd Type"))`,
			`INSERT INTO enum_saved.saved(id) VALUES (1)`,
			`CREATE VIEW enum_saved.stored AS SELECT id,v::enum_saved."Odd Type" AS v FROM enum_saved.saved WHERE v::enum_saved."Odd Type"='O''K'::enum_saved."Odd Type"`,
			`ALTER SCHEMA enum_saved RENAME TO enum_kept`,
		} {
			affected := int64(0)
			if strings.HasPrefix(query, "INSERT") {
				affected = 1
			}
			if !t.Run(query, func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec(query, affected))
			}) {
				t.FailNow()
			}
		}
		check := func() {
			t.Helper()
			for _, prepared := range []bool{false, true} {
				query := fixture.Select("SELECT m AS v FROM enum_rows ORDER BY m", []string{"v"}, []string{""}, [][]any{{"sad"}, {"ok"}, {"happy"}, {nil}})
				query.Prepared = prepared
				query.CheckMetadata = fixture.EnumReferenceMetadata(backend.PG, true)
				fixture.EnumReferenceRunStep(t, p.Ctx, conn, backend.Recorder, query)
				fixture.EnumReferenceRunStep(t, p.Ctx, conn, backend.Recorder, fixture.Select(`SELECT v FROM enum_kept.stored`, []string{"v"}, []string{""}, [][]any{{"O'K"}}))
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Select("SELECT id FROM enum_rows WHERE m::text=h::text ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}}))
			}
		}
		check()
		if backend.PG {
			return
		}
		heldParameter, err := conn.PrepareContext(p.Ctx, "SELECT $1::mood AS v")
		if err != nil {
			t.Fatal(err)
		}
		defer heldParameter.Close()
		heldResult, err := conn.PrepareContext(p.Ctx, "SELECT m AS v FROM enum_rows ORDER BY m")
		if err != nil {
			t.Fatal(err)
		}
		defer heldResult.Close()
		rawConn, err := p.Native.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer rawConn.Close()
		err = rawConn.Raw(func(raw any) error {
			eager, err := raw.(driver.QueryerContext).QueryContext(p.Ctx, "SELECT m AS v FROM enum_rows ORDER BY m", nil)
			if err != nil {
				return err
			}
			defer eager.Close()
			metadata := eager.(driver.RowsColumnTypeDatabaseTypeName)
			if metadata.ColumnTypeDatabaseTypeName(0) != "" || eager.(driver.RowsColumnTypeScanType).ColumnTypeScanType(0) != reflect.TypeFor[string]() {
				t.Fatal("eager enum metadata")
			}
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				return err
			}
			for range 2 {
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					return err
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					return err
				}
				check()
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("INSERT INTO enum_kept.saved(id) VALUES (2)", 1))
				fixture.EnumReferenceRunStep(t, p.Ctx, conn, backend.Recorder, fixture.Select("SELECT v FROM enum_kept.stored WHERE id=2", []string{"v"}, []string{""}, [][]any{{"O'K"}}))
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("DELETE FROM enum_kept.saved WHERE id=2", 1))
				var redump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
					return err
				}
				dump = redump
			}
			for _, held := range []*sql.Stmt{heldParameter, heldResult} {
				args := []any(nil)
				want := "cached plan must not change result type"
				if held == heldParameter {
					args = []any{"ok"}
					want = "cached plan must not change parameter type"
				}
				rows, err := held.QueryContext(p.Ctx, args...)
				if rows != nil {
					rows.Close()
					t.Fatal("retained enum plan exposed rows")
				}
				fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: want})
			}
			dst := make([]driver.Value, 1)
			for _, want := range []driver.Value{"sad", "ok", "happy", nil} {
				if err := eager.Next(dst); err != nil || dst[0] != want {
					t.Fatal(dst, want, err)
				}
			}
			if err := eager.Next(dst); err != io.EOF {
				t.Fatal(err)
			}
			if metadata.ColumnTypeDatabaseTypeName(0) != "" {
				t.Fatal("eager metadata changed")
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	})
}

func TestReferenceEnumRestoreAtomicity(t *testing.T) {
	fixture.EnumReferenceScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn) {
		fixture.EnumReferenceSetup(t, p, backend, conn)
		assertSaved := func() {
			t.Helper()
			fixture.EnumReferenceRunStep(t, p.Ctx, conn, backend.Recorder, fixture.Select("SELECT m AS v FROM enum_rows ORDER BY m", []string{"v"}, []string{""}, [][]any{{"sad"}, {"ok"}, {"happy"}, {nil}}))
		}
		assertSaved()
		eager, err := backend.DB.QueryContext(p.Ctx, "SELECT m AS v FROM enum_rows ORDER BY m")
		if err != nil {
			t.Fatal(err)
		}
		defer eager.Close()
		fixture.EnumReferenceMetadata(backend.PG, true)(t, eager)
		assertHeld := func() {
			t.Helper()
			for _, want := range []any{"sad", "ok", "happy", nil} {
				if !eager.Next() {
					t.Fatal("missing eager row", eager.Err())
				}
				var raw any
				if err := eager.Scan(&raw); err != nil {
					t.Fatal(err)
				}
				if want == nil {
					if raw != nil {
						t.Fatal("eager NULL", raw)
					}
				} else if backend.PG {
					bytes, ok := raw.([]byte)
					if !ok || string(bytes) != want {
						t.Fatal("eager pq enum", raw, want)
					}
				} else if raw != want {
					t.Fatal("eager native enum", raw, want)
				}
			}
			// database/sql closes rows automatically when Next reaches EOF.
			// Inspect the retained metadata while the final row is still current.
			fixture.EnumReferenceMetadata(backend.PG, true)(t, eager)
			if eager.Next() || eager.Err() != nil {
				t.Fatal("extra eager row", eager.Err())
			}
			if err := eager.Close(); err != nil {
				t.Fatal(err)
			}
		}
		if backend.PG {
			assertHeld()
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		script := `CREATE TYPE replacement AS ENUM ('ok'); CREATE TABLE incoming(v replacement); INSERT INTO incoming VALUES ('ok'),('nope');`
		err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script))
		position := strings.Index(script, "'nope'") + 1
		fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: `invalid input value for enum replacement: "nope"`, Position: strconv.Itoa(position)})
		assertSaved()
		absent := fixture.ImplicitError("SELECT NULL::replacement", fixture.Diagnostic{Code: "42704", Message: `type "replacement" does not exist`, Position: "14"})
		absent.SelectRows = true
		absent.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, conn, backend.Recorder, absent)
		for _, input := range []string{`CREATE TYPE replacement AS ENUM ('ok'); CREATE TABLE incoming(`, `CREATE TYPE replacement AS ENUM ('ok'); SET SESSION AUTHORIZATION absent_owner;`} {
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(input)); err == nil {
				t.Fatal("malformed import succeeded")
			}
			assertSaved()
		}
		canceled, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(canceled, p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		failure := errors.New("enum restore source failure")
		if err := stuntgres.Restore(p.Ctx, p.Native, uuidReferenceFailingReader{failure}); !errors.Is(err, failure) {
			t.Fatal(err)
		}
		duringRead, cancelRead := context.WithCancel(p.Ctx)
		reader := fixture.EnumCancelReader{Reader: bytes.NewReader(dump.Bytes()), Cancel: cancelRead}
		if err := stuntgres.Restore(duringRead, p.Native, &reader); !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation during source read", err)
		}
		cancelRead()
		assertSaved()
		assertHeld()
	})
}

func TestReferenceEnumOwnerAndTemporaryRemapping(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"enum_control_owner"})
	fixture.EnumReferenceScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn) {
		defer func() {
			if _, err := conn.ExecContext(p.Ctx, "RESET ROLE"); err != nil {
				t.Error(err)
			}
			if _, err := conn.ExecContext(p.Ctx, "DROP SCHEMA IF EXISTS enum_owned CASCADE"); err != nil {
				t.Error(err)
			}
		}()
		for _, query := range []string{
			"CREATE ROLE enum_control_owner",
			"CREATE SCHEMA IF NOT EXISTS enum_owned",
			"ALTER SCHEMA enum_owned OWNER TO enum_control_owner",
			"SET ROLE enum_control_owner",
			"CREATE TYPE enum_owned.mood AS ENUM ('z','a')",
			"CREATE DOMAIN enum_owned.wrapped AS enum_owned.mood",
			"CREATE TABLE enum_owned.saved(v enum_owned.wrapped)",
			"RESET ROLE",
		} {
			if !t.Run(query, func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec(query, 0))
			}) {
				t.FailNow()
			}
		}
		fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("INSERT INTO enum_owned.saved VALUES ('a'),('z'),(NULL)", 3))
		check := func(ctx context.Context, executor fixture.Executor, recorder *fixture.Recorder) {
			t.Helper()
			for _, prepared := range []bool{false, true} {
				step := fixture.Select("SELECT v FROM enum_owned.saved ORDER BY v", []string{"v"}, []string{""}, [][]any{{"z"}, {"a"}, {nil}})
				step.Prepared = prepared
				fixture.EnumReferenceRunStep(t, ctx, executor, recorder, step)
			}
		}
		check(p.Ctx, conn, backend.Recorder)
		fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("CREATE TEMP SEQUENCE enum_temp START 50", 0))
		fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Select("SELECT nextval('enum_temp')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(50)}}))
		if backend.PG {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		target := fixture.NewFixture(t, false, true)
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE TABLE sentinel(v integer)", 0))
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("INSERT INTO sentinel VALUES (42)", 1))
		if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err == nil {
			t.Fatal("missing owner import succeeded")
		}
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Select("SELECT v FROM sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
		for _, query := range []string{"CREATE ROLE enum_remap_spacer", "CREATE ROLE enum_control_owner"} {
			fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec(query, 0))
		}
		reader := enumMutationReader{Reader: bytes.NewReader(dump.Bytes()), mutate: func() error {
			for _, query := range []string{"DROP ROLE enum_control_owner", "CREATE ROLE enum_control_owner"} {
				if _, err := target.Memory.ExecContext(target.Ctx, query); err != nil {
					return err
				}
			}
			return nil
		}}
		if err := stuntgres.Restore(target.Ctx, target.Native, &reader); err == nil {
			t.Fatal("recreated captured owner import succeeded")
		}
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Select("SELECT v FROM sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
		targetConn, err := target.Memory.Conn(target.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer targetConn.Close()
		fixture.RunStep(t, target.Ctx, targetConn, target.MemoryRecord, fixture.Exec("CREATE TEMP SEQUENCE enum_target_temp START 90", 0))
		fixture.RunStep(t, target.Ctx, targetConn, target.MemoryRecord, fixture.Select("SELECT nextval('enum_target_temp')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(90)}}))
		for range 2 {
			if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			check(target.Ctx, targetConn, target.MemoryRecord)
			var next bytes.Buffer
			if err := stuntgres.Dump(target.Ctx, target.Native, &next); err != nil {
				t.Fatal(err)
			}
			dump = next
		}
		fixture.RunStep(t, target.Ctx, targetConn, target.MemoryRecord, fixture.Select("SELECT nextval('enum_target_temp')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(91)}}))
		// Owner-name remapping must preserve owner authority, not just readable rows.
		for _, query := range []string{"SET ROLE enum_control_owner", "CREATE TABLE enum_owned.owner_witness(v enum_owned.mood)", "RESET ROLE"} {
			fixture.RunStep(t, target.Ctx, targetConn, target.MemoryRecord, fixture.Exec(query, 0))
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		check(p.Ctx, conn, backend.Recorder)
		fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Select("SELECT nextval('enum_temp')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(51)}}))
	})
}

type enumMutationReader struct {
	*bytes.Reader
	mutate func() error
}

func (r *enumMutationReader) Read(dst []byte) (int, error) {
	if r.mutate != nil {
		mutate := r.mutate
		r.mutate = nil
		if err := mutate(); err != nil {
			return 0, err
		}
	}
	return r.Reader.Read(dst)
}
