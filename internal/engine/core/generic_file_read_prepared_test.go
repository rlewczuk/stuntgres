package core_test

import (
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
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Public held-call outcomes live in reference/roles. These assertions inspect
// the real definition and portal owners, not a parallel observation registry.
func TestGenericFileReadPreparedOwnership(t *testing.T) {
	for _, tc := range []struct {
		name, argument string
		oid ids.OID
		erased bool
	}{
		{"pg_read_file", "NULL::text", 3826, true},
		{"pg_read_binary_file", "NULL::text", 3828, true},
		{"pg_stat_file", "NULL::text", 2623, false},
		{"pg_ls_tmpdir", "NULL::oid", 5030, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "BEGIN")
			query := preparedFoldPoint+","+tc.name+"("+tc.argument+") AS v"
			// Keep the original typed binder product and complete manifest. A
			// description deliberately retains syntax, not an executable graph.
			op, err := s.Acquire(context.Background())
			if err != nil { t.Fatal(err) }
			e.Mu.Lock()
			if tc.name == "pg_ls_tmpdir" { query = preparedFoldPoint+",name AS v FROM pg_ls_tmpdir("+tc.argument+")" }
			auth, err := e.AuthorizationLocked(op)
			binder := core.NewExpressionBinder(op.Context(), e, sqltype.InputEnvironment{}, op, nil)
			binder.Query.Auth = &auth
			var original *plan.Query
			if err == nil { original, err = binder.BindSelect(enginetest.Statement(t, query).(*parser.Select), false) }
			e.Mu.Unlock()
			op.Done()
			if err != nil { t.Fatal(err) }
			originalRoot := original.Root.(*plan.ProjectNode)
			originalCall := originalRoot.Expressions[1]
			if tc.name == "pg_ls_tmpdir" { originalCall = originalRoot.Child.(*plan.FunctionFromNode).Calls[0] }
			selected, err := exec.ExpressionRoutineDescriptor(originalCall)
			if err != nil || selected == nil || selected.Address().ObjectID != tc.oid || expr.InfoOf(originalCall).RoutineReference == nil { t.Fatal("original invocation identity", selected, err) }
			manifest := append([]catalog.ExpressionDependency(nil), binder.Dependencies...)
			originalInfo := expr.InfoOf(originalCall)
			cycle := s.NewProtocolCycle()
			command := func(fn func(*core.StatementScope) error) { preparedFoldProtocolCommand(t,e,s,cycle,fn) }
			command(func(scope *core.StatementScope) error { _, err := scope.ParseProtocol("file_owned", query, nil); return err })
			registry := core.PayloadOf(s).PreparedSQL
			definition := registry.Lookup("file_owned")
			owner := definition.Description.Folds
			if owner == nil || !owner.Live() || owner.Current().Generation != 0 || len(owner.Current().Entries) != 0 { t.Fatal("Parse ownership") }
			bind := func(name string) {
				command(func(scope *core.StatementScope) error { _, err := scope.BindProtocolPortal(name,"file_owned",&protocolUnitInput{data:[]byte{0,0,0,0,0,0}}); return err })
			}
			bind("file_first")
			first := registry.Binding("file_first")
			accepted := owner.Current() // Snapshot the value, not the mutable owner.
			acceptedEntries := append([]exec.FoldConstant(nil),accepted.Entries...)
			analysis := definition.Description.Analysis
			dependencies := append([]catalog.ExpressionDependency(nil), analysis.Dependencies...)
			found := false
			for _, dependency := range dependencies { found = found || dependency.Ref.Address.ObjectID == tc.oid }
			if !found || len(accepted.Entries) == 0 { t.Fatal("lost analysis lifetime or immutable sibling backing") }
			assertBinding := func(binding *core.PreparedBinding) {
				t.Helper()
				if binding == nil || binding.Resource == nil || binding.Resource.Root == nil || binding.Entry.Description.Folds != owner || binding.Folds == nil || binding.Folds.Generation != accepted.Generation || &binding.Folds.Entries[0] != &accepted.Entries[0] { t.Fatal("lost accepted owner/generation/backing") }
				node := binding.Resource.Root.Expressions[1]
				if tc.name == "pg_ls_tmpdir" { node = binding.Resource.Root.Child.(*plan.FunctionFromNode).Calls[0] }
				descriptor, err := exec.ExpressionRoutineDescriptor(node)
				if err != nil { t.Fatal(err) }
				if tc.erased {
					literal, ok := node.(plan.LiteralExpr)
					if !ok || !literal.Value.IsNull() || descriptor != nil || literal.RoutineReference != nil { t.Fatal("strict erasure retained invocation EXECUTE metadata", node) }
				} else if descriptor == nil || descriptor.Address().ObjectID != tc.oid || expr.InfoOf(node).RoutineReference == nil { t.Fatal("RECORD/SRF invocation was erased", node) }
			}
			assertUnchanged := func() {
				t.Helper()
				current := owner.Current()
				for i, entry := range acceptedEntries {
					if !samePublishedFold(entry,current.Entries[i]) { t.Fatal("accepted immutable fold value/provenance changed") }
				}
				sourceCall := originalRoot.Expressions[1]
				if tc.name == "pg_ls_tmpdir" { sourceCall = originalRoot.Child.(*plan.FunctionFromNode).Calls[0] }
				if definition.Description.Folds != owner || current.Generation != accepted.Generation || &current.Entries[0] != &accepted.Entries[0] || !reflect.DeepEqual(current,accepted) || definition.Body == nil || definition.Source != query || !reflect.DeepEqual(analysis.Dependencies,dependencies) || !reflect.DeepEqual(binder.Dependencies,manifest) || !reflect.DeepEqual(expr.InfoOf(originalCall),originalInfo) || !reflect.DeepEqual(sourceCall,originalCall) { t.Fatal("accepted publication mutated source/manifest/backing") }
				assertBinding(first)
			}
			assertUnchanged()
			bind("file_second")
			second := registry.Binding("file_second")
			assertBinding(second)
			if second.Resource.Root == first.Resource.Root { t.Fatal("portals share mutable graphs") }
			for _, name := range []string{"file_first","file_second"} {
				command(func(scope *core.StatementScope) error { _, err := scope.ExecuteProtocolPortal(name,0,func([]session.ProtocolField) error{return nil},nil,nil,core.CopyProtocolHooks{}); return err })
				assertUnchanged()
				assertBinding(second)
			}
			// Stage a genuinely private replacement in the actual definition
			// publication, then cancel at its final acceptance fence.
			op, err = s.Acquire(context.Background())
			if err != nil { t.Fatal(err) }
			e.Mu.Lock()
			auth, err = e.AuthorizationLocked(op)
			var environment core.PreparedEnvironment
			if err == nil { environment, err = e.PreparedEnvironmentLocked(auth,true) }
			var publication *core.FoldPublication
			if err == nil { publication, err = core.BeginFoldPublication(&definition.Description,definition.Body,environment,auth.Catalog,nil) }
			if err == nil && publication != nil {
				publication.Group.Entries = append([]exec.FoldConstant(nil),publication.Group.Entries...)
				publication.Group.Private = true
				publication.Group.Entries[0].Value = sqltype.Null(publication.Group.Entries[0].Result.Type)
				ctx,cancel := context.WithCancel(context.Background()); cancel()
				err = publication.Accept(ctx,auth.Catalog,environment,e.Catalog.State.Epoch())
			}
			e.Mu.Unlock()
			op.Done()
			if publication == nil || !errors.Is(err,context.Canceled) || publication.Group.Sealed { t.Fatal("rejected publication",err) }
			assertUnchanged()
			assertBinding(second)
			for _, name := range []string{"file_first","file_second"} { command(func(scope *core.StatementScope) error{return scope.CloseProtocolPortal(name)}) }
			command(func(scope *core.StatementScope) error{return scope.CloseProtocolStatement("file_owned")})
			op,_,err = s.AcquireCommand(context.Background())
			if err != nil { t.Fatal(err) }
			err = e.SyncProtocolCycle(op,cycle); op.Done()
			if err != nil { t.Fatal(err) }
			enginetest.SessionSQL(t,s,"ROLLBACK")
		})
	}
}

func TestGenericFileReadPreparedFrames(t *testing.T) {
	for _, name := range []string{"pg_read_file","pg_read_binary_file","pg_stat_file","pg_ls_tmpdir"} {
		t.Run(name,func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t,e,nil)
			enginetest.SessionSQL(t,s,"BEGIN")
			cycle := s.NewProtocolCycle()
			command := func(fn func(*core.StatementScope) error){preparedFoldProtocolCommand(t,e,s,cycle,fn)}
			query := preparedFoldPoint+","+name+"($1::text) AS v"
			nonnullData := []byte{0,0,0,1,0,0,0,10,'P','G','_','V','E','R','S','I','O','N',0,0}
			mechanism := "physical file reading"
			if name == "pg_stat_file" { mechanism = "physical file metadata" }
			if name == "pg_ls_tmpdir" {
				query = preparedFoldPoint+",name AS v FROM pg_ls_tmpdir($1::oid)"
				nonnullData = []byte{0,0,0,1,0,0,0,4,'1','6','6','3',0,0}
				mechanism = "physical directory listing"
			}
			command(func(scope *core.StatementScope) error{_,err:=scope.ParseProtocol("file_frame",query,nil);return err})
			registry := core.PayloadOf(s).PreparedSQL
			definition := registry.Lookup("file_frame")
			owner := definition.Description.Folds
			analysis := definition.Description.Analysis
			manifest := append([]catalog.ExpressionDependency(nil),analysis.Dependencies...)
			for _, tc := range []struct{name string; data []byte}{
				{"null",[]byte{0,0,0,1,255,255,255,255,0,0}},
				{"nonnull",nonnullData},
			} {
				command(func(scope *core.StatementScope) error{_,err:=scope.BindProtocolPortal(tc.name,"file_frame",&protocolUnitInput{data:tc.data});return err})
			}
			null, nonnull := registry.Binding("null"),registry.Binding("nonnull")
			if null.Folds == nil || nonnull.Folds == nil || len(null.Folds.Entries)==0 || len(nonnull.Folds.Entries)==0 || &null.Folds.Entries[0]==&nonnull.Folds.Entries[0] { t.Fatal("frames borrowed definition or sibling backing") }
			nullAccepted, nonnullAccepted := null.Folds, nonnull.Folds
			nullFolds,nonnullFolds := *nullAccepted,*nonnullAccepted
			snapshotEntries := func(entries []exec.FoldConstant) []exec.FoldConstant {
				snapshot := slices.Clone(entries)
				for i := range snapshot { snapshot[i].Inputs = slices.Clone(entries[i].Inputs) }
				return snapshot
			}
			nullEntries := snapshotEntries(nullFolds.Entries)
			nonnullEntries := snapshotEntries(nonnullFolds.Entries)
			nullDependencies := slices.Clone(nullFolds.Dependencies)
			nonnullDependencies := slices.Clone(nonnullFolds.Dependencies)
			nullResource,nonnullResource := null.Resource,nonnull.Resource
			nullRoot,nonnullRoot := nullResource.Root,nonnullResource.Root
			if nullResource == nonnullResource || nullRoot == nonnullRoot { t.Fatal("frames share mutable resources or executable graphs") }
			nullExpressions := slices.Clone(nullRoot.Expressions)
			nonnullExpressions := slices.Clone(nonnullRoot.Expressions)
			nullCall,nonnullCall := nullRoot.Expressions[1],nonnullRoot.Expressions[1]
			if name == "pg_ls_tmpdir" {
				nullCall = nullRoot.Child.(*plan.FunctionFromNode).Calls[0]
				nonnullCall = nonnullRoot.Child.(*plan.FunctionFromNode).Calls[0]
			}
			if name == "pg_read_file" || name == "pg_read_binary_file" {
				literal,ok := nullCall.(plan.LiteralExpr)
				if !ok || !literal.Value.IsNull() || literal.RoutineReference!=nil { t.Fatal("NULL frame retained EXECUTE invocation") }
			} else if descriptor,err:=exec.ExpressionRoutineDescriptor(nullCall);err!=nil || descriptor==nil || expr.InfoOf(nullCall).RoutineReference==nil { t.Fatal("NULL RECORD/SRF frame lost invocation",err) }
			if descriptor,err:=exec.ExpressionRoutineDescriptor(nonnullCall);err!=nil || descriptor==nil || expr.InfoOf(nonnullCall).RoutineReference==nil { t.Fatal("nonNULL frame lost EXECUTE invocation",err) }
			command(func(scope *core.StatementScope) error{_,err:=scope.ExecuteProtocolPortal("null",0,func([]session.ProtocolField)error{return nil},nil,nil,core.CopyProtocolHooks{});return err})
			err:=preparedFoldProtocolTry(t,e,s,cycle,false,func(scope *core.StatementScope)error{_,err:=scope.ExecuteProtocolPortal("nonnull",0,func([]session.ProtocolField)error{return nil},nil,nil,core.CopyProtocolHooks{});return err})
			var diagnostic *sqlstate.Error
			if !errors.As(err,&diagnostic) || diagnostic.Code!="0A000" || diagnostic.Message!=name+" "+mechanism+" is unsupported in embedded mode" { t.Fatal("nonNULL frame did not reach physical boundary",err) }
			if definition.Description.Folds != owner { t.Fatal("definition fold owner changed") }
			if owner != nil {
				current := owner.Current()
				if current.Generation != 0 { t.Fatalf("parameterized definition generation = %d, want 0",current.Generation) }
				if len(current.Entries) != 0 { t.Fatalf("parameterized definition published %d fold entries",len(current.Entries)) }
			}
			if null.Entry.Description.Folds != owner || nonnull.Entry.Description.Folds != owner { t.Fatal("frame definition owner changed") }
			if null.Resource != nullResource || nonnull.Resource != nonnullResource { t.Fatal("frame resource identity changed") }
			if null.Resource.Root != nullRoot { t.Fatal("successful NULL frame detached or replaced its accepted graph") }
			if null.Folds != nullAccepted { t.Fatal("successful NULL frame detached or replaced its accepted folds") }
			// Failed Execute releases the producer and frame, not the immutable
			// artifacts already accepted at Bind. Inspect those actual retained
			// objects, separately from the live resource's terminal cleanup.
			if nonnull.Resource.Root != nil { t.Fatal("failed nonNULL frame retained its live producer graph") }
			if nonnull.Folds != nil { t.Fatal("failed nonNULL frame retained its live fold group") }
			if !reflect.DeepEqual(nullRoot.Expressions,nullExpressions) { t.Fatal("retained NULL graph expressions changed") }
			if !reflect.DeepEqual(nonnullRoot.Expressions,nonnullExpressions) { t.Fatal("retained nonNULL graph expressions changed") }
			retainedCall := func(root *plan.ProjectNode) expr.Expr {
				if name == "pg_ls_tmpdir" { return root.Child.(*plan.FunctionFromNode).Calls[0] }
				return root.Expressions[1]
			}
			if !reflect.DeepEqual(retainedCall(nullRoot),nullCall) { t.Fatal("retained NULL invocation identity/provenance/operands changed") }
			if !reflect.DeepEqual(retainedCall(nonnullRoot),nonnullCall) { t.Fatal("retained nonNULL invocation identity/provenance/operands changed") }
			assertFolds := func(label string, retained *exec.AcceptedFolds, saved exec.AcceptedFolds, entries []exec.FoldConstant, dependencies []exec.FoldDependency) {
				t.Helper()
				if retained.Generation != saved.Generation { t.Fatalf("%s fold generation = %d, want %d",label,retained.Generation,saved.Generation) }
				if len(retained.Entries) != len(entries) { t.Fatalf("%s fold entry count = %d, want %d",label,len(retained.Entries),len(entries)) }
				if &retained.Entries[0] != &saved.Entries[0] { t.Fatalf("%s immutable fold backing changed",label) }
				if !reflect.DeepEqual(retained.Dependencies,dependencies) { t.Fatalf("%s fold dependency manifest changed",label) }
				if len(dependencies) != 0 && &retained.Dependencies[0] != &saved.Dependencies[0] { t.Fatalf("%s fold dependency backing changed",label) }
				for i, entry := range entries {
					if !samePublishedFold(entry,retained.Entries[i]) { t.Fatalf("%s fold entry %d immutable value/provenance/inputs changed",label,i) }
					if !reflect.DeepEqual(entry.Inputs,retained.Entries[i].Inputs) { t.Fatalf("%s fold entry %d exact input state changed",label,i) }
				}
			}
			assertFolds("NULL",nullAccepted,nullFolds,nullEntries,nullDependencies)
			assertFolds("nonNULL",nonnullAccepted,nonnullFolds,nonnullEntries,nonnullDependencies)
			if !reflect.DeepEqual(analysis.Dependencies,manifest) { t.Fatal("original analysis dependency manifest changed") }
			if definition.Source != query { t.Fatalf("definition source changed: got %q, want %q",definition.Source,query) }
			op,_,err:=s.AcquireCommand(context.Background())
			if err!=nil {t.Fatal(err)}
			err=e.SyncProtocolCycle(op,cycle);op.Done()
			if err!=nil {t.Fatal(err)}
			enginetest.SessionSQL(t,s,"ROLLBACK")
		})
	}
}
