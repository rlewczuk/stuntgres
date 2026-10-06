//go:build reference

package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

const uuidA = "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11"
const uuidZ = "00000000-0000-0000-0000-000000000000"
const uuidF = "ffffffff-ffff-ffff-ffff-ffffffffffff"

func TestReferenceUUIDCreateInputAndOrder(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE u(v UUID)", 0))
	p.step(t, referenceExec("INSERT INTO u(v) VALUES(NULL),('ffffffff-ffff-ffff-ffff-ffffffffffff'),('{A0EEBC99-9C0B-4EF8-BB6D-6BB9BD380A11}'),('00000000000000000000000000000000'),('a0eebc999c0b4ef8bb6d6bb9bd380a11')", 5))
	p.step(t, referenceSelect("SELECT v FROM u ORDER BY v", []string{"v"}, []string{"UUID"}, [][]any{{uuidZ}, {uuidA}, {uuidA}, {uuidF}, {nil}}))
	p.step(t, referencePrepared(referenceSelect("SELECT v FROM u WHERE v=$1", []string{"v"}, []string{"UUID"}, [][]any{{uuidA}, {uuidA}}, "{A0EEBC999C0B4EF8BB6D6BB9BD380A11}")))
}

func TestReferenceUUIDNoncanonicalGroupedForms(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE u(v UUID)", 0))
	for _, prepared := range []bool{false, true} {
		for _, form := range []string{
			"a0ee-bc99-9c0b-4ef8-bb6d-6bb9-bd38-0a11",
			"{a0eebc99-9c0b4ef8-bb6d6bb9-bd380a11}",
		} {
			for _, step := range []referenceStep{
				referenceExec("INSERT INTO u VALUES('"+form+"')", 1),
				referenceExec("INSERT INTO u VALUES($1)", 1, form),
				referenceExec("INSERT INTO u VALUES($1)", 1, []byte(form)),
			} {
				step.prepared = prepared
				p.step(t, step)
			}
		}
	}
	want := make([][]any, 12)
	for i := range want {
		want[i] = []any{uuidA}
	}
	p.step(t, referenceSelect("SELECT v FROM u ORDER BY v", []string{"v"}, []string{"UUID"}, want))
	for _, prepared := range []bool{false, true} {
		for _, form := range []string{
			"a0ee-bc99-9c0b-4ef8-bb6d-6bb9-bd38-0a11",
			"{a0eebc99-9c0b4ef8-bb6d6bb9-bd380a11}",
		} {
			for _, step := range []referenceStep{
				referenceSelect("SELECT v FROM u WHERE v='"+form+"'", []string{"v"}, []string{"UUID"}, want),
				referenceSelect("SELECT v FROM u WHERE v=$1", []string{"v"}, []string{"UUID"}, want, form),
				referenceSelect("SELECT v FROM u WHERE v=$1", []string{"v"}, []string{"UUID"}, want, []byte(form)),
			} {
				step.prepared = prepared
				p.step(t, step)
			}
		}
	}
}

func TestReferenceUUIDBindingsAndRawMetadata(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE u(v UUID)", 0))
	for _, prepared := range []bool{false, true} {
		for _, arg := range []any{"{A0EEBC999C0B4EF8BB6D6BB9BD380A11}", []byte("ffffffffffffffffffffffffffffffff"), nil} {
			s := referenceExec("INSERT INTO u VALUES($1)", 1, arg)
			s.prepared = prepared
			p.step(t, s)
		}
	}
	p.step(t, referenceSelect("SELECT v FROM u ORDER BY v DESC", []string{"v"}, []string{"UUID"}, [][]any{{nil}, {nil}, {uuidF}, {uuidF}, {uuidA}, {uuidA}}))
	p.step(t, referenceSelect("SELECT v FROM u WHERE v=NULL", []string{"v"}, []string{"UUID"}, nil))
	p.step(t, referencePrepared(referenceSelect("SELECT v FROM u WHERE v=$1", []string{"v"}, []string{"UUID"}, nil, nil)))
	p.step(t, referenceExec("CREATE TABLE repeats(a UUID,b UUID)", 0))
	for _, arg := range []any{uuidA, nil} {
		p.step(t, referencePrepared(referenceExec("INSERT INTO repeats VALUES($1,$1)", 1, arg)))
	}
	p.step(t, referenceSelect("SELECT * FROM repeats ORDER BY a", []string{"a", "b"}, []string{"UUID", "UUID"}, [][]any{{uuidA, uuidA}, {nil, nil}}))
	for _, backend := range []struct {
		db *sql.DB
		pg bool
	}{{p.memory, false}, {p.postgres, true}} {
		for _, prepared := range []bool{false, true} {
			query := func(q string) (*sql.Rows, error) {
				if !prepared {
					return backend.db.QueryContext(p.ctx, q)
				}
				s, err := backend.db.PrepareContext(p.ctx, q)
				if err != nil {
					return nil, err
				}
				defer s.Close()
				return s.QueryContext(p.ctx)
			}
			for _, q := range []string{"SELECT v FROM u ORDER BY v", "SELECT v FROM u WHERE v=NULL"} {
				rows, err := query(q)
				if err != nil {
					t.Fatal(err)
				}
				types, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				scan := reflect.TypeFor[string]()
				if backend.pg {
					scan = reflect.TypeFor[any]()
				}
				if types[0].DatabaseTypeName() != "UUID" || types[0].ScanType() != scan {
					t.Fatal(types[0])
				}
				if _, ok := types[0].Length(); ok {
					t.Fatal("length")
				}
				if _, _, ok := types[0].DecimalSize(); ok {
					t.Fatal("decimal size")
				}
				if !backend.pg {
					n, k := types[0].Nullable()
					if !n || !k {
						t.Fatal(n, k)
					}
				}
				var retained []byte
				var count int
				want := []any{uuidA, uuidA, uuidF, uuidF, nil, nil}
				if q == "SELECT v FROM u WHERE v=NULL" {
					want = nil
				}
				for rows.Next() {
					var raw any
					if err := rows.Scan(&raw); err != nil {
						t.Fatal(err)
					}
					if count >= len(want) {
						t.Fatalf("%s returned unexpected row %d: %T %v", q, count+1, raw, raw)
					}
					if want[count] == nil {
						if raw != nil {
							t.Fatalf("row %d got %T %v, want NULL", count+1, raw, raw)
						}
					} else if backend.pg {
						b, ok := raw.([]byte)
						if !ok || string(b) != want[count] {
							t.Fatalf("row %d got %T %v, want canonical bytes %v", count+1, raw, raw, want[count])
						}
						if count == 0 {
							retained = b
						}
					} else if text, ok := raw.(string); !ok || text != want[count] {
						t.Fatalf("row %d got %T %v, want canonical string %v", count+1, raw, raw, want[count])
					}
					count++
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				rows.Close()
				if count != len(want) {
					t.Fatalf("%s returned %d rows, want %d", q, count, len(want))
				}
				if backend.pg && len(want) > 0 && string(retained) != uuidA {
					t.Fatal("first pq UUID buffer changed or was not retained")
				}
			}
		}
	}
}

func TestReferenceUUIDDiagnosticsAndAtomicity(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE u(v UUID)", 0))
	for _, tc := range []struct{ text string }{{"bad"}, {""}, {" a0eebc999c0b4ef8bb6d6bb9bd380a11"}, {"a0eebc999c0b4ef8bb6d6bb9bd380a11-"}, {"{a0eebc999c0b4ef8bb6d6bb9bd380a11"}, {"urn:uuid:a0eebc999c0b4ef8bb6d6bb9bd380a11"}} {
		for _, prefix := range []string{"", "/*é*/ "} {
			for _, q := range []string{"INSERT INTO u(v) VALUES('" + tc.text + "')", "SELECT v FROM u WHERE v='" + tc.text + "'"} {
				pos := "25"
				if prefix != "" {
					pos = "31"
				}
				s := referenceImplicitError(prefix+q, referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type uuid: "` + tc.text + `"`, Position: pos})
				s.selectRows = strings.HasPrefix(q, "SELECT")
				s.errorStage = "execute"
				p.step(t, s)
				s.errorStage = "prepare"
				p.step(t, referencePrepared(s))
			}
		}
	}
	for _, arg := range []any{"bad", []byte("bad"), 1, true} {
		text := "bad"
		if arg == 1 {
			text = "1"
		}
		if arg == true {
			text = "true"
		}
		for _, q := range []string{"INSERT INTO u(v) VALUES($1)", "SELECT v FROM u WHERE v=$1"} {
			s := referenceImplicitError(q, referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type uuid: "` + text + `"`}, arg)
			s.selectRows = strings.HasPrefix(q, "SELECT")
			s.errorStage = "execute"
			pg := *s.diagnostic
			pg.Context = "unnamed portal parameter $1 = '...'"
			s.postgresDiagnostic = &pg
			p.step(t, s)
			p.step(t, referencePrepared(s))
		}
	}
	for _, tc := range []struct{ q, code, message, hint, pos string }{
		{"INSERT INTO u(v) VALUES(1)", "42804", `column "v" is of type uuid but expression is of type integer`, "You will need to rewrite or cast the expression.", "25"},
		{"INSERT INTO u(v) VALUES(TRUE)", "42804", `column "v" is of type uuid but expression is of type boolean`, "You will need to rewrite or cast the expression.", "25"},
		{"SELECT v FROM u WHERE v=1", "42883", "operator does not exist: uuid = integer", "No operator matches the given name and argument types. You might need to add explicit type casts.", "24"},
		{"SELECT v FROM u WHERE v=TRUE", "42883", "operator does not exist: uuid = boolean", "No operator matches the given name and argument types. You might need to add explicit type casts.", "24"},
		{"SELECT v FROM u WHERE v", "42804", "argument of WHERE must be type boolean, not type uuid", "", "23"},
	} {
		s := referenceImplicitError(tc.q, referenceDiagnostic{Code: tc.code, Message: tc.message, Hint: tc.hint, Position: tc.pos})
		s.selectRows = strings.HasPrefix(tc.q, "SELECT")
		s.errorStage = "execute"
		p.step(t, s)
		s.errorStage = "prepare"
		p.step(t, referencePrepared(s))
	}
	p.step(t, referenceExec("CREATE TABLE required(id INT NOT NULL,v UUID NOT NULL)", 0))
	p.step(t, referenceExec("INSERT INTO required VALUES(99,$1)", 1, uuidA))
	for _, tc := range []struct {
		q          string
		args       []any
		diagnostic referenceDiagnostic
		stage      string
	}{
		{"INSERT INTO required VALUES(1,'00000000000000000000000000000000'),(2,'bad')", nil, referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type uuid: "bad"`, Position: "70"}, "prepare"},
		{"INSERT INTO required VALUES(1,$1),(2,$2)", []any{uuidZ, "bad"}, referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type uuid: "bad"`}, "execute"},
		{"INSERT INTO required VALUES(1,'00000000000000000000000000000000'),(2,NULL)", nil, referenceDiagnostic{Code: "23502", Message: `null value in column "v" of relation "required" violates not-null constraint`, Detail: "Failing row contains (2, null).", Schema: "public", Table: "required", Column: "v"}, "execute"},
		{"INSERT INTO required VALUES(NULL,$1)", []any{"{A0EEBC999C0B4EF8BB6D6BB9BD380A11}"}, referenceDiagnostic{Code: "23502", Message: `null value in column "id" of relation "required" violates not-null constraint`, Detail: "Failing row contains (null, " + uuidA + ").", Schema: "public", Table: "required", Column: "id"}, "execute"},
	} {
		s := referenceImplicitError(tc.q, tc.diagnostic, tc.args...)
		s.errorStage = "execute"
		if tc.diagnostic.Code == "22P02" && len(tc.args) > 0 {
			pg := *s.diagnostic
			pg.Context = "unnamed portal parameter $2 = '...'"
			s.postgresDiagnostic = &pg
		}
		p.step(t, s)
		s.errorStage = tc.stage
		p.step(t, referencePrepared(s))
		p.step(t, referenceSelect("SELECT * FROM required", []string{"id", "v"}, []string{"INT4", "UUID"}, [][]any{{int64(99), uuidA}}))
	}
}

func TestReferenceUUIDControls(t *testing.T) {
	p := newReferencePair(t)
	script := "CREATE TABLE empty(v UUID); CREATE TABLE saved(v UUID NOT NULL,n UUID); INSERT INTO saved VALUES('00000000000000000000000000000000',NULL),('{A0EEBC999C0B4EF8BB6D6BB9BD380A11}','ffffffffffffffffffffffffffffffff')"
	if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(script)); err != nil {
		t.Fatal(err)
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(dump.String(), err)
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &redump); err != nil {
		t.Fatal(redump.String(), err)
	}
	if _, err := p.postgres.ExecContext(p.ctx, redump.String()); err != nil {
		t.Fatal(err)
	}
	p.step(t, referenceSelect("SELECT * FROM saved ORDER BY v", []string{"v", "n"}, []string{"UUID", "UUID"}, [][]any{{uuidZ, nil}, {uuidA, uuidF}}))
	p.step(t, referenceSelect("SELECT * FROM empty", []string{"v"}, []string{"UUID"}, nil))
	// Verify the restored identity through behavior, not incidental dump layout.
	p.step(t, referenceImplicitError("INSERT INTO saved VALUES(NULL,NULL)", referenceDiagnostic{Code: "23502", Message: `null value in column "v" of relation "saved" violates not-null constraint`, Detail: "Failing row contains (null, null).", Schema: "public", Table: "saved", Column: "v"}))
	p.step(t, referenceExec("ALTER TABLE saved DROP CONSTRAINT saved_v_not_null", 0))
	p.step(t, referenceExec("INSERT INTO saved VALUES(NULL,NULL)", 1))
	p.step(t, referenceSelect("SELECT * FROM saved WHERE v=$1", []string{"v", "n"}, []string{"UUID", "UUID"}, [][]any{{uuidA, uuidF}}, uuidA))
}

func TestReferenceUUIDAuthorization(t *testing.T) {
	roleReferenceCleanup(t, []string{"uuid_reader"})
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE ROLE uuid_reader", 0))
	// The fixture recreates public without PUBLIC privileges. Match native public
	// namespace visibility before testing the independently denied table rights.
	if _, err := referenceAdmin.ExecContext(p.ctx, "GRANT USAGE, CREATE ON SCHEMA public TO PUBLIC"); err != nil {
		t.Fatal(err)
	}
	p.step(t, referenceExec("CREATE TABLE u(v UUID)", 0))
	p.step(t, referenceExec("INSERT INTO u VALUES($1)", 1, uuidA))
	for _, backend := range []struct {
		db     *sql.DB
		record *referenceRecorder
		pg     bool
	}{{p.memory, p.memoryRecord, false}, {p.postgres, p.postgresRecord, true}} {
		c, err := backend.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		held, err := c.PrepareContext(p.ctx, "SELECT v FROM u")
		if err != nil {
			t.Fatal(err)
		}
		referenceRunStep(t, p.ctx, c, backend.record, referenceExec("SET SESSION AUTHORIZATION uuid_reader", 0))
		rows, err := held.QueryContext(p.ctx)
		if rows != nil {
			rows.Close()
		}
		want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table u"}
		referenceAssertDiagnostic(t, err, &want)
		held.Close()
		for _, prepared := range []bool{false, true} {
			for _, q := range []string{"SELECT v FROM u", "INSERT INTO u VALUES('a0eebc999c0b4ef8bb6d6bb9bd380a11')", "INSERT INTO u VALUES($1)"} {
				s := referenceImplicitError(q, referenceDiagnostic{Code: "42501", Message: "permission denied for table u"})
				s.prepared = prepared
				s.selectRows = strings.HasPrefix(q, "SELECT")
				s.errorStage = "execute"
				if strings.Contains(q, "$1") {
					s.args = []any{uuidA}
				}
				referenceRunStep(t, p.ctx, c, backend.record, s)
			}
			s := referenceImplicitError("INSERT INTO u VALUES($1)", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type uuid: "bad"`}, "bad")
			s.prepared = prepared
			s.errorStage = "execute"
			if backend.pg {
				pg := *s.diagnostic
				pg.Context = "unnamed portal parameter $1 = '...'"
				s.diagnostic = &pg
			}
			referenceRunStep(t, p.ctx, c, backend.record, s)
		}
		referenceRunStep(t, p.ctx, c, backend.record, referenceExec("RESET SESSION AUTHORIZATION", 0))
		c.Close()
	}
	p.step(t, referenceSelect("SELECT v FROM u", []string{"v"}, []string{"UUID"}, [][]any{{uuidA}}))
}

func TestReferenceUUIDOrderingAndNullMetadata(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE u(v UUID)", 0))
	p.step(t, referenceExec("INSERT INTO u VALUES('80000000000000000000000000000000'),('00000100000000000000000000000000'),('00000001000000000000000000000000'),('7fffffffffffffffffffffffffffffff'),('00000000000000000000000000000001'),('00000000000000000000000000000000')", 6))
	p.step(t, referenceSelect("SELECT v FROM u ORDER BY v", []string{"v"}, []string{"UUID"}, [][]any{{uuidZ}, {"00000000-0000-0000-0000-000000000001"}, {"00000001-0000-0000-0000-000000000000"}, {"00000100-0000-0000-0000-000000000000"}, {"7fffffff-ffff-ffff-ffff-ffffffffffff"}, {"80000000-0000-0000-0000-000000000000"}}))
	p.step(t, referenceExec("CREATE TABLE nulls(v UUID)", 0))
	p.step(t, referenceExec("INSERT INTO nulls VALUES(NULL)", 1))
	p.step(t, referencePrepared(referenceSelect("SELECT v,v FROM nulls", []string{"v", "v"}, []string{"UUID", "UUID"}, [][]any{{nil, nil}})))
	p.step(t, referenceExec("CREATE TABLE required(v UUID NOT NULL)", 0))
	p.step(t, referenceSelect("SELECT * FROM required", []string{"v"}, []string{"UUID"}, nil))
	for _, backend := range []struct {
		db *sql.DB
		pg bool
	}{{p.memory, false}, {p.postgres, true}} {
		for _, q := range []string{"SELECT * FROM nulls", "SELECT * FROM required"} {
			rows, err := backend.db.QueryContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			types, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			scan := reflect.TypeFor[string]()
			if backend.pg {
				scan = reflect.TypeFor[any]()
			}
			if types[0].DatabaseTypeName() != "UUID" || types[0].ScanType() != scan {
				t.Fatal(types[0])
			}
			if !backend.pg {
				n, k := types[0].Nullable()
				if !k || n != (q == "SELECT * FROM nulls") {
					t.Fatal(n, k)
				}
			}
			if rows.Next() {
				var raw any
				if err := rows.Scan(&raw); err != nil || raw != nil {
					t.Fatal(raw, err)
				}
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			rows.Close()
		}
	}
}
