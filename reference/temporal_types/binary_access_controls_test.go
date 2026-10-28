package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBinaryAccessControls(t *testing.T) {
	binaryAccessScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE access_control(id integer,v bytea,b bytea GENERATED ALWAYS AS (set_byte(v,0,256)) STORED,CHECK(bit_count(v)<=8))", 0),
			fixture.Exec(`INSERT INTO access_control(id,v) VALUES(1,'\x0180'),(2,NULL)`, 2),
			fixture.Exec("CREATE INDEX access_control_idx ON access_control(get_bit(v,15::bigint))", 0),
			fixture.Exec("CREATE VIEW access_control_view AS SELECT id,"+binaryAccessProjection+" FROM access_control", 0),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		want := fixture.Select("SELECT bit_count,get_bit,get_byte,set_bit,set_byte,reverse FROM access_control_view ORDER BY id", binaryAccessNames, binaryAccessTypes, [][]any{{int64(2), int64(1), int64(1), "\x01\x00", "\x00\x80", "\x80\x01"}, {nil, nil, nil, nil, nil, nil}})
		want.CheckMetadata = func(t *testing.T, r *sql.Rows) {
			columns, err := r.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			binaryAccessColumns(t, columns, b.PG, true)
		}
		binaryAccessBoth(t, p, b, c, want)
		if b.PG {
			return
		} // Controls are native APIs; actual emitted SQL is the wire oracle.
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		for _, name := range binaryAccessNames {
			if !strings.Contains(dump.String(), name+"(") && !strings.Contains(dump.String(), `"`+name+`"(`) {
				t.Fatal("lost symbolic function", name, dump.String())
			}
		}
		old, err := c.QueryContext(p.Ctx, want.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		meta, err := old.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		binaryAccessColumns(t, meta, false, true)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		fixture.StringCoreRetainedRows(t, old, nil, want.Columns, want.Types, want.Rows, false)
		binaryAccessColumns(t, meta, false, true)
		binaryAccessBoth(t, p, b, c, want)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v,b FROM access_control WHERE id=1", []string{"v", "b"}, []string{"BYTEA", "BYTEA"}, [][]any{{"\x01\x80", "\x00\x80"}}))
		var redump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
			t.Fatal(err)
		}
		if dump.String() != redump.String() {
			t.Fatal("canonical redump changed", dump.String(), redump.String())
		}
		if p.Postgres != nil {
			if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				t.Fatal(err)
			}
			if _, err := p.Postgres.ExecContext(p.Ctx, redump.String()); err != nil {
				t.Fatal("actual emitted SQL replay", err)
			}
			// Replay uses pq metadata, not the native stored-column convention.
			want.CheckMetadata = func(t *testing.T, r *sql.Rows) {
				columns, err := r.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				binaryAccessColumns(t, columns, true, true)
			}
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, want)
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Select("SELECT v,b FROM access_control WHERE id=1", []string{"v", "b"}, []string{"BYTEA", "BYTEA"}, [][]any{{"\x01\x80", "\x00\x80"}}))
			if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				t.Fatal(err)
			}
		}
		valid := `CREATE TABLE access_restored(n bigint,b integer,v bytea); INSERT INTO access_restored VALUES(bit_count('\x0180'::bytea),get_bit('\x0180'::bytea,15),set_byte('\x0180'::bytea,0,256));`
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(valid)); err != nil {
			t.Fatal(err)
		}
		var before bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct{ expr, code, msg string }{
			{`set_byte('\x0180'::bytea,2,256)`, "2202E", "index 2 out of valid range, 0..1"},
			{`set_bit('\x0180'::bytea,0,2)`, "22023", "new bit must be 0 or 1"},
		} {
			bad := `CREATE TABLE access_bad(v bytea); INSERT INTO access_bad VALUES('\x00'); INSERT INTO access_bad VALUES(` + tc.expr + `);`
			d := fixture.ControlsReferenceDiagnostic(tc.code, tc.msg)
			fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)), &d)
			var after bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
				t.Fatal(err)
			}
			if before.String() != after.String() {
				t.Fatal("failed replacement published prefix")
			}
		}
		for _, expr := range []string{"bit_count($1)", "get_bit($1,0)", `get_bit('\x00'::bytea,$1)`, "get_byte($1,0)", `get_byte('\x00'::bytea,$1)`, "set_bit($1,0,1)", `set_bit('\x00'::bytea,$1,1)`, `set_bit('\x00'::bytea,0,$1)`, "set_byte($1,0,1)", `set_byte('\x00'::bytea,$1,1)`, `set_byte('\x00'::bytea,0,$1)`, `set_byte(reverse($1),0,1)`} {
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE access_bad(v bytea); INSERT INTO access_bad VALUES("+expr+");")); err == nil {
				t.Fatal("Restore accepted parameter", expr)
			}
			var after bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
				t.Fatal(err)
			}
			if before.String() != after.String() {
				t.Fatal("parameter replacement changed destination", expr)
			}
		}
		binaryAccessBoth(t, p, b, c, fixture.Select("SELECT n,b,v FROM access_restored", []string{"n", "b", "v"}, []string{"INT8", "INT4", "BYTEA"}, [][]any{{int64(2), int64(1), "\x00\x80"}}))
	})
}

func TestReferenceBinaryAccessFeatureSmoke(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(*testing.T)
	}{
		{"released projection", TestReferenceBinaryAccessValues},
		{"orientation and truncation", TestReferenceBinaryAccessOrientation},
		{"held frames and source ownership", TestReferenceBinaryAccessOwnership},
		{"bounds newbit and recovery", TestReferenceBinaryAccessDiagnostics},
		{"empty and NULL metadata", TestReferenceBinaryAccessMetadata},
		{"symbolic controls and emitted SQL replay", TestReferenceBinaryAccessControls},
	} {
		t.Run(tc.name, tc.run)
	}
}
