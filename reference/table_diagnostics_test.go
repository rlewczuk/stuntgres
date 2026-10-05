//go:build reference

package reference_test

import (
	"fmt"
	"testing"
)

func TestReferenceTableCoreDiagnostics(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(id INT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(11)", 1))
	for _, tc := range []struct {
		q    string
		want referenceDiagnostic
	}{
		{"CREATE TABLE t (id integer)", referenceDiagnostic{Code: "42P07", Message: `relation "t" already exists`}},
		{"CREATE TABLE bad (id integer,id text)", referenceDiagnostic{Code: "42701", Message: `column "id" specified more than once`}},
		{"CREATE TABLE bad (id no_such_type)", referenceDiagnostic{Code: "42704", Message: `type "no_such_type" does not exist`, Position: "22"}},
		{"/*é*/ CREATE TABLE bad (id no_such_type)", referenceDiagnostic{Code: "42704", Message: `type "no_such_type" does not exist`, Position: "28"}},
		{"CREATE TABLE clone (LIKE missing)", referenceDiagnostic{Code: "42P01", Message: `relation "missing" does not exist`, Position: "26"}},
		{"/*é*/ CREATE TABLE clone (LIKE missing)", referenceDiagnostic{Code: "42P01", Message: `relation "missing" does not exist`, Position: "32"}},
		{"CREATE TABLE clone (LIKE public.missing)", referenceDiagnostic{Code: "42P01", Message: `relation "public.missing" does not exist`, Position: "26"}},
		{"CREATE TABLE sys (ctid integer)", referenceDiagnostic{Code: "42701", Message: `column name "ctid" conflicts with a system column name`}},
		{"DROP TABLE public.missing", referenceDiagnostic{Code: "42P01", Message: `table "missing" does not exist`}},
		{"DROP TABLE absent_schema.t", referenceDiagnostic{Code: "3F000", Message: `schema "absent_schema" does not exist`}},
	} {
		for _, prepared := range []bool{false, true} {
			s := referenceImplicitError(tc.q, tc.want)
			s.prepared = prepared
			s.errorStage = "execute"
			p.step(t, s)
			p.step(t, referenceSelect("SELECT * FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(11)}}))
		}
	}
}

func TestReferenceTableCoreLikeOptionsExternalBoundary(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE source(i INT NOT NULL)", 0))
	for _, prepared := range []bool{false, true} {
		for _, prefix := range []string{"INCLUDING", "EXCLUDING"} {
			for _, option := range []string{"COMMENTS", "COMPRESSION", "CONSTRAINTS", "DEFAULTS", "GENERATED", "IDENTITY", "INDEXES", "STATISTICS", "STORAGE", "ALL"} {
				q := fmt.Sprintf("CREATE TABLE boundary(LIKE source %s %s)", prefix, option)
				native := referenceImplicitError(q, referenceDiagnostic{Code: "0A000", Message: "LIKE options are unsupported", Position: "35"})
				native.prepared = prepared
				native.errorStage = "execute"
				referenceRunStep(t, p.ctx, p.memory, &referenceRecorder{}, native)
				pg := referenceExec(q, 0)
				pg.prepared = prepared
				referenceRunStep(t, p.ctx, p.postgres, &referenceRecorder{}, pg)
				// PostgreSQL supports these options; excluding constraints still copies NOT NULL.
				failure := referenceImplicitError("INSERT INTO boundary VALUES(NULL)", referenceDiagnostic{Code: "23502", Message: `null value in column "i" of relation "boundary" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "boundary", Column: "i"})
				failure.errorStage = "execute"
				referenceRunStep(t, p.ctx, p.postgres, &referenceRecorder{}, failure)
				referenceRunStep(t, p.ctx, p.postgres, &referenceRecorder{}, referenceExec("DROP TABLE boundary", 0))
			}
		}
	}
}

func TestReferenceTableCoreExternalDependencyBoundary(t *testing.T) {
	p := newReferencePair(t)
	// These are genuine PostgreSQL graph objects, not native compatibility claims.
	for _, q := range []string{"CREATE TABLE products(id integer primary key)", "CREATE TABLE orders(product_no integer references products(id))", "CREATE VIEW product_view AS SELECT id FROM products"} {
		if _, err := referenceAdmin.ExecContext(p.ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	_, err := referenceAdmin.ExecContext(p.ctx, "DROP TABLE products RESTRICT")
	want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: "cannot drop table products because other objects depend on it", Detail: "constraint orders_product_no_fkey on table orders depends on table products\nview product_view depends on table products", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}
	referenceAssertDiagnostic(t, err, &want)
	if _, err = referenceAdmin.ExecContext(p.ctx, "DROP TABLE products CASCADE"); err != nil {
		t.Fatal(err)
	}
	referenceRunStep(t, p.ctx, p.postgres, &referenceRecorder{}, referenceSelect("SELECT * FROM orders", []string{"product_no"}, []string{"INT4"}, nil))
	if _, err = referenceAdmin.ExecContext(p.ctx, "INSERT INTO orders VALUES(99)"); err != nil {
		t.Fatal("FK survived cascade", err)
	}
	rows, err := referenceAdmin.QueryContext(p.ctx, "SELECT * FROM product_view")
	if rows != nil {
		rows.Close()
	}
	referenceState(t, err, "42P01")
	for _, tc := range []struct{ q, message, position string }{{"CREATE TABLE native_pk(id INT PRIMARY KEY)", "column constraints are unsupported", "31"}, {"CREATE TABLE native_fk(id INT REFERENCES products)", "column constraints are unsupported", "31"}, {"CREATE VIEW native_view AS SELECT id FROM orders", "CREATE syntax is unsupported", "8"}} {
		s := referenceImplicitError(tc.q, referenceDiagnostic{Code: "0A000", Message: tc.message, Position: tc.position})
		referenceRunStep(t, p.ctx, p.memory, &referenceRecorder{}, s)
	}
}

func TestReferenceTableCoreQuotedTypeNamesAndBoundaries(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferencePair(t)
			run := func(s referenceStep) { s.prepared = prepared; p.step(t, s) }
			run(referenceExec(`CREATE TABLE raw(i "int4" NOT NULL,b "int8",s "text",f "bool")`, 0))
			run(referenceExec("INSERT INTO raw VALUES(7,9223372036854775807,'O''Brien',TRUE)", 1))
			unchanged := referenceSelect("SELECT * FROM raw", []string{"i", "b", "s", "f"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, [][]any{{int64(7), int64(9223372036854775807), "O'Brien", true}})
			run(unchanged)
			for _, name := range []string{"integer", "int", "bigint", "boolean", "smallint", "real", "decimal", "INTEGER", "INT4", "SERIAL"} {
				s := referenceImplicitError(`CREATE TABLE bad (id "`+name+`")`, referenceDiagnostic{Code: "42704", Message: `type "` + name + `" does not exist`, Position: "22"})
				s.errorStage = "execute"
				run(s)
				run(unchanged)
			}
			for _, tc := range []struct{ q, message string }{{`CREATE TABLE bad (id "a""b")`, `type "a"b" does not exist`}, {`CREATE TABLE bad (id "a\b")`, `type "a\b" does not exist`}} {
				s := referenceImplicitError(tc.q, referenceDiagnostic{Code: "42704", Message: tc.message, Position: "22"})
				s.errorStage = "execute"
				run(s)
			}
			run(referenceExec(`CREATE TABLE IF NOT EXISTS raw(id "integer")`, 0))
			run(unchanged)
			// Genuine unsupported raw names and serial utility pseudo names are separate
			// external-only controls, not nonexistent-type parity or native family support.
			for _, name := range []string{"int2", "numeric", "name", "bpchar", "float8", "serial", "smallserial", "bigserial"} {
				q := `CREATE TABLE bad (id "` + name + `")`
				s := referenceImplicitError(q, referenceDiagnostic{Code: "0A000", Message: "unsupported column type", Position: "22"})
				s.prepared = prepared
				s.errorStage = "execute"
				referenceRunStep(t, p.ctx, p.memory, &referenceRecorder{}, s)
				s = referenceExec(q, 0)
				s.prepared = prepared
				referenceRunStep(t, p.ctx, p.postgres, &referenceRecorder{}, s)
				referenceRunStep(t, p.ctx, p.postgres, &referenceRecorder{}, referenceExec("DROP TABLE bad CASCADE", 0))
			}
		})
	}
}

func TestReferenceTableCoreQuotedLikeSourceDiagnostics(t *testing.T) {
	p := newReferencePair(t)
	for _, tc := range []struct{ q, message string }{{`CREATE TABLE clone (LIKE "a""b")`, `relation "a"b" does not exist`}, {`CREATE TABLE clone (LIKE "a\b")`, `relation "a\b" does not exist`}} {
		for _, prepared := range []bool{false, true} {
			s := referenceImplicitError(tc.q, referenceDiagnostic{Code: "42P01", Message: tc.message, Position: "26"})
			s.prepared = prepared
			s.errorStage = "execute"
			p.step(t, s)
		}
	}
}

func TestReferenceTableCoreQuotedDuplicateRelation(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferencePair(t)
			run := func(s referenceStep) { s.prepared = prepared; p.step(t, s) }
			for _, tc := range []struct{ identifier, name, message, nullMessage string }{
				{`"a""b"`, `a"b`, `relation "a"b" already exists`, `null value in column "id" of relation "a"b" violates not-null constraint`},
				{`"a\b"`, `a\b`, `relation "a\b" already exists`, `null value in column "id" of relation "a\b" violates not-null constraint`},
			} {
				run(referenceExec("CREATE TABLE "+tc.identifier+"(id INT NOT NULL)", 0))
				run(referenceExec("INSERT INTO "+tc.identifier+" VALUES(11)", 1))
				unchanged := referenceSelect("SELECT * FROM "+tc.identifier, []string{"id"}, []string{"INT4"}, [][]any{{int64(11)}})
				run(unchanged)
				s := referenceImplicitError("CREATE TABLE "+tc.identifier+"(lost TEXT)", referenceDiagnostic{Code: "42P07", Message: tc.message})
				s.errorStage = "execute"
				run(s)
				run(unchanged)
				// Failed duplicate creation must preserve the original NOT NULL.
				s = referenceImplicitError("INSERT INTO "+tc.identifier+" VALUES(NULL)", referenceDiagnostic{Code: "23502", Message: tc.nullMessage, Detail: "Failing row contains (null).", Schema: "public", Table: tc.name, Column: "id"})
				s.errorStage = "execute"
				run(s)
				run(unchanged)
			}
		})
	}
}
