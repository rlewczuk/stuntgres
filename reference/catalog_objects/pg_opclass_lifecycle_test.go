package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

const pgOpclassInt4Query = "SELECT * FROM pg_catalog.pg_opclass WHERE opcname='int4_ops' AND opcmethod=$1::oid"

func pgOpclassInt4(method int64) [][]any {
	if method == 403 {
		return [][]any{{int64(1978), int64(403), "int4_ops", int64(11), int64(10), int64(1976), int64(23), true, int64(0)}}
	}
	return [][]any{{int64(10020), int64(405), "int4_ops", int64(11), int64(10), int64(1977), int64(23), true, int64(0)}}
}

func pgOpclassConsume(t *testing.T, rows *sql.Rows, want [][]any) {
	t.Helper()
	var got [][]any
	for rows.Next() {
		var oid, method, namespace, owner, family, input, key int64
		var name string
		var def bool
		if err := rows.Scan(&oid, &method, &name, &namespace, &owner, &family, &input, &def, &key); err != nil {
			t.Fatal(err)
		}
		got = append(got, []any{oid, method, name, namespace, owner, family, input, def, key})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("retained rows got %v want %v", got, want)
	}
}

func TestReferencePGOpclassHeldExecution(t *testing.T) {
	pgOpclassScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		stmt, err := b.DB.PrepareContext(p.Ctx, pgOpclassInt4Query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: pgOpclassInt4Query}
		old, err := stmt.QueryContext(p.Ctx, 403)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		pgOpclassMetadata(t, old, b.PG)
		run := func(q string) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(q, 0)) }
		run("CREATE TABLE opclass_held(n INT)")
		run("CREATE INDEX opclass_held_b ON opclass_held(n)")
		run("CREATE INDEX opclass_held_h ON opclass_held USING hash(n)")
		run("CREATE DOMAIN opclass_held_domain AS INT")
		run("CREATE TYPE opclass_held_enum AS ENUM('a','b')")
		for _, method := range []int64{405, 403} {
			pgOpclassRows(t, p, b, held, pgOpclassInt4Query, pgOpclassInt4(method), false, method)
		}
		run("DROP INDEX opclass_held_b")
		run("DROP INDEX opclass_held_h")
		pgOpclassRows(t, p, b, held, pgOpclassInt4Query, pgOpclassInt4(405), false, 405)
		pgOpclassConsume(t, old, pgOpclassInt4(403))
		pgOpclassInventory(t, p, b)
	})
}

func pgOpclassViewRows(facts [][]any) [][]any {
	var want [][]any
	for _, r := range facts {
		if r[1] == int64(403) {
			want = append(want, []any{r[0], r[2]})
		}
	}
	return want
}

func pgOpclassLifecycle(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	facts := pgOpclassFacts(pgOpclassPGLSNOID(t, p, b))
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
	run(fixture.Exec("CREATE TABLE opclass_life(n INT,s TEXT)", 0))
	run(fixture.Exec("INSERT INTO opclass_life VALUES(7,'kept'),(8,'also')", 2))
	run(fixture.Exec("CREATE INDEX opclass_life_b ON opclass_life(n)", 0))
	run(fixture.Exec("CREATE INDEX opclass_life_h ON opclass_life USING hash(s)", 0))
	run(fixture.Exec("CREATE VIEW opclass_life_view AS SELECT oid,opcname FROM pg_catalog.pg_opclass WHERE opcmethod=403", 0))
	stmt, err := b.DB.PrepareContext(p.Ctx, pgOpclassInt4Query)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: pgOpclassInt4Query}
	check := func() {
		pgOpclassRows(t, p, b, held, pgOpclassInt4Query, pgOpclassInt4(405), false, 405)
		q := "SELECT oid,opcname FROM opclass_life_view"
		if b.PG {
			var ids []string
			for _, r := range pgOpclassViewRows(facts) {
				ids = append(ids, fmt.Sprint(r[0]))
			}
			q += " WHERE oid IN (" + strings.Join(ids, ",") + ")"
		}
		s := fixture.Select(q+" ORDER BY oid", []string{"oid", "opcname"}, []string{"OID", "NAME"}, pgOpclassViewRows(facts))
		fixture.PgDescriptionRows(t, p, b, s)
		fixture.PgDescriptionRows(t, p, b, fixture.Prepared(s))
		run(fixture.Select("SELECT n,s FROM opclass_life ORDER BY n", []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "kept"}, {int64(8), "also"}}))
		run(fixture.Select("SELECT pg_get_indexdef(oid) FROM pg_class WHERE relname='opclass_life_h'", []string{"pg_get_indexdef"}, []string{"TEXT"}, [][]any{{"CREATE INDEX opclass_life_h ON public.opclass_life USING hash (s)"}}))
	}
	check()
	if b.PG {
		return
	} // Native controls have no PostgreSQL API equivalent.
	old, err := b.DB.QueryContext(p.Ctx, "SELECT * FROM pg_opclass ORDER BY oid")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	pgOpclassMetadata(t, old, false)
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dump.String(), "CREATE VIEW") || !strings.Contains(dump.String(), "opclass_life_view") || strings.Contains(dump.String(), "INSERT INTO pg_catalog.pg_opclass") {
		t.Fatal("not a logical user-object dump", dump.String())
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	pgOpclassInventory(t, p, b)
	pgOpclassRows(t, p, b, held, pgOpclassInt4Query, pgOpclassInt4(403), false, 403)
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
		t.Fatal(err)
	}
	check()
	pgOpclassInventory(t, p, b)
	// database/sql closes Rows automatically at EOF; inspect the retained
	// cursor's metadata after replacement, while it is still open.
	pgOpclassMetadata(t, old, false)
	pgOpclassConsume(t, old, facts)
	var again bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil {
		t.Fatal(err)
	}
	if again.String() != dump.String() {
		t.Fatalf("logical redump changed\nbefore:%s\nafter:%s", dump.String(), again.String())
	}
}

func TestReferencePGOpclassLifecycle(t *testing.T) { pgOpclassScenario(t, pgOpclassLifecycle) }

func TestReferencePGOpclassCancellation(t *testing.T) {
	pgOpclassScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgOpclassRows(t, p, b, b.DB, pgOpclassInt4Query, pgOpclassInt4(403), false, 403)
		if b.PG {
			return
		} // pq precancel scheduling is not a server diagnostic contract.
		stmt, err := b.DB.PrepareContext(p.Ctx, pgOpclassInt4Query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		rows, err := b.DB.QueryContext(ctx, pgOpclassInt4Query, 403)
		if rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("direct canceled", rows, err)
		}
		rows, err = stmt.QueryContext(ctx, 403)
		if rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("held canceled", rows, err)
		}
		var state interface{ SQLState() string }
		if errors.As(err, &state) {
			t.Fatal("ordinary context error gained SQLSTATE", err)
		}
		if s, err := b.DB.PrepareContext(ctx, pgOpclassInt4Query); s != nil || !errors.Is(err, context.Canceled) {
			if s != nil {
				s.Close()
			}
			t.Fatal("canceled prepare", s, err)
		}
		pgOpclassRows(t, p, b, fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: pgOpclassInt4Query}, pgOpclassInt4Query, pgOpclassInt4(405), false, 405)
	})
}

func TestReferencePGOpclassFeatureSmoke(t *testing.T) {
	pgOpclassScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		// Shared helpers preserve exactly the same independent assertions/routes
		// in the standalone smoke and the focused diagnostic/lifecycle proofs.
		for _, method := range []int64{403, 405} {
			pgOpclassRows(t, p, b, b.DB, pgOpclassInt4Query, pgOpclassInt4(method), false, method)
		}
		pgOpclassIndexSemantics(t, p, b)
		pgOpclassDiagnosticFailures(t, p, b)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE opclass_smoke_array(a INT[])", 0))
		s := fixture.Exec("CREATE INDEX opclass_smoke_array_h ON opclass_smoke_array USING hash(a)", 0)
		if !b.PG {
			s = fixture.ImplicitError(s.Query, fixture.Diagnostic{Code: "0A000", Message: "index operator class is unsupported"})
			s.ErrorStage = "execute"
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(fixture.Select("SELECT a FROM opclass_smoke_array", []string{"a"}, []string{"_INT4"}, nil)))
		pgOpclassLifecycle(t, p, b)
	})
}
