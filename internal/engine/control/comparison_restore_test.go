package control

import (
	"context"
	"encoding/base64"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

const (
	comparisonBegin  = "-- " + ComparisonContextBegin + "\nSET search_path = pg_catalog;\n"
	comparisonClose  = "-- " + ComparisonBindingEnd + "\nSET search_path = pg_catalog;\n"
	comparisonFinish = "-- " + ComparisonContextEnd + "\nRESET search_path;\n"
)

func comparisonBind(namespace string) string {
	raw := base64.StdEncoding.EncodeToString([]byte(`{"namespace":"` + namespace + `"}`))
	return "-- " + comparisonBindingPrefix + raw + "\nSET search_path = pg_catalog, " + namespace + ";\n"
}

// pairedComparison parses source the way Restore does and pairs its markers.
func pairedComparison(t *testing.T, source string) (*ComparisonRestore, []parser.Statement, error) {
	t.Helper()
	c := &ComparisonRestore{}
	statements, err := parser.ParseWithOptions(source, parser.Options{Context: context.Background(), StandardConformingStrings: true, LineComment: func(comment string, position parser.Position) error {
		_, err := c.Decode(comment, position)
		return err
	}})
	if err != nil {
		t.Fatal(err)
	}
	return c, statements, c.Pair(source, statements)
}

func emptyComparisonCatalog() catalog.Read { return catalog.NewRead(nil, catalog.NewState(1)) }

func wantInvalidComparison(t *testing.T, step string, err error) {
	t.Helper()
	if err == nil || err.Error() != InvalidComparisonContext().Error() {
		t.Fatalf("%s: got %v, want the invalid comparison context error", step, err)
	}
}

func wantComparisonStep(t *testing.T, c *ComparisonRestore, step string, index int, stmt parser.Statement, current catalog.Read, execute bool) {
	t.Helper()
	got, err := c.Before(index, stmt, current)
	if err != nil || got != execute {
		t.Fatalf("%s: execute=%v err=%v, want execute=%v", step, got, err, execute)
	}
}

func TestComparisonRestorePairing(t *testing.T) {
	c, statements, err := pairedComparison(t, comparisonBegin+"CREATE TABLE public.t(v integer);\n")
	if err != nil {
		t.Fatal(err)
	}
	wantComparisonStep(t, c, "marked SET", 0, statements[0], emptyComparisonCatalog(), true)
	wantComparisonStep(t, c, "unmarked DDL", 1, statements[1], emptyComparisonCatalog(), false)

	for name, source := range map[string]string{
		"unpaired marker":         "CREATE TABLE public.t(v integer);\n-- " + ComparisonContextBegin + "\n",
		"statement after marker":  "-- " + ComparisonContextBegin + "\nSELECT 1;\nSET search_path = pg_catalog;\n",
		"two markers on one SET":  "-- " + ComparisonContextBegin + "\n" + comparisonClose,
		"marker after its RESET":  "RESET search_path;\n-- " + ComparisonContextEnd + "\n",
		"malformed binding codec": "-- stuntgres:comparison-binding !!!\nSET search_path = pg_catalog, public;\n",
	} {
		t.Run(name, func(t *testing.T) {
			c := &ComparisonRestore{}
			statements, err := parser.ParseWithOptions(source, parser.Options{Context: context.Background(), StandardConformingStrings: true, LineComment: func(comment string, position parser.Position) error {
				_, err := c.Decode(comment, position)
				return err
			}})
			if err == nil {
				err = c.Pair(source, statements)
			}
			wantInvalidComparison(t, name, err)
		})
	}
}

// TestComparisonRestoreWindow walks begin, bind, one DDL, close and finish.
// A valid stock cube manifest needs the engine, so the bind step is checked
// to reject a catalog without cube and the bound window is then seeded.
func TestComparisonRestoreWindow(t *testing.T) {
	source := comparisonBegin + comparisonBind("public") +
		"CREATE TABLE public.t(v integer);\nCREATE TABLE public.u(v integer);\n" +
		comparisonClose + comparisonFinish
	c, statements, err := pairedComparison(t, source)
	if err != nil {
		t.Fatal(err)
	}
	wantComparisonStep(t, c, "begin", 0, statements[0], emptyComparisonCatalog(), true)
	_, err = c.Before(1, statements[1], emptyComparisonCatalog())
	wantInvalidComparison(t, "bind without cube", err)

	namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 2200}
	c.state, c.namespace = comparisonBound, namespace
	before := catalog.NewState(7)
	wantComparisonStep(t, c, "window DDL", 2, statements[2], catalog.NewRead(nil, before), false)
	wantInvalidComparison(t, "finish inside a window", c.Finish())
	check, ok := c.After()
	if !ok || check.Namespace != namespace || check.Position != statements[2].(*parser.CreateTable).Pos.Offset || check.Before.State() != before {
		t.Fatalf("check=%+v ok=%v", check, ok)
	}
	c.Bound()
	if _, ok := c.After(); ok {
		t.Fatal("check due twice for one window")
	}
	_, err = c.Before(3, statements[3], emptyComparisonCatalog())
	wantInvalidComparison(t, "second DDL in one window", err)
	wantComparisonStep(t, c, "close", 4, statements[4], emptyComparisonCatalog(), true)
	wantComparisonStep(t, c, "finish", 5, statements[5], emptyComparisonCatalog(), true)
	if err := c.Finish(); err != nil {
		t.Fatal(err)
	}
	_, err = c.Before(6, statements[3], emptyComparisonCatalog())
	wantInvalidComparison(t, "statement after finish", err)
}

func TestComparisonRestoreBoundWindowNeedsDDL(t *testing.T) {
	statements, err := parser.Parse("SELECT 1;")
	if err != nil {
		t.Fatal(err)
	}
	c := &ComparisonRestore{state: comparisonBound}
	_, err = c.Before(0, statements[0], emptyComparisonCatalog())
	wantInvalidComparison(t, "non-DDL in a bound window", err)
}

func TestComparisonRestoreOutsideWindow(t *testing.T) {
	statements, err := parser.Parse("SET search_path = public; RESET search_path; SET search_path = pg_catalog; CREATE TABLE public.t(v integer);")
	if err != nil {
		t.Fatal(err)
	}
	c := &ComparisonRestore{}
	for i, stmt := range statements[:3] {
		_, err := c.Before(i, stmt, emptyComparisonCatalog())
		wantInvalidComparison(t, "unmarked search_path change", err)
	}
	wantComparisonStep(t, c, "ordinary DDL", 3, statements[3], emptyComparisonCatalog(), false)
	if err := c.Finish(); err != nil {
		t.Fatal(err)
	}
}

func TestComparisonRestoreFinishRejections(t *testing.T) {
	t.Run("begin without finish", func(t *testing.T) {
		c, statements, err := pairedComparison(t, comparisonBegin)
		if err != nil {
			t.Fatal(err)
		}
		wantComparisonStep(t, c, "begin", 0, statements[0], emptyComparisonCatalog(), true)
		wantInvalidComparison(t, "finish", c.Finish())
	})
	t.Run("finish without a window", func(t *testing.T) {
		c, statements, err := pairedComparison(t, comparisonBegin+comparisonFinish)
		if err != nil {
			t.Fatal(err)
		}
		wantComparisonStep(t, c, "begin", 0, statements[0], emptyComparisonCatalog(), true)
		_, err = c.Before(1, statements[1], emptyComparisonCatalog())
		wantInvalidComparison(t, "finish marker", err)
	})
	t.Run("finish without begin", func(t *testing.T) {
		c, statements, err := pairedComparison(t, comparisonFinish)
		if err != nil {
			t.Fatal(err)
		}
		_, err = c.Before(0, statements[0], emptyComparisonCatalog())
		wantInvalidComparison(t, "finish marker", err)
	})
	t.Run("close without bind", func(t *testing.T) {
		c, statements, err := pairedComparison(t, comparisonBegin+comparisonClose)
		if err != nil {
			t.Fatal(err)
		}
		wantComparisonStep(t, c, "begin", 0, statements[0], emptyComparisonCatalog(), true)
		_, err = c.Before(1, statements[1], emptyComparisonCatalog())
		wantInvalidComparison(t, "close marker", err)
	})
	t.Run("unconsumed marker", func(t *testing.T) {
		c, _, err := pairedComparison(t, comparisonBegin)
		if err != nil {
			t.Fatal(err)
		}
		wantInvalidComparison(t, "finish", c.Finish())
	})
}

func TestValidateComparisonBinding(t *testing.T) {
	namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 2200}
	for name, sql := range map[string]string{
		"unsupported statement": "CREATE SEQUENCE public.s",
		"unqualified target":    "CREATE TABLE t(v integer)",
		"unqualified index":     "CREATE INDEX i ON t(v)",
		"no published receipt":  "CREATE TABLE public.t(v integer)",
	} {
		t.Run(name, func(t *testing.T) {
			statements, err := parser.Parse(sql)
			if err != nil {
				t.Fatal(err)
			}
			err = ValidateComparisonBinding(emptyComparisonCatalog(), emptyComparisonCatalog(), statements[0], namespace, 0)
			wantInvalidComparison(t, name, err)
		})
	}
}
