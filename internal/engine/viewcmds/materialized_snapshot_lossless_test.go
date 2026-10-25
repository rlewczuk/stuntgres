package viewcmds_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/engine/viewcmds"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestMaterializedViewSnapshotLosslessCoercions(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	input, _ := op.Session.Settings.Presentation()
	input.ExactSnapshot = true
	for _, id := range []sqltype.ID{sqltype.Timestamp, sqltype.Time, sqltype.Timestamptz} {
		b := core.NewExpressionBinder(context.Background(), e, input, op, nil)
		u, _, err := b.Type(id).ResolveModifiers(3)
		if err != nil {
			t.Fatal(err)
		}
		text := "2024-01-02 12:34:56.123789"
		if id == sqltype.Time {
			text = "12:34:56.123789"
		}
		if id == sqltype.Timestamptz {
			text += "+00"
		}
		if _, err := b.InputConstant(text, u, parser.Position{}); err == nil {
			t.Fatal("bind-time temporal input erased precision", id)
		}
		if len(b.Prepared.Temporals) != 0 {
			t.Fatal("failed input installed temporal capture")
		}
	}
	b := core.NewExpressionBinder(context.Background(), e, input, op, nil)
	u, _, err := b.Type(sqltype.Numeric).ResolveModifiers(5, 2)
	if err != nil {
		t.Fatal(err)
	}
	v, err := u.Type.Input(context.Background(), "12.345", -1)
	if err != nil {
		t.Fatal(err)
	}
	coercionPlan, err := sqltype.PlanCoercion(u.Type.Usage(), u, sqltype.Explicit)
	if err != nil {
		t.Fatal(err)
	}
	literal := plan.LiteralExpr{Info: expr.Info{Usage: u.Type.Usage()}, Value: v}
	inner := plan.CoercionExpr{Info: expr.Info{Usage: u}, Child: literal, Plan: coercionPlan, Mode: sqltype.Explicit}
	// The explicit node already has the destination usage. A final assignment
	// wrapper is neither required nor sufficient to validate its child.
	if _, err := inner.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Input: input}}); err == nil {
		t.Fatal("matching explicit usage lost datum")
	}
	wide, err := sqltype.PlanCoercion(u, u.Type.Usage(), sqltype.Explicit)
	if err != nil {
		t.Fatal(err)
	}
	outer := plan.CoercionExpr{Info: expr.Info{Usage: u.Type.Usage()}, Child: inner, Plan: wide, Mode: sqltype.Explicit}
	if _, err := outer.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Input: input}}); err == nil {
		t.Fatal("outer widening hid inner loss")
	}
	// Ordinary SQL retains its intentional rounding semantics.
	if _, err := outer.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}); err != nil {
		t.Fatal("ordinary coercion changed", err)
	}
	// The routine container codec emits a closed concatenation tree. Admission
	// must recurse through both operands rather than accept arbitrary SQL below
	// a recognized operator.
	for _, sample := range []struct {
		sql   string
		valid bool
	}{
		{`SELECT '(' || ('pg_catalog.abs(integer)'::regprocedure::oid::text) || ',integer)'`, true},
		{`SELECT 'prefix' || lower('suffix')`, false},
		{`SELECT 'prefix' || current_user`, false},
		{`SELECT 'prefix' || missing_column`, false},
		{`SELECT 'prefix' || (1 + 2)::text`, false},
	} {
		statements, err := parser.Parse(sample.sql)
		if err != nil {
			t.Fatal(sample.sql, err)
		}
		raw := statements[0].(*parser.Select).Targets[0]
		if err := view.MaterializedSnapshotScalar(raw); (err == nil) != sample.valid {
			t.Fatal("snapshot concatenation boundary", sample.sql, err)
		}
	}
}

func TestMaterializedViewSnapshotConstructorTargets(t *testing.T) {
	record := view.MaterializedSnapshot{Schema: "public", Name: "snapshot"}
	d := &catalog.Relation{Columns: []catalog.Column{{Name: "a"}, {Name: "b"}}}
	for _, sample := range []struct {
		sql   string
		valid bool
	}{
		{`INSERT INTO public.snapshot VALUES (NULL, NULL)`, true},
		{`INSERT INTO public.snapshot (a[-2:-1][3:4], b) VALUES (ARRAY[ARRAY[1,2],ARRAY[3,4]], NULL)`, true},
		{`INSERT INTO public.snapshot (a, b[0:1]) VALUES (NULL, ARRAY[1,2])`, true},
		{`INSERT INTO public.snapshot (b[-2:-1], a) VALUES (ARRAY[1,2], NULL)`, false},
		{`INSERT INTO public.snapshot (a[-2:-1], a) VALUES (ARRAY[1,2], NULL)`, false},
		{`INSERT INTO public.snapshot (a[-2:-1]) VALUES (ARRAY[1,2])`, false},
		{`INSERT INTO public.snapshot (a, b) VALUES (NULL, NULL)`, false},
		{`INSERT INTO public.snapshot (a[-2], b) VALUES (1, NULL)`, false},
		{`INSERT INTO public.snapshot (a[:-1], b) VALUES (ARRAY[1,2], NULL)`, false},
		{`INSERT INTO public.snapshot (a[-2:], b) VALUES (ARRAY[1,2], NULL)`, false},
		{`INSERT INTO public.snapshot (a[lower('x'):1], b) VALUES (ARRAY[1,2], NULL)`, false},
		{`INSERT INTO public.snapshot (a[missing_column:1], b) VALUES (ARRAY[1,2], NULL)`, false},
		{`INSERT INTO public.snapshot (a[-2:-1], b) VALUES (lower('x'), NULL)`, false},
		{`INSERT INTO public.snapshot (a[-2:-1], b) VALUES (ARRAY[1,2], NULL) RETURNING a`, false},
		{`INSERT INTO public.snapshot (a[-2:-1], b) VALUES (ARRAY[1,2], NULL) ON CONFLICT DO NOTHING`, false},
		{`INSERT INTO public.other (a[-2:-1], b) VALUES (ARRAY[1,2], NULL)`, false},
	} {
		statements, err := parser.Parse(sample.sql)
		if err != nil {
			t.Fatal(sample.sql, err)
		}
		if _, err := view.MaterializedSnapshotInsert(record, d, statements[0]); (err == nil) != sample.valid {
			t.Fatal("snapshot constructor target boundary", sample.sql, err)
		}
	}
}

// Exercise the closed snapshot transport and destination-owned evaluation
// together: recognizing a function name alone must never authorize its children.
func TestMaterializedViewSnapshotNestedConstructors(t *testing.T) {
	e := engine.New().RestoreStage()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	domainUnitTable(t, e, op, "CREATE TABLE public.snapshot_leaf(c circle)")
	relation := domainUnitTable(t, e, op, "CREATE TABLE public.snapshot_nested(v public.snapshot_leaf[])")
	holder := domainUnitTable(t, e, op, "CREATE TABLE public.snapshot_holder(v public.snapshot_nested)")
	auth, err := core.OperationAuthorization(op)
	if err != nil {
		t.Fatal(err)
	}
	input, _ := op.Session.Settings.Presentation()
	input.ExactSnapshot = true
	record := view.MaterializedSnapshot{Schema: "public", Name: "snapshot_nested"}
	leaf := `ROW(pg_catalog.circle('(1,2)'::pg_catalog.point,'-3'::pg_catalog.float8))::public.snapshot_leaf`
	seed := `pg_catalog.array_fill(` + leaf + `, ARRAY[1], ARRAY[-2])`
	cat := `pg_catalog.array_cat(` + seed + `, CAST(ARRAY[NULL::public.snapshot_leaf,ROW(NULL::circle)::public.snapshot_leaf] AS public.snapshot_leaf[]))`
	importValue := func(text string) ([][]sqltype.Value, error) {
		t.Helper()
		statements, err := parser.Parse("INSERT INTO public.snapshot_nested VALUES(" + text + ")")
		if err != nil {
			return nil, err
		}
		insert, err := view.MaterializedSnapshotInsert(record, relation, statements[0])
		if err != nil {
			return nil, err
		}
		return viewcmds.ImportMaterializedSnapshotRow(e, op.Context(), op, auth, relation, insert, input, nil)
	}
	for _, sample := range []struct {
		text   string
		length int
	}{{seed, 1}, {cat, 3}} {
		rows, err := importValue(sample.text)
		if err != nil || len(rows) != 1 || len(rows[0]) != 1 {
			t.Fatal("closed constructor import", rows, err)
		}
		value := rows[0][0]
		if value.Type() != relation.Columns[0].Usage.Type || value.ArrayRank() != 1 || value.ArrayDimension(0) != (sqltype.ArrayDimension{Lower: -2, Length: int32(sample.length)}) {
			t.Fatal("closed constructor destination shape", value)
		}
		circle := value.ArrayElement(0).CompositeField(0)
		text, err := circle.Type().Output(op.Context(), circle)
		if err != nil || text != "<(1,2),-3>" {
			t.Fatal("closed constructor payload", text, err)
		}
		if sample.length == 3 && (!value.ArrayElement(1).IsNull() || value.ArrayElement(2).IsNull() || !value.ArrayElement(2).CompositeField(0).IsNull()) {
			t.Fatal("NULL composite confused with ROW(NULL)")
		}
	}
	for _, text := range []string{
		`array_fill(` + leaf + `, ARRAY[1], ARRAY[-2])`,
		`public.array_fill(` + leaf + `, ARRAY[1], ARRAY[-2])`,
		`pg_catalog.array_fill(` + leaf + `, ARRAY[1])`,
		`pg_catalog.array_fill(` + leaf + `, ARRAY[2], ARRAY[-2])`,
		`pg_catalog.array_fill(` + leaf + `, ARRAY[1,1], ARRAY[-2,3])`,
		`pg_catalog.array_fill(` + leaf + `, ARRAY[1], ARRAY[2147483648])`,
		`pg_catalog.array_fill(` + leaf + `, ARRAY[1], ARRAY[-2147483649])`,
		`pg_catalog.array_fill(` + leaf + `, ARRAY[1], ARRAY[$1])`,
		`pg_catalog.array_fill(` + leaf + `, ARRAY[1], ARRAY[missing_column])`,
		`pg_catalog.array_fill(` + leaf + `, ARRAY[1], ARRAY[pg_catalog.abs(-2)])`,
		`pg_catalog.array_fill(lower('x'), ARRAY[1], ARRAY[-2])`,
		`pg_catalog.array_fill(` + leaf + `, ARRAY[1], VARIADIC ARRAY[-2])`,
		`pg_catalog.array_fill(` + leaf + `, ARRAY[1], array_lower => ARRAY[-2])`,
		seed + ` FILTER (WHERE true)`,
		seed + ` OVER ()`,
		`pg_catalog.array_cat(` + seed + `, ARRAY[` + leaf + `])`,
		`pg_catalog.array_cat(` + seed + `, CAST(ARRAY[] AS public.snapshot_leaf[]))`,
		`pg_catalog.array_cat(` + seed + `, CAST(ARRAY[` + leaf + `] AS snapshot_leaf[]))`,
		`pg_catalog.array_cat(` + seed + `, CAST(ARRAY[1] AS pg_catalog.int4[]))`,
		`pg_catalog.array_cat(` + seed + `, CAST(ARRAY[missing_column] AS public.snapshot_leaf[]))`,
		`pg_catalog.array_cat(` + seed + `, ` + seed + `)`,
	} {
		if _, err := importValue(text); err == nil {
			t.Fatal("noncanonical or unbound snapshot constructor admitted", text)
		}
	}
	// Failed imports do not poison the statement owner or later valid imports.
	if _, err := importValue(cat); err != nil {
		t.Fatal("closed import after refusal", err)
	}
	// Direct rank-two bounded targets remain supported. Without its own target,
	// this exact value must be refused rather than silently rebased.
	source := enginetest.Statement(t, `INSERT INTO public.snapshot_nested(v[-2:-1][3:3]) VALUES(CAST(ARRAY[ARRAY[`+leaf+`],ARRAY[`+leaf+`]] AS public.snapshot_leaf[]))`)
	insert, err := view.MaterializedSnapshotInsert(record, relation, source)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := viewcmds.ImportMaterializedSnapshotRow(e, op.Context(), op, auth, relation, insert, input, nil)
	if err != nil {
		t.Fatal("direct rank-two target", err)
	}
	value := rows[0][0]
	literal := plan.LiteralExpr{Info: expr.Info{Usage: relation.Columns[0].Usage}, Value: value}
	if _, err := regobj.Literal(op.Context(), auth.Catalog, literal); err == nil {
		t.Fatal("non-one-based rank-two constructor escaped without its target")
	}
	if _, err := regobj.Literal(regobj.ConstructorBoundedInputContext(op.Context()), auth.Catalog, literal); err != nil {
		t.Fatal("direct rank-two bounded literal refused", err)
	}
	wrapped, err := sqltype.CompositeValue(holder.Columns[0].Usage.Type, []sqltype.Value{value})
	if err != nil {
		t.Fatal(err)
	}
	literal = plan.LiteralExpr{Info: expr.Info{Usage: holder.Columns[0].Usage}, Value: wrapped}
	if _, err := regobj.Literal(regobj.ConstructorBoundedInputContext(op.Context()), auth.Catalog, literal); err == nil {
		t.Fatal("outer authority leaked into a nested rank-two constructor")
	}
}

func TestMaterializedViewSnapshotConstructorProviders(t *testing.T) {
	for _, sample := range []struct {
		name       string
		definition string
		value      string
	}{
		{"fill", `CREATE FUNCTION pg_catalog.array_fill(integer,integer[],integer[]) RETURNS integer[] LANGUAGE SQL AS $$SELECT ARRAY[$1]$$`, `pg_catalog.array_fill(7,ARRAY[1],ARRAY[-2])`},
		{"cat", `CREATE FUNCTION pg_catalog.array_cat(integer[],integer[]) RETURNS integer[] LANGUAGE SQL AS $$SELECT $1$$`, `pg_catalog.array_cat(pg_catalog.array_fill(7,ARRAY[1],ARRAY[-2]),CAST(ARRAY[8] AS pg_catalog.int4[]))`},
	} {
		t.Run(sample.name, func(t *testing.T) {
			e := engine.New().RestoreStage()
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			relation := domainUnitTable(t, e, op, "CREATE TABLE public.snapshot_provider(v integer[])")
			auth, err := core.OperationAuthorization(op)
			if err != nil {
				t.Fatal(err)
			}
			declaration := enginetest.Statement(t, sample.definition).(*parser.CreateFunction)
			e.Mu.Lock()
			stage := e.BeginCatalogStage(auth.Catalog)
			err = routine.StageCreateFunction(schemacmds.RoutineHost{core.DDLHost{E: e, Op: op, CatalogStage: stage}}, auth, declaration, sqltype.InputEnvironment{})
			e.Mu.Unlock()
			if err != nil {
				t.Fatal("private callable declaration", err)
			}
			domainUnitPublish(t, e, stage)
			auth, err = core.OperationAuthorization(op)
			if err != nil {
				t.Fatal(err)
			}
			record := view.MaterializedSnapshot{Schema: "public", Name: "snapshot_provider"}
			source := enginetest.Statement(t, "INSERT INTO public.snapshot_provider VALUES("+sample.value+")")
			insert, err := view.MaterializedSnapshotInsert(record, relation, source)
			if err != nil {
				t.Fatal("canonical codec syntax rejected before provider selection", err)
			}
			input, _ := op.Session.Settings.Presentation()
			input.ExactSnapshot = true
			_, err = viewcmds.ImportMaterializedSnapshotRow(e, op.Context(), op, auth, relation, insert, input, nil)
			if err == nil || err.Error() != "stuntgres: invalid materialized view snapshot constructor provider" {
				t.Fatal("same-named SQL callable gained codec authority", err)
			}
		})
	}
}

func domainUnitTable(t *testing.T, e *core.Engine, op *session.Operation, q string) *catalog.Relation {
	t.Helper()
	raw := enginetest.Statement(t, q).(*parser.CreateTable)
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	stage := e.BeginCatalogStage(auth.Catalog)
	if err == nil {
		err = tablecmds.SchemaTableLocked(e, op, auth, stage, raw, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, auth.State.CurrentUser, sqltype.InputEnvironment{}, nil)
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	domainUnitPublish(t, e, stage)
	view := e.CatalogRead()
	addresses := view.Lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: raw.Name.Parts[len(raw.Name.Parts)-1].Text})
	if len(addresses) != 1 {
		t.Fatal(addresses)
	}
	return view.MustRelation(addresses[0])
}

func domainUnitPublish(t *testing.T, e *core.Engine, stage *core.CatalogStage) {
	t.Helper()
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: stage.Base.Epoch()}); err != nil {
		t.Fatal(err)
	}
}
