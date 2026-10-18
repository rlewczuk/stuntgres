package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestAlterTypeCheckRebind(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(p INT,a INT,b INT,CONSTRAINT z CHECK(a>0),CONSTRAINT aa CHECK(b>0),CONSTRAINT both_values CHECK(a<b),CONSTRAINT explicit_cast CHECK(a::text<>'0') NO INHERIT)")
	run(t, e, "INSERT INTO t VALUES(9,2,7)")
	ref := roleTableRef(t, e, "t")
	old := e.catalog.state.MustRelation(ref.Address)
	oldChecks := append([]checkConstraint(nil), old.CheckConstraints...)
	op, cleanup, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	actions := statement(t, "ALTER TABLE t ALTER a TYPE BIGINT USING b,ALTER b TYPE BIGINT USING a*10").(*parser.AlterTable).Actions
	queue, err := captureAlterTypeChecks(op, e.CatalogRead(), old, old, actions)
	if err != nil || len(queue) != 4 {
		t.Fatal(queue, err)
	}
	for i, want := range []string{"z", "both_values", "explicit_cast", "aa"} {
		if old.CheckConstraints[queue[i].index].Name != want {
			t.Fatal("discovery/declaration order", queue, i, want)
		}
	}
	if !strings.Contains(queue[2].sql, "CAST(") {
		t.Fatal("lost explicit cast", queue[2].sql)
	}
	run(t, e, "ALTER TABLE t DROP p,ALTER a TYPE BIGINT USING b,ALTER b TYPE BIGINT USING a*10")
	next := e.catalog.state.MustRelation(ref.Address)
	if next == old || !reflect.DeepEqual(old.CheckConstraints, oldChecks) || old.Columns[1].Usage.Type.ID() != 23 || next.Columns[0].Usage.Type.ID() != 20 || next.Columns[1].Usage.Type.ID() != 20 || !reflect.DeepEqual(next.Attributes, old.Attributes[1:]) {
		t.Fatal("mutated original/final layout", old, next)
	}
	if err := validateChecks(e.CatalogRead(), next, map[string]bool{}); err != nil {
		t.Fatal("new expression/manifest invalid", err)
	}
	for i, c := range next.CheckConstraints {
		if c.Name != oldChecks[i].Name || c.ordinal != oldChecks[i].ordinal || c.NoInherit != oldChecks[i].NoInherit {
			t.Fatal("lost identity", c, oldChecks[i])
		}
		if _, err := (scalarRenderer{owner: next}).sql(context.Background(), e.CatalogRead(), c.expression); err != nil {
			t.Fatal("stale usage/origin/provider", c.Name, err)
		}
		for _, dep := range c.dependencies {
			if dep.ref.Address.ClassID == ids.RelationClassID && dep.ref.Address.ObjectID == ref.Address.ObjectID && dep.ref.Address.SubID == old.Attributes[0] {
				t.Fatal("retained dropped slot", dep)
			}
		}
	}
	if rows := tuples(run(t, e, "SELECT * FROM t")); !reflect.DeepEqual(rows, [][]driver.Value{{int64(7), int64(20)}}) {
		t.Fatal(rows)
	}
}

func TestAlterTypeCheckAtomicity(t *testing.T) {
	for _, tc := range []struct {
		name, declaration, seed, action string
		want                            *sqlstate.Error
	}{
		{"same-type", "c INT CONSTRAINT ck CHECK(c>0)", "(1),(2)", "ALTER c TYPE INT USING -c", &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23514", Message: `check constraint "ck" of relation "t" is violated by some row`, Schema: "public", Table: "t", Constraint: "ck"}},
		{"late-false", "c INT CONSTRAINT ck CHECK(c>0)", "(3),(2)", "ALTER c TYPE BIGINT USING c-2", &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23514", Message: `check constraint "ck" of relation "t" is violated by some row`, Schema: "public", Table: "t", Constraint: "ck"}},
		{"check-eval", "c INT CONSTRAINT ck CHECK(10/c>0)", "(1)", "ALTER c TYPE BIGINT USING c-1", &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22012", Message: "division by zero"}},
		{"nn-before-check", "c INT NOT NULL CONSTRAINT ck CHECK(c IS NOT NULL)", "(1)", "ALTER c TYPE BIGINT USING NULL", &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `column "c" of relation "t" contains null values`, Schema: "public", Table: "t", Column: "c"}},
		{"later-nn", "c INT CONSTRAINT ck CHECK(c>0),n INT", "(1,NULL)", "ALTER c TYPE BIGINT,ALTER n SET NOT NULL", &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `column "n" of relation "t" contains null values`, Schema: "public", Table: "t", Column: "n"}},
		{"later-add-nn", "c INT CONSTRAINT ck CHECK(c>0),n INT", "(1,NULL)", "ALTER c TYPE BIGINT,ADD CONSTRAINT n_nn NOT NULL n", &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `column "n" of relation "t" contains null values`, Schema: "public", Table: "t", Column: "n"}},
		{"binding-before-conversion", "c INT CONSTRAINT ck CHECK(c>0)", "(1)", "ALTER c TYPE TEXT USING (10/(c-c))::text", &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "operator does not exist: text > integer", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := New()
			run(t, e, "CREATE TABLE t("+tc.declaration+")")
			run(t, e, "INSERT INTO t VALUES"+tc.seed)
			ref := roleTableRef(t, e, "t")
			old, storage := e.catalog.state.Get(ref.Address), e.storage[ref.Address]
			rows := tuples(run(t, e, "SELECT * FROM t"))
			_, err := testExecute(e, context.Background(), statement(t, "ALTER TABLE t "+tc.action), nil)
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, tc.want) {
				t.Fatal(diagnostic, err, tc.want)
			}
			if e.catalog.state.Get(ref.Address) != old || e.storage[ref.Address] != storage || !reflect.DeepEqual(tuples(run(t, e, "SELECT * FROM t")), rows) {
				t.Fatal("published failed CHECK/NN candidate")
			}
			if err := validateChecks(e.CatalogRead(), old.(*relationDescriptor), map[string]bool{}); err != nil {
				t.Fatal("damaged incumbent manifest", err)
			}
		})
	}
}

func TestAlterTypeCheckQuotedOwnerParse(t *testing.T) {
	e := New()
	run(t, e, `CREATE SCHEMA "Type Schema"`)
	run(t, e, `CREATE TABLE "Type Schema"."Odd Table" (c INT CONSTRAINT ck CHECK(c>0))`)
	run(t, e, `INSERT INTO "Type Schema"."Odd Table" VALUES(1)`)
	run(t, e, `ALTER TABLE "Type Schema"."Odd Table" ALTER c TYPE BIGINT`)
	result := run(t, e, `SELECT c FROM "Type Schema"."Odd Table"`)
	if result.Rows.Schema()[0].Usage.Type.ID() != 20 || !reflect.DeepEqual(tuples(result), [][]driver.Value{{int64(1)}}) {
		t.Fatal("quoted owner CHECK reparse lost its final usage")
	}
	if _, err := parser.Parse("SELECT c"); err != nil {
		t.Fatal("TYPE CHECK reparse changed ordinary no-FROM parsing", err)
	}
}

func TestAlterTypeCheckResolvedLiteralCapture(t *testing.T) {
	for _, tc := range []struct {
		declaration, bound string
		id                 sqltype.ID
	}{
		{"TEXT", "0", sqltype.Text}, {"VARCHAR(3)", "0", sqltype.VarChar},
		{"CHAR(3)", "0", sqltype.BPChar}, {"NAME", "0", sqltype.Name},
		{"SMALLINT", "0", sqltype.Int2}, {"INT", "0", sqltype.Int4},
		{"BIGINT", "0", sqltype.Int8}, {"BOOL", "false", sqltype.Bool},
		{"REAL", "1.0000001", sqltype.Float4}, {"DOUBLE PRECISION", "1.0000001", sqltype.Float8},
		{"NUMERIC(8,3)", "0.125", sqltype.Numeric},
		{"UUID", "00000000-0000-0000-0000-000000000000", sqltype.UUID},
		{"BYTEA", `\x00`, sqltype.Bytea},
		{"DATE", "2000-01-01", sqltype.Date}, {"TIME(3)", "12:00:00.123", sqltype.Time},
		{"TIMETZ(3)", "12:00:00.123+00", sqltype.Timetz},
		{"TIMESTAMP(3)", "2000-01-01 12:00:00.123", sqltype.Timestamp},
		{"TIMESTAMPTZ(3)", "2000-01-01 12:00:00.123+00", sqltype.Timestamptz},
		{"INTERVAL", "1 day", sqltype.Interval},
	} {
		t.Run(tc.declaration, func(t *testing.T) {
			e := New()
			run(t, e, "CREATE TABLE t(c "+tc.declaration+" CONSTRAINT ck CHECK(c>'"+tc.bound+"' AND c<>NULL))")
			ref := roleTableRef(t, e, "t")
			original := e.catalog.state.MustRelation(ref.Address)
			op, cleanup, err := testOperation(e, context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			actions := statement(t, "ALTER TABLE t ALTER c TYPE "+tc.declaration).(*parser.AlterTable).Actions
			queue, err := captureAlterTypeChecks(op, e.CatalogRead(), original, original, actions)
			if err != nil || len(queue) != 1 {
				t.Fatal(queue, err)
			}
			pending := queue[0]
			if len(pending.literals) != 2 || !strings.Contains(pending.sql, "$1") || !strings.Contains(pending.sql, "$2") {
				t.Fatal("missing private literal transport", pending)
			}
			binder := newExpressionBinder(op.Context(), e, sqltype.InputEnvironment{}, op, nil)
			binder.checkConsumer, binder.noParameters, binder.checkLiterals = true, true, pending.literals
			for i, literal := range pending.literals {
				if literal.ResultUsage().Type.ID() != tc.id || literal.value.IsNull() != (i == 1) {
					t.Fatal("lost concrete/NULL usage", i, literal)
				}
				node, err := binder.bind(parser.ParameterExpr{Number: i + 1}, expressionPolicy{context: projectionContext, expected: binder.typ(sqltype.Text).Usage()})
				if err != nil || !reflect.DeepEqual(node, literal) {
					t.Fatal("re-input/coercion changed captured literal", i, node, literal, err)
				}
			}
			if len(binder.parameters) != 0 || len(binder.uses) != 0 || len(binder.anyParameters) != 0 {
				t.Fatal("transport leaked into parameter inference", binder)
			}
			for _, number := range []int{0, -1, 3} {
				_, err := binder.bind(parser.ParameterExpr{Number: number}, expressionPolicy{context: projectionContext})
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != "42P02" {
					t.Fatal("accepted unknown transport marker", number, err)
				}
			}
			binder.checkLiterals = nil
			_, err = binder.bind(parser.ParameterExpr{Number: 1}, expressionPolicy{context: projectionContext})
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "42P02" {
				t.Fatal("public CHECK parameter admission changed", err)
			}
			ordinary, err := (scalarRenderer{owner: original}).sql(context.Background(), e.CatalogRead(), original.CheckConstraints[0].expression)
			if err != nil || strings.Contains(ordinary, "$") {
				t.Fatal("changed ordinary CHECK serialization", ordinary, err)
			}
			if tc.id == sqltype.Float4 {
				literal := pending.literals[0]
				ordinaryLiteral, err := (scalarRenderer{}).sql(context.Background(), e.CatalogRead(), literal)
				if err != nil || ordinaryLiteral != "'1.0000001'" {
					t.Fatal("ordinary FLOAT4 output changed", ordinaryLiteral, err)
				}
				replayLiteral, err := (scalarRenderer{checkReplayPrecision: true}).sql(context.Background(), e.CatalogRead(), literal)
				if err != nil || replayLiteral != "'1.0000001192092896'" {
					t.Fatal("lost exact promoted CHECK value", replayLiteral, err)
				}
				for _, raw := range []string{"NaN", "Infinity", "-Infinity"} {
					value, err := literal.ResultUsage().Type.Input(context.Background(), raw, -1)
					if err != nil {
						t.Fatal(err)
					}
					special := literal
					special.value = value
					before, err := (scalarRenderer{}).sql(context.Background(), e.CatalogRead(), special)
					if err != nil {
						t.Fatal(err)
					}
					after, err := (scalarRenderer{checkReplayPrecision: true}).sql(context.Background(), e.CatalogRead(), special)
					if err != nil || before != after {
						t.Fatal("special float encoding changed", raw, before, after, err)
					}
				}
				typedNull := pending.literals[1]
				replayNull, err := (scalarRenderer{checkReplayPrecision: true}).sql(context.Background(), e.CatalogRead(), typedNull)
				if err != nil || replayNull != "NULL" {
					t.Fatal("typed NULL replay changed", replayNull, err)
				}
			}
			run(t, e, "ALTER TABLE t ALTER c TYPE "+tc.declaration)
		})
	}
}
