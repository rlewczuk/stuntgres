package parser

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestParseScratchRetainedASTAndType(t *testing.T) {
	const query = `INSERT INTO kept VALUES('first','a\nb'),('second',E'c\n'); SELECT count(*) OVER(ORDER BY k RANGE BETWEEN 1 PRECEDING AND 2 FOLLOWING) FROM kept`
	statements, err := Parse(query)
	if err != nil || len(statements) != 2 {
		t.Fatal(statements, err)
	}
	typ, err := ParseTypeName(context.Background(), "numeric(7,2)[]", nil)
	if err != nil {
		t.Fatal(err)
	}
	for range 24 {
		// Both a deep successful parse and a malformed whole-input script follow
		// retained results. The latter must never publish its valid INSERT prefix.
		if _, err := Parse("SELECT " + strings.Repeat("(", 64) + "1" + strings.Repeat(")", 64)); err != nil {
			t.Fatal(err)
		}
		prefix, err := Parse("INSERT INTO discarded VALUES(1); SELECT (")
		var diagnostic *sqlstate.Error
		if prefix != nil || !errors.As(err, &diagnostic) || diagnostic.Code != "42601" {
			t.Fatal(prefix, err)
		}
		if _, err := ParseTypeName(context.Background(), "varchar(99)", nil); err != nil {
			t.Fatal(err)
		}
	}
	insert := statements[0].(*Insert)
	if insert.Name.Parts[0].Text != "kept" || len(insert.Rows) != 2 || insert.Rows[0][0].(LiteralExpr).Value.Text != "first" || insert.Rows[0][1].(LiteralExpr).Value.Text != `a\nb` || insert.Rows[1][1].(LiteralExpr).Value.Text != "c\n" {
		t.Fatal("retained INSERT changed", insert)
	}
	frame := statements[1].(*Select).Targets[0].(AggregateExpr).Window.Frame
	if frame.Start.Kind != FrameOffsetPreceding || frame.End.Kind != FrameOffsetFollowing || frame.Start.Offset.(LiteralExpr).Value.Integer != 1 || frame.End.Offset.(LiteralExpr).Value.Integer != 2 {
		t.Fatal("retained frame changed", frame)
	}
	if typ.Name != "numeric" || len(typ.ArrayBounds) != 1 || len(typ.Modifiers) != 2 || *typ.Modifiers[0].Integer != 7 || *typ.Modifiers[1].Integer != 2 {
		t.Fatal("retained type changed", typ)
	}
}

func TestParseScratchOptionsReentryAndCancellation(t *testing.T) {
	for _, standard := range []bool{false, true, false} {
		var inner []Statement
		outer, err := ParseWithOptions("-- outer\nINSERT INTO t VALUES('a\\nb',E'c\\n')", Options{StandardConformingStrings: standard, LineComment: func(comment string) error {
			if !strings.Contains(comment, "outer") {
				return fmt.Errorf("wrong callback input: %q", comment)
			}
			// Reenter while the outer receiver is active; the inner parse and VALUES
			// replay must have their own captured lexical mode and AST storage.
			var err error
			inner, err = ParseWithOptions(`INSERT INTO nested VALUES('x\ny')`, Options{StandardConformingStrings: !standard})
			return err
		}})
		if err != nil || len(outer) != 1 || len(inner) != 1 {
			t.Fatal(outer, inner, err)
		}
		wantOuter, wantInner := "a\nb", `x\ny`
		if standard {
			wantOuter, wantInner = `a\nb`, "x\ny"
		}
		outerRow, innerRow := outer[0].(*Insert).Rows[0], inner[0].(*Insert).Rows[0]
		if outerRow[0].(LiteralExpr).Value.Text != wantOuter || outerRow[1].(LiteralExpr).Value.Text != "c\n" || innerRow[0].(LiteralExpr).Value.Text != wantInner {
			t.Fatal(standard, outerRow, innerRow)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	statements, err := ParseWithOptions("-- cancel during lexing\nSELECT 1", Options{Context: ctx, StandardConformingStrings: true, LineComment: func(string) error { cancel(); return nil }})
	var diagnostic *sqlstate.Error
	if statements != nil || !errors.Is(err, context.Canceled) || errors.As(err, &diagnostic) {
		t.Fatal(statements, err)
	}
	failure := errors.New("private import rejected")
	statements, err = ParseWithOptions("-- private import\nSELECT 1", Options{LineComment: func(string) error { return failure }})
	if statements != nil || !errors.Is(err, failure) {
		t.Fatal(statements, err)
	}
	recovery, err := Parse("SELECT 7")
	if err != nil || len(recovery) != 1 || recovery[0].(*Select).Targets[0].(LiteralExpr).Value.Integer != 7 {
		t.Fatal(recovery, err)
	}
}

func TestParseScratchTypeErrorsAndCallbackPanic(t *testing.T) {
	typ, err := ParseTypeName(context.Background(), `"Odd.Schema"."a.b"[]`, nil)
	if err != nil {
		t.Fatal(err)
	}
	invalid, err := ParseTypeName(context.Background(), "integer trailing", nil)
	var diagnostic *sqlstate.Error
	if invalid.Name != "" || !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || diagnostic.Position != "9" || diagnostic.Context != `invalid type name "integer trailing"` {
		t.Fatal(invalid, err)
	}
	failure := errors.New("trusted callback panic")
	func() {
		defer func() {
			if got := recover(); got != failure {
				t.Fatalf("callback panic changed: %v", got)
			}
		}()
		_, _ = ParseWithOptions("-- panic\nSELECT 1", Options{LineComment: func(string) error { panic(failure) }})
		t.Fatal("callback panic was suppressed")
	}()
	fresh, err := ParseTypeName(context.Background(), "integer", nil)
	if err != nil || fresh.Name != "int4" || len(fresh.Modifiers) != 0 {
		t.Fatal(fresh, err)
	}
	if typ.Name != "a.b" || !typ.Quoted || len(typ.Parts) != 2 || typ.Parts[0].Text != "Odd.Schema" || typ.Parts[1].Text != "a.b" || len(typ.ArrayBounds) != 1 {
		t.Fatal("retained qualified type changed", typ)
	}
}

func TestParseScratchConcurrentIndependentResults(t *testing.T) {
	var workers sync.WaitGroup
	for id := range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			name := fmt.Sprintf("worker_%d", id)
			standard := id%2 == 0
			want := "a\nb"
			if standard {
				want = `a\nb`
			}
			var retained *Insert
			var retainedType TypeName
			for range 20 {
				statements, err := ParseWithOptions("INSERT INTO t VALUES('"+name+"','a\\nb')", Options{StandardConformingStrings: standard})
				if err != nil || len(statements) != 1 {
					t.Errorf("worker%d: %v", id, err)
					return
				}
				insert := statements[0].(*Insert)
				if insert.Rows[0][0].(LiteralExpr).Value.Text != name || insert.Rows[0][1].(LiteralExpr).Value.Text != want {
					t.Errorf("worker%d mixed AST/options", id)
					return
				}
				precision := int32(id + 3)
				typ, err := ParseTypeName(context.Background(), fmt.Sprintf("numeric(%d,2)[]", precision), nil)
				if err != nil || typ.Name != "numeric" || len(typ.Modifiers) != 2 || *typ.Modifiers[0].Integer != precision || *typ.Modifiers[1].Integer != 2 {
					t.Errorf("worker%d mixed type: %v", id, err)
					return
				}
				if retainedType.Name != "" && (*retainedType.Modifiers[0].Integer != precision || *retainedType.Modifiers[1].Integer != 2 || len(retainedType.ArrayBounds) != 1) {
					t.Errorf("worker%d retained type mutated", id)
					return
				}
				retainedType = typ
				if retained != nil && (retained.Rows[0][0].(LiteralExpr).Value.Text != name || retained.Rows[0][1].(LiteralExpr).Value.Text != want) {
					t.Errorf("worker%d retained result mutated", id)
					return
				}
				retained = insert
			}
		}()
	}
	workers.Wait()
}

func TestParseScratchRetainedDeclarationCarriers(t *testing.T) {
	const query = `CREATE TEMP TABLE owned(a numeric(7,2),b varchar(9)) ON COMMIT DELETE ROWS;
CREATE TYPE pair AS (x numeric(8,3),y varchar(11));
ALTER TABLE owned ADD COLUMN IF NOT EXISTS c numeric(10,4) DEFAULT 5 NOT NULL, ADD d varchar(13), ALTER COLUMN a TYPE numeric(12,5), DROP CONSTRAINT IF EXISTS old_key;
CREATE TABLE copied AS SELECT count(*) OVER -- nested parse while the outer stack owns an aggregate
(ORDER BY a RANGE BETWEEN 3 PRECEDING AND 4 FOLLOWING),count(*) OVER w FROM owned WINDOW w AS (ORDER BY b RANGE BETWEEN 5 PRECEDING AND CURRENT ROW) WITH NO DATA`
	statements, err := ParseWithOptions(query, Options{StandardConformingStrings: true, LineComment: func(string) error {
		_, err := Parse("CREATE TEMP TABLE noise(n numeric(99,9)) ON COMMIT DROP; CREATE TYPE noise_pair AS (x varchar(1)); ALTER TABLE noise ADD z numeric(2,1)")
		return err
	}})
	if err != nil || len(statements) != 4 {
		t.Fatal(statements, err)
	}
	check := func() {
		t.Helper()
		table := statements[0].(*CreateTable)
		if table.Name.Parts[0].Text != "owned" || table.Persistence != "temporary" || table.OnCommit != OnCommitDelete || table.OnCommitPos.Character != strings.Index(query, "ON COMMIT")+1 || len(table.Elements) != 2 {
			t.Fatal("retained table changed", table)
		}
		a, b := table.Elements[0].Column, table.Elements[1].Column
		if a.Name != "a" || a.TypeName.Name != "numeric" || len(a.TypeName.Modifiers) != 2 || *a.TypeName.Modifiers[0].Integer != 7 || *a.TypeName.Modifiers[1].Integer != 2 || b.Name != "b" || b.TypeName.Name != "varchar" || len(b.TypeName.Modifiers) != 1 || *b.TypeName.Modifiers[0].Integer != 9 {
			t.Fatal("table columns aliased", a, b)
		}
		composite := statements[1].(*CreateComposite)
		if composite.Name.Parts[0].Text != "pair" || len(composite.Members) != 2 {
			t.Fatal(composite)
		}
		x, y := composite.Members[0], composite.Members[1]
		if x.Name != "x" || x.TypeName.Name != "numeric" || len(x.TypeName.Modifiers) != 2 || *x.TypeName.Modifiers[0].Integer != 8 || *x.TypeName.Modifiers[1].Integer != 3 || y.Name != "y" || y.TypeName.Name != "varchar" || len(y.TypeName.Modifiers) != 1 || *y.TypeName.Modifiers[0].Integer != 11 {
			t.Fatal("composite value boundary changed", x, y)
		}
		alter := statements[2].(*AlterTable)
		if alter.Name.Parts[0].Text != "owned" || len(alter.Actions) != 4 {
			t.Fatal(alter)
		}
		c, d, change, drop := alter.Actions[0], alter.Actions[1], alter.Actions[2], alter.Actions[3]
		if c.Action != AddColumn || !c.IfNotExists || c.Column.Text != "c" || c.Definition == nil || d.Action != AddColumn || d.Column.Text != "d" || d.Definition == nil {
			t.Fatal("ALTER addition value boundary changed", c, d)
		}
		if c.Definition.Name != "c" || len(c.Definition.TypeName.Modifiers) != 2 || *c.Definition.TypeName.Modifiers[0].Integer != 10 || *c.Definition.TypeName.Modifiers[1].Integer != 4 || len(c.Definition.Constraints) != 2 || c.Definition.Constraints[0].Default.(LiteralExpr).Value.Integer != 5 || !c.Definition.Constraints[1].NotNull || d.Definition.Name != "d" || len(d.Definition.TypeName.Modifiers) != 1 || *d.Definition.TypeName.Modifiers[0].Integer != 13 {
			t.Fatal("ALTER column nodes aliased", c, d)
		}
		if change.Action != AlterColumnType || change.Column.Text != "a" || len(change.TypeName.Modifiers) != 2 || *change.TypeName.Modifiers[0].Integer != 12 || *change.TypeName.Modifiers[1].Integer != 5 || drop.Action != DropConstraint || drop.Constraint.Text != "old_key" || !drop.IfExists {
			t.Fatal("ALTER actions aliased", change, drop)
		}
		creation := statements[3].(*CreateQueryTable)
		if creation.Name.Parts[0].Text != "copied" || !creation.NoData || creation.Form != CreateTableAs {
			t.Fatal(creation)
		}
		selectQuery := LeftmostSelect(creation.Query)
		if len(selectQuery.Targets) != 2 || len(selectQuery.Windows) != 1 {
			t.Fatal(selectQuery)
		}
		inline, named := selectQuery.Targets[0].(AggregateExpr).Window, selectQuery.Targets[1].(AggregateExpr).Window
		definition := selectQuery.Windows[0]
		if inline.Frame.Start.Kind != FrameOffsetPreceding || inline.Frame.End.Kind != FrameOffsetFollowing || inline.Frame.Start.Offset.(LiteralExpr).Value.Integer != 3 || inline.Frame.End.Offset.(LiteralExpr).Value.Integer != 4 || named.Name == nil || named.Name.Text != "w" || !named.Bare || named.Frame != nil {
			t.Fatal("OVER nodes aliased", inline, named)
		}
		if definition.Name.Text != "w" || definition.Spec.Frame.Start.Kind != FrameOffsetPreceding || definition.Spec.Frame.Start.Offset.(LiteralExpr).Value.Integer != 5 || definition.Spec.Frame.End.Kind != FrameCurrentRow {
			t.Fatal("named specification value boundary changed", definition)
		}
	}
	check()
	for range 20 {
		_, err := Parse("CREATE TEMP TABLE overwritten(k varchar(31)) ON COMMIT DROP; CREATE TYPE erased AS (z numeric(2,1)); ALTER TABLE overwritten ADD q varchar(3), ALTER k TYPE numeric(4,1); SELECT count(*) OVER(ORDER BY z RANGE 9 PRECEDING) FROM overwritten")
		if err != nil {
			t.Fatal(err)
		}
		prefix, err := Parse("CREATE TYPE broken AS (a numeric(2,1)); ALTER TABLE overwritten ADD q")
		if prefix != nil || err == nil {
			t.Fatal("malformed script published a prefix", prefix, err)
		}
		check()
	}
}

func TestParseScratchRetainedCollationSource(t *testing.T) {
	const query = `CREATE COLLATION IF NOT EXISTS s."Mixed" FROM pg_catalog."C"; -- reenter
CREATE COLLATION next_copy (FROM=pg_catalog."POSIX")`
	const nestedQuery = `CREATE COLLATION nested_copy FROM pg_catalog."POSIX"`
	var nested []Statement
	statements, err := ParseWithOptions(query, Options{StandardConformingStrings: true, LineComment: func(string) error {
		var err error
		nested, err = ParseWithOptions(nestedQuery, Options{StandardConformingStrings: false})
		return err
	}})
	if err != nil || len(statements) != 2 || len(nested) != 1 {
		t.Fatal(statements, nested, err)
	}
	check := func() error {
		create, option, inner := statements[0].(*CreateCollation), statements[1].(*CreateCollation), nested[0].(*CreateCollation)
		if !create.IfNotExists || len(create.Name.Parts) != 2 || create.Name.Parts[0].Text != "s" || create.Name.Parts[1].Text != "Mixed" || create.Name.Parts[1].Pos.Character != strings.Index(query, `"Mixed"`)+1 || create.From == nil || len(create.From.Parts) != 2 || create.From.Parts[0].Text != "pg_catalog" || create.From.Parts[1].Text != "C" || create.From.Parts[0].Pos.Offset != strings.Index(query, "pg_catalog") || create.From.Parts[1].Pos.Character != strings.Index(query, `"C"`)+1 {
			return fmt.Errorf("retained FROM metadata changed: %#v", create)
		}
		if len(option.Options) != 1 || option.Options[0].Name.Text != "from" || option.Options[0].Name.Pos.Character != strings.Index(query, "FROM=")+1 || option.Options[0].Source == nil || len(option.Options[0].Source.Parts) != 2 || option.Options[0].Source.Parts[0].Text != "pg_catalog" || option.Options[0].Source.Parts[1].Text != "POSIX" || option.Options[0].Source.Parts[1].Pos.Character != strings.Index(query, `"POSIX"`)+1 {
			return fmt.Errorf("retained FROM option changed: %#v", option)
		}
		if inner.Name.Parts[0].Text != "nested_copy" || inner.From == nil || len(inner.From.Parts) != 2 || inner.From.Parts[1].Text != "POSIX" || inner.From.Parts[0].Pos.Offset != strings.Index(nestedQuery, "pg_catalog") {
			return fmt.Errorf("reentrant FROM metadata changed: %#v", inner)
		}
		return nil
	}
	if err := check(); err != nil {
		t.Fatal(err)
	}
	for _, standard := range []bool{false, true, false} {
		if _, err := ParseWithOptions(`SELECT 'a\nb'`, Options{StandardConformingStrings: standard}); err != nil {
			t.Fatal(err)
		}
		prefix, err := Parse(`CREATE COLLATION discarded FROM pg_catalog."C"; CREATE COLLATION broken FROM`)
		if prefix != nil || err == nil {
			t.Fatal(prefix, err)
		}
		if err := check(); err != nil {
			t.Fatal(err)
		}
	}
	var workers sync.WaitGroup
	for id := range 4 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			source := "C"
			if id%2 != 0 {
				source = "POSIX"
			}
			var retained *CreateCollation
			for iteration := range 12 {
				name := fmt.Sprintf("owned_%d_%d", id, iteration)
				parsed, err := Parse(fmt.Sprintf(`CREATE COLLATION %s FROM pg_catalog."%s"`, name, source))
				if err != nil || len(parsed) != 1 {
					t.Errorf("concurrent collation: %v", err)
					return
				}
				create := parsed[0].(*CreateCollation)
				if create.Name.Parts[0].Text != name || create.From == nil || len(create.From.Parts) != 2 || create.From.Parts[1].Text != source {
					t.Errorf("concurrent FROM source changed for %s", name)
					return
				}
				if retained != nil && (retained.From == nil || len(retained.From.Parts) != 2 || retained.From.Parts[1].Text != source) {
					t.Errorf("retained concurrent FROM source changed")
					return
				}
				retained = create
				if err := check(); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	workers.Wait()
	if err := check(); err != nil {
		t.Fatal(err)
	}
}
