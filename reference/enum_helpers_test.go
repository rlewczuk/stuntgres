package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func enumReferenceScenario(t *testing.T, body func(*testing.T, *referencePair, referenceBackend, *sql.Conn)) {
	t.Helper()
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			conn, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			body(t, p, backend, conn)
		})
	}
}

func enumReferenceSetup(t *testing.T, p *referencePair, backend referenceBackend, conn *sql.Conn) {
	t.Helper()
	for _, q := range []string{
		"CREATE TYPE mood AS ENUM ('sad','ok','happy')",
		"CREATE TYPE happiness AS ENUM ('happy','ok','sad')",
		"CREATE TABLE enum_rows(id integer,m mood,h happiness)",
	} {
		referenceRunStep(t, p.ctx, conn, backend.recorder, referenceExec(q, 0))
	}
	referenceRunStep(t, p.ctx, conn, backend.recorder, referenceExec("INSERT INTO enum_rows VALUES (1,'happy','sad'),(2,'sad','happy'),(3,'ok','ok'),(4,NULL,NULL)", 4))
}

// Enum columns opt into exact transport checks; named builtins retain the
// fixture's generic validator. Both scanned and recorder-owned values are checked.
func enumReferenceRunStep(t *testing.T, ctx context.Context, db referenceExecutor, recorder *referenceRecorder, s referenceStep) referenceObserved {
	t.Helper()
	s.checkRaw = func(t *testing.T, column, row int, value any) bool {
		t.Helper()
		if s.types[column] != "" {
			return false
		}
		if s.bag || row >= len(s.rows) {
			t.Fatal("enum raw check requires independently ordered expected rows")
		}
		want := s.rows[row][column]
		if want == nil {
			if value != nil {
				t.Fatalf("enum raw column=%d row=%d got %T %#v, want NULL", column, row, value, value)
			}
			return true
		}
		label, ok := want.(string)
		if !ok {
			t.Fatalf("enum expected label has type %T", want)
		}
		if recorder.rawBackend == referenceRawPostgres {
			bytes, ok := value.([]byte)
			if !ok || bytes == nil || string(bytes) != label {
				t.Fatalf("enum raw column=%d row=%d got %T %#v, want nonnil []byte(%q)", column, row, value, value, label)
			}
		} else if text, ok := value.(string); !ok || text != label {
			t.Fatalf("enum raw column=%d row=%d got %T %#v, want string %q", column, row, value, value, label)
		}
		return true
	}
	return referenceRunStep(t, ctx, db, recorder, s)
}

// Pinned PostgreSQL18/pq exposes unknown enum OIDs as interface{} with
// unknown flags. Native exposes string and preserves stored-column nullability.
func enumReferenceMetadata(pg, stored bool) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, rows *sql.Rows) {
		t.Helper()
		types, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if len(types) != 1 {
			t.Fatalf("enum metadata columns got %d want 1", len(types))
		}
		typ := types[0]
		length, lengthKnown := typ.Length()
		precision, scale, decimalKnown := typ.DecimalSize()
		nullable, nullableKnown := typ.Nullable()
		scan := reflect.TypeFor[string]()
		if pg {
			scan = reflect.TypeFor[any]()
		}
		wantNullable := !pg && stored
		if typ.Name() != "v" || typ.DatabaseTypeName() != "" || typ.ScanType() != scan || length != 0 || lengthKnown || precision != 0 || scale != 0 || decimalKnown || nullable != wantNullable || nullableKnown != wantNullable {
			t.Fatalf("enum metadata got name=%q type=%q scan=%v length=%d/%t decimal=%d,%d/%t nullable=%t/%t; want v/empty/%v/0,false/0,0,false/%t,%t", typ.Name(), typ.DatabaseTypeName(), typ.ScanType(), length, lengthKnown, precision, scale, decimalKnown, nullable, nullableKnown, scan, wantNullable, wantNullable)
		}
	}
}

func enumReferenceAssertDiagnostic(t *testing.T, err error, want referenceDiagnostic) {
	t.Helper()
	if got := referenceExtractDiagnostic(t, err); got != want {
		t.Fatalf("enum diagnostic got %#v want %#v", got, want)
	}
}

// Expectations are independently specified from root's pinned release captures,
// never from the native result. preparedStage is also part of each oracle entry.
func enumReferenceError(t *testing.T, p *referencePair, conn *sql.Conn, query, state string, queryMode bool, args ...any) {
	t.Helper()
	want, preparedStage := enumReferenceErrorOracle(t, query)
	if want.Code != state {
		t.Fatalf("enum oracle state %q differs from scenario %q", want.Code, state)
	}
	enumReferenceErrorChecked(t, p, conn, query, queryMode, preparedStage, func(t *testing.T, got referenceDiagnostic) {
		if got != want {
			t.Fatalf("enum diagnostic query=%q got %#v want %#v", query, got, want)
		}
	}, args...)
}

func enumReferenceErrorChecked(t *testing.T, p *referencePair, conn *sql.Conn, query string, queryMode bool, preparedStage string, check func(*testing.T, referenceDiagnostic), args ...any) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		func() {
			stage := "execute"
			var stmt *sql.Stmt
			var err error
			if prepared {
				stmt, err = conn.PrepareContext(p.ctx, query)
				if err != nil {
					stage = "prepare"
					if stmt != nil {
						stmt.Close()
						t.Fatal("failed enum Prepare exposed statement")
					}
				} else {
					defer stmt.Close()
				}
			}
			if err == nil {
				if queryMode {
					var rows *sql.Rows
					if stmt != nil {
						rows, err = stmt.QueryContext(p.ctx, args...)
					} else {
						rows, err = conn.QueryContext(p.ctx, query, args...)
					}
					if rows != nil {
						rows.Close()
						t.Fatal("expected enum error, Query exposed cursor")
					}
				} else {
					var result sql.Result
					if stmt != nil {
						result, err = stmt.ExecContext(p.ctx, args...)
					} else {
						result, err = conn.ExecContext(p.ctx, query, args...)
					}
					if result != nil {
						t.Fatal("expected enum error, Exec exposed result")
					}
				}
			}
			if err == nil {
				t.Fatal("expected enum diagnostic")
			}
			wantStage := "execute"
			if prepared {
				wantStage = preparedStage
			}
			if stage != wantStage {
				t.Fatalf("enum error query=%q prepared=%t stage got %q want %q", query, prepared, stage, wantStage)
			}
			check(t, referenceExtractDiagnostic(t, err))
		}()
	}
}

func enumReferenceDuplicateLabelError(t *testing.T, p *referencePair, conn *sql.Conn) {
	t.Helper()
	enumReferenceErrorChecked(t, p, conn, "CREATE TYPE enum_duplicate AS ENUM ('ok','ok')", false, "execute", func(t *testing.T, got referenceDiagnostic) {
		t.Helper()
		const prefix = "Key (enumtypid, enumlabel)=("
		const suffix = ", ok) already exists."
		oidText := strings.TrimSuffix(strings.TrimPrefix(got.Detail, prefix), suffix)
		oid, err := strconv.ParseUint(oidText, 10, 32)
		if err != nil || oid == 0 || strconv.FormatUint(oid, 10) != oidText || got.Detail != prefix+oidText+suffix {
			t.Fatalf("duplicate enum label Detail got %q, want exact key detail with positive uint32 OID", got.Detail)
		}
		got.Detail = ""
		want := referenceDiagnostic{
			Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23505",
			Message: `duplicate key value violates unique constraint "pg_enum_typid_label_index"`,
			Schema:  "pg_catalog", Table: "pg_enum", Constraint: "pg_enum_typid_label_index",
		}
		if got != want {
			t.Fatalf("duplicate enum label diagnostic got %#v want %#v", got, want)
		}
	})
}
