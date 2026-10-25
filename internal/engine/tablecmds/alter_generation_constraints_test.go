package tablecmds_test

import (
	"errors"
	"maps"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func alterGenerationName(name string) parser.ObjectName {
	return parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}
}
func alterGenerationColumn(name string) parser.Expr {
	return parser.ColumnExpr{Name: alterGenerationName(name)}
}
func alterGenerationOperator(name string, args ...parser.Expr) parser.Expr {
	return parser.OperatorExpr{Name: alterGenerationName(name), Args: args}
}

func TestAlterGeneratedExpressionConstraintRoots(t *testing.T) {
	for _, kind := range []string{"not-null", "check", "unique", "expression-index", "predicate-index", "exclusion", "fk-child", "fk-parent"} {
		t.Run(kind, func(t *testing.T) {
			e := engine.New()
			admin, cleanup := enginetest.RoleOperation(t, e, "postgres")
			defer cleanup()
			execute := func(stmt parser.Statement) {
				t.Helper()
				result, err := e.Execute(admin, stmt, 0, nil)
				if result.Rows != nil {
					result.Rows.Close()
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			column := func(name string, constraints ...parser.ColumnConstraint) parser.TableElement {
				return parser.TableElement{Column: &parser.Column{Name: name, TypeName: parser.TypeName{Name: "int4"}, Constraints: constraints}}
			}
			unique := func(name string) *parser.UniqueConstraint {
				return &parser.UniqueConstraint{Name: parser.Identifier{Text: name}, Columns: []parser.Identifier{{Text: "b"}}}
			}
			fk := func(name, target string) *parser.ForeignKeyConstraint {
				return &parser.ForeignKeyConstraint{Name: parser.Identifier{Text: name}, Columns: []parser.Identifier{{Text: "b"}}, Target: alterGenerationName(target), Referenced: []parser.Identifier{{Text: "b"}}}
			}
			insert := func(name, column string, numbers ...int64) *parser.Insert {
				rows := make([][]parser.Expr, len(numbers))
				for i, n := range numbers {
					rows[i] = []parser.Expr{alterGenerationLiteral(n)}
				}
				return &parser.Insert{Name: alterGenerationName(name), Targets: []parser.InsertTarget{{Name: column}}, Rows: rows}
			}
			if kind == "fk-child" {
				execute(&parser.CreateTable{Name: alterGenerationName("p"), Elements: []parser.TableElement{column("b"), {Unique: unique("p_u")}}})
				execute(insert("p", "b", 2, 3))
			}
			constraints := []parser.ColumnConstraint{{Generation: &parser.GenerationDefinition{Mode: parser.IdentityAlways, Kind: "stored", Expression: alterGenerationOperator("+", alterGenerationColumn("a"), alterGenerationLiteral(1))}}}
			if kind == "not-null" {
				constraints = append(constraints, parser.ColumnConstraint{NotNull: true})
			}
			elements := []parser.TableElement{column("a"), column("b", constraints...)}
			if kind == "predicate-index" {
				elements = append(elements, column("k", parser.ColumnConstraint{Default: alterGenerationLiteral(0)}))
			}
			switch kind {
			case "check":
				elements = append(elements, parser.TableElement{Check: &parser.CheckConstraint{Name: parser.Identifier{Text: "ck"}, Expression: alterGenerationOperator("<", alterGenerationColumn("b"), alterGenerationLiteral(4))}})
			case "unique", "fk-parent":
				elements = append(elements, parser.TableElement{Unique: unique("g_u")})
			case "fk-child":
				elements = append(elements, parser.TableElement{ForeignKey: fk("g_fk", "p")})
			case "exclusion":
				elements = append(elements, parser.TableElement{Exclusion: &parser.ExclusionConstraint{Name: parser.Identifier{Text: "g_ex"}, Index: parser.CreateIndex{Method: "btree", Keys: []parser.IndexKey{{Column: parser.Identifier{Text: "b"}}}}, Operators: []parser.ObjectName{alterGenerationName("=")}}})
			}
			execute(&parser.CreateTable{Name: alterGenerationName("g"), Elements: elements})
			execute(insert("g", "a", 1, 2))
			if kind == "fk-parent" {
				execute(&parser.CreateTable{Name: alterGenerationName("h"), Elements: []parser.TableElement{column("b"), {ForeignKey: fk("h_fk", "g")}}})
				execute(insert("h", "b", 2, 3))
			}
			if kind == "expression-index" {
				execute(&parser.CreateIndex{Name: parser.Identifier{Text: "g_expression"}, Table: alterGenerationName("g"), Method: "btree", Keys: []parser.IndexKey{{Expression: alterGenerationOperator("/", alterGenerationColumn("b"), alterGenerationOperator("-", alterGenerationColumn("b"), alterGenerationLiteral(5)))}}})
			}
			if kind == "predicate-index" {
				execute(&parser.CreateIndex{Name: parser.Identifier{Text: "g_predicate"}, Table: alterGenerationName("g"), Method: "btree", Unique: true, Keys: []parser.IndexKey{{Column: parser.Identifier{Text: "k"}}}, Predicate: alterGenerationOperator("<", alterGenerationColumn("b"), alterGenerationLiteral(3))})
			}
			ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "g")
			old := e.Catalog.State.MustRelation(ref.Address)
			store := e.Storage.Relations[ref.Address]
			catalog := e.Catalog.State
			roots := maps.Clone(e.Storage.IndexRoots)
			rowID, versionID, commit := e.Storage.RowFrontier(), e.Storage.VersionFrontier(), e.Visibility.CommitSeq()
			generation, ends := store.Generation, store.Ends
			values := make([][]sqltype.Value, len(store.Versions))
			for i, row := range store.Versions {
				values[i] = slices.Clone(row.Values)
			}
			scope, err := e.StartStatement(admin)
			if err != nil {
				t.Fatal(err)
			}
			defer scope.Finish(errors.New("constraint unit cleanup"))
			writes, deletes := maps.Clone(scope.Owner.Writes), maps.Clone(scope.Owner.Deletes)
			expr := alterGenerationLiteral(2)
			code := "23505"
			switch kind {
			case "not-null":
				expr = parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}
				code = "23502"
			case "check":
				expr = alterGenerationLiteral(5)
				code = "23514"
			case "exclusion":
				code = "23P01"
			case "fk-parent":
				expr = alterGenerationOperator("+", alterGenerationColumn("a"), alterGenerationLiteral(2))
				code = "23503"
			case "fk-child":
				expr = alterGenerationLiteral(5)
				code = "23503"
			case "expression-index":
				expr = alterGenerationLiteral(5)
				code = "22012"
			}
			_, err = scope.Execute(alterGenerationStatement(alterGenerationAction(expr)), 0, nil)
			if err == nil && kind == "expression-index" {
				err = scope.Finish(nil)
			}
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != code {
				t.Fatal("constraint rewrite accepted/incorrect failure", kind, err)
			}
			if e.Catalog.State != catalog || e.Catalog.State.Get(ref.Address) != old || e.Storage.Relations[ref.Address] != store || store.Generation != generation || store.Ends != ends || e.Storage.RowFrontier() != rowID || e.Storage.VersionFrontier() != versionID || e.Visibility.CommitSeq() != commit || scope.Owner.Catalog != nil || !reflect.DeepEqual(scope.Owner.Writes, writes) || !reflect.DeepEqual(scope.Owner.Deletes, deletes) {
				t.Fatal("constraint failure leaked private/installed roots/counters")
			}
			if len(e.Storage.IndexRoots) != len(roots) {
				t.Fatal("index inventory changed")
			}
			for address, root := range roots {
				if e.Storage.IndexRoots[address] != root {
					t.Fatal("index/FK root changed", address)
				}
			}
			for i, row := range store.Versions {
				if !slices.Equal(row.Values, values[i]) {
					t.Fatal("source row changed")
				}
			}
			if !scope.Finished {
				if err := scope.Finish(err); err == nil {
					t.Fatal("failed statement committed")
				}
			}
			enginetest.LockEmpty(t, e)
		})
	}
}
