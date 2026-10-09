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
	"testing"
)

func TestReferenceEnumControls(t *testing.T) {
	enumReferenceScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, conn *sql.Conn) {
		defer func() {
			for _, schema := range []string{"enum_saved", "enum_kept"} {
				if _, err := conn.ExecContext(p.ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE"); err != nil {
					t.Error(err)
				}
			}
		}()
		enumReferenceSetup(t, p, backend, conn)
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
				referenceRunStep(t, p.ctx, conn, backend.recorder, referenceExec(query, affected))
			}) {
				t.FailNow()
			}
		}
		check := func() {
			t.Helper()
			for _, prepared := range []bool{false, true} {
				query := referenceSelect("SELECT m AS v FROM enum_rows ORDER BY m", []string{"v"}, []string{""}, [][]any{{"sad"}, {"ok"}, {"happy"}, {nil}})
				query.prepared = prepared
				query.checkMetadata = enumReferenceMetadata(backend.pg, true)
				enumReferenceRunStep(t, p.ctx, conn, backend.recorder, query)
				enumReferenceRunStep(t, p.ctx, conn, backend.recorder, referenceSelect(`SELECT v FROM enum_kept.stored`, []string{"v"}, []string{""}, [][]any{{"O'K"}}))
				referenceRunStep(t, p.ctx, conn, backend.recorder, referenceSelect("SELECT id FROM enum_rows WHERE m::text=h::text ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}}))
			}
		}
		check()
		if backend.pg {
			return
		}
		heldParameter, err := conn.PrepareContext(p.ctx, "SELECT $1::mood AS v")
		if err != nil {
			t.Fatal(err)
		}
		defer heldParameter.Close()
		heldResult, err := conn.PrepareContext(p.ctx, "SELECT m AS v FROM enum_rows ORDER BY m")
		if err != nil {
			t.Fatal(err)
		}
		defer heldResult.Close()
		rawConn, err := p.native.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer rawConn.Close()
		err = rawConn.Raw(func(raw any) error {
			eager, err := raw.(driver.QueryerContext).QueryContext(p.ctx, "SELECT m AS v FROM enum_rows ORDER BY m", nil)
			if err != nil {
				return err
			}
			defer eager.Close()
			metadata := eager.(driver.RowsColumnTypeDatabaseTypeName)
			if metadata.ColumnTypeDatabaseTypeName(0) != "" || eager.(driver.RowsColumnTypeScanType).ColumnTypeScanType(0) != reflect.TypeFor[string]() {
				t.Fatal("eager enum metadata")
			}
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
				return err
			}
			for range 2 {
				if err := stuntgres.Reset(p.ctx, p.native); err != nil {
					return err
				}
				if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
					return err
				}
				check()
				referenceRunStep(t, p.ctx, conn, backend.recorder, referenceExec("INSERT INTO enum_kept.saved(id) VALUES (2)", 1))
				enumReferenceRunStep(t, p.ctx, conn, backend.recorder, referenceSelect("SELECT v FROM enum_kept.stored WHERE id=2", []string{"v"}, []string{""}, [][]any{{"O'K"}}))
				referenceRunStep(t, p.ctx, conn, backend.recorder, referenceExec("DELETE FROM enum_kept.saved WHERE id=2", 1))
				var redump bytes.Buffer
				if err := stuntgres.Dump(p.ctx, p.native, &redump); err != nil {
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
				rows, err := held.QueryContext(p.ctx, args...)
				if rows != nil {
					rows.Close()
					t.Fatal("retained enum plan exposed rows")
				}
				referenceAssertDiagnostic(t, err, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: want})
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
	enumReferenceScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, conn *sql.Conn) {
		enumReferenceSetup(t, p, backend, conn)
		assertSaved := func() {
			t.Helper()
			enumReferenceRunStep(t, p.ctx, conn, backend.recorder, referenceSelect("SELECT m AS v FROM enum_rows ORDER BY m", []string{"v"}, []string{""}, [][]any{{"sad"}, {"ok"}, {"happy"}, {nil}}))
		}
		assertSaved()
		eager, err := backend.db.QueryContext(p.ctx, "SELECT m AS v FROM enum_rows ORDER BY m")
		if err != nil {
			t.Fatal(err)
		}
		defer eager.Close()
		enumReferenceMetadata(backend.pg, true)(t, eager)
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
				} else if backend.pg {
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
			enumReferenceMetadata(backend.pg, true)(t, eager)
			if eager.Next() || eager.Err() != nil {
				t.Fatal("extra eager row", eager.Err())
			}
			if err := eager.Close(); err != nil {
				t.Fatal(err)
			}
		}
		if backend.pg {
			assertHeld()
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
			t.Fatal(err)
		}
		script := `CREATE TYPE replacement AS ENUM ('ok'); CREATE TABLE incoming(v replacement); INSERT INTO incoming VALUES ('ok'),('nope');`
		err = stuntgres.Restore(p.ctx, p.native, strings.NewReader(script))
		position := strings.Index(script, "'nope'") + 1
		referenceAssertDiagnostic(t, err, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: `invalid input value for enum replacement: "nope"`, Position: strconv.Itoa(position)})
		assertSaved()
		absent := referenceImplicitError("SELECT NULL::replacement", referenceDiagnostic{Code: "42704", Message: `type "replacement" does not exist`, Position: "14"})
		absent.selectRows = true
		absent.errorStage = "execute"
		referenceRunStep(t, p.ctx, conn, backend.recorder, absent)
		for _, input := range []string{`CREATE TYPE replacement AS ENUM ('ok'); CREATE TABLE incoming(`, `CREATE TYPE replacement AS ENUM ('ok'); SET SESSION AUTHORIZATION absent_owner;`} {
			if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(input)); err == nil {
				t.Fatal("malformed import succeeded")
			}
			assertSaved()
		}
		canceled, cancel := context.WithCancel(p.ctx)
		cancel()
		if err := stuntgres.Restore(canceled, p.native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		failure := errors.New("enum restore source failure")
		if err := stuntgres.Restore(p.ctx, p.native, uuidReferenceFailingReader{failure}); !errors.Is(err, failure) {
			t.Fatal(err)
		}
		duringRead, cancelRead := context.WithCancel(p.ctx)
		reader := enumCancelReader{Reader: bytes.NewReader(dump.Bytes()), cancel: cancelRead}
		if err := stuntgres.Restore(duringRead, p.native, &reader); !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation during source read", err)
		}
		cancelRead()
		assertSaved()
		assertHeld()
	})
}

func TestReferenceEnumOwnerAndTemporaryRemapping(t *testing.T) {
	roleReferenceCleanup(t, []string{"enum_control_owner"})
	enumReferenceScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, conn *sql.Conn) {
		defer func() {
			if _, err := conn.ExecContext(p.ctx, "RESET ROLE"); err != nil {
				t.Error(err)
			}
			if _, err := conn.ExecContext(p.ctx, "DROP SCHEMA IF EXISTS enum_owned CASCADE"); err != nil {
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
				referenceRunStep(t, p.ctx, conn, backend.recorder, referenceExec(query, 0))
			}) {
				t.FailNow()
			}
		}
		referenceRunStep(t, p.ctx, conn, backend.recorder, referenceExec("INSERT INTO enum_owned.saved VALUES ('a'),('z'),(NULL)", 3))
		check := func(ctx context.Context, executor referenceExecutor, recorder *referenceRecorder) {
			t.Helper()
			for _, prepared := range []bool{false, true} {
				step := referenceSelect("SELECT v FROM enum_owned.saved ORDER BY v", []string{"v"}, []string{""}, [][]any{{"z"}, {"a"}, {nil}})
				step.prepared = prepared
				enumReferenceRunStep(t, ctx, executor, recorder, step)
			}
		}
		check(p.ctx, conn, backend.recorder)
		referenceRunStep(t, p.ctx, conn, backend.recorder, referenceExec("CREATE TEMP SEQUENCE enum_temp START 50", 0))
		referenceRunStep(t, p.ctx, conn, backend.recorder, referenceSelect("SELECT nextval('enum_temp')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(50)}}))
		if backend.pg {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
			t.Fatal(err)
		}
		target := newReferenceFixture(t, false, true)
		referenceRunStep(t, target.ctx, target.memory, target.memoryRecord, referenceExec("CREATE TABLE sentinel(v integer)", 0))
		referenceRunStep(t, target.ctx, target.memory, target.memoryRecord, referenceExec("INSERT INTO sentinel VALUES (42)", 1))
		if err := stuntgres.Restore(target.ctx, target.native, bytes.NewReader(dump.Bytes())); err == nil {
			t.Fatal("missing owner import succeeded")
		}
		referenceRunStep(t, target.ctx, target.memory, target.memoryRecord, referenceSelect("SELECT v FROM sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
		for _, query := range []string{"CREATE ROLE enum_remap_spacer", "CREATE ROLE enum_control_owner"} {
			referenceRunStep(t, target.ctx, target.memory, target.memoryRecord, referenceExec(query, 0))
		}
		reader := enumMutationReader{Reader: bytes.NewReader(dump.Bytes()), mutate: func() error {
			for _, query := range []string{"DROP ROLE enum_control_owner", "CREATE ROLE enum_control_owner"} {
				if _, err := target.memory.ExecContext(target.ctx, query); err != nil {
					return err
				}
			}
			return nil
		}}
		if err := stuntgres.Restore(target.ctx, target.native, &reader); err == nil {
			t.Fatal("recreated captured owner import succeeded")
		}
		referenceRunStep(t, target.ctx, target.memory, target.memoryRecord, referenceSelect("SELECT v FROM sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
		targetConn, err := target.memory.Conn(target.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer targetConn.Close()
		referenceRunStep(t, target.ctx, targetConn, target.memoryRecord, referenceExec("CREATE TEMP SEQUENCE enum_target_temp START 90", 0))
		referenceRunStep(t, target.ctx, targetConn, target.memoryRecord, referenceSelect("SELECT nextval('enum_target_temp')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(90)}}))
		for range 2 {
			if err := stuntgres.Restore(target.ctx, target.native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			check(target.ctx, targetConn, target.memoryRecord)
			var next bytes.Buffer
			if err := stuntgres.Dump(target.ctx, target.native, &next); err != nil {
				t.Fatal(err)
			}
			dump = next
		}
		referenceRunStep(t, target.ctx, targetConn, target.memoryRecord, referenceSelect("SELECT nextval('enum_target_temp')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(91)}}))
		// Owner-name remapping must preserve owner authority, not just readable rows.
		for _, query := range []string{"SET ROLE enum_control_owner", "CREATE TABLE enum_owned.owner_witness(v enum_owned.mood)", "RESET ROLE"} {
			referenceRunStep(t, target.ctx, targetConn, target.memoryRecord, referenceExec(query, 0))
		}
		if err := stuntgres.Reset(p.ctx, p.native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		check(p.ctx, conn, backend.recorder)
		referenceRunStep(t, p.ctx, conn, backend.recorder, referenceSelect("SELECT nextval('enum_temp')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(51)}}))
	})
}

type enumCancelReader struct {
	*bytes.Reader
	cancel context.CancelFunc
}

func (r *enumCancelReader) Read(dst []byte) (int, error) {
	n, err := r.Reader.Read(dst)
	if err == io.EOF {
		r.cancel()
	}
	return n, err
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
