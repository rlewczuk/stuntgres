package reference_test

import (
	"bytes"
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"strconv"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceEnumAlterControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"alter_saved_owner"})
	enumAlterScenario(t, func(t *testing.T, f fixture.ControlsReferenceFixture, pg bool) {
		defer func() {
			f.A.ExecContext(f.Pair.Ctx, "RESET SESSION AUTHORIZATION")
			if _, err := f.A.ExecContext(f.Pair.Ctx, "DROP SCHEMA IF EXISTS alter_saved CASCADE"); err != nil {
				t.Error(err)
			}
		}()
		for _, q := range []string{
			"CREATE ROLE alter_saved_owner", "CREATE SCHEMA alter_saved AUTHORIZATION alter_saved_owner", "SET SESSION AUTHORIZATION alter_saved_owner",
			`CREATE TYPE alter_saved.mood AS ENUM ('z','','O''K','back\slash')`,
			"CREATE DOMAIN alter_saved.wrapped AS alter_saved.mood",
			"CREATE TYPE alter_saved.box AS (v alter_saved.mood)",
			`CREATE TABLE alter_saved.saved(id integer,v alter_saved.mood DEFAULT 'O''K'::alter_saved.mood,CHECK (v<='back\slash'::alter_saved.mood))`,
			"CREATE TABLE alter_saved.nested(d alter_saved.wrapped,c alter_saved.box)",
			`CREATE VIEW alter_saved.stored AS SELECT id,v FROM alter_saved.saved WHERE v='O''K'::alter_saved.mood`,
			"CREATE VIEW alter_saved.provider AS SELECT enum_last(NULL::alter_saved.mood) AS v",
			"RESET SESSION AUTHORIZATION",
		} {
			enumAlterExec(t, f, f.A, q)
		}
		fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, fixture.Exec("INSERT INTO alter_saved.saved(id) VALUES (1)", 1))
		fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, fixture.Exec(`INSERT INTO alter_saved.nested VALUES ('O''K','("O''K")')`, 1))
		enumAlterExec(t, f, f.A, "ALTER TYPE alter_saved.mood ADD VALUE 'a' BEFORE 'O''K'")
		enumAlterExec(t, f, f.A, "ALTER TYPE alter_saved.mood RENAME VALUE 'O''K' TO 'end'")
		check := func() {
			t.Helper()
			s := fixture.Select("SELECT v FROM alter_saved.stored ORDER BY id", []string{"v"}, []string{""}, [][]any{{"end"}})
			s.CheckMetadata = fixture.EnumReferenceMetadata(pg, true)
			fixture.EnumReferenceRunStep(t, f.Pair.Ctx, f.A, f.Recorder, s)
			enumAlterRows(t, f, f.A, "SELECT d::alter_saved.mood AS v FROM alter_saved.nested", "end")
			for _, prepared := range []bool{false, true} {
				composite := fixture.Select("SELECT c::text AS v FROM alter_saved.nested", []string{"v"}, []string{"TEXT"}, [][]any{{"(end)"}})
				composite.Prepared = prepared
				fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, composite)
			}
			provider := fixture.Select("SELECT v FROM alter_saved.provider", []string{"v"}, []string{""}, [][]any{{`back\slash`}})
			provider.CheckMetadata = fixture.EnumReferenceMetadata(pg, true)
			fixture.EnumReferenceRunStep(t, f.Pair.Ctx, f.A, f.Recorder, provider)
			fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, fixture.Select("SELECT enum_range(NULL::alter_saved.mood)::text AS v", []string{"v"}, []string{"TEXT"}, [][]any{{`{z,"",a,end,"back\\slash"}`}}))
		}
		check()
		if pg {
			return
		} // Native logical controls are not PostgreSQL disk/network backup.
		held, err := f.A.PrepareContext(f.Pair.Ctx, "SELECT 'end'::alter_saved.mood AS v")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		raw, err := f.Pair.Native.Conn(f.Pair.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer raw.Close()
		err = raw.Raw(func(c any) error {
			eager, err := c.(driver.QueryerContext).QueryContext(f.Pair.Ctx, "SELECT v FROM alter_saved.saved ORDER BY id", nil)
			if err != nil {
				return err
			}
			defer eager.Close()
			var dump bytes.Buffer
			enumAlterExec(t, f, f.A, "BEGIN")
			enumAlterExec(t, f, f.A, "ALTER TYPE alter_saved.mood ADD VALUE 'private'")
			if err := stuntgres.Dump(f.Pair.Ctx, f.Pair.Native, &dump); err != nil {
				return err
			}
			if strings.Contains(dump.String(), "private") || strings.Contains(dump.String(), "O''K") {
				t.Fatal("dump retained private label or stale stored literal", dump.String())
			}
			enumAlterExec(t, f, f.A, "ROLLBACK")
			for range 2 {
				if err := stuntgres.Reset(f.Pair.Ctx, f.Pair.Native); err != nil {
					return err
				}
				if err := stuntgres.Restore(f.Pair.Ctx, f.Pair.Native, bytes.NewReader(dump.Bytes())); err != nil {
					return err
				}
				check()
				fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, fixture.Exec("INSERT INTO alter_saved.saved(id) VALUES (2)", 1))
				s := fixture.Select("SELECT v FROM alter_saved.stored WHERE id=2", []string{"v"}, []string{""}, [][]any{{"end"}})
				s.CheckMetadata = fixture.EnumReferenceMetadata(false, true)
				fixture.EnumReferenceRunStep(t, f.Pair.Ctx, f.A, f.Recorder, s)
				fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, fixture.Exec("DELETE FROM alter_saved.saved WHERE id=2", 1))
				var again bytes.Buffer
				if err := stuntgres.Dump(f.Pair.Ctx, f.Pair.Native, &again); err != nil {
					return err
				}
				dump = again
			}
			rows, err := held.QueryContext(f.Pair.Ctx)
			if rows != nil {
				rows.Close()
				t.Fatal("replacement-retained literal exposed rows")
			}
			replacedLiteral := enumAlterDiagnostic("0A000", "cached plan must not change result type")
			replacedLiteral.Position = "8" // Existing inputErrorAt contract locates the original literal.
			fixture.EnumReferenceAssertDiagnostic(t, err, replacedLiteral)
			dst := make([]driver.Value, 1)
			if err := eager.Next(dst); err != nil || dst[0] != "end" {
				t.Fatal("retained eager member", dst, err)
			}
			if err := eager.Next(dst); err != io.EOF {
				t.Fatal(err)
			}
			bad := `CREATE TYPE replacement AS ENUM ('old');CREATE TABLE incoming(v replacement);INSERT INTO incoming VALUES ('missing');`
			err = stuntgres.Restore(f.Pair.Ctx, f.Pair.Native, strings.NewReader(bad))
			want := enumAlterDiagnostic("22P02", `invalid input value for enum replacement: "missing"`)
			want.Position = strconv.Itoa(strings.Index(bad, "'missing'") + 1)
			fixture.EnumReferenceAssertDiagnostic(t, err, want)
			check()
			for _, script := range []string{`CREATE TYPE incoming AS ENUM ('end');CREATE TABLE invalid(`, `CREATE TYPE incoming AS ENUM ('end');SET SESSION AUTHORIZATION absent_alter_owner;`} {
				if err := stuntgres.Restore(f.Pair.Ctx, f.Pair.Native, strings.NewReader(script)); err == nil {
					t.Fatal("invalid evolved import succeeded")
				}
				check()
			}
			failure := errors.New("evolved enum import reader failure")
			if err := stuntgres.Restore(f.Pair.Ctx, f.Pair.Native, fixture.UUIDReferenceFailingReader{failure}); !errors.Is(err, failure) {
				t.Fatal(err)
			}
			check()
			canceled, cancel := context.WithCancel(f.Pair.Ctx)
			cancel()
			if err := stuntgres.Restore(canceled, f.Pair.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			check()
			during, cancelRead := context.WithCancel(f.Pair.Ctx)
			reader := fixture.EnumCancelReader{Reader: bytes.NewReader(dump.Bytes()), Cancel: cancelRead}
			if err := stuntgres.Restore(during, f.Pair.Native, &reader); !errors.Is(err, context.Canceled) {
				t.Fatal("evolved import read cancellation", err)
			}
			cancelRead()
			check()
			enumAlterImportedOwnerAndTemp(t, dump.Bytes())
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		// Restored owner identity, not superuser wiring, authorizes this mutation.
		enumAlterExec(t, f, f.A, "SET SESSION AUTHORIZATION alter_saved_owner")
		enumAlterExec(t, f, f.A, "ALTER TYPE alter_saved.mood ADD VALUE 'owned'")
		enumAlterExec(t, f, f.A, "RESET SESSION AUTHORIZATION")
	})
}

func enumAlterImportedOwnerAndTemp(t *testing.T, script []byte) {
	t.Helper()
	target := fixture.NewFixture(t, false, true)
	fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE TABLE sentinel(v integer)", 0))
	fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("INSERT INTO sentinel VALUES (42)", 1))
	preserved := func() {
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Select("SELECT v FROM sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
	}
	if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(script)); err == nil {
		t.Fatal("missing evolved enum owner accepted")
	}
	preserved()
	for _, q := range []string{"CREATE ROLE alter_owner_spacer", "CREATE ROLE alter_saved_owner"} {
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec(q, 0))
	}
	reader := enumMutationReader{Reader: bytes.NewReader(script), mutate: func() error {
		for _, q := range []string{"DROP ROLE alter_saved_owner", "CREATE ROLE alter_saved_owner"} {
			if _, err := target.Memory.ExecContext(target.Ctx, q); err != nil {
				return err
			}
		}
		return nil
	}}
	if err := stuntgres.Restore(target.Ctx, target.Native, &reader); err == nil {
		t.Fatal("recreated evolved enum owner accepted")
	}
	preserved()
	conn, err := target.Memory.Conn(target.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fixture.RunStep(t, target.Ctx, conn, target.MemoryRecord, fixture.Exec("CREATE TEMP SEQUENCE alter_retained_temp START 90", 0))
	fixture.RunStep(t, target.Ctx, conn, target.MemoryRecord, fixture.Select("SELECT nextval('alter_retained_temp')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(90)}}))
	for range 2 {
		if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(script)); err != nil {
			t.Fatal(err)
		}
		s := fixture.Select("SELECT v FROM alter_saved.stored", []string{"v"}, []string{""}, [][]any{{"end"}})
		s.CheckMetadata = fixture.EnumReferenceMetadata(false, true)
		fixture.EnumReferenceRunStep(t, target.Ctx, conn, target.MemoryRecord, s)
		fixture.RunStep(t, target.Ctx, conn, target.MemoryRecord, fixture.Select("SELECT d::text AS d,c::text AS c FROM alter_saved.nested", []string{"d", "c"}, []string{"TEXT", "TEXT"}, [][]any{{"end", "(end)"}}))
	}
	fixture.RunStep(t, target.Ctx, conn, target.MemoryRecord, fixture.Select("SELECT nextval('alter_retained_temp')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(91)}}))
	for _, q := range []string{"SET ROLE alter_saved_owner", "ALTER TYPE alter_saved.mood RENAME VALUE 'end' TO 'owned_end'", "RESET ROLE"} {
		fixture.RunStep(t, target.Ctx, conn, target.MemoryRecord, fixture.Exec(q, 0))
	}
}
