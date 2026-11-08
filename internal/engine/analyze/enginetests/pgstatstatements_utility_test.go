package enginetests

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/parser"
)

// Keep the closed parser statement inventory here even for wrappers excluded
// from collection: nested raw utility syntax must still have a projection.
func TestPgStatStatementsFingerprintUtilityInventory(t *testing.T) {
	roots := []parser.Statement{
		&parser.Explain{}, &parser.CreateDatabaseStmt{}, &parser.Vacuum{}, &parser.Analyze{}, &parser.Load{}, &parser.Checkpoint{}, &parser.AlterSystem{}, &parser.CreateTablespace{},
		&parser.AlterDatabaseStmt{},
		&parser.DropDatabaseStmt{},
		&parser.AlterTablespaceRename{}, &parser.DropTablespace{},
		&parser.AlterTrigger{}, &parser.DropTrigger{}, &parser.CreateRule{}, &parser.AlterRule{}, &parser.DropRule{}, &parser.Notify{}, &parser.Listen{}, &parser.Unlisten{},
		&parser.CreatePolicy{}, &parser.AlterPolicy{}, &parser.DropPolicy{}, &parser.Copy{}, &parser.PrepareSQL{}, &parser.ExecuteSQL{}, &parser.DeallocateSQL{}, &parser.Discard{},
		&parser.CreateExtension{}, &parser.DropExtension{}, &parser.AlterExtensionUpdate{}, &parser.AlterExtension{}, &parser.CreateEnum{}, &parser.CreateRange{}, &parser.AlterEnum{}, &parser.AlterType{},
		&parser.CreateComposite{}, &parser.AlterComposite{}, &parser.DropType{}, &parser.AlterTableOf{}, &parser.CreateDomain{}, &parser.DropDomain{}, &parser.AlterDomain{},
		&parser.CreateTable{}, &parser.AlterTableAttachPartition{}, &parser.AlterTableDetachPartition{}, &parser.CreateQueryTable{}, &parser.RefreshMaterializedView{}, &parser.CreateIndex{},
		&parser.CreateView{}, &parser.AlterView{}, &parser.DropView{}, &parser.CreateSequence{}, &parser.AlterSequence{}, &parser.DropSequence{}, &parser.DropTable{},
		&parser.AlterTableRenameColumn{}, &parser.AlterTableRenameTable{}, &parser.AlterTableSetSchema{}, &parser.AlterTable{}, &parser.AlterTableAllInTablespace{}, &parser.AlterTableDefault{},
		&parser.Insert{}, &parser.Merge{}, &parser.Delete{}, &parser.Update{}, &parser.Select{}, &parser.DeclareCursor{}, &parser.FetchCursor{}, &parser.CloseCursor{},
		&parser.LockTable{}, &parser.Truncate{}, &parser.Comment{}, &parser.TransactionControl{}, &parser.SavepointControl{}, &parser.Show{}, &parser.CreateRole{}, &parser.RoleMembership{}, &parser.ObjectGrant{},
		&parser.Set{}, &parser.Reset{}, &parser.SessionAuthorization{}, &parser.RoleSelection{}, &parser.TransactionCharacteristics{}, &parser.AlterRole{}, &parser.DropRole{},
		&parser.CreateSchema{}, &parser.AlterSchema{}, &parser.DropSchema{}, &parser.CreateAccessMethod{}, &parser.DropAccessMethod{}, &parser.CreateCast{}, &parser.DropCast{}, &parser.Cluster{},
		&parser.CreateCollation{}, &parser.AlterCollation{}, &parser.DropCollation{}, &parser.CreateConversion{}, &parser.AlterConversion{}, &parser.DropConversion{}, &parser.AlterDefaultPrivileges{},
		&parser.CreateDictionary{}, &parser.AlterDictionary{}, &parser.DropDictionary{}, &parser.CreateEventTrigger{}, &parser.AlterEventTrigger{}, &parser.DropEventTrigger{}, &parser.SetConstraints{}, &parser.AlterTableRenameConstraint{},
		&parser.ForeignDDL{}, &parser.DropIndex{}, &parser.AlterIndex{}, &parser.Reindex{}, &parser.CreateOperator{}, &parser.AlterOperator{}, &parser.DropOperator{},
		&parser.CreateOperatorClass{}, &parser.AlterOperatorClass{}, &parser.DropOperatorClass{}, &parser.CreateOperatorFamily{}, &parser.AlterOperatorFamily{}, &parser.DropOperatorFamily{},
		&parser.AlterOwner{}, &parser.ReassignOwned{}, &parser.DropOwned{}, &parser.Do{}, &parser.Publication{}, &parser.RenameRole{}, &parser.AlterRoleSettings{}, &parser.AlterDatabaseSettings{},
		&parser.Call{}, &parser.CreateFunction{}, &parser.CreateLanguage{}, &parser.AlterLanguage{}, &parser.DropLanguage{}, &parser.CreateTransform{}, &parser.DropTransform{},
		&parser.CreateAggregate{}, &parser.DropRoutine{}, &parser.AlterRoutine{}, &parser.SecurityLabel{}, &parser.CreateStatistics{}, &parser.DropStatistics{}, &parser.AlterStatisticsTarget{}, &parser.AlterStatisticsRename{}, &parser.AlterStatisticsSchema{},
		&parser.Subscription{}, &parser.CreateTemplate{}, &parser.AlterTemplate{}, &parser.DropTemplate{}, &parser.CreateTrigger{}, &parser.CreateConfiguration{}, &parser.AlterConfiguration{}, &parser.DropConfiguration{}, &parser.CreateParser{}, &parser.AlterParser{}, &parser.DropParser{},
	}
	for _, root := range roots {
		t.Run(reflect.TypeOf(root).Elem().Name(), func(t *testing.T) {
			// Populate optional statically typed children too: an empty root alone
			// cannot expose a missing nested option/constraint/type projection.
			var populate func(reflect.Value, int)
			populate = func(v reflect.Value, depth int) {
				if depth == 0 || !v.CanSet() {
					return
				}
				switch v.Kind() {
				case reflect.Pointer:
					v.Set(reflect.New(v.Type().Elem()))
					populate(v.Elem(), depth-1)
				case reflect.Struct:
					if v.Type() == reflect.TypeFor[parser.Position]() {
						return
					}
					if v.Type() == reflect.TypeFor[parser.LikeOption]() {
						v.FieldByName("Name").SetString("comments")
					}
					for i := range v.NumField() {
						populate(v.Field(i), depth-1)
					}
				case reflect.Slice:
					v.Set(reflect.MakeSlice(v.Type(), 1, 1))
					populate(v.Index(0), depth-1)
				case reflect.Map:
					v.Set(reflect.MakeMap(v.Type()))
					key := reflect.New(v.Type().Key()).Elem()
					element := reflect.New(v.Type().Elem()).Elem()
					populate(element, depth-1)
					v.SetMapIndex(key, element)
				}
			}
			populate(reflect.ValueOf(root).Elem(), 12)
			first, _ := analyze.JumbleUtility(root)
			// A source relocation never changes a released utility's identity.
			value := reflect.ValueOf(root).Elem()
			position := value.FieldByName("Pos")
			if position.IsValid() {
				position.Set(reflect.ValueOf(parser.Position{Offset: 41, Character: 37}))
			}
			second, _ := analyze.JumbleUtility(root)
			if first.QueryID() == 0 || first.QueryID() != second.QueryID() {
				t.Fatal("position entered utility identity", first.QueryID(), second.QueryID())
			}
		})
	}
}

func TestPgStatStatementsFingerprintUtilityFields(t *testing.T) {
	// Independently selected raw fields (REL_18_6 parsenodes.h) survive; source
	// coordinates, redundant spelling and identifier quote provenance do not.
	for _, tc := range []struct {
		left, right string
		same        bool
	}{
		{"CREATE TABLE t(k integer)", "CREATE TABLE t(k int)", true},
		{"CREATE TABLE t(k integer)", "CREATE TABLE t(k bigint)", false},
		{"CREATE TABLE t(k integer)", "CREATE TABLE t(v integer)", false},
		{"CREATE TABLE t(k integer)", "CREATE TABLE t(k integer DEFAULT 1)", false},
		{"CREATE TABLE t(k integer DEFAULT 1)", "CREATE TABLE t(k integer DEFAULT 2)", false},
		{"CREATE TABLE t(k integer)", "CREATE TABLE \"t\"(\"k\" integer)", true},
		{"CREATE VIEW v AS SELECT 1 AS k", "CREATE VIEW v AS SELECT 2 AS k", false},
		{"CREATE VIEW v AS SELECT 1 AS k", "CREATE VIEW v AS SELECT 1 AS x", false},
		{"COPY t TO STDOUT", "COPY t TO STDOUT WITH (FORMAT csv)", false},
		{"LISTEN channel", "LISTEN \"channel\"", true},
		{"NOTIFY channel,'first'", "NOTIFY channel,'second'", false},
		{"COMMENT ON TABLE t IS 'first'", "COMMENT ON TABLE t IS 'second'", false},
		{"CREATE INDEX i ON t(k)", "CREATE UNIQUE INDEX i ON t(k)", false},
		{"DROP TABLE t", "DROP TABLE IF EXISTS t", false},
		{"DROP TABLE t", "DROP TABLE t CASCADE", false},
		{"ALTER TABLE t ALTER i SET DEFAULT 7", "ALTER TABLE IF EXISTS t ALTER i SET DEFAULT 7", false},
		{"BEGIN", "START TRANSACTION", false},
		{"BEGIN", "BEGIN TRANSACTION", true},
		{"SAVEPOINT first_name", "SAVEPOINT other_name", true},
		{"RELEASE first_name", "RELEASE SAVEPOINT other_name", true},
		{"ROLLBACK TO first_name", "ROLLBACK TO SAVEPOINT other_name", true},
		{"SAVEPOINT first_name", "RELEASE first_name", false},
		{"PREPARE TRANSACTION 'first'", "PREPARE TRANSACTION 'other'", true},
		{"COMMIT PREPARED 'first'", "COMMIT PREPARED 'other'", true},
		{"ROLLBACK PREPARED 'first'", "ROLLBACK PREPARED 'other'", true},
		{"COMMIT PREPARED 'first'", "ROLLBACK PREPARED 'first'", false},
		{"CREATE INDEX i ON t(k)", "CREATE INDEX i ON t(k ASC)", false},
		{"SET TRANSACTION ISOLATION LEVEL SERIALIZABLE", "SET TRANSACTION ISOLATION LEVEL READ COMMITTED", false},
		{"DEALLOCATE first", "DEALLOCATE PREPARE second", true},
		{"SET application_name='first'", "SET application_name='second'", true},
	} {
		t.Run(tc.left+" / "+tc.right, func(t *testing.T) {
			left, _ := analyze.JumbleUtility(enginetest.Statement(t, tc.left))
			right, _ := analyze.JumbleUtility(enginetest.Statement(t, tc.right))
			if (left.QueryID() == right.QueryID()) != tc.same {
				t.Fatalf("selected utility fields: same=%v, want %v", left.QueryID() == right.QueryID(), tc.same)
			}
		})
	}
}

func TestPgStatStatementsFingerprintUtilityExpressions(t *testing.T) {
	for _, node := range []parser.Expr{
		parser.LiteralExpr{}, parser.TypedLiteralExpr{}, parser.ColumnExpr{}, parser.ParameterExpr{}, parser.DefaultExpr{}, parser.StarExpr{},
		parser.OperatorExpr{}, parser.InListExpr{}, parser.ArrayComparisonExpr{}, parser.LogicalExpr{}, parser.PredicateExpr{}, parser.IdentityExpr{}, parser.CurrentDateTimeExpr{},
		parser.FunctionExpr{}, parser.AggregateExpr{}, parser.CastExpr{}, parser.RowExpr{}, parser.FieldExpr{}, parser.SubqueryExpr{}, parser.GroupingExpr{},
		parser.CaseExpr{Arms: []parser.CaseArm{{}}}, parser.ConditionalExpr{}, parser.MergeActionExpr{}, parser.CallExpr{}, parser.RegclassExpr{},
		parser.ArrayExpr{}, parser.SubscriptExpr{Subscripts: []parser.ArraySubscript{{}}}, parser.TimeZoneExpr{}, parser.CollateExpr{},
		parser.SQLJSONExpr{Formats: []*parser.JSONFormat{{}}}, parser.SQLJSONQueryExpr{Passing: []parser.SQLJSONPassing{{}}}, parser.JSONPredicateExpr{},
		parser.XMLParseExpr{}, parser.XMLSerializeExpr{}, parser.XMLDocumentPredicateExpr{}, parser.XMLConstructExpr{Named: []parser.XMLNamedOperand{{}}},
	} {
		t.Run(reflect.TypeOf(node).Name(), func(t *testing.T) {
			jumble, _ := analyze.JumbleUtility(&parser.Select{Targets: []parser.Expr{node}})
			if jumble.QueryID() == 0 {
				t.Fatal("empty nested-expression identity")
			}
		})
		t.Run(reflect.TypeOf(node).Name()+"/nil", func(t *testing.T) {
			// Expr is an interface: optional typed nils do not participate in
			// the reflected statement inventory above.
			empty := reflect.Zero(reflect.PointerTo(reflect.TypeOf(node))).Interface().(parser.Expr)
			jumble, _ := analyze.JumbleUtility(&parser.Select{Targets: []parser.Expr{empty}})
			if jumble.QueryID() == 0 {
				t.Fatal("empty typed-nil expression identity")
			}
		})
	}
	for _, source := range []parser.FromItem{
		&parser.TableReference{}, &parser.FunctionFrom{Calls: []parser.FunctionExpr{{}}}, &parser.FunctionTable{Calls: []parser.FunctionExpr{{ColumnDefinitions: []parser.FunctionColumnDefinition{{}}}}}, &parser.ValuesQuery{}, &parser.JoinReference{}, &parser.AliasReference{ColumnDefinitions: []parser.FunctionColumnDefinition{{}}}, &parser.DerivedQuery{},
		&parser.JSONTable{Columns: []parser.JSONTableColumn{{}}}, &parser.XMLTable{Namespaces: []parser.XMLNamespace{{}}, Columns: []parser.XMLTableColumn{{Options: []parser.XMLTableOption{{}}}}},
	} {
		t.Run(reflect.TypeOf(source).Elem().Name(), func(t *testing.T) {
			jumble, _ := analyze.JumbleUtility(&parser.Select{From: source})
			if jumble.QueryID() == 0 {
				t.Fatal("empty nested-source identity")
			}
		})
		t.Run(reflect.TypeOf(source).Elem().Name()+"/nil", func(t *testing.T) {
			empty := reflect.Zero(reflect.TypeOf(source)).Interface().(parser.FromItem)
			jumble, _ := analyze.JumbleUtility(&parser.Select{From: empty})
			if jumble.QueryID() == 0 {
				t.Fatal("empty typed-nil source identity")
			}
		})
	}
}

func TestPgStatStatementsFingerprintUtilityArraySubquery(t *testing.T) {
	// ARRAY(SELECT ...) is ordinary parsed syntax; its utility identity is the
	// SubqueryExpr projection, distinct from a scalar subquery's identity.
	seen := make(map[int64]string, 2)
	for _, source := range []string{"SELECT ARRAY(SELECT 1)", "SELECT (SELECT 1)"} {
		statements, err := parser.Parse(source)
		if err != nil || len(statements) != 1 {
			t.Fatalf("parse %q: %v", source, err)
		}
		jumble, _ := analyze.JumbleUtility(statements[0])
		id := jumble.QueryID()
		if id == 0 {
			t.Fatalf("%q lost open utility identity", source)
		}
		if other, ok := seen[id]; ok {
			t.Fatalf("%q shares identity with %q", source, other)
		}
		seen[id] = source
	}
}
