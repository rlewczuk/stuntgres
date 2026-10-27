package core_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
)

func TestRoutineTreeDependencyMutation(t *testing.T) {
	for _, name := range []string{"builtin-cost", "user-replacement"} {
		t.Run(name, func(t *testing.T) {
			e := engine.New()
			// abs is deliberately not admitted in defaults. Power retains the
			// same real builtin-capture hazard on an already-supported route.
			defaultExpression := "power(2::float8,3::float8)::integer"
			bodyExpression := "power(a::float8,3::float8)::integer"
			mutation := `ALTER FUNCTION pg_catalog.power(double precision,double precision) COST 7`
			if name == "user-replacement" {
				defaultExpression = "tree_dependency_callee(7)"
				bodyExpression = "tree_dependency_callee(a)"
				enginetest.Run(t, e, `CREATE FUNCTION tree_dependency_callee(integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1+1'`)
				mutation = `CREATE OR REPLACE FUNCTION tree_dependency_callee(integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1+2'`
			}
			enginetest.Run(t, e, `CREATE FUNCTION tree_dependency_consumer(a integer DEFAULT `+defaultExpression+`) RETURNS integer LANGUAGE SQL RETURN `+bodyExpression)
			before := e.CatalogRead()
			original := routineTreeProcedure(t, before, "tree_dependency_consumer")
			typ := routineTreeType(t, before)
			runtime := core.EngineCatalogRuntime{}
			defaults, body, err := runtime.RoutineTrees(context.Background(), before, original, typ)
			if err != nil {
				t.Fatal("initial metadata", err)
			}
			oldDefault, err := typ.Output(context.Background(), defaults)
			if err != nil {
				t.Fatal(err)
			}
			oldBody, err := typ.Output(context.Background(), body)
			if err != nil {
				t.Fatal(err)
			}
			enginetest.Run(t, e, mutation)
			current := e.CatalogRead()
			consumer := routineTreeProcedure(t, current, "tree_dependency_consumer")
			if consumer.SQLBody != original.SQLBody || consumer.SQLDefaults != original.SQLDefaults {
				t.Fatal("callee mutation replaced immutable consumer declaration owners")
			}
			// The executable validator must still distinguish this stale capture.
			if err := consumer.SQLBody.Validate(current); err == nil {
				t.Fatal("current executable admission was weakened")
			}
			if name == "user-replacement" {
				if err := consumer.SQLDefaults.Validate(current); err == nil {
					t.Fatal("current executable default validation was weakened")
				}
			}
			freshDefaults, freshBody, err := runtime.RoutineTrees(context.Background(), current, consumer, typ)
			if err != nil {
				t.Fatal("metadata improperly demanded current executable equivalence", err)
			}
			carrier, err := freshBody.Tree()
			if err != nil || carrier != original.SQLBody {
				t.Fatal("metadata substituted the retained checked program", err)
			}
			listCarrier, err := freshDefaults.Tree()
			if err != nil {
				t.Fatal(err)
			}
			list, ok := listCarrier.(plan.ExpressionListTree)
			checkedDefault, present := original.SQLDefaults.Default(0)
			if !ok || list.Len() != 1 || !present || !reflect.DeepEqual(list.At(0), checkedDefault) {
				t.Fatal("metadata substituted the retained checked default")
			}
			if got, err := typ.Output(context.Background(), freshDefaults); err != nil || got != oldDefault {
				t.Fatal("default tree changed during metadata acquisition", got, err)
			}
			if got, err := typ.Output(context.Background(), freshBody); err != nil || got != oldBody {
				t.Fatal("program tree changed during metadata acquisition", got, err)
			}
			if got, err := typ.Output(context.Background(), body); err != nil || got != oldBody {
				t.Fatal("old retained body changed", got, err)
			}
			foreign := engine.New().CatalogRead()
			if err := consumer.SQLBody.ValidateMetadata(foreign); !errors.Is(err, ids.ErrCatalogReplaced) {
				t.Fatal("foreign program provenance accepted", err)
			}
			if err := consumer.SQLDefaults.ValidateMetadata(foreign); !errors.Is(err, ids.ErrCatalogReplaced) {
				t.Fatal("foreign default provenance accepted", err)
			}
			if name == "user-replacement" {
				renderer := stored.Deparser{View: current, RoutineDeclaration: true}
				if text, err := renderer.SQL(context.Background(), checkedDefault, stored.DeparseParent{}); err != nil || !strings.Contains(text, "tree_dependency_callee(7)") {
					t.Fatal("default metadata cannot be deparsed after callee replacement", text, err)
				}
				if _, err := (stored.Deparser{View: current}).SQL(context.Background(), checkedDefault, stored.DeparseParent{}); err == nil {
					t.Fatal("ordinary stored deparser admission was weakened")
				}
				enginetest.Run(t, e, `ALTER FUNCTION tree_dependency_callee(integer) RENAME TO tree_dependency_renamed`)
				renderer.View = e.CatalogRead()
				if text, err := renderer.SQL(context.Background(), checkedDefault, stored.DeparseParent{}); err != nil || !strings.Contains(text, "tree_dependency_renamed(7)") {
					t.Fatal("default metadata did not use current logical label", text, err)
				}
			}
		})
	}
}
