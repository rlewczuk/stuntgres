package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSmokeBitwiseSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			ctx, db := p.Ctx, backend.DB
			exec := func(q string, want int64) {
				t.Helper()
				fixture.RunStep(t, ctx, db, backend.Recorder, fixture.Exec(q, want))
			}
			check := func(q string, types []string, want [][]any, args ...any) {
				t.Helper()
				columns := make([]string, len(types))
				for i := range columns {
					columns[i] = "?column?"
				}
				// Physical projection labels remain independently checked by RunStep.
				if strings.HasPrefix(q, "SELECT s,b FROM null_write") {
					columns = []string{"s", "b"}
				}
				if strings.HasPrefix(q, "SELECT id,s FROM atomic") {
					columns = []string{"id", "s"}
				}
				if strings.HasPrefix(q, "SELECT s,i,b,n,w FROM defaults") {
					columns = []string{"s", "i", "b", "n", "w"}
				}
				if strings.HasPrefix(q, "SELECT ns,ss,mixed FROM masks") {
					columns = []string{"ns", "ss", "mixed"}
				}
				if strings.HasPrefix(q, "SELECT s FROM ") {
					columns = []string{"s"}
				}
				step := fixture.Select(q, columns, types, want, args...)
				step.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
					meta, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					for _, m := range meta {
						wantScan := reflect.TypeFor[int64]()
						if backend.PG {
							switch m.DatabaseTypeName() {
							case "INT2":
								wantScan = reflect.TypeFor[int16]()
							case "INT4":
								wantScan = reflect.TypeFor[int32]()
							}
						}
						if m.ScanType() != wantScan {
							t.Fatal(m)
						}
						if strings.Contains(q, " FROM t ") {
							if _, known := m.Nullable(); known {
								t.Fatal("computed nullability became physical")
							}
						}
					}
				}
				fixture.RunStep(t, ctx, db, backend.Recorder, step)
				fixture.RunStep(t, ctx, db, backend.Recorder, fixture.Prepared(step))
			}
			exec("CREATE TABLE t(id INTEGER,s SMALLINT,i INTEGER,b BIGINT)", 0)
			exec("INSERT INTO t VALUES(1,1,1,1),(2,-8,-8,-8),(3,-32768,-2147483648,-9223372036854775808),(4,32767,2147483647,9223372036854775807),(5,NULL,NULL,NULL)", 5)
			for _, tc := range []struct{ cast, typ string }{{"smallint", "INT2"}, {"integer", "INT4"}, {"bigint", "INT8"}} {
				q := "SELECT 91::" + tc.cast + " & 15::" + tc.cast + ",32::" + tc.cast + " | 3::" + tc.cast + ",17::" + tc.cast + " # 5::" + tc.cast + ",~1::" + tc.cast + ",1::" + tc.cast + " << 4,8::" + tc.cast + " >> 2 FROM t WHERE id=1"
				check(q, []string{tc.typ, tc.typ, tc.typ, tc.typ, tc.typ, tc.typ}, [][]any{{int64(11), int64(35), int64(20), int64(-2), int64(16), int64(2)}})
			}
			check("SELECT s & 15::smallint,i | 3,b # 5,~s,s << 1,b >> 2 FROM t ORDER BY id", []string{"INT2", "INT4", "INT8", "INT2", "INT2", "INT8"}, [][]any{
				{int64(1), int64(3), int64(4), int64(-2), int64(2), int64(0)},
				{int64(8), int64(-5), int64(-3), int64(7), int64(-16), int64(-2)},
				{int64(0), int64(-2147483645), int64(-9223372036854775803), int64(32767), int64(0), int64(-2305843009213693952)},
				{int64(15), int64(2147483647), int64(9223372036854775802), int64(-32768), int64(-2), int64(2305843009213693951)},
				{nil, nil, nil, nil, nil, nil},
			})
			check("SELECT s << 32,i << -1,b << 64 FROM t WHERE id=1", []string{"INT2", "INT4", "INT8"}, [][]any{{int64(1), int64(-2147483648), int64(1)}})
			check("SELECT ~s,i & 1,b >> 1 FROM t WHERE FALSE", []string{"INT2", "INT4", "INT8"}, [][]any{})
			held, err := db.PrepareContext(ctx, "SELECT s & $1,b << $2 FROM t WHERE id=1")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			heldExecutor := fixture.HeldStatement{db, held, "SELECT s & $1,b << $2 FROM t WHERE id=1"}
			for _, a := range []any{3, nil, 1} {
				want := any(int64(1))
				if a == nil {
					want = nil
				}
				fixture.RunStep(t, ctx, heldExecutor, backend.Recorder, fixture.Select(heldExecutor.SQL, []string{"?column?", "?column?"}, []string{"INT2", "INT8"}, [][]any{{want, int64(2)}}, a, 1))
			}
			exec("UPDATE t SET i=i | 4 WHERE i & 1=1", 2)
			exec("DELETE FROM t WHERE id=2 AND i & 1=0", 1)
			for _, tc := range []struct {
				q    string
				want stuntgres.Error
			}{
				{"SELECT '1' & '2' FROM t", stuntgres.Error{Code: "42725", Message: "operator is not unique: unknown & unknown", Position: "12", Hint: "Could not choose a best candidate operator. You might need to add explicit type casts."}},
				{"SELECT ~true FROM t", stuntgres.Error{Code: "42883", Message: "operator does not exist: ~ boolean", Position: "8", Hint: "No operator matches the given name and argument type. You might need to add an explicit type cast."}},
				{"SELECT 1::bigint << 1::bigint FROM t", stuntgres.Error{Code: "42883", Message: "operator does not exist: bigint << bigint", Position: "18", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}},
				{"SELECT NULL::integer & 'bad' FROM t", stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Position: "24"}},
			} {
				step := fixture.ImplicitError(tc.q, fixture.MigrationDiagnostic(tc.want))
				step.SelectRows, step.ErrorStage = true, "execute"
				fixture.RunStep(t, ctx, db, backend.Recorder, step)
				step.ErrorStage = "prepare"
				fixture.RunStep(t, ctx, db, backend.Recorder, fixture.Prepared(step))
			}
			exec("CREATE TABLE atomic(id INTEGER,s SMALLINT)", 0)
			exec("INSERT INTO atomic VALUES(99,99)", 1)
			exec("CREATE TABLE source(id INTEGER,x TEXT)", 0)
			exec("INSERT INTO source VALUES(1,'1'),(2,'bad')", 2)
			check("SELECT NULL::integer & CAST(x AS integer),CAST(x AS bigint) >> NULL::integer,NULL::smallint << CAST(x AS integer) FROM source ORDER BY id", []string{"INT4", "INT8", "INT2"}, [][]any{{nil, nil, nil}, {nil, nil, nil}})
			exec("CREATE TABLE null_write(id INTEGER,s SMALLINT,b BIGINT)", 0)
			exec("INSERT INTO null_write SELECT id,NULL::smallint << CAST(x AS integer),CAST(x AS bigint) | NULL::bigint FROM source ORDER BY id", 2)
			exec("UPDATE null_write SET b=NULL::bigint >> id", 2)
			check("SELECT s,b FROM null_write ORDER BY id", []string{"INT2", "INT8"}, [][]any{{nil, nil}, {nil, nil}})
			failure := fixture.ImplicitError("INSERT INTO atomic SELECT id,CAST(x AS INTEGER) & 1 FROM source ORDER BY id", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`})
			failure.ErrorStage = "execute"
			fixture.RunStep(t, ctx, db, backend.Recorder, failure)
			check("SELECT id,s FROM atomic", []string{"INT4", "INT2"}, [][]any{{int64(99), int64(99)}})
			exec("INSERT INTO atomic VALUES(1,1),(2,2)", 2)
			failure = fixture.ImplicitError("UPDATE atomic SET s=CASE WHEN id=2 THEN 1::bigint << 16 ELSE s::bigint & 1 END", fixture.Diagnostic{Code: "22003", Message: "smallint out of range"})
			failure.ErrorStage = "execute"
			fixture.RunStep(t, ctx, db, backend.Recorder, failure)
			check("SELECT id,s FROM atomic ORDER BY id", []string{"INT4", "INT2"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(99), int64(99)}})
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			if _, err := held.QueryContext(canceled, 1, 1); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(canceled, "INSERT INTO atomic VALUES(4,1::smallint<<2)"); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			exec("CREATE TABLE defaults(id INTEGER,s SMALLINT DEFAULT (1::smallint << 15),i INTEGER DEFAULT (~0),b BIGINT DEFAULT (1::bigint << 63),n SMALLINT DEFAULT (~NULL::smallint),w BIGINT DEFAULT (32767::smallint << 1::smallint))", 0)
			exec("INSERT INTO defaults(id) VALUES(1)", 1)
			exec("INSERT INTO defaults VALUES(2,DEFAULT,DEFAULT,DEFAULT,DEFAULT,DEFAULT)", 1)
			exec("CREATE VIEW masks AS SELECT id,~s AS ns,s << 32 AS ss,s & b AS mixed FROM defaults", 0)
			check("SELECT s,i,b,n,w FROM defaults ORDER BY id", []string{"INT2", "INT4", "INT8", "INT2", "INT8"}, [][]any{{int64(-32768), int64(-1), int64(-9223372036854775808), nil, int64(-2)}, {int64(-32768), int64(-1), int64(-9223372036854775808), nil, int64(-2)}})
			if backend.PG {
				return
			} // Public catalog controls and replacement are native-only.
			var dump bytes.Buffer
			if err := stuntgres.Dump(ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(canceled, p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			exec("INSERT INTO defaults(id) VALUES(3)", 1)
			check("SELECT ns,ss,mixed FROM masks ORDER BY id", []string{"INT2", "INT2", "INT8"}, [][]any{{int64(32767), int64(-32768), int64(-9223372036854775808)}, {int64(32767), int64(-32768), int64(-9223372036854775808)}, {int64(32767), int64(-32768), int64(-9223372036854775808)}})
			if err := stuntgres.Restore(ctx, p.Native, strings.NewReader("CREATE TABLE replacement(i INTEGER); INSERT INTO replacement VALUES(1 & 1); INSERT INTO replacement VALUES(~true)")); err == nil {
				t.Fatal("invalid replacement installed")
			}
			check("SELECT s FROM defaults WHERE id=3", []string{"INT2"}, [][]any{{int64(-32768)}})
			if err := stuntgres.Restore(ctx, p.Native, strings.NewReader("CREATE TABLE replacement(s SMALLINT); INSERT INTO replacement VALUES(1::smallint << 15)")); err != nil {
				t.Fatal(err)
			}
			check("SELECT s FROM replacement", []string{"INT2"}, [][]any{{int64(-32768)}})
		})
	}
}
