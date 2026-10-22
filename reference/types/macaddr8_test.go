package reference_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

const macaddr8Six = "08:00:2b:ff:fe:01:02:03"
const macaddr8Eight = "08:00:2b:01:02:03:04:05"

func macaddr8Scenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

func macaddr8TypeName(pg bool) string {
	if pg {
		return "" // lib/pq has no DatabaseTypeName entry for macaddr8 OID 774.
	}
	return "MACADDR8"
}

func macaddr8Select(pg bool, q string, columns, types []string, rows [][]any, args ...any) fixture.Step {
	s := fixture.Select(q, columns, types, rows, args...)
	if pg {
		// Keep raw codec validation independent of pq's missing catalog name.
		s.RawTypes = types
		s.Types = append([]string(nil), types...)
		for i, typ := range s.Types {
			if typ == "MACADDR8" {
				s.Types[i] = macaddr8TypeName(pg)
			}
		}
	}
	return s
}

func macaddr8Value(pg bool, q, value string, args ...any) fixture.Step {
	return macaddr8Select(pg, q, []string{"v"}, []string{"MACADDR8"}, [][]any{{value}}, args...)
}

func TestReferenceMACADDR8Values(t *testing.T) {
	macaddr8Scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE mac8_values(id integer,v macaddr8)", 0))
		q := "SELECT $1::macaddr8 AS v"
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
		var vectors [][2]string
		for _, input := range macaddrForms {
			vectors = append(vectors, [2]string{input, macaddr8Six}, [2]string{strings.ToUpper(input), macaddr8Six})
		}
		for _, input := range []string{"08:00:2b:01:02:03:04:05", "08-00-2b-01-02-03-04-05", "08002b:0102030405", "08002b-0102030405", "0800.2b01.0203.0405", "0800-2b01-0203-0405", "08002b01:02030405", "08002b0102030405"} {
			vectors = append(vectors, [2]string{input, macaddr8Eight}, [2]string{strings.ToUpper(input), macaddr8Eight})
		}
		vectors = append(vectors, [2]string{"0800:2b01:0203", macaddr8Six}, [2]string{"08:002b0102030405", macaddr8Eight}, [2]string{"08:00:2B:01:02:03:04:05", macaddr8Eight}, [2]string{"000000000000", "00:00:00:ff:fe:00:00:00"}, [2]string{"0000000000000000", "00:00:00:00:00:00:00:00"}, [2]string{"ffffffffffff", "ff:ff:ff:ff:fe:ff:ff:ff"}, [2]string{"ffffffffffffffff", "ff:ff:ff:ff:ff:ff:ff:ff"}, [2]string{"8001020304050607", "80:01:02:03:04:05:06:07"})
		for id, v := range vectors {
			for _, prepared := range []bool{false, true} {
				for _, query := range []string{"SELECT " + macaddrQuote(v[0]) + "::macaddr8 AS v", "SELECT MACADDR8 " + macaddrQuote(v[0]) + " AS v", "SELECT pg_catalog.macaddr8 " + macaddrQuote(v[0]) + " AS v", "SELECT CAST(" + macaddrQuote(v[0]) + " AS pg_catalog.macaddr8) AS v", "SELECT " + macaddrQuote(v[0]) + "::text::macaddr8 AS v"} {
					s := macaddr8Value(b.PG, query, v[1])
					s.Prepared = prepared
					run(s)
				}
				for _, arg := range []any{v[0], []byte(v[0]), macaddrValuer{v[0]}} {
					s := macaddr8Value(b.PG, q, v[1], arg)
					s.Prepared = prepared
					run(s)
				}
			}
			for _, arg := range []any{v[0], []byte(v[0]), macaddrValuer{v[0]}} {
				fixture.RunStep(t, p.Ctx, held, b.Recorder, macaddr8Value(b.PG, q, v[1], arg))
			}
			run(fixture.Exec("INSERT INTO mac8_values VALUES($1,$2)", 1, id, v[0]))
			run(macaddr8Value(b.PG, "SELECT v FROM mac8_values WHERE id=$1", v[1], id))
			run(macaddr8Value(b.PG, q, v[1], v[1]))
		}
		s := fixture.Select("SELECT '08002b010203'::macaddr8::text AS v", []string{"v"}, []string{"TEXT"}, [][]any{{macaddr8Six}})
		s.NonContext = true
		run(s)
	})
}

func TestReferenceMACADDR8Comparison(t *testing.T) {
	macaddr8Scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		pairs := []struct {
			left, right string
			equal, less bool
		}{
			{"00:00:00:00:00:00:00:ff", "00:00:00:00:00:00:01:00", false, true},
			{"00:00:00:00:ff:ff:ff:ff", "00:00:00:01:00:00:00:00", false, true},
			{"7f:ff:ff:ff:ff:ff:ff:ff", "80:00:00:00:00:00:00:00", false, true},
			{"0000000000000000", "ffffffffffffffff", false, true},
			{"08002b010203", macaddr8Six, true, false},
			{macaddr8Six, macaddr8Eight, false, false},
		}
		for _, pair := range pairs {
			for _, reverse := range []bool{false, true} {
				left, right, less := pair.left, pair.right, pair.less
				if reverse {
					left, right = right, left
					less = !pair.equal && !less
				}
				wants := []any{pair.equal, !pair.equal, less, less || pair.equal, !less && !pair.equal, !less, !pair.equal}
				for i, op := range []string{"=", "<>", "<", "<=", ">", ">=", "!="} {
					q := "SELECT $1::macaddr8 " + op + " $2::macaddr8 AS v"
					stmt, err := b.DB.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
					for _, args := range [][]any{{left, right}, {nil, right}, {left, nil}, {left, right}} {
						want := wants[i]
						if args[0] == nil || args[1] == nil {
							want = nil
						}
						s := fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{want}}, args...)
						run(s)
						s.Prepared = true
						run(s)
						s.Prepared = false
						fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
					}
					stmt.Close()
				}
			}
		}
		run(fixture.Exec("CREATE TABLE mac8_compare(id integer,v macaddr8)", 0))
		run(fixture.Exec("INSERT INTO mac8_compare VALUES(1,'08002b010203'),(2,'08002b0102030405'),(3,NULL)", 3))
		q := "SELECT id FROM mac8_compare WHERE v=$1 ORDER BY id"
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
		for _, frame := range []struct {
			arg  any
			rows [][]any
		}{{macaddr8Six, [][]any{{int64(1)}}}, {macaddr8Eight, [][]any{{int64(2)}}}, {nil, nil}, {"08002b010203", [][]any{{int64(1)}}}} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"id"}, []string{"INT4"}, frame.rows, frame.arg))
		}
		run(fixture.Select("SELECT id FROM mac8_compare WHERE v=NULL", []string{"id"}, []string{"INT4"}, nil))
		run(fixture.Select("SELECT id FROM mac8_compare WHERE v IS NULL", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}}))
	})
}

func TestReferenceMACADDR8Identity(t *testing.T) {
	macaddr8Scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Select("SELECT oid::bigint AS id,typname,typlen,typbyval,typalign,typstorage,typcategory,typispreferred,typcollation::bigint AS coll,typarray::bigint AS arr FROM pg_type WHERE oid=774", []string{"id", "typname", "typlen", "typbyval", "typalign", "typstorage", "typcategory", "typispreferred", "coll", "arr"}, []string{"INT8", "NAME", "INT2", "BOOL", "CHAR", "CHAR", "CHAR", "BOOL", "INT8", "INT8"}, [][]any{{int64(774), "macaddr8", int64(8), false, "i", "p", "U", false, int64(0), int64(775)}}))
		run(fixture.Select("SELECT oid::bigint AS id,typname,typelem::bigint AS elem FROM pg_type WHERE oid=775", []string{"id", "typname", "elem"}, []string{"INT8", "NAME", "INT8"}, [][]any{{int64(775), "_macaddr8", int64(774)}}))
		run(fixture.Select("SELECT typinput::oid::bigint AS input,typoutput::oid::bigint AS output,typreceive::oid::bigint AS receive,typsend::oid::bigint AS send FROM pg_type WHERE oid=774", []string{"input", "output", "receive", "send"}, []string{"INT8", "INT8", "INT8", "INT8"}, [][]any{{int64(4110), int64(4111), int64(3446), int64(3447)}}))
		for _, tc := range []struct {
			oid, proc int64
			name      string
			want      bool
		}{{3362, 4113, "macaddr8_eq", true}, {3363, 4118, "macaddr8_ne", false}, {3364, 4114, "macaddr8_lt", false}, {3365, 4115, "macaddr8_le", true}, {3366, 4116, "macaddr8_gt", false}, {3367, 4117, "macaddr8_ge", true}} {
			run(fixture.Select(fmt.Sprintf("SELECT oid::bigint,oprleft::bigint,oprright::bigint,oprresult::bigint,oprcode::oid::bigint FROM pg_operator WHERE oid=%d", tc.oid), []string{"oid", "oprleft", "oprright", "oprresult", "oprcode"}, []string{"INT8", "INT8", "INT8", "INT8", "INT8"}, [][]any{{tc.oid, int64(774), int64(774), int64(16), tc.proc}}))
			run(fixture.Select("SELECT proname,pronargs,prorettype::bigint,proisstrict,provolatile FROM pg_proc WHERE oid=$1::oid", []string{"proname", "pronargs", "prorettype", "proisstrict", "provolatile"}, []string{"NAME", "INT2", "INT8", "BOOL", "CHAR"}, [][]any{{tc.name, int64(2), int64(16), true, "i"}}, tc.proc))
			for _, arg := range []any{macaddr8Six, nil} {
				var want any = tc.want
				if arg == nil {
					want = nil
				}
				run(fixture.Select("SELECT "+tc.name+"($1::macaddr8,$2::macaddr8) AS v", []string{"v"}, []string{"BOOL"}, [][]any{{want}}, "08002b010203", arg))
			}
		}
	})
}

func TestReferenceMACADDR8Boundaries(t *testing.T) {
	macaddr8Scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		if b.PG {
			return
		} // Native scalar-only limits, not PostgreSQL limitations.
		for _, tc := range []struct{ q, message, position string }{{"CREATE TABLE excluded(v macaddr8[])", "unsupported column type", "25"}, {"SELECT '{}'::macaddr8[]", "unsupported column type", "14"}, {"SELECT ARRAY[MACADDR8 '08002b010203']", "unsupported array element type", "8"}} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "0A000", Message: tc.message, Position: tc.position})
				s.Prepared = prepared
				s.SelectRows = strings.HasPrefix(tc.q, "SELECT")
				s.ErrorStage = "execute" // Utility declarations bind at execution.
				if prepared && s.SelectRows {
					s.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
		}
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE mac8_boundary(v macaddr8)", 0))
		for _, tc := range []struct{ q, code, message, hint string }{
			{"SELECT v FROM mac8_boundary ORDER BY v", "42883", "could not identify an ordering operator for type macaddr8", "Use an explicit ordering operator or modify the query."},
			{"CREATE INDEX mac8_btree ON mac8_boundary(v)", "42704", "data type macaddr8 has no default operator class for access method \"btree\"", "You must specify an operator class for the index or define a default operator class for the data type."},
		} {
			d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
			d.Hint = tc.hint
			selectRows := strings.HasPrefix(tc.q, "SELECT")
			if selectRows {
				d.Position = fixture.Position(strings.LastIndex(tc.q, "v") + 1)
			}
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(tc.q, d)
				s.Prepared, s.SelectRows = prepared, selectRows
				s.ErrorStage = "execute"
				if prepared && selectRows {
					s.ErrorStage = "prepare"
				}
				run(s)
			}
		}
		conn, err := p.Native.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		// Embedded COPY resolves codecs on non-NULL datum demand, unlike
		// protocol COPY's eager declared-provider initialization.
		run(fixture.Exec("INSERT INTO mac8_boundary VALUES('08002b010203')", 1))
		payload := []byte("PGCOPY\n\xff\r\n\x00")
		payload = binary.BigEndian.AppendUint32(payload, 0) // Flags.
		payload = binary.BigEndian.AppendUint32(payload, 0) // Header extension.
		payload = binary.BigEndian.AppendUint16(payload, 1) // One field.
		payload = binary.BigEndian.AppendUint32(payload, 8)
		payload = append(payload, 8, 0, 43, 255, 254, 1, 2, 3)
		payload = binary.BigEndian.AppendUint16(payload, 0xffff) // Trailer.
		for _, input := range []bool{false, true} {
			var result stuntgres.CopyResult
			var err error
			direction := "output"
			if input {
				direction = "input"
				result, err = stuntgres.CopyFrom(p.Ctx, conn, "COPY mac8_boundary FROM STDIN (FORMAT binary)", bytes.NewReader(payload))
			} else {
				var output bytes.Buffer
				result, err = stuntgres.CopyTo(p.Ctx, conn, "COPY mac8_boundary TO STDOUT (FORMAT binary)", &output)
			}
			d := fixture.ControlsReferenceDiagnostic("0A000", "binary "+direction+" is not supported for type macaddr8")
			if input {
				d.Context = "COPY mac8_boundary, line 1, column v"
			}
			fixture.AssertDiagnostic(t, err, &d)
			if result != (stuntgres.CopyResult{}) {
				t.Fatal("failed binary COPY returned success", result)
			}
		}
	})
}
