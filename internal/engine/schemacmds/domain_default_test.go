package schemacmds_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestDomainDefaultRecipesInternal(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	before := e.CatalogRead()
	for _, q := range []string{
		"CREATE DOMAIN public.base_default AS integer DEFAULT 11 NOT NULL",
		"CREATE DOMAIN public.inherited_default AS public.base_default",
		"CREATE DOMAIN public.own_default AS public.base_default DEFAULT 22",
		"CREATE DOMAIN public.null_mask AS public.base_default DEFAULT NULL",
		"CREATE DOMAIN public.no_default AS integer",
		"CREATE DOMAIN public.numeric_default AS numeric(5,2) DEFAULT 12.345",
		"CREATE DOMAIN public.numeric_mask AS public.numeric_default DEFAULT NULL",
	} {
		domainUnitPublish(t, e, domainUnitStage(t, e, op, q))
	}
	view := e.CatalogRead()
	parent := domainUnitType(t, view, "base_default")
	child := domainUnitType(t, view, "inherited_default")
	own := domainUnitType(t, view, "own_default")
	mask := domainUnitType(t, view, "null_mask")
	if parent.DomainDefault == nil || child.DomainDefault != parent.DomainDefault || child.DomainDefaultOwn || !own.DomainDefaultOwn || !mask.DomainDefaultOwn || !child.SQLType.DomainNotNull() || child.SQLType.OwnDomainNotNull() {
		t.Fatal("immutable effective recipe/own authority lost")
	}
	if before.Get(parent.Address()) != nil {
		t.Fatal("old snapshot mutated")
	}
	for _, d := range []*catalog.Type{parent, child, own, mask} {
		if err := stored.ValidateDomainDefault(view, d); err != nil {
			t.Fatal(d.Name(), err)
		}
	}
	for _, tc := range []struct {
		d    *catalog.Type
		want int64
	}{{parent, 11}, {child, 11}, {own, 22}} {
		relation := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 60000}, Columns: []catalog.Column{{Name: "v", Usage: tc.d.SQLType.Usage()}}, Attributes: []int32{1}}
		node, origin := analyze.ColumnDefault(view, relation, 0)
		if origin.Definition.OID != 0 || origin.Consumer != nil || origin.Quote.Owner.Address != tc.d.Address() || origin.Quote.Attribute != 0 || origin.Quote.Definition != 0 {
			t.Fatal("domain recipe lost type origin or invented column identity")
		}
		c := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: view}}
		value, err := analyze.EvalDefault(&c, node, origin)
		if err != nil {
			t.Fatal(tc.d.Name(), err)
		}
		if c.RoutineRecipe || c.RoutineReferences != nil || c.RoutineConsumer != nil {
			t.Fatal("default evaluation leaked recipe authority")
		}
		physical, err := value.BaseValue()
		if err != nil {
			t.Fatal(err)
		}
		n, err := physical.Integer()
		if err != nil || n != tc.want || value.Type() != tc.d.SQLType {
			t.Fatal(tc.d.Name(), value, err)
		}
	}
	// NULL cooking never executes the immediate constrained base. Demand does.
	c := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: view}}
	_, err := mask.DomainDefault.Expression.Eval(&c)
	domainUnitDiagnostic(t, err, "23502", "domain public.base_default does not allow null values")
	if !plan.StructuralNull(mask.DomainDefault.Expression) {
		t.Fatal("NULL mask recipe lost structural null")
	}
	numericMask := domainUnitType(t, view, "numeric_mask")
	plan.WalkExpression(numericMask.DomainDefault.Expression, func(node expr.Expr) {
		if leaf, ok := node.(plan.LiteralExpr); ok && leaf.Value.IsNull() && leaf.Usage.Typmod != -1 {
			t.Fatal("NULL leaf pre-applied inherited typmod")
		}
	})
	facts, err := pgcatalog.TypeFacts(context.Background(), view, numericMask, sqltype.OutputEnvironment{})
	if err != nil || facts.Default != "NULL::numeric" || !facts.HasDefaultTree {
		t.Fatal("NULL mask recipe metadata", facts, err)
	}
	plain := domainUnitType(t, view, "no_default")
	integer, _ := view.LookupType(sqltype.Int4)
	null := plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage()}, Value: sqltype.Null(integer)}
	if !stored.OmitColumnDefault(view, null, integer.Usage()) || stored.OmitColumnDefault(view, null, parent.SQLType.Usage()) || !stored.OmitColumnDefault(view, null, plain.SQLType.Usage()) {
		t.Fatal("plain NULL omission/domain mask distinction")
	}
	badPresentation := *parent
	badPresentation.DomainDefaultText = ""
	if err := stored.ValidateDomainDefault(view, &badPresentation); err == nil {
		t.Fatal("recipe accepted absent stored presentation")
	}
	badPresentation = *plain
	badPresentation.DomainDefaultText = "NULL"
	if err := stored.ValidateDomainDefault(view, &badPresentation); err == nil {
		t.Fatal("presentation accepted absent recipe")
	}
	bad := *parent
	definition := *parent.DomainDefault
	definition.OID = 1
	bad.DomainDefault = &definition
	if err := stored.ValidateDomainDefault(view, &bad); err == nil {
		t.Fatal("domain accepted pg_attrdef identity")
	}
	foreign, _ := engine.New().CatalogRead().LookupType(sqltype.Int4)
	definition = *parent.DomainDefault
	definition.Expression = plan.LiteralExpr{Info: expr.Info{Usage: foreign.Usage()}, Value: sqltype.Null(foreign)}
	bad.DomainDefault = &definition
	if err := stored.ValidateDomainDefault(view, &bad); err == nil {
		t.Fatal("foreign recipe type accepted")
	}
}

func TestDomainDefaultLogicalSelectionInternal(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE DOMAIN public.view_default AS integer DEFAULT 22", "CREATE DOMAIN public.view_absent AS integer NOT NULL", "CREATE DOMAIN public.view_mask AS public.view_default DEFAULT NULL"} {
		domainUnitPublish(t, e, domainUnitStage(t, e, op, q))
	}
	view := e.CatalogRead()
	for _, tc := range []struct {
		name   string
		recipe bool
		null   bool
	}{{"view_default", true, false}, {"view_absent", false, false}, {"view_mask", true, true}} {
		typ := domainUnitType(t, view, tc.name).SQLType
		relation := &catalog.Relation{Columns: []catalog.Column{{Usage: typ.Usage()}}, Attributes: []int32{1}}
		node, origin := analyze.ColumnDefault(view, relation, 0)
		if (origin.Definition.Expression != nil) != tc.recipe {
			t.Fatal("logical absence conflated with domain recipe", tc.name)
		}
		if tc.null && (node == nil || !plan.StructuralNull(node)) {
			t.Fatal("NULL mask erased")
		}
		if !tc.recipe && (node == nil || !node.Properties().RuntimeDomainConversion) {
			t.Fatal("missing default lost fresh constrained NULL input")
		}
	}
	stamp := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13254})
	if stamp.DomainDefault == nil {
		t.Fatal("builtin time recipe absent")
	}
	if err := stored.ValidateDomainDefault(view, stamp); err != nil {
		t.Fatal(err)
	}
	if stamp.DomainDefault.Expression.Properties().Volatility != expr.Stable {
		t.Fatal("builtin time frozen")
	}
}

func TestDomainDefaultRemapInternal(t *testing.T) {
	source, target := engine.New(), engine.New()
	op, done := enginetest.RoleOperation(t, source, "postgres")
	defer done()
	for _, q := range []string{"CREATE DOMAIN public.remap_base AS integer DEFAULT 11 NOT NULL", "CREATE DOMAIN public.remap_child AS public.remap_base", "CREATE DOMAIN public.remap_cast AS integer DEFAULT 7::public.remap_base"} {
		domainUnitPublish(t, source, domainUnitStage(t, source, op, q))
	}
	original := source.CatalogRead()
	parent := domainUnitType(t, original, "remap_base")
	next := catalog.CloneState(original.State())
	address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Int4)}
	copy := *next.MustType(address)
	copy.SQLType = target.CatalogRead().MustType(address).SQLType
	next.SetDescriptor(address, &copy)
	if _, err := typedef.ReownDomainTypes(next, nil, false, routine.RangeTypeRebinder(next, nil)); err != nil {
		t.Fatal(err)
	}
	if err := stored.ReownExpressions(source.Catalog.Read(next), next, nil, true, nil); err != nil {
		t.Fatal(err)
	}
	remapped := source.Catalog.Read(next)
	for _, name := range []string{"remap_base", "remap_child", "remap_cast"} {
		d := domainUnitType(t, remapped, name)
		if err := stored.ValidateDomainDefault(remapped, d); err != nil {
			t.Fatal(name, err)
		}
		if d.DomainDefault.Expression.ResultUsage().Type == domainUnitType(t, original, name).DomainDefault.Expression.ResultUsage().Type {
			t.Fatal("recipe retained source codec", name)
		}
	}
	if !domainUnitType(t, remapped, "remap_child").SQLType.DomainNotNull() || !domainUnitType(t, remapped, "remap_base").DomainDefaultOwn || domainUnitType(t, remapped, "remap_child").DomainDefaultOwn {
		t.Fatal("remap lost own/effective provenance")
	}
	if parent.SQLType.DomainBase().Type == copy.SQLType || original.Get(parent.Address()) != parent {
		t.Fatal("source mutated")
	}
}

func TestDomainDefaultDependencyReasonInternal(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{
		"CREATE DOMAIN public.recipe_provider AS integer",
		"CREATE DOMAIN public.recipe_parent AS integer DEFAULT 7::public.recipe_provider",
		"CREATE DOMAIN public.recipe_child AS public.recipe_parent",
	} {
		domainUnitPublish(t, e, domainUnitStage(t, e, op, q))
	}
	view := e.CatalogRead()
	provider := domainUnitType(t, view, "recipe_provider")
	parent := domainUnitType(t, view, "recipe_parent")
	child := domainUnitType(t, view, "recipe_child")
	stage := domainUnitStage(t, e, op, "DROP DOMAIN public.recipe_provider CASCADE")
	defer e.DiscardCatalogStage(stage)
	edges, err := core.DomainDependencyClosure(context.Background(), stage, stage.DomainTargets)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, edge := range edges {
		if edge.Address != parent.Address() && edge.Address != child.Address() {
			continue
		}
		found++
		if edge.Root != provider.Address() || edge.Target != provider.Address() {
			t.Fatal("lost direct default-provider dependency", edge)
		}
	}
	if found != 2 {
		t.Fatal("missing own/inherited provider edges", found)
	}
}

func TestDomainDefaultValuesSchedulingInternal(t *testing.T) {
	for _, recipe := range []string{"", " DEFAULT 1"} {
		t.Run(recipe, func(t *testing.T) {
			e := engine.New()
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			for _, query := range []string{
				"CREATE SEQUENCE scheduled_counter START 50 CACHE 1",
				"CREATE DOMAIN scheduled_required AS integer NOT NULL" + recipe,
				"CREATE TABLE scheduled_rows(id integer,v scheduled_required,p bigint DEFAULT nextval('scheduled_counter'))",
				"CREATE VIEW scheduled_view AS SELECT p,v,id FROM scheduled_rows",
				"INSERT INTO scheduled_rows(id,v) VALUES(1,1)",
			} {
				enginetest.RoleExecute(t, e, op, query)
			}
			for _, target := range []string{"scheduled_rows", "scheduled_view"} {
				_, err := e.Execute(op, enginetest.Statement(t, "INSERT INTO "+target+"(id,v) VALUES(2,NULL)"), 0, nil)
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != "23502" {
					t.Fatal(target, err)
				}
			}
			result := enginetest.RoleExecute(t, e, op, "SELECT last_value,is_called FROM scheduled_counter")
			defer result.Rows.Close()
			rows := enginetest.Tuples(result)
			if len(rows) != 1 || len(rows[0]) != 2 || rows[0][0] != int64(50) || rows[0][1] != true {
				t.Fatal("rejected domain input demanded a later default", rows)
			}
		})
	}
}

func TestDomainDefaultBareNullInternal(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	for _, query := range []string{
		"CREATE DOMAIN public.elided_null AS integer DEFAULT NULL",
		"CREATE DOMAIN public.elided_child AS public.elided_null",
		"CREATE DOMAIN public.elided_nn AS integer DEFAULT NULL NOT NULL",
		"CREATE DOMAIN public.retained_numeric AS numeric(5,2) DEFAULT NULL",
		"CREATE DOMAIN public.retained_case AS integer DEFAULT CASE WHEN true THEN NULL::integer ELSE 1 END",
		"CREATE TABLE public.elided_nn_rows(v public.elided_nn)",
	} {
		enginetest.RoleExecute(t, e, op, query)
	}
	view := e.CatalogRead()
	for _, name := range []string{"elided_null", "elided_child", "elided_nn"} {
		d := domainUnitType(t, view, name)
		if d.DomainDefault != nil || d.DomainDefaultOwn || d.DomainDefaultText != "" {
			t.Fatal("bare NULL retained a recipe or fabricated own provenance", name)
		}
		if err := stored.ValidateDomainDefault(view, d); err != nil {
			t.Fatal(name, err)
		}
		facts, err := pgcatalog.TypeFacts(op.Context(), view, d, sqltype.OutputEnvironment{})
		if err != nil || facts.HasDefault || facts.HasDefaultTree || facts.Default != "" {
			t.Fatal("elided NULL catalog presence", name, facts, err)
		}
	}
	for _, name := range []string{"retained_numeric", "retained_case"} {
		d := domainUnitType(t, view, name)
		if d.DomainDefault == nil || !d.DomainDefaultOwn || d.DomainDefaultText == "" {
			t.Fatal("real NULL conversion/expression was elided", name)
		}
		if _, literal := d.DomainDefault.Expression.(plan.LiteralExpr); literal {
			t.Fatal("control lost its actual conversion/expression node", name)
		}
		if err := stored.ValidateDomainDefault(view, d); err != nil {
			t.Fatal(name, err)
		}
	}
	if !domainUnitType(t, view, "elided_nn").SQLType.OwnDomainNotNull() {
		t.Fatal("NULL recipe normalization removed NOT NULL")
	}
	_, err := e.Execute(op, enginetest.Statement(t, "INSERT INTO public.elided_nn_rows DEFAULT VALUES"), 0, nil)
	domainUnitDiagnostic(t, err, "23502", "domain elided_nn does not allow null values")
	result := enginetest.RoleExecute(t, e, op, "SELECT count(*) FROM public.elided_nn_rows")
	defer result.Rows.Close()
	rows := enginetest.Tuples(result)
	if len(rows) != 1 || len(rows[0]) != 1 || rows[0][0] != int64(0) {
		t.Fatal("elided default bypassed fresh NULL enforcement or atomicity", rows)
	}
}
