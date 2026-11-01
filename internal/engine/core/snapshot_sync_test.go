package core_test

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func snapshotSyncScope(t *testing.T,e *core.Engine,demand bool,isolation session.Isolation)*core.StatementScope{
	t.Helper()
	op,done,err:=enginetest.Operation(e,context.Background());if err!=nil{t.Fatal(err)}
	s,err:=e.StartStatement(op);if err!=nil{done();t.Fatal(err)}
	if err:=s.Gate(&parser.Select{});err!=nil{done();t.Fatal(err)}
	s.Owner.Mu.Lock();s.Owner.State=session.TxActive;s.Owner.Isolation=isolation;s.Owner.Mu.Unlock()
	core.SetScopeExportRootDemand(s,demand)
	t.Cleanup(func(){s.Finish(nil);done()})
	return s
}

func TestSnapshotSyncCaptureDemand(t *testing.T){
	e:=engine.New()
	enginetest.Run(t,e,"CREATE TABLE sync_structural(id INT PRIMARY KEY,v INT)")
	enginetest.Run(t,e,"INSERT INTO sync_structural VALUES(1,10)")
	enginetest.Run(t,e,"CREATE TABLE sync_empty(id INT)")
	address:=enginetest.RoleTableRef(t,e,"sync_structural").Address
	empty:=enginetest.RoleTableRef(t,e,"sync_empty").Address
	ordinary:=snapshotSyncScope(t,e,false,session.ReadCommitted)
	if err:=core.ScopeSnapshotDataPhase(ordinary);err!=nil{t.Fatal(err)}
	if roots,_,_,_:=core.ScopeExportCapture(ordinary);roots!=nil{t.Fatal("ordinary RC allocated complete roots")}
	selected:=snapshotSyncScope(t,e,true,session.ReadCommitted)
	if err:=core.ScopeSnapshotDataPhase(selected);err!=nil{t.Fatal(err)}
	roots,catalog,value,frontier:=core.ScopeExportCapture(selected)
	if roots==nil||catalog==nil||value==nil{t.Fatal("missing coherent capture")}
	e.Mu.Lock();published:=e.Storage.Relations[address];descriptor,_:=e.Catalog.State.Relation(address);e.Mu.Unlock()
	if roots[address].Storage!=published||roots[address].Relation!=descriptor||roots[empty].Storage==nil||len(roots[address].Indexes)==0{t.Fatal("missing descriptor/storage/empty/index root pairs")}
	enginetest.Run(t,e,"UPDATE sync_structural SET v=20 WHERE id=1")
	token,err:=core.ScopeExportSnapshot(selected);if err!=nil{t.Fatal(err)}
	saved,snapshot,cut:=core.EngineExportCapture(e,token)
	if reflect.ValueOf(saved).Pointer()!=reflect.ValueOf(roots).Pointer()||snapshot!=value||cut!=frontier||saved[address].Storage!=published{t.Fatal("delayed export recaptured roots or copied immutable backing")}
	next,err:=core.ScopeExportSnapshot(selected);if err!=nil{t.Fatal(err)}
	second,secondValue,secondCut:=core.EngineExportCapture(e,next)
	if next==token||reflect.ValueOf(second).Pointer()!=reflect.ValueOf(saved).Pointer()||secondValue!=snapshot||secondCut!=cut{t.Fatal("same-command exports lost distinct identity/shared committed backing")}
	stable:=snapshotSyncScope(t,e,true,session.RepeatableRead)
	if err:=core.ScopeSnapshotDataPhase(stable);err!=nil{t.Fatal(err)}
	r,_,_,f:=core.ScopeExportCapture(stable)
	if reflect.ValueOf(r).Pointer()!=reflect.ValueOf(stable.Owner.Roots).Pointer()||f!=stable.Owner.Frontier{t.Fatal("stable export allocated a second owner root map")}
}

func TestSnapshotSyncRegistryLifecycle(t *testing.T){
	for range 16 {
		e:=engine.New();enginetest.Run(t,e,"CREATE TABLE sync_registry(id INT)")
		source:=snapshotSyncScope(t,e,true,session.ReadCommitted)
		if err:=core.ScopeSnapshotDataPhase(source);err!=nil{t.Fatal(err)}
		token,err:=core.ScopeExportSnapshot(source);if err!=nil{t.Fatal(err)}
		importer:=snapshotSyncScope(t,e,false,session.RepeatableRead)
		start:=make(chan struct{});var wg sync.WaitGroup;var imported error
		wg.Add(2)
		go func(){defer wg.Done();<-start;imported=core.ScopeImportSnapshot(importer,token)}()
		go func(){defer wg.Done();<-start;core.EngineExpireSnapshots(e,source.TxRef)}()
		close(start);wg.Wait()
		if imported==nil {
			if !importer.Owner.SnapshotSet||importer.Owner.CurrentSnapshot==nil||importer.Owner.Roots==nil{t.Fatal("half-installed import")}
		} else {
			var d *sqlstate.Error
			if !errors.As(imported,&d)||d.Code!="42704"||importer.Owner.SnapshotSet||importer.Owner.Roots!=nil{t.Fatal("race neither retained nor cleanly absent",imported)}
		}
		core.EngineExpireSnapshots(e,source.TxRef)
		if core.EngineExportCount(e)!=0{t.Fatal("idempotent source cleanup leaked token")}
	}
}

func TestSnapshotSyncAbortedExports(t *testing.T){
	e:=engine.New()
	source:=snapshotSyncScope(t,e,true,session.ReadCommitted)
	if err:=core.ScopeSnapshotDataPhase(source);err!=nil{t.Fatal(err)}
	token,err:=core.ScopeExportSnapshot(source);if err!=nil{t.Fatal(err)}
	primary:=core.SQLError("22012","division by zero")
	if err:=source.Finish(primary);err!=primary{t.Fatal(err)}
	if core.EngineExportCount(e)!=1{t.Fatal("failed block removed identifier before block cleanup")}
	roots,snapshot,_:=core.EngineExportCapture(e,token)
	if roots!=nil||snapshot!=nil{t.Fatal("aborted identifier retained live data authority")}
	importer:=snapshotSyncScope(t,e,false,session.RepeatableRead)
	err=core.ScopeImportSnapshot(importer,token)
	var diagnostic *sqlstate.Error
	if !errors.As(err,&diagnostic)||diagnostic.Code!="55000"||diagnostic.Message!="could not import the requested snapshot"||diagnostic.Detail!=fmt.Sprintf("The source process with PID %d is not running anymore.",source.Op.Session.Token().ID())||importer.Owner.SnapshotSet{t.Fatal("aborted source installed or lost liveness diagnostic",err)}
	e.Mu.Lock();_,advertised:=e.Visibility.Entries[source.TxRef];e.Mu.Unlock()
	if advertised{t.Fatal("aborted export retained running transaction advertisement")}
	core.EngineExpireSnapshots(e,source.TxRef)
	if core.EngineExportCount(e)!=0{t.Fatal("block cleanup retained aborted identifier")}
}


func TestSnapshotSyncCanonicalIdentity(t *testing.T){
	e:=engine.New();enginetest.Run(t,e,"CREATE TABLE sync_identity(id INT)")
	source:=snapshotSyncScope(t,e,true,session.ReadCommitted)
	e.Mu.Lock()
	err:=e.AssignTransactionXIDLocked(source.TxRef)
	own:=e.Visibility.FullXIDLocked(source.TxRef)
	e.Mu.Unlock()
	if err!=nil||own==0{t.Fatal("source canonical assignment",own,err)}
	enginetest.Run(t,e,"INSERT INTO sync_identity VALUES(1)")
	if err:=core.ScopeSnapshotDataPhase(source);err!=nil{t.Fatal(err)}
	_,_,active,_:=core.ScopeExportCapture(source)
	if own>=active.Xmax(){t.Fatal("witness did not place source below xmax")}
	for i:=range active.Len(){if active.Member(i)==own{t.Fatal("ordinary active value unexpectedly contains self")}}
	token,err:=core.ScopeExportSnapshot(source);if err!=nil{t.Fatal(err)}
	_,exported,_:=core.EngineExportCapture(e,token)
	found:=false;for i:=range exported.Len(){found=found||exported.Member(i)==own}
	if !found||exported.Visible(own){t.Fatal("export failed to insert source full XID")}
	importer:=snapshotSyncScope(t,e,false,session.RepeatableRead)
	e.Mu.Lock();err=e.AssignTransactionXIDLocked(importer.TxRef);e.Mu.Unlock()
	if err!=nil{t.Fatal(err)}
	if importer.Owner.SnapshotSet{t.Fatal("canonical assignment witness already selected snapshot")}
	err=core.ScopeImportSnapshot(importer,token)
	var diagnostic *sqlstate.Error
	if !errors.As(err,&diagnostic)||diagnostic.Code!="25001"||diagnostic.Message!="SET TRANSACTION SNAPSHOT must be called before any query"||importer.Owner.SnapshotSet{t.Fatal("assigned top-level XID did not close import atomically",err)}
}

func TestSnapshotSyncFastpath(t *testing.T){
	e:=engine.New();enginetest.Run(t,e,"CREATE TABLE sync_fastpath(id INT)")
	s:=snapshotSyncScope(t,e,false,session.ReadCommitted)
	message:=binary.BigEndian.AppendUint32(nil,3809)
	message=binary.BigEndian.AppendUint16(message,0)
	message=binary.BigEndian.AppendUint16(message,0)
	message=binary.BigEndian.AppendUint16(message,0)
	field,err:=s.CallProtocolFunction(&enginetest.ProtocolReader{Data:message})
	if err!=nil||field.Null||len(field.Bytes)==0{t.Fatal("real fastpath export",field,err)}
	roots,_,snapshot,frontier:=core.ScopeExportCapture(s)
	saved,value,cut:=core.EngineExportCapture(e,string(field.Bytes))
	if roots==nil||reflect.ValueOf(roots).Pointer()!=reflect.ValueOf(saved).Pointer()||value!=snapshot||cut!=frontier{t.Fatal("dynamic protocol admission did not capture before invocation")}
}

func TestSnapshotSyncTypedDemand(t *testing.T){
	e:=engine.New()
	for _,tc:=range []struct{query string;demand bool}{
		{"SELECT 1",false},
		{"SELECT pg_current_snapshot()",false},
		{"SELECT pg_export_snapshot()",true},
		{"SELECT pg_typeof(pg_export_snapshot())",true},
	}{
		b:=enginetest.ExpressionBinder(t,context.Background(),e,sqltype.InputEnvironment{},nil)
		b.Clause.AggregateClause="SELECT"
		raw:=enginetest.Statement(t,tc.query).(*parser.Select).Targets[0]
		node,err:=b.Bind(raw,analyze.ExpressionPolicy{Context:analyze.ProjectionContext})
		if err!=nil{t.Fatal(err)}
		if b.Consumer.ExportRoots!=tc.demand{t.Fatal("typed demand",tc.query,b.Consumer.ExportRoots)}
		retained:=enginetest.ExpressionBinder(t,context.Background(),e,sqltype.InputEnvironment{},nil)
		if err:=retained.CaptureRetainedWriteInput([]expr.Expr{node},nil,nil);err!=nil{t.Fatal(err)}
		if retained.Consumer.ExportRoots!=tc.demand{t.Fatal("retained child demand",tc.query)}
		if core.EngineExportCount(e)!=0{t.Fatal("analysis exported a snapshot")}
	}
}

func TestSnapshotSyncVacuumRetention(t *testing.T) {
	for _, imported := range []bool{false, true} {
		name := "ordinary_immutable_reader"
		if imported {
			name = "exported_then_imported_cut"
		}
		t.Run(name, func(t *testing.T) {
			e := engine.New()
			writer := enginetest.StartSession(t, e, nil)
			source := enginetest.StartSession(t, e, nil)
			reader := enginetest.StartSession(t, e, nil)
			for _, q := range []string{
				"CREATE TABLE sync_vacuum(n INT)",
				"INSERT INTO sync_vacuum VALUES(1)",
				"VACUUM (FREEZE) sync_vacuum",
			} {
				enginetest.SessionSQL(t, writer, q)
			}
			address := enginetest.RoleTableRef(t, e, "sync_vacuum").Address
			// Genuine aborted publishers separate writer order and public full
			// XIDs from CommitSeq without altering any allocator or row header.
			for range 32 {
				for _, q := range []string{"BEGIN", "INSERT INTO sync_vacuum VALUES(99)", "ROLLBACK"} {
					enginetest.SessionSQL(t, writer, q)
				}
			}
			begin := "BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY"
			if imported {
				begin = "BEGIN ISOLATION LEVEL READ COMMITTED READ ONLY"
			}
			enginetest.SessionSQL(t, source, begin)
			export := func() string {
				t.Helper()
				rows := enginetest.Tuples(enginetest.SessionSQL(t, source, "SELECT pg_export_snapshot()"))
				if len(rows) != 1 || len(rows[0]) != 1 {
					t.Fatal("export lost its single text result", rows)
				}
				token, ok := rows[0][0].(string)
				if !ok || token == "" {
					t.Fatal("export lost nonempty text identity", rows)
				}
				return token
			}
			var token string
			if imported {
				token = export()
			} else {
				enginetest.Tuples(enginetest.SessionSQL(t, source, "SELECT n FROM sync_vacuum"))
			}
			sourceRef := source.Transactions.Ref
			roots, snapshot, frontier := source.Transactions.Roots, source.Transactions.CurrentSnapshot, source.Transactions.Frontier
			if imported {
				roots, snapshot, frontier = core.EngineExportCapture(e, token)
			}
			if snapshot == nil || roots == nil || source.Transactions.Ticket.AssignedFullXID() != 0 {
				t.Fatal("read-only source lacks an unassigned immutable snapshot")
			}
			retained := roots[address]
			if len(retained.Versions) != 1 || !retained.Versions[0].Frozen {
				t.Fatal("retained source lacks a genuine frozen seed")
			}
			before := retained.Versions[0]
			enginetest.SessionSQL(t, writer, "INSERT INTO sync_vacuum VALUES(2)")
			if imported {
				next := export()
				_, nextSnapshot, nextFrontier := core.EngineExportCapture(e, next)
				if next == token || nextSnapshot.Xmin() <= snapshot.Xmin() || nextFrontier <= frontier {
					t.Fatal("later RC export did not acquire a distinct newer cut")
				}
				enginetest.Tuples(enginetest.SessionSQL(t, source, "SELECT 1"))
			}
			e.Mu.Lock()
			advertisement := e.Visibility.Entries[sourceRef]
			e.Mu.Unlock()
			writerXmin := advertisement.Xmin
			if imported {
				writerXmin = advertisement.ExportXmin
				if advertisement.ExportFullXmin != snapshot.Xmin() || advertisement.ImportedFullXmin != 0 {
					t.Fatal("newer command/export replaced the oldest exported full-XID cut", advertisement)
				}
			} else if advertisement.ExportFullXmin != 0 || advertisement.ImportedFullXmin != 0 {
				t.Fatal("ordinary immutable reader advertised a retained import/export cut", advertisement)
			}
			enginetest.SessionSQL(t, writer, "VACUUM (FREEZE) sync_vacuum")
			current := e.Storage.Relations[address]
			newer := current.Versions[1]
			if newer.Born <= frontier || writerXmin <= uint64(newer.Born) {
				t.Fatal("fixture confused CommitSeq with the real writer-order horizon", newer, frontier, advertisement)
			}
			if newer.Frozen == imported {
				t.Fatal("ordinary roots and retained export incorrectly share a freezing horizon", newer, imported)
			}
			if imported {
				enginetest.SessionSQL(t, reader, "BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY")
				enginetest.SessionSQL(t, reader, "SET TRANSACTION SNAPSHOT '"+token+"'")
				readerRef := reader.Transactions.Ref
				enginetest.SessionSQL(t, source, "COMMIT")
				e.Mu.Lock()
				_, sourceLive := e.Visibility.Entries[sourceRef]
				entry := e.Visibility.Entries[readerRef]
				cutoff, err := e.Visibility.VacuumCutoffLocked()
				e.Mu.Unlock()
				if sourceLive || entry.ImportedFullXmin != snapshot.Xmin() || entry.ExportFullXmin != 0 || cutoff != snapshot.Xmin() || err != nil {
					t.Fatal("source end lost independent imported full-XID retention", sourceLive, entry, cutoff, err)
				}
				if reader.Transactions.CurrentSnapshot != snapshot || reader.Transactions.Frontier != frontier || reader.Transactions.Ticket.AssignedFullXID() != 0 {
					t.Fatal("import did not retain the exact unassigned public/CommitSeq pair")
				}
				enginetest.SessionSQL(t, writer, "VACUUM (FREEZE) sync_vacuum")
				if e.Storage.Relations[address].Versions[1].Frozen {
					t.Fatal("exporter end released an importer's freezing horizon")
				}
				rows := enginetest.Tuples(enginetest.SessionSQL(t, reader, "SELECT n FROM sync_vacuum ORDER BY n"))
				if len(rows) != 1 || len(rows[0]) != 1 || rows[0][0] != int64(1) {
					t.Fatal("imported cut widened after source end/maintenance", rows)
				}
				enginetest.SessionSQL(t, reader, "ROLLBACK")
				e.Mu.Lock()
				_, readerLive := e.Visibility.Entries[readerRef]
				e.Mu.Unlock()
				if readerLive {
					t.Fatal("importer end retained its horizon")
				}
			} else {
				enginetest.SessionSQL(t, source, "ROLLBACK")
			}
			enginetest.SessionSQL(t, writer, "VACUUM (FREEZE) sync_vacuum")
			if !e.Storage.Relations[address].Versions[1].Frozen {
				t.Fatal("ended retained cuts kept protecting the newer publisher")
			}
			if len(retained.Versions) != 1 || !reflect.DeepEqual(retained.Versions[0], before) || &retained.Versions[0].Values[0] != &before.Values[0] {
				t.Fatal("maintenance mutated the immutable retained source root")
			}
		})
	}
}
