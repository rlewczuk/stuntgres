package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func inetSubnetStoredSetup() []fixture.Step {
	return []fixture.Step{
		fixture.Exec("CREATE TABLE subnet_saved(id INTEGER,h INET DEFAULT INET '192.168.1.5',n CIDR DEFAULT CIDR '192.168.1/24',d BOOL DEFAULT (INET '::1' <<= INET '::/0'),s BOOL GENERATED ALWAYS AS (h << n) STORED,e BOOL GENERATED ALWAYS AS (h OPERATOR(pg_catalog.<<=) n) STORED,r BOOL GENERATED ALWAYS AS (n >> h) STORED,i BOOL GENERATED ALWAYS AS (n >>= h) STORED,o BOOL GENERATED ALWAYS AS (h && n) STORED,CONSTRAINT subnet_valid CHECK (h IS NULL OR h <<= n))", 0),
		fixture.Exec("CREATE VIEW subnet_saved_view AS SELECT id,d,s,e,r,i,o,(h && n) AND (n >>= h) AS nested FROM subnet_saved", 0),
		fixture.Exec("INSERT INTO subnet_saved(id) VALUES(1)", 1),
		fixture.Exec("INSERT INTO subnet_saved(id,h) VALUES(2,'192.168.1.254/24'),(3,NULL)", 2),
	}
}

func inetSubnetStoredCheck(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, s := range []fixture.Step{
		fixture.Select("SELECT id,h,d,s,e,r,i,o FROM subnet_saved ORDER BY id", []string{"id", "h", "d", "s", "e", "r", "i", "o"}, []string{"INT4", "INET", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{int64(1), "192.168.1.5", true, true, true, true, true, true}, {int64(2), "192.168.1.254/24", true, false, true, false, true, true}, {int64(3), nil, true, nil, nil, nil, nil, nil}}),
		fixture.Select("SELECT id,d,s,e,r,i,o,nested FROM subnet_saved_view ORDER BY id", []string{"id", "d", "s", "e", "r", "i", "o", "nested"}, []string{"INT4", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{int64(1), true, true, true, true, true, true, true}, {int64(2), true, false, true, false, true, true, true}, {int64(3), true, nil, nil, nil, nil, nil, nil}}),
	} {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
	}
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO subnet_saved(id) VALUES(4)", 1))
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT d,s,e,r,i,o FROM subnet_saved WHERE id=4", []string{"d", "s", "e", "r", "i", "o"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true, true, true}}))
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("DELETE FROM subnet_saved WHERE id=4", 1))
}

func TestReferenceINETSubnetControls(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		for _, s := range inetSubnetStoredSetup() {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		}
		inetSubnetStoredCheck(t, p, b)
		d := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "subnet_saved" violates check constraint "subnet_valid"`)
		d.Schema = "public"
		d.Table = "subnet_saved"
		d.Constraint = "subnet_valid"
		d.Detail = "Failing row contains (5, 192.168.2.5, 192.168.1.0/24, t, f, f, f, f, f)."
		s := fixture.ErrorDiagnostic("INSERT INTO subnet_saved(id,h) VALUES(5,'192.168.2.5')", d)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		s = fixture.Prepared(s)
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		inetSubnetStoredCheck(t, p, b)
		if b.PG {
			continue
		} // Only native has controls; SQL assertions above remain shared.
		old, err := b.DB.QueryContext(p.Ctx, "SELECT id,h FROM subnet_saved ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		inetSubnetStoredCheck(t, p, b)
		var second bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &second); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(dump.Bytes(), second.Bytes()) {
			t.Fatal("unstable subnet symbolic replay")
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(second.Bytes())); err != nil {
			t.Fatal(err)
		}
		inetSubnetStoredCheck(t, p, b)
		target := fixture.NewFixture(t, false, true)
		if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		tb := fixture.Backend{DB: target.Memory, Recorder: target.MemoryRecord}
		inetSubnetStoredCheck(t, target, tb)
		script := "CREATE TABLE subnet_staged(id INTEGER);CREATE TABLE subnet_bad(v BOOL DEFAULT (INET 'bad' << INET '::/0'));"
		bad := inetDiagnostic("inet", "bad", false)
		bad.Position = fixture.Position(strings.Index(script, "'bad'") + 1)
		fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)), &bad)
		inetSubnetStoredCheck(t, p, b)
		retained := fixture.Select("SELECT id,h FROM subnet_saved ORDER BY id", []string{"id", "h"}, []string{"INT4", "INET"}, [][]any{{int64(1), "192.168.1.5"}, {int64(2), "192.168.1.254/24"}, {int64(3), nil}})
		retained.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
			columns, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			fixture.ScalarAssertDescription(t, columns, retained.Columns, retained.Types)
			for i, scan := range []reflect.Type{reflect.TypeFor[int64](), reflect.TypeFor[string]()} {
				column := columns[i]
				if column.ScanType() != scan {
					t.Fatalf("retained %s ScanType=%v want %v", column.Name(), column.ScanType(), scan)
				}
				if length, known := column.Length(); length != 0 || known {
					t.Fatalf("retained %s Length=(%d,%v), want (0,false)", column.Name(), length, known)
				}
				if precision, scale, known := column.DecimalSize(); precision != 0 || scale != 0 || known {
					t.Fatalf("retained %s DecimalSize=(%d,%d,%v), want (0,0,false)", column.Name(), precision, scale, known)
				}
				if nullable, known := column.Nullable(); !nullable || !known {
					t.Fatalf("retained %s Nullable=(%v,%v), want (true,true)", column.Name(), nullable, known)
				}
			}
		}
		retained.CheckRaw = func(t *testing.T, column, row int, value any) bool {
			fixture.AssertRawValue(t, false, retained.Types[column], value)
			return true
		}
		fixture.RangeConsumeHeld(t, old, retained)
		if p.Postgres != nil {
			for _, q := range []string{"DROP SCHEMA public CASCADE;CREATE SCHEMA public", dump.String()} {
				if _, err := p.Postgres.ExecContext(p.Ctx, q); err != nil {
					t.Fatal(err)
				}
			}
			inetSubnetStoredCheck(t, p, fixture.Backend{DB: p.Postgres, Recorder: p.PostgresRecord, PG: true})
			if _, err := p.Postgres.ExecContext(p.Ctx, "DROP VIEW subnet_saved_view;DROP TABLE subnet_saved"); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestReferenceINETSubnetFeatureSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE subnet_smoke(id INTEGER,h INET,n CIDR)", 0),
			fixture.Exec("INSERT INTO subnet_smoke VALUES(1,$1,$2),(2,$3,$2),(3,$4,$2)", 3, "192.168.1.5", []byte("192.168.1.0/24"), []byte("192.168.1.254/24"), "192.168.2.5"),
			fixture.Select(inetSubnetQuery("h", "n")+" FROM subnet_smoke ORDER BY id", inetSubnetNames, inetSubnetTypes, [][]any{{true, true, false, false, true}, {false, true, false, true, true}, {false, false, false, false, false}}),
			fixture.Select("SELECT h FROM subnet_smoke ORDER BY id", []string{"h"}, []string{"INET"}, [][]any{{"192.168.1.5"}, {"192.168.1.254/24"}, {"192.168.2.5"}}),
		} {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		}
		q := inetSubnetQuery("$1::inet", "$2::inet")
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
		for _, tc := range []struct {
			a, z any
			v    []any
		}{
			{"192.168.1.255/25", "192.168.1.199/24", []any{true, true, false, false, true}},
			{"2001:db8::ffff/114", "2001:db8::8001/113", []any{true, true, false, false, true}},
			{"1.2.3.4", "::ffff:1.2.3.4", []any{false, false, false, false, false}},
			{nil, "::/0", []any{nil, nil, nil, nil, nil}},
		} {
			s := fixture.Select(q, inetSubnetNames, inetSubnetTypes, [][]any{tc.v}, tc.a, tc.z)
			s.CheckMetadata = inetSubnetMetadata
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		d := inetDiagnostic("inet", "bad", false)
		d.Context = "unnamed portal parameter $2 = '...'"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(q, d, "::1", "bad"))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, inetSubnetNames, inetSubnetTypes, [][]any{{true, true, false, false, true}}, "::1", "::/0"))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT count(*) AS n FROM subnet_smoke", []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}}))
		for _, s := range inetSubnetStoredSetup() {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		}
		inetSubnetStoredCheck(t, p, b)
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			inetSubnetStoredCheck(t, p, b)
			connector, err := stuntgres.NewDriver().OpenConnector("subnet-smoke-fresh")
			if err != nil {
				t.Fatal(err)
			}
			fresh := sql.OpenDB(connector)
			defer fresh.Close()
			if err := stuntgres.Restore(p.Ctx, fresh, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			recorded, recorder := fixture.InsertSelectRecordedPool(t, connector)
			inetSubnetStoredCheck(t, p, fixture.Backend{DB: recorded, Recorder: recorder})
		}
	}
}
