package engine

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestCheckReview2OrderOrdinalInvariants(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE TABLE t(a INT,b INT,CONSTRAINT z CHECK(a>0),CONSTRAINT m CHECK(b>0),CONSTRAINT a CHECK(a<10))")
	view := e.CatalogRead()
	d := view.state.descriptors[catalogRef(t, e, view, "t").Address].(*relationDescriptor)
	if err := validateChecks(view, d, map[string]bool{}); err != nil {
		t.Fatal("valid initial descriptor rejected", err)
	}
	for _, tc := range []struct {
		name   string
		change func([]checkConstraint)
	}{
		{"negative", func(c []checkConstraint) { c[0].ordinal = -1 }},
		{"duplicate", func(c []checkConstraint) { c[1].ordinal = c[0].ordinal }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copy := *d
			copy.checkConstraints = append([]checkConstraint(nil), d.checkConstraints...)
			tc.change(copy.checkConstraints)
			if err := validateChecks(view, &copy, map[string]bool{}); err == nil {
				t.Fatal("malformed declaration ordinal accepted")
			}
		})
	}
	// Actual DROP paths leave legal nondense identity metadata; alphabetic
	// enforcement storage must not renumber declaration identity to hide gaps.
	sessionSQL(t, a, "ALTER TABLE t DROP CONSTRAINT z")
	view = e.CatalogRead()
	d = view.state.descriptors[catalogRef(t, e, view, "t").Address].(*relationDescriptor)
	ordered := orderedChecks(d)
	if len(ordered) != 2 || ordered[0].Name != "m" || ordered[0].ordinal != 1 || ordered[1].Name != "a" || ordered[1].ordinal != 2 {
		t.Fatal("DROP CONSTRAINT rewrote surviving declaration identities", ordered)
	}
	if err := validateChecks(view, d, map[string]bool{}); err != nil {
		t.Fatal("nondense post-DROP CONSTRAINT descriptor rejected", err)
	}
	sessionSQL(t, a, "ALTER TABLE t DROP COLUMN b")
	view = e.CatalogRead()
	d = view.state.descriptors[catalogRef(t, e, view, "t").Address].(*relationDescriptor)
	if len(d.checkConstraints) != 1 || d.checkConstraints[0].Name != "a" || d.checkConstraints[0].ordinal != 2 {
		t.Fatal("DROP COLUMN rewrote surviving declaration identity", d.checkConstraints)
	}
	if err := validateChecks(view, d, map[string]bool{}); err != nil {
		t.Fatal("nondense post-DROP COLUMN descriptor rejected", err)
	}
	sessionSQL(t, a, "INSERT INTO t VALUES(1)")
}

func checkReview2OrderNativeRestrict(t *testing.T, e *Engine, a *Session, detail string) {
	t.Helper()
	op, err := a.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	result, err := e.Execute(op, statement(t, "DROP SEQUENCE s"), 0, nil)
	op.Done()
	if result.Rows != nil || result.Count != 0 {
		t.Fatal("failed DROP exposed successful result", result)
	}
	nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "2BP01", Message: "cannot drop sequence s because other objects depend on it", Detail: detail, Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
}

func TestCheckReview2OrderNativeRoundtrip(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		t.Run(map[bool]string{false: "tables", true: "mixed"}[mixed], func(t *testing.T) {
			e := New()
			a := sessionStart(t, e, nil)
			sessionSQL(t, a, "CREATE SEQUENCE s")
			detail := "constraint cz on table z depends on sequence s\nconstraint ca on table a depends on sequence s"
			if mixed {
				sessionSQL(t, a, "CREATE VIEW v AS SELECT nextval('s') AS n")
				detail = "view v depends on sequence s\n" + detail
			}
			sessionSQL(t, a, "CREATE TABLE z(a INT CONSTRAINT cz CHECK(nextval('s')>0))")
			sessionSQL(t, a, "CREATE TABLE a(a INT CONSTRAINT ca CHECK(nextval('s')>0))")
			sessionSQL(t, a, "INSERT INTO z VALUES(11)")
			sessionSQL(t, a, "INSERT INTO a VALUES(22)")
			checkReview2OrderNativeRestrict(t, e, a, detail)
			var dump, again strings.Builder
			if err := e.Dump(context.Background(), &dump); err != nil {
				t.Fatal(err)
			}
			if err := e.Restore(context.Background(), strings.NewReader(dump.String())); err != nil {
				t.Fatal(err)
			}
			checkReview2OrderNativeRestrict(t, e, a, detail)
			checkReviewDependencyValid(t, e)
			if err := e.Dump(context.Background(), &again); err != nil || dump.String() != again.String() {
				t.Fatal("roundtrip changed definitions/data/provider state", err)
			}
		})
	}
}

func TestCheckReview2OrderRemappedSourceTopology(t *testing.T) {
	ctx := context.Background()
	source := New()
	a := sessionStart(t, source, nil)
	for _, sql := range []string{
		"CREATE SEQUENCE s MINVALUE -10 START 1",
		"CREATE TABLE t(a INT CONSTRAINT c CHECK(nextval('s')>0))",
		"INSERT INTO t VALUES(7)",
		"CREATE VIEW v AS SELECT a FROM t",
		"CREATE VIEW w AS SELECT a FROM v",
	} {
		sessionSQL(t, a, sql)
	}
	var first, second strings.Builder
	if err := source.Dump(ctx, &first); err != nil {
		t.Fatal(err)
	}
	destination := New()
	b := sessionStart(t, destination, nil)
	sessionSQL(t, b, "CREATE TEMPORARY SEQUENCE retained")
	// Advance through real disposable relations beyond the source graph before
	// replacing it. Retained temporary collisions then move the table above
	// its views, independent of definition allocations between those relations.
	lastSource := catalogRef(t, source, source.CatalogRead(), "w")
	for i := 0; ; i++ {
		name := fmt.Sprintf("advance_%d", i)
		sessionSQL(t, b, "CREATE SEQUENCE "+name)
		ref := catalogRef(t, destination, destination.CatalogRead(), name)
		if ref.Address.ObjectID > lastSource.Address.ObjectID {
			break
		}
	}
	if err := destination.Restore(ctx, strings.NewReader(first.String())); err != nil {
		t.Fatal(err)
	}
	view := destination.CatalogRead()
	table := catalogRef(t, destination, view, "t")
	dependent := catalogRef(t, destination, view, "v")
	// Assert the exceptional identity relationship, never a literal allocator
	// value or diagnostic-order parity with the unremapped source.
	if table.Address.ObjectID <= dependent.Address.ObjectID {
		t.Fatal("fixture did not remap the table above its dependent view", table, dependent)
	}
	if err := destination.Dump(ctx, &second); err != nil {
		t.Fatal(err)
	}
	tableAt := strings.Index(second.String(), `CREATE TABLE "public"."t"`)
	viewAt := strings.Index(second.String(), `CREATE VIEW "public"."v"`)
	nestedAt := strings.Index(second.String(), `CREATE VIEW "public"."w"`)
	if tableAt < 0 || viewAt <= tableAt || nestedAt <= viewAt {
		t.Fatal("remapped creation addresses overrode source topology", second.String())
	}
	replayed := New()
	c := sessionStart(t, replayed, nil)
	if err := replayed.Restore(ctx, strings.NewReader(second.String())); err != nil {
		t.Fatal("topology-ordered dump was not executable", err)
	}
	if rows := tuples(sessionSQL(t, c, "SELECT a FROM w")); len(rows) != 1 || rows[0][0] != int64(7) {
		t.Fatal("replayed nested view lost table data", rows)
	}
	checkReviewDependencyValid(t, replayed)
	checkReview2OrderNativeRestrict(t, replayed, c, "constraint c on table t depends on sequence s")
	sessionSQL(t, c, "SELECT setval('s',-1,true)")
	op, err := c.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	result, err := replayed.Execute(op, statement(t, "INSERT INTO t VALUES(8)"), 0, nil)
	op.Done()
	if result.Rows != nil || result.Count != 0 {
		t.Fatal("failed replayed CHECK exposed successful result", result)
	}
	nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "23514", Message: `new row for relation "t" violates check constraint "c"`, Detail: "Failing row contains (8).", Schema: "public", Table: "t", Constraint: "c"})
	if rows := tuples(sessionSQL(t, c, "SELECT a FROM w")); len(rows) != 1 || rows[0][0] != int64(7) {
		t.Fatal("replayed CHECK failure changed nested view rows", rows)
	}
}
