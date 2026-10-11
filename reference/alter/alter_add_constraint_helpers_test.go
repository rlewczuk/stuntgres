package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"reflect"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func addConstraintScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, b, c)
		})
	}
}

type addConstraintCase struct {
	name, kind, clause string
	bad                any
	build, write       fixture.Diagnostic
}

func addConstraintCases() []addConstraintCase {
	check := fixture.ControlsReferenceDiagnostic("23514", `check constraint "ck" of relation "t" is violated by some row`)
	check.Schema, check.Table, check.Constraint = "public", "t", "ck"
	nn := fixture.ControlsReferenceDiagnostic("23502", `column "a" of relation "t" contains null values`)
	nn.Schema, nn.Table, nn.Column = "public", "t", "a"
	unique := fixture.ControlsReferenceDiagnostic("23505", `could not create unique index "uq"`)
	unique.Detail, unique.Schema, unique.Table, unique.Constraint = "Key (a)=(1) is duplicated.", "public", "t", "uq"
	pk := unique
	pk.Message, pk.Constraint = `could not create unique index "pk"`, "pk"
	ex := fixture.ControlsReferenceDiagnostic("23P01", `could not create exclusion constraint "ex"`)
	ex.Detail, ex.Schema, ex.Table, ex.Constraint = "Key (a)=(1) conflicts with key (a)=(1).", "public", "t", "ex"
	fk := addConstraintFK("t", "fk", "a", "9", "p")
	ckwrite := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "t" violates check constraint "ck"`)
	ckwrite.Detail, ckwrite.Schema, ckwrite.Table, ckwrite.Constraint = "Failing row contains (0, bad).", "public", "t", "ck"
	return []addConstraintCase{
		{"check", "c", "CONSTRAINT ck CHECK(a>0)", int64(0), check, ckwrite},
		{"not_null", "n", "CONSTRAINT nn NOT NULL a", nil, nn, fixture.PrimaryKeyReferenceNull("t", "a", "null, bad")},
		{"unique", "u", "CONSTRAINT uq UNIQUE(a)", int64(1), unique, fixture.UniqueReferenceDuplicate("t", "uq", "Key (a)=(1) already exists.")},
		{"primary_key", "p", "CONSTRAINT pk PRIMARY KEY(a)", int64(1), pk, fixture.UniqueReferenceDuplicate("t", "pk", "Key (a)=(1) already exists.")},
		{"foreign_key", "f", "CONSTRAINT fk FOREIGN KEY(a) REFERENCES p(id)", int64(9), fk, fk},
		{"exclude", "x", "CONSTRAINT ex EXCLUDE USING btree(a WITH =)", int64(1), ex, fixture.ExclusionReferenceConflict("t", "ex", "a", "1", "1")},
	}
}

func addConstraintFK(table, name, column, value, parent string) fixture.Diagnostic {
	d := fixture.ControlsReferenceDiagnostic("23503", fmt.Sprintf(`insert or update on table "%s" violates foreign key constraint "%s"`, table, name))
	d.Detail = fmt.Sprintf("Key (%s)=(%s) is not present in table \"%s\".", column, value, parent)
	d.Schema, d.Table, d.Constraint = "public", table, name
	return d
}

func addConstraintSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) func(fixture.Step) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
	run(fixture.Exec("INSERT INTO p VALUES(1),(2),(3),(4)", 4))
	run(fixture.Exec("CREATE TABLE t(a INT,note TEXT)", 0))
	return run
}

func addConstraintRows(rows ...[]any) fixture.Step {
	return fixture.Select("SELECT a,note FROM t ORDER BY a,note", []string{"a", "note"}, []string{"INT4", "TEXT"}, rows)
}

func addConstraintHeld(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string) fixture.HeldStatement {
	t.Helper()
	s, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return fixture.HeldStatement{Executor: c, Statement: s, SQL: q}
}

func addConstraintInvalid(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, table, name, definition string) int64 {
	t.Helper()
	var oid int64
	var got string
	if err := c.QueryRowContext(p.Ctx, "SELECT oid,pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid=$1::regclass::oid AND conname=$2", table, name).Scan(&oid, &got); err != nil {
		t.Fatal(err)
	}
	if oid <= 0 || got != definition {
		t.Fatalf("constraint %s identity/definition: %d %q, want %q", name, oid, got, definition)
	}
	if b.PG {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT convalidated FROM pg_constraint WHERE oid=$1", []string{"convalidated"}, []string{"BOOL"}, [][]any{{false}}, oid))
	}
	return oid
}

func addConstraintCatalog(t *testing.T, p *fixture.Pair, c *sql.Conn) [][]any {
	t.Helper()
	rows, err := c.QueryContext(p.Ctx, "SELECT c.oid,c.conname,c.contype,c.conrelid,pg_get_constraintdef(c.oid) FROM pg_constraint c WHERE c.conrelid='t'::regclass::oid ORDER BY c.conname")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var bag [][]any
	for rows.Next() {
		var oid, parent int64
		var name, kind, def string
		if err := rows.Scan(&oid, &name, &kind, &parent, &def); err != nil {
			t.Fatal(err)
		}
		bag = append(bag, []any{oid, name, kind, parent, def})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return bag
}

func addConstraintRelations(t *testing.T, p *fixture.Pair, c *sql.Conn) [][]any {
	t.Helper()
	rows, err := c.QueryContext(p.Ctx, "SELECT oid,relname,relkind FROM pg_class WHERE relnamespace=(SELECT relnamespace FROM pg_class WHERE oid='t'::regclass::oid) ORDER BY relname")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var bag [][]any
	for rows.Next() {
		var oid int64
		var name, kind string
		if err := rows.Scan(&oid, &name, &kind); err != nil {
			t.Fatal(err)
		}
		bag = append(bag, []any{oid, name, kind})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return bag
}

func addConstraintDump(t *testing.T, p *fixture.Pair) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &out); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func addConstraintUnchanged(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, catalog, relations [][]any, dump []byte) {
	t.Helper()
	if got := addConstraintCatalog(t, p, c); !reflect.DeepEqual(got, catalog) {
		t.Fatalf("failed ALTER changed constraint identities/definitions: %v -> %v", catalog, got)
	}
	if got := addConstraintRelations(t, p, c); !reflect.DeepEqual(got, relations) {
		t.Fatalf("failed ALTER changed backing relations: %v -> %v", relations, got)
	}
	if !b.PG && !bytes.Equal(dump, addConstraintDump(t, p)) {
		t.Fatal("failed ALTER changed native dump")
	}
}

// Assert the complete pinned PostgreSQL record and public failure phase.
func addConstraintError(t *testing.T, p *fixture.Pair, c *sql.Conn, query string, want fixture.Diagnostic, prepared bool, phase string) {
	t.Helper()
	var err error
	observedPhase := "execute"
	if prepared {
		var stmt *sql.Stmt
		stmt, err = c.PrepareContext(p.Ctx, query)
		if err != nil {
			observedPhase = "prepare"
		} else {
			defer stmt.Close()
			var result sql.Result
			result, err = stmt.ExecContext(p.Ctx)
			if result != nil {
				t.Fatal("failed DDL published a result", query)
			}
		}
	} else {
		var result sql.Result
		result, err = c.ExecContext(p.Ctx, query)
		if result != nil {
			t.Fatal("failed DDL published a result", query)
		}
	}
	if err == nil {
		t.Fatal("expected constraint error", query)
	}
	fixture.AssertDiagnostic(t, err, &want)
	if observedPhase != phase {
		t.Fatalf("error phase %s, want %s for %q", observedPhase, phase, query)
	}
}
