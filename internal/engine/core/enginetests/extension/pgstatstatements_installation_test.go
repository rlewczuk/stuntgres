package extension_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func pgssOwnerSQL(t *testing.T, e *core.Engine, s *session.Session, text string) (core.Result, error) {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	op.OriginalSQL, op.SQLAvailable = text, true
	result, err := e.Execute(op, enginetest.Statement(t, text), 0, nil)
	if result.Rows != nil {
		if closeErr := result.Rows.Close(); err == nil {
			err = closeErr
		}
	}
	return result, err
}
func pgssOwnerExec(t *testing.T, e *core.Engine, s *session.Session, text string) {
	t.Helper()
	if _, err := pgssOwnerSQL(t, e, s, text); err != nil {
		t.Fatal(text, err)
	}
}
func TestPgStatStatementsInstallation(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	pgssOwnerExec(t, e, s, "SET pg_stat_statements.track='none'")
	pgssOwnerExec(t, e, s, "CREATE EXTENSION pg_stat_statements")
	read := e.CatalogRead()
	ext := read.MustExtension(e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "pg_stat_statements"}))
	if ext.Version != "1.12" || len(ext.Installation.Members) != 3 || len(ext.Members) != 9 {
		t.Fatal("recipe roots versus ordinary companion closure", ext)
	}
	if err := extension.StockPgStatStatements.ValidateExtension(read, ext); err != nil {
		t.Fatal(err)
	}
	procedures := make(map[string]*catalog.Procedure)
	for slot, address := range ext.Installation.Members {
		p := read.MustProcedure(address)
		procedures[p.Name()] = p
		if p.Origin == nil || p.Origin.Installation != ext.Installation || p.Origin.Declaration != slot || p.Implementation != extension.StockPgStatStatements || !p.Strict || p.Installed.Parallel != 's' {
			t.Fatal("unsealed or wrong declaration", p)
		}
		if err := extension.StockPgStatStatements.ValidateProcedure(read, p); err != nil {
			t.Fatal(err)
		}
		changed := *p
		changed.Display.Source = "forged"
		if extension.StockPgStatStatements.ValidateProcedure(read, &changed) == nil {
			t.Fatal("mutable spelling manufactured callable authority")
		}
	}
	counts := map[string]int{"pg_stat_statements": 52, "pg_stat_statements_info": 2}
	for name, width := range counts {
		address := e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ext.TargetNamespace, Name: name})
		relation := read.MustRelation(address)
		if relation.RelationKind != catalog.ViewRelation || relation.Extension != ext.Address() || catalog.DescriptorOrigin(relation) != nil || len(relation.Columns) != width {
			t.Fatal("ordinary scripted view", relation)
		}
		definition := stored.ViewOf(relation)
		if definition == nil || definition.Query() == nil {
			t.Fatal("view bypassed ordinary binding/freeze")
		}
		found := false
		for _, dependency := range definition.Dependencies() {
			if dependency.Ref.Address == procedures[name].Address() {
				found = true
				if read.MustProcedure(dependency.Ref.Address) != procedures[name] {
					t.Fatal("view did not capture staged sealed function")
				}
			}
		}
		if !found {
			t.Fatal("missing real provider dependency", name)
		}
		granted := false
		for _, entry := range relation.Security.ACL.Entries {
			granted = granted || entry.Grantee.Public && entry.Privileges&authorization.SelectPrivilege != 0
		}
		if !granted {
			t.Fatal("missing actual script PUBLIC SELECT grant", name)
		}
		row := read.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(relation.RowType)})
		array := read.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(row.Array)})
		if row.OwningRelation != address || array.OwningRelation != address || row.Extension != ext.Address() || array.Extension != ext.Address() {
			t.Fatal("ordinary reciprocal rowtype/array membership", name)
		}
	}
	t.Run("second_view_conflict_is_atomic", func(t *testing.T) {
		other := engine.New()
		session := enginetest.StartSession(t, other, nil)
		pgssOwnerExec(t, other, session, "SET pg_stat_statements.track='none'")
		pgssOwnerExec(t, other, session, "CREATE VIEW pg_stat_statements_info AS SELECT 17 AS sentinel")
		before := other.CatalogRead()
		existing := before.MustRelation(other.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "pg_stat_statements_info"}))
		_, err := pgssOwnerSQL(t, other, session, "CREATE EXTENSION pg_stat_statements")
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42P07" {
			t.Fatal("ordinary view collision", err)
		}
		current := other.CatalogRead()
		if current.MustRelation(existing.Address()) != existing {
			t.Fatal("failed candidate replaced existing view")
		}
		if address := other.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "pg_stat_statements"}); address != (ids.ObjectAddress{}) {
			t.Fatal("failed candidate leaked extension", address)
		}
		for name, signature := range map[string]string{"pg_stat_statements": "16", "pg_stat_statements_info": "", "pg_stat_statements_reset": "26,26,20,16"} {
			if address := other.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: existing.Namespace(), Name: name, Signature: signature}); address != (ids.ObjectAddress{}) {
				t.Fatal("failed candidate leaked installed procedure", address)
			}
		}
	})
}

func TestPgStatStatementsInstallationCallIdentity(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	pgssOwnerExec(t, e, s, "SET pg_stat_statements.track='none'")
	pgssOwnerExec(t, e, s, "CREATE EXTENSION pg_stat_statements")
	pgssOwnerExec(t, e, s, "CREATE SCHEMA pgss_call_move")
	read := e.CatalogRead()
	ext := read.MustExtension(e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "pg_stat_statements"}))
	for slot, sqlName := range []string{"pg_stat_statements", "pg_stat_statements_info"} {
		original := read.MustProcedure(ext.Installation.Members[slot])
		symbol := sqlName
		if slot == 0 {
			symbol = "pg_stat_statements_1_12"
		}
		bind := func(schema, name string) plan.InstalledPgStatStatementsExpr {
			t.Helper()
			binder := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
			binder.Clause.AggregateClause = "FROM"
			binder.Query.Namespace = &analyze.QueryNamespace{}
			raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: schema}, {Text: name}}}}
			if slot == 0 {
				raw.Args = []parser.Expr{enginetest.TypedLiteral("boolean", "true")}
			}
			node, err := binder.BindFunctionSource(raw)
			if err != nil {
				t.Fatal("released SQL gateway failed", schema, name, err)
			}
			call, ok := node.(plan.InstalledPgStatStatementsExpr)
			if !ok {
				t.Fatalf("gateway lost installed carrier: %T", node)
			}
			current := e.CatalogRead().MustProcedure(original.Address())
			if call.Procedure != current || call.RoutineReference != current || call.Ref.Address != original.Address() || call.InfoOnly != (slot == 1) || current.Origin != original.Origin {
				t.Fatal("gateway substituted mutable name authority for the selected sealed declaration")
			}
			if err := stored.ValidateInstalledPgStatStatements(call, e.CatalogRead()); err != nil {
				t.Fatal(err)
			}
			return call
		}
		installed := bind("public", sqlName)
		if got, err := extension.ProcedureSymbol(read, original); err != nil || got != symbol {
			t.Fatal("changed executable C symbol", got, err)
		}
		if got, err := extension.ProcedureInstallationName(read, original); err != nil || got != sqlName {
			t.Fatal("lost released SQL declaration name", got, err)
		}
		if got, err := extension.ProcedureBindingName(read, original); err != nil || got != sqlName {
			t.Fatal("installed gateway lost registered SQL binding recipe", got, err)
		}
		signature := "()"
		if slot == 0 {
			signature = "(boolean)"
		}
		renamed := "pgss_selected_" + sqlName
		pgssOwnerExec(t, e, s, "ALTER FUNCTION public."+sqlName+signature+" RENAME TO "+renamed)
		pgssOwnerExec(t, e, s, "ALTER FUNCTION public."+renamed+signature+" SET SCHEMA pgss_call_move")
		moved := bind("pgss_call_move", renamed)
		if installed.Ref.Address != moved.Ref.Address || installed.Procedure == moved.Procedure || installed.Procedure.Name() != sqlName || moved.Procedure.Name() != renamed {
			t.Fatal("relabel mutated retained descriptor or changed logical callable identity")
		}
		if got, err := extension.ProcedureSymbol(e.CatalogRead(), moved.Procedure); err != nil || got != symbol {
			t.Fatal("relabel changed executable authority", got, err)
		}
		if got, err := extension.ProcedureBindingName(e.CatalogRead(), moved.Procedure); err != nil || got != sqlName {
			t.Fatal("relabel changed installed gateway binding recipe", got, err)
		}
		if _, err := extension.ProcedureBindingName(e.CatalogRead(), original); err == nil {
			t.Fatal("installed gateway binding accepted stale descriptor")
		}
	}
}

func TestPgStatStatementsInstallationViewCommentOwnership(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	pgssOwnerExec(t, e, s, "SET pg_stat_statements.track='none'")
	pgssOwnerExec(t, e, s, "CREATE EXTENSION pg_stat_statements")
	read := e.CatalogRead()
	ext := read.MustExtension(e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "pg_stat_statements"}))
	address := read.State().NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ext.TargetNamespace, Name: "pg_stat_statements"})
	original := read.MustRelation(address)
	row := read.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(original.RowType)})
	metadata := stored.ViewOf(original)
	pgssOwnerExec(t, e, s, "COMMENT ON VIEW public.pg_stat_statements IS 'quote''雪'")
	current := e.CatalogRead()
	commented := current.MustRelation(address)
	if commented == original || commented.Revision() == original.Revision() || commented.Comment != "quote'雪" || original.Comment != "" {
		t.Fatal("comment publication mutated the retained view or lost the metadata revision")
	}
	if stored.ViewOf(commented) != metadata || &commented.Columns[0] != &original.Columns[0] || commented.RowType != original.RowType || commented.Extension != original.Extension || commented.Owner != original.Owner || !reflect.DeepEqual(commented.Security, original.Security) {
		t.Fatal("comment publication replaced executable view or ownership authority")
	}
	if current.MustExtension(ext.Address()) != ext || current.MustType(row.Address()) != row {
		t.Fatal("comment publication changed provider membership or rowtype")
	}
	for _, provider := range ext.Installation.Members {
		if current.MustProcedure(provider) != read.MustProcedure(provider) {
			t.Fatal("comment publication changed sealed function authority")
		}
	}
	pgssOwnerExec(t, e, s, "COMMENT ON VIEW public.pg_stat_statements IS NULL")
	removed := e.CatalogRead().MustRelation(address)
	if removed.Comment != "" || commented.Comment != "quote'雪" || stored.ViewOf(removed) != metadata {
		t.Fatal("comment removal mutated retained metadata or executable backing")
	}
}

func TestPgStatStatementsInstallationReplacementIdentity(t *testing.T) {
	setup := func(shift bool) (*core.Engine, *session.Session, *catalog.Extension) {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		pgssOwnerExec(t, e, s, "SET pg_stat_statements.track='none'")
		if shift {
			pgssOwnerExec(t, e, s, "CREATE TABLE replacement_shift(k integer)")
		}
		pgssOwnerExec(t, e, s, "CREATE SCHEMA replacement_installed")
		pgssOwnerExec(t, e, s, "CREATE EXTENSION pg_stat_statements SCHEMA replacement_installed")
		ext := e.CatalogRead().MustExtension(e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "pg_stat_statements"}))
		return e, s, ext
	}
	target, _, live := setup(false)
	source, sourceSession, imported := setup(true)
	before, input := target.CatalogRead(), source.CatalogRead()
	if live.Address() == imported.Address() {
		t.Fatal("fixture did not distinguish replay allocation from live identity")
	}
	fixed, err := control.CompatibleInstallationIdentities(before, input)
	if err != nil || len(fixed) != 4 || fixed[imported.Address()] != live.Address() {
		t.Fatal("genuine active cohort did not select its live header and three procedures", fixed, err)
	}
	for slot, address := range imported.Installation.Members {
		if fixed[address] != live.Installation.Members[slot] {
			t.Fatal("authentic recipe slot lost live identity", slot, fixed)
		}
	}
	for _, descriptor := range input.Descriptors() {
		if relation, ok := descriptor.(*catalog.Relation); ok && relation.RelationKind == catalog.ViewRelation {
			if _, matched := fixed[relation.Address()]; matched || catalog.DescriptorOrigin(relation) != nil {
				t.Fatal("ordinary consumer acquired installed identity authority")
			}
		}
	}
	if _, err := target.ReplaceCatalog(context.Background(), source, input); err != nil {
		t.Fatal(err)
	}
	current := target.CatalogRead()
	restored := current.MustExtension(live.Address())
	if restored.Installation.Cohort != live.Address() || !restored.Installation.Valid() || restored == live {
		t.Fatal("replacement adopted stale authority instead of remapping the authentic cohort")
	}
	for slot, address := range live.Installation.Members {
		procedure := current.MustProcedure(address)
		if restored.Installation.Members[slot] != address || !catalog.SameInstallation(procedure, restored.Installation) || procedure.Owner != current.Owner().Principals.Principal(procedure.Owner.ID()) {
			t.Fatal("replacement lost live procedure identity or principal authority")
		}
		if err := extension.StockPgStatStatements.ValidateProcedure(current, procedure); err != nil {
			t.Fatal(err)
		}
	}
	if live.Installation.Cohort != live.Address() || before.MustExtension(live.Address()) != live || source.CatalogRead().MustExtension(imported.Address()) != imported {
		t.Fatal("replacement mutated retained live or source authority")
	}
	fresh := engine.New()
	if fixed, err := control.CompatibleInstallationIdentities(fresh.CatalogRead(), input); err != nil || len(fixed) != 0 {
		t.Fatal("fresh destination invented compatible installed authority", fixed, err)
	}
	if _, err := fresh.ReplaceCatalog(context.Background(), source, input); err != nil {
		t.Fatal(err)
	}
	freshRead := fresh.CatalogRead()
	freshExtension := freshRead.MustExtension(fresh.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "pg_stat_statements"}))
	if freshExtension == imported || !freshExtension.Installation.Valid() || freshExtension.Installation.Cohort != freshExtension.Address() {
		t.Fatal("cross-engine replacement lost its destination descriptor or sealed logical recipe")
	}
	// Released recipes are immutable logical data, not live engine authority;
	// unchanged coordinates legitimately share their sealed backing.
	if source.CatalogRead().MustExtension(imported.Address()) != imported || input.MustExtension(imported.Address()) != imported {
		t.Fatal("cross-engine replacement mutated source publication")
	}
	for _, address := range freshExtension.Installation.Members {
		procedure := freshRead.MustProcedure(address)
		if procedure.Owner != freshRead.Owner().Principals.Principal(procedure.Owner.ID()) || !catalog.SameInstallation(procedure, freshExtension.Installation) {
			t.Fatal("fresh replacement retained foreign principal authority or lost its sealed slot")
		}
		if err := extension.StockPgStatStatements.ValidateProcedure(freshRead, freshRead.MustProcedure(address)); err != nil {
			t.Fatal(err)
		}
	}
	// A same-name SQL function replacing a detached/dropped recipe root has
	// no declaration origin and cannot be matched to that live native body.
	pgssOwnerExec(t, source, sourceSession, "ALTER EXTENSION pg_stat_statements DROP VIEW replacement_installed.pg_stat_statements")
	pgssOwnerExec(t, source, sourceSession, "DROP VIEW replacement_installed.pg_stat_statements")
	pgssOwnerExec(t, source, sourceSession, "ALTER EXTENSION pg_stat_statements DROP FUNCTION replacement_installed.pg_stat_statements(boolean)")
	pgssOwnerExec(t, source, sourceSession, "DROP FUNCTION replacement_installed.pg_stat_statements(boolean)")
	pgssOwnerExec(t, source, sourceSession, "CREATE FUNCTION replacement_installed.pg_stat_statements(boolean) RETURNS integer LANGUAGE sql AS $$SELECT 17$$")
	impostorRead := source.CatalogRead()
	impostorAddress := source.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: imported.TargetNamespace, Name: "pg_stat_statements", Signature: "16"})
	impostor := impostorRead.MustProcedure(impostorAddress)
	matches, err := control.CompatibleInstallationIdentities(before, impostorRead)
	if err != nil || impostor.Origin != nil || matches[impostor.Address()] != (ids.ObjectAddress{}) || matches[imported.Installation.Members[0]] != (ids.ObjectAddress{}) {
		t.Fatal("same-name ordinary function acquired native declaration identity", matches, err)
	}
}

func TestPgStatStatementsInstallationReplacementCohorts(t *testing.T) {
	setup := func(shift bool) (*core.Engine, *catalog.Extension, *catalog.Extension) {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		pgssOwnerExec(t, e, s, "SET pg_stat_statements.track='none'")
		if shift {
			pgssOwnerExec(t, e, s, "CREATE TABLE cohort_shift(k integer)")
		}
		pgssOwnerExec(t, e, s, "CREATE SCHEMA cohort_old")
		pgssOwnerExec(t, e, s, "CREATE SCHEMA cohort_new")
		pgssOwnerExec(t, e, s, "CREATE EXTENSION pg_stat_statements SCHEMA cohort_old")
		old := e.CatalogRead().MustExtension(e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "pg_stat_statements"}))
		for _, object := range []string{"VIEW cohort_old.pg_stat_statements", "VIEW cohort_old.pg_stat_statements_info", "FUNCTION cohort_old.pg_stat_statements(boolean)", "FUNCTION cohort_old.pg_stat_statements_info()"} {
			pgssOwnerExec(t, e, s, "ALTER EXTENSION pg_stat_statements DROP "+object)
		}
		pgssOwnerExec(t, e, s, "DROP EXTENSION pg_stat_statements")
		pgssOwnerExec(t, e, s, "CREATE EXTENSION pg_stat_statements SCHEMA cohort_new")
		current := e.CatalogRead().MustExtension(e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "pg_stat_statements"}))
		return e, old, current
	}
	target, _, live := setup(false)
	source, detached, active := setup(true)
	fixed, err := control.CompatibleInstallationIdentities(target.CatalogRead(), source.CatalogRead())
	if err != nil || len(fixed) != 4 || fixed[active.Address()] != live.Address() || fixed[detached.Address()] != (ids.ObjectAddress{}) {
		t.Fatal("historical source cohort was conflated with the live provider", fixed, err)
	}
	for slot, address := range detached.Installation.Members {
		if _, exists := fixed[address]; exists || fixed[active.Installation.Members[slot]] != live.Installation.Members[slot] {
			t.Fatal("detached old declaration acquired active-cohort identity", fixed)
		}
	}
	if _, err := target.ReplaceCatalog(context.Background(), source, source.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	read := target.CatalogRead()
	oldNamespace := target.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "cohort_old"})
	oldAddress := target.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: oldNamespace, Name: "pg_stat_statements", Signature: "16"})
	oldProcedure := read.MustProcedure(oldAddress)
	current := read.MustExtension(live.Address())
	if oldProcedure.Address() == live.Installation.Members[0] || oldProcedure.Origin.Installation.Cohort == current.Installation.Cohort || oldProcedure.Extension != (ids.ObjectAddress{}) {
		t.Fatal("replacement cross-bound old detached and current native declarations")
	}
	if err := extension.StockPgStatStatements.ValidateProcedure(read, oldProcedure); err != nil {
		t.Fatal(err)
	}
}
