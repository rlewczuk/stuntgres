package enginetests_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

// Engine SQL here only constructs ordinary catalog/storage owners. Assertions
// inspect private bindings, immutable roots and exact physical posting identity;
// public SQL outcomes belong exclusively to reference/indexes.
func compoundEngine(t *testing.T, populated bool) (*core.Engine, *catalog.Relation, *storage.IndexRoot) {
	t.Helper()
	e := engine.New()
	for _, q := range []string{
		"CREATE EXTENSION btree_gin VERSION '1.3'",
		"CREATE TYPE compound_private_enum AS ENUM('z','a','m')",
		"ALTER TYPE compound_private_enum ADD VALUE 'q' BEFORE 'a'",
		"CREATE TABLE compound_private(id integer,b bit(3),v varbit,e compound_private_enum,m money,a integer[],t tsvector)",
		"CREATE INDEX compound_private_i ON compound_private USING gin(b bit_ops,v varbit_ops,e enum_ops,m money_ops,a,t)",
	} {
		enginetest.Run(t, e, q)
	}
	if populated {
		enginetest.Run(t, e, "INSERT INTO compound_private VALUES(1,'000','','z','-92233720368547758.08','{1}','cat:1'),(2,'011','0','q','-0.01','{2}','dog:1'),(3,'100','00','a','0','{2}','cat:1'),(4,'100','01','a','0.01','{2}','cat:1'),(5,'111','1','m','0.01','{}','dog:1'),(6,NULL,'10',NULL,'92233720368547758.07',NULL,''),(7,NULL,NULL,NULL,NULL,NULL,NULL)")
	}
	idx, root := ginInstalledRoot(t, e, "compound_private_i")
	return e, idx, root
}

func TestBtreeGINCompoundBinding(t *testing.T) {
	e, idx, root := compoundEngine(t, false)
	view := e.CatalogRead()
	for column, input := range []ids.OID{1560, 1562, 3500, 790} {
		class := idx.SQLIndex.Keys[column].Class
		if !class.IsGin() || class.InputOID() != uint32(input) || class.StorageOID() != 0 {
			t.Fatal("selected scalar class inventory", column, class)
		}
		for slot := 1; slot <= 5; slot++ {
			member, ok := view.FamilySupport(ids.OID(class.FamilyOID()), input, input, int16(slot))
			if !ok || class.Support(slot).Routine != uint32(member.Function) {
				t.Fatal("missing exact support", column, slot)
			}
			state := catalog.CloneState(view.State())
			state.RemoveDescriptor(member.Address())
			if _, err := routine.BindClassSupport(view.WithState(state), class); err == nil {
				t.Fatal("empty key authorized missing support", column, slot)
			}
			state = catalog.CloneState(view.State())
			proc, ok := view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: member.Function})
			if !ok {
				t.Fatal("support does not resolve to procedure")
			}
			changed := *proc
			changed.ObjectRevision = proc.Revision() + 1
			state.SetDescriptor(changed.Address(), &changed)
			if _, err := routine.BindClassSupport(view.WithState(state), class); err == nil {
				t.Fatal("empty key authorized stale routine", column, slot)
			}
		}
		if class.Support(6).Routine != 0 {
			t.Fatal("invented slot6", class)
		}
		for strategy := 1; strategy <= 5; strategy++ {
			member, ok := view.FamilyOperator(ids.OID(class.FamilyOID()), input, input, int16(strategy), 's')
			if !ok {
				t.Fatal("missing selected search strategy", column, strategy)
			}
			state := catalog.CloneState(view.State())
			state.RemoveDescriptor(member.Address())
			if _, err := routine.BindClassSupport(view.WithState(state), class); err == nil {
				t.Fatal("empty key authorized missing strategy", column, strategy)
			}
		}
		fake := sqltype.NewIndexClass(sqltype.IndexClassDefinition{OID: class.OID(), Method: 2742, Family: 424, Input: uint32(input), Name: class.Name, FamilyName: class.Family, Gin: true})
		if _, err := routine.BindClassSupport(view, fake); err == nil {
			t.Fatal("class spelling authorized foreign family")
		}
	}
	if len(root.Entries) != 0 || len(root.Gin.Columns) != 6 {
		t.Fatal("empty mixed root lost bindings")
	}
	// The mixed vector lane owns both released strategies, including the
	// deprecated spelling whose genuine backing routine is ts_match_vq.
	vectorClass := root.Gin.Columns[5].Binding.Class
	legacy, ok := view.Operator(ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: 3660})
	legacyStamp := vectorClass.Search(2)
	if !ok || legacy.Left != 3614 || legacy.Right != 3615 || legacy.Result != 16 || legacy.Function != 3634 || legacyStamp.Operator != 3660 || legacyStamp.Routine != 3634 || legacyStamp.OperatorRevision != uint64(legacy.Revision()) {
		t.Fatal("mixed vector binding lost actual released strategy2 authority", legacy, legacyStamp)
	}
	state := catalog.CloneState(view.State())
	forged := *legacy
	forged.Function = 3635
	state.SetDescriptor(forged.Address(), &forged)
	if _, err := routine.BindClassSupport(view.WithState(state), vectorClass); err == nil {
		t.Fatal("mixed vector binding accepted forged search routine")
	}
	state = catalog.CloneState(view.State())
	backer := view.MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 3634})
	changedBacker := *backer
	changedBacker.ObjectRevision = backer.Revision() + 1
	state.SetDescriptor(changedBacker.Address(), &changedBacker)
	if _, err := routine.BindClassSupport(view.WithState(state), vectorClass); err == nil {
		t.Fatal("mixed vector binding refreshed a detached backing-routine capture")
	}
	bit, varbit := root.Gin.Columns[0].Binding.Usage, root.Gin.Columns[1].Binding.Usage
	if _, err := sqltype.BindGinTokens(bit, idx.SQLIndex.Keys[1].Class); err != nil {
		t.Fatal("varbit_ops must accept bit", err)
	}
	if _, err := sqltype.BindGinTokens(varbit, idx.SQLIndex.Keys[0].Class); err != nil {
		t.Fatal("bit_ops must accept varbit", err)
	}
	integer, _ := view.LookupType(sqltype.Int4)
	if _, err := sqltype.BindGinTokens(integer.Usage(), idx.SQLIndex.Keys[3].Class); err == nil {
		t.Fatal("money class accepted integer")
	}
	text, _ := view.LookupType(sqltype.Text)
	if _, err := sqltype.BindGinTokens(text.Usage(), idx.SQLIndex.Keys[2].Class); err == nil {
		t.Fatal("enum class accepted text")
	}
	compoundInstallationAuthorityProof(t, e, idx)
}

func TestBtreeGINCompoundTokens(t *testing.T) {
	e, _, root := compoundEngine(t, true)
	integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
	for column, texts := range [][]string{{"000", "100", "111"}, {"", "0", "00", "01", "1", "10"}, {"z", "q", "a", "m"}, {"-92233720368547758.08", "-0.01", "0", "0.01", "92233720368547758.07"}} {
		binding := root.Gin.Columns[column].Binding
		var previous sqltype.GinToken
		for i, text := range texts {
			value := ginInput(t, binding.Usage.Type, text)
			var tokens []sqltype.GinToken
			if err := binding.Extract(context.Background(), value, func(token sqltype.GinToken) error { tokens = append(tokens, token); return nil }); err != nil {
				t.Fatal(err)
			}
			if len(tokens) != 1 || tokens[0].Kind != sqltype.GinOrdinary || tokens[0].Element.Type() != value.Type() || tokens[0].Lexeme != "" {
				t.Fatal("scalar must emit one concrete ordinary token", column, text, tokens)
			}
			if i > 0 {
				cmp, err := binding.Compare(context.Background(), previous, tokens[0], sqltype.InputEnvironment{})
				if err != nil || cmp >= 0 {
					t.Fatal("lexical/rank/minor-unit dictionary order", column, text, cmp, err)
				}
			}
			previous = tokens[0]
		}
		var null []sqltype.GinToken
		if err := binding.Extract(context.Background(), sqltype.Null(binding.Usage.Type), func(token sqltype.GinToken) error { null = append(null, token); return nil }); err != nil || len(null) != 1 || null[0].Kind != sqltype.GinNullItem {
			t.Fatal("scalar NULL category", column, null, err)
		}
		if err := binding.Extract(context.Background(), sqltype.Null(integer), func(sqltype.GinToken) error { t.Fatal("foreign key emitted a token"); return nil }); err == nil {
			t.Fatal("foreign key generation accepted")
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := binding.Extract(ctx, sqltype.Null(binding.Usage.Type), func(sqltype.GinToken) error { t.Fatal("canceled extraction emitted a token"); return nil }); !errors.Is(err, context.Canceled) {
			t.Fatal("token cancellation", err)
		}
	}
}

func TestBtreeGINCompoundPostings(t *testing.T) {
	e, idx, _ := compoundEngine(t, false)
	view := e.CatalogRead()
	parent := view.MustRelation(idx.SQLIndex.Parent)
	integer, _ := view.LookupType(sqltype.Int4)
	scalarInputs := [][]string{{"000", "", "z", "-92233720368547758.08"}, {"011", "0", "q", "-0.01"}, {"100", "00", "a", "0"}, {"100", "01", "a", "0.01"}, {"111", "1", "m", "0.01"}, {"", "10", "", "92233720368547758.07"}}
	store := &storage.Relation{}
	for i, texts := range scalarInputs {
		id, err := sqltype.Integer(integer, int64(i+1))
		if err != nil {
			t.Fatal(err)
		}
		values := []sqltype.Value{id}
		for col, text := range texts {
			typ := parent.Columns[col+1].Usage.Type
			value := sqltype.Null(typ)
			if i != 5 || col != 0 && col != 2 {
				value = ginInput(t, typ, text)
			}
			values = append(values, value)
		}
		values = append(values, ginInput(t, parent.Columns[5].Usage.Type, "{2}"), ginInput(t, parent.Columns[6].Usage.Type, "cat:1"))
		store.Versions = append(store.Versions, storage.RowVersion{Row: ids.RowID(101 + i), Version: ids.VersionID(201 + i), Born: 1, Values: values})
	}
	nullID, _ := sqltype.Integer(integer, 7)
	nullValues := []sqltype.Value{nullID}
	for _, c := range parent.Columns[1:] {
		nullValues = append(nullValues, sqltype.Null(c.Usage.Type))
	}
	store.Versions = append(store.Versions, storage.RowVersion{Row: 107, Version: 207, Born: 1, Values: nullValues})
	evaluation := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: view}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	root, err := index.BuildIndexRoot(evaluation, parent, idx.SQLIndex, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	for col, want := range [][]struct {
		text      string
		positions []int
	}{
		{{"000", []int{0}}, {"011", []int{1}}, {"100", []int{2, 3}}, {"111", []int{4}}},
		{{"", []int{0}}, {"0", []int{1}}, {"00", []int{2}}, {"01", []int{3}}, {"1", []int{4}}, {"10", []int{5}}},
		{{"z", []int{0}}, {"q", []int{1}}, {"a", []int{2, 3}}, {"m", []int{4}}},
		{{"-92233720368547758.08", []int{0}}, {"-0.01", []int{1}}, {"0", []int{2}}, {"0.01", []int{3, 4}}, {"92233720368547758.07", []int{5}}},
	} {
		column := root.Gin.Columns[col]
		if len(column.Tokens) != len(want)+1 {
			t.Fatal("scalar dictionary category count", col, len(column.Tokens))
		}
		for ordinal, entry := range want {
			token := sqltype.GinToken{Element: ginInput(t, column.Binding.Usage.Type, entry.text)}
			cmp, err := column.Binding.Compare(context.Background(), column.Tokens[ordinal].Token, token, sqltype.InputEnvironment{})
			if err != nil || cmp != 0 {
				t.Fatal("exact typed dictionary order", col, ordinal, cmp, err)
			}
			var exact []storage.GinPosting
			for _, pos := range entry.positions {
				exact = append(exact, storage.GinPosting{Row: ids.RowID(101 + pos), Version: ids.VersionID(201 + pos), Position: pos})
			}
			got := ginLookup(t, column, token)
			if !slices.Equal(got, exact) || !slices.Equal(column.Tokens[ordinal].Postings, exact) {
				t.Fatal("exact Row/Version/Position list", col, entry.text, got, exact)
			}
			if &got[0] != &column.Tokens[ordinal].Postings[0] {
				t.Fatal("exact Lookup copied dictionary backing")
			}
		}
		nullPositions := []int{6}
		if col == 0 || col == 2 {
			nullPositions = []int{5, 6}
		}
		var exact []storage.GinPosting
		for _, pos := range nullPositions {
			exact = append(exact, storage.GinPosting{Row: ids.RowID(101 + pos), Version: ids.VersionID(201 + pos), Position: pos})
		}
		if got := ginLookup(t, column, sqltype.GinToken{Kind: sqltype.GinNullItem}); !slices.Equal(got, exact) {
			t.Fatal("NULL postings", col, got, exact)
		}
		for _, kind := range []sqltype.GinTokenKind{sqltype.GinEmptyItem, sqltype.GinNullElement} {
			if got := ginLookup(t, column, sqltype.GinToken{Kind: kind}); len(got) != 0 {
				t.Fatal("scalar invented array category", col, kind, got)
			}
		}
	}
	ginAssertOwners(t, root, store)
	compoundScalarProbeProof(t, root)
	compoundScalarMergeProof(t, view, parent, idx, store, root)
}

func TestBtreeGINCompoundPublication(t *testing.T) {
	e, idx, old := compoundEngine(t, true)
	entries, payload := gistCopyEntries(old), ginCopyPayload(old.Gin)
	column := old.Gin.Columns[3]
	money := sqltype.GinToken{Element: ginInput(t, column.Binding.Usage.Type, "0.01")}
	retained := compoundScalarLookup(t, old.Gin, 3, money.Element)
	enginetest.Run(t, e, "INSERT INTO compound_private VALUES(8,'000','','z','-0.02','{8}','new:1')")
	_, inserted := ginInstalledRoot(t, e, "compound_private_i")
	same := compoundScalarLookup(t, inserted.Gin, 3, money.Element)
	if inserted == old || &same[0] != &retained[0] || inserted.Construction != old.Construction {
		t.Fatal("unrelated scalar delta rebuilt old posting backing")
	}
	bitToken := sqltype.GinToken{Element: ginInput(t, old.Gin.Columns[0].Binding.Usage.Type, "100")}
	duplicates := slices.Clone(compoundScalarLookup(t, old.Gin, 0, bitToken.Element))
	enginetest.Run(t, e, "UPDATE compound_private SET b='011',m='0.02' WHERE id=3")
	_, updated := ginInstalledRoot(t, e, "compound_private_i")
	got := compoundScalarLookup(t, updated.Gin, 0, bitToken.Element)
	if len(duplicates) != 2 || len(got) != 1 || got[0] != duplicates[1] {
		t.Fatal("update did not end exact predecessor", duplicates, got)
	}
	enginetest.Run(t, e, "DELETE FROM compound_private WHERE id=4")
	_, deleted := ginInstalledRoot(t, e, "compound_private_i")
	if got := compoundScalarLookup(t, deleted.Gin, 0, bitToken.Element); len(got) != 0 {
		t.Fatal("delete retained ended duplicate", got)
	}
	session := enginetest.StartSession(t, e, nil)
	tx := enginetest.TransactionBegin(t, session)
	enginetest.SessionSQL(t, session, "INSERT INTO compound_private VALUES(9,'100','0','a','0.01','{2}','cat:1')")
	if e.Storage.IndexRoots[idx.Address()] != deleted {
		t.Fatal("private write published root")
	}
	enginetest.TransactionEnd(t, session, tx, false)
	if e.Storage.IndexRoots[idx.Address()] != deleted {
		t.Fatal("rollback replaced committed root")
	}
	// A successful private write survives its savepoint, but the later write
	// does not. Neither candidate can replace the committed root before commit.
	tx = enginetest.TransactionBegin(t, session)
	enginetest.SessionSQL(t, session, "INSERT INTO compound_private VALUES(9,'100','0','a','0.01','{2}','cat:1')")
	enginetest.SessionSQL(t, session, "SAVEPOINT compound_saved")
	enginetest.SessionSQL(t, session, "INSERT INTO compound_private VALUES(10,'100','0','a','0.01','{2}','cat:1')")
	enginetest.SessionSQL(t, session, "ROLLBACK TO SAVEPOINT compound_saved")
	if e.Storage.IndexRoots[idx.Address()] != deleted {
		t.Fatal("savepoint candidate escaped before commit")
	}
	enginetest.TransactionEnd(t, session, tx, true)
	_, committed := ginInstalledRoot(t, e, "compound_private_i")
	if got := compoundScalarLookup(t, committed.Gin, 0, bitToken.Element); len(got) != 1 || committed == deleted {
		t.Fatal("commit/savepoint did not publish exactly one surviving scalar posting", got)
	}
	enginetest.Run(t, e, "ALTER TABLE compound_private ADD CONSTRAINT compound_positive CHECK(id>0)")
	_, admitted := ginInstalledRoot(t, e, "compound_private_i")
	admittedEntries, admittedPayload := gistCopyEntries(admitted), ginCopyPayload(admitted.Gin)
	_, err := enginetest.Execute(e, context.Background(), enginetest.Statement(t, "INSERT INTO compound_private VALUES(11,'100','0','a','0.01','{2}','cat:1'),(-1,'100','0','a','0.01','{2}','cat:1')"), nil)
	if err == nil || e.Storage.IndexRoots[idx.Address()] != admitted || !reflect.DeepEqual(admitted.Entries, admittedEntries) || !reflect.DeepEqual(admitted.Gin, admittedPayload) {
		t.Fatal("later failing row published a partial scalar posting candidate", err)
	}
	if !reflect.DeepEqual(old.Entries, entries) || !reflect.DeepEqual(old.Gin, payload) || !slices.Equal(retained, compoundScalarLookup(t, old.Gin, 3, money.Element)) {
		t.Fatal("publication mutated retained scalar root")
	}
}

func TestBtreeGINCompoundTransport(t *testing.T) {
	source, sourceIndex, old := compoundEngine(t, true)
	before, entries := ginCopyPayload(old.Gin), gistCopyEntries(old)
	compoundScalarTransportProof(t, source, sourceIndex, old)
	var dump bytes.Buffer
	if err := engine.Dump(context.Background(), source, &dump); err != nil {
		t.Fatal(err)
	}
	target := engine.New()
	if err := engine.Restore(context.Background(), target, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	destination, restored := ginInstalledRoot(t, target, "compound_private_i")
	if restored == old || restored.Gin == old.Gin || destination == sourceIndex {
		t.Fatal("restore reused source executable root")
	}
	for portion, columns := range [2][]storage.GinColumn{old.Gin.Columns, old.Gin.Pending} {
		current := restored.Gin.Columns
		if portion == 1 {
			current = restored.Gin.Pending
		}
		if len(columns) != len(current) {
			t.Fatal("restore changed actual main/pending column count", portion)
		}
		if len(columns) == 0 {
			continue
		}
		for column := range 4 {
			a, b := columns[column], current[column]
			if a.Binding.Same(b.Binding) || column == 2 && a.Binding.Usage.Type == b.Binding.Usage.Type {
				t.Fatal("restore retained source descriptor/binding generation", column)
			}
			if len(a.Tokens) != len(b.Tokens) {
				t.Fatal("restore lost scalar dictionary", column)
			}
			if len(a.Tokens) > 0 && &a.Tokens[0] == &b.Tokens[0] {
				t.Fatal("restore borrowed source dictionary backing", column)
			}
			for _, list := range b.Tokens {
				if list.Token.Kind == sqltype.GinOrdinary && list.Token.Element.Type() != b.Binding.Usage.Type {
					t.Fatal("restored scalar token retained source type", column)
				}
			}
		}
	}
	for _, q := range []string{"ALTER TABLE compound_private ADD COLUMN padding integer", "REINDEX INDEX compound_private_i", "DELETE FROM compound_private WHERE id=2", "VACUUM compound_private"} {
		enginetest.Run(t, target, q)
		idx, root := ginInstalledRoot(t, target, "compound_private_i")
		if len(root.Gin.Columns) != 6 || !index.ConstructionMatches(root, idx) {
			t.Fatal("mixed scalar/array/vector transport lost payload or construction", q)
		}
	}
	if !reflect.DeepEqual(old.Gin, before) || !reflect.DeepEqual(old.Entries, entries) {
		t.Fatal("destination transport mutated source")
	}
}

func TestBtreeGINCompoundPresentation(t *testing.T) {
	e, idx, old := compoundEngine(t, true)
	enginetest.Run(t, e, "CREATE SCHEMA compound_private_dest")
	enginetest.Run(t, e, "CREATE ROLE compound_private_owner")
	for _, change := range []struct {
		query  string
		column int
	}{
		{"ALTER OPERATOR CLASS bit_ops USING gin RENAME TO compound_private_bit", 0},
		{"ALTER OPERATOR FAMILY varbit_ops USING gin RENAME TO compound_private_varbit", 1},
		{"ALTER OPERATOR CLASS enum_ops USING gin OWNER TO compound_private_owner", 2},
		{"ALTER OPERATOR FAMILY money_ops USING gin OWNER TO compound_private_owner", 3},
		{"ALTER OPERATOR CLASS compound_private_bit USING gin SET SCHEMA compound_private_dest", 0},
		{"ALTER OPERATOR FAMILY compound_private_varbit USING gin SET SCHEMA compound_private_dest", 1},
	} {
		priorIndex, prior := ginInstalledRoot(t, e, "compound_private_i")
		saved, entries := ginCopyPayload(prior.Gin), gistCopyEntries(prior)
		detached := priorIndex.SQLIndex.Keys[change.column].Class
		enginetest.Run(t, e, change.query)
		currentIndex, current := ginInstalledRoot(t, e, "compound_private_i")
		if current == prior || currentIndex == priorIndex || current.Gin == prior.Gin {
			t.Fatal("presentation did not refresh actual live wrappers", change.query)
		}
		compoundSharedPresentation(t, prior, current)
		for _, columns := range [2][]storage.GinColumn{current.Gin.Columns, current.Gin.Pending} {
			for col, column := range columns {
				want := ginClass(t, e.CatalogRead(), ids.OID(currentIndex.SQLIndex.Keys[col].Class.OID()))
				if currentIndex.SQLIndex.Keys[col].Class != want || column.Binding.Class != want {
					t.Fatal("live key/root authority stamps differ", col)
				}
			}
		}
		if &prior.Entries[0] != &current.Entries[0] || !reflect.DeepEqual(prior.Gin, saved) || !reflect.DeepEqual(prior.Entries, entries) {
			t.Fatal("presentation changed retained actual backing")
		}
		if _, err := routine.BindClassSupport(e.CatalogRead(), detached); err == nil {
			t.Fatal("live refresh laundered detached stale capture")
		}
	}
	// Strategy5 is a real executable member, not optional presentation metadata.
	// Its effective revision must fence detached binding even when1..4 agree.
	currentIndex, current := ginInstalledRoot(t, e, "compound_private_i")
	view := e.CatalogRead()
	for col, input := range []ids.OID{1560, 1562, 3500, 790} {
		class := currentIndex.SQLIndex.Keys[col].Class
		member, ok := view.FamilyOperator(ids.OID(class.FamilyOID()), input, input, 5, 's')
		if !ok {
			t.Fatal("missing fifth strategy")
		}
		state := catalog.CloneState(view.State())
		changed := *member
		changed.ObjectRevision = member.Revision() + 1
		state.SetDescriptor(changed.Address(), &changed)
		if _, err := routine.BindClassSupport(view.WithState(state), class); err == nil {
			t.Fatal("unchanged first four authorized changed fifth strategy", col)
		}
		if e.Storage.IndexRoots[currentIndex.Address()] != current {
			t.Fatal("rejected fifth-member candidate published root")
		}
	}
	if idx.Address() != currentIndex.Address() || old == current {
		t.Fatal("presentation changed logical index or omitted refresh")
	}
	t.Run("extension-cohort", compoundExtensionPresentationProof)
}
