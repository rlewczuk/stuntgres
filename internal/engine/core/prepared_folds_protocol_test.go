package core_test

import (
	rootengine "stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"context"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
	"testing"
	"stuntgres/internal/engine/core"
)

func preparedFoldProtocolCommand(t *testing.T, e *core.Engine, s *session.Session, cycle *session.ProtocolCycle, fn func(*core.StatementScope) error) {
	t.Helper()
	op, _, err := s.AcquireCommand(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartProtocolCommand(op, cycle)
	if err != nil {
		t.Fatal(err)
	}
	err = fn(scope)
	if err = scope.Finish(err); err != nil {
		t.Fatal(err)
	}
}

func TestPreparedFoldProtocolOwnership(t *testing.T) {
	e := rootengine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "BEGIN")
	cycle := s.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) { preparedFoldProtocolCommand(t, e, s, cycle, fn) }
	parse := func(name string) {
		command(func(scope *core.StatementScope) error {
			_, err := scope.ParseProtocol(name, preparedFoldPoint, nil)
			return err
		})
	}
	bind := func(name, definition string) {
		command(func(scope *core.StatementScope) error {
			_, err := scope.BindProtocolPortal(name, definition, &protocolUnitInput{data: []byte{0, 0, 0, 0, 0, 0}})
			return err
		})
	}
	closeStatement := func(name string) {
		command(func(scope *core.StatementScope) error { return scope.CloseProtocolStatement(name) })
	}
	enginetest.SessionSQL(t, s, "SET extra_float_digits=3")
	parse("fold_named")
	registry := core.PayloadOf(s).PreparedSQL
	definition := registry.Lookup("fold_named")
	cell := definition.Description.Folds
	if cell == nil || cell.Current().Entries != nil {
		t.Fatal("Parse computed or omitted definition ownership")
	}
	bind("fold_old", "fold_named")
	published := snapshotFoldDescription(definition.Description)
	portal := registry.Binding("fold_old")
	if portal.Entry.Description.Folds != cell || portal.Resource == nil || portal.Resource.Root == nil {
		t.Fatal("Bind did not share cell and own a private graph")
	}
	root := portal.Resource.Root
	enginetest.SessionSQL(t, s, "SET extra_float_digits=0")
	command(func(scope *core.StatementScope) error { _, err := scope.DescribeProtocolStatement("fold_named"); return err })
	if definition.Description.Folds != cell || &cell.Current().Entries[0] != &published.Accepted.Entries[0] {
		t.Fatal("Describe erased accepted generation")
	}
	bind("fold_again", "fold_named")
	if registry.Binding("fold_again").Entry.Description.Folds != cell || &cell.Current().Entries[0] != &published.Accepted.Entries[0] {
		t.Fatal("repeated Bind failed to share definition folds")
	}
	preparedFoldEntry(t, definition.Description, sqltype.Text, "(0.10000000000000002,1)")
	closeStatement("fold_named")
	if cell.Live() || portal.Retired || portal.Resource.Root != root {
		t.Fatal("close changed live portal graph or retained publication rights")
	}
	parse("fold_named")
	if registry.Lookup("fold_named").Description.Folds == cell {
		t.Fatal("replacement name adopted retired owner")
	}
	bind("fold_new", "fold_named")
	preparedFoldEntry(t, registry.Lookup("fold_named").Description, sqltype.Text, "(0.1,1)")
	if portal.Resource.Root != root {
		t.Fatal("replacement mutated old portal graph")
	}
	// Anonymous constructor descriptors are analysis-owned too. A fresh
	// statement Describe cannot force a persistent compound-copy on next Bind.
	enginetest.SessionSQL(t, s, "SET extra_float_digits=3")
	command(func(scope *core.StatementScope) error {
		_, err := scope.ParseProtocol("fold_record", "SELECT ROW((point '0.10000000000000002,1')::text,7) AS v", nil)
		return err
	})
	bind("fold_record_old", "fold_record")
	recordOwner := registry.Lookup("fold_record").Description.Folds
	recordEntries := recordOwner.Current().Entries
	enginetest.SessionSQL(t, s, "SET extra_float_digits=0")
	command(func(scope *core.StatementScope) error {
		_, err := scope.DescribeProtocolStatement("fold_record")
		return err
	})
	bind("fold_record_again", "fold_record")
	if registry.Lookup("fold_record").Description.Folds != recordOwner || &recordEntries[0] != &recordOwner.Current().Entries[0] {
		t.Fatal("Describe/Bind replaced an unchanged compound generation")
	}
	parse("")
	unnamed := registry.Lookup("").Description.Folds
	parse("")
	if unnamed.Live() || registry.Lookup("").Description.Folds == unnamed {
		t.Fatal("unnamed replacement did not retire exact owner")
	}
	// SQL and Go use the same owner discipline, without adapter/name side maps.
	enginetest.SessionSQL(t, s, "PREPARE fold_sql AS "+preparedFoldPoint)
	sqlOwner := registry.Lookup("fold_sql").Description.Folds
	enginetest.SessionSQL(t, s, "EXECUTE fold_sql").Rows.Close()
	enginetest.SessionSQL(t, s, "DEALLOCATE fold_sql")
	if sqlOwner.Live() {
		t.Fatal("SQL deallocation retained publication rights")
	}
	_, goDescription := sequencePreparedDescription(t, e, s, preparedFoldPoint)
	if goDescription.Folds == cell || goDescription.Folds == sqlOwner {
		t.Fatal("independent definitions shared cells")
	}
	e.RetirePreparedDescription(&goDescription)
	if goDescription.Folds.Live() {
		t.Fatal("Go close retained publication rights")
	}
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("fold_old") })
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("fold_again") })
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("fold_new") })
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("fold_record_old") })
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("fold_record_again") })
	op, _, err := s.AcquireCommand(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	err = e.SyncProtocolCycle(op, cycle)
	op.Done()
	if err != nil {
		t.Fatal(err)
	}
	enginetest.SessionSQL(t, s, "ROLLBACK")
	remaining := registry.Lookup("fold_named").Description.Folds
	if err := registry.EndSession(s.SessionToken, session.CleanClose); err != nil {
		t.Fatal(err)
	}
	if remaining.Live() {
		t.Fatal("session retirement kept definition ownership")
	}
	for range registry.Definitions() { t.Fatal("session retirement kept definitions") }
	for range registry.Bindings() { t.Fatal("session retirement kept bindings") }
}
