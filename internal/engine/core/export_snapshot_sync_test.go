package core

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
	"stuntgres/internal/parser"
)

func SetScopeExportRootDemand(s *StatementScope, demand bool) { s.exportRootDemand = demand }
func ScopeExportRootDemand(s *StatementScope) bool { return s.exportRootDemand }
func ScopeInstallExportDemand(s *StatementScope, b *analyze.Binding) { s.installExportDemand(b) }
func ScopeInstallExportDemandFor(s *StatementScope,b *analyze.Binding,statement parser.Statement){s.installExportDemandFor(b,statement)}
func ScopeExportSnapshot(s *StatementScope) (string,error) { return (*statementHost)(s).ExportSnapshot() }
func ScopeImportSnapshot(s *StatementScope, token string) error { s.Owner.Mu.Lock();defer s.Owner.Mu.Unlock();return s.importSnapshot(token) }
func ScopeExportCapture(s *StatementScope) (map[ids.ObjectAddress]visibility.Root,*catalog.State,*sqltype.Snapshot,ids.CommitSeq) {
	if s.exportCapture==nil{return nil,nil,nil,0};c:=s.exportCapture;return c.roots,c.catalog,c.snapshot,c.frontier
}
func EngineExportCapture(e *Engine,token string) (map[ids.ObjectAddress]visibility.Root,*sqltype.Snapshot,ids.CommitSeq) {
	e.Mu.Lock();defer e.Mu.Unlock();c:=e.Cluster.snapshots[token];if c==nil{return nil,nil,0};return c.roots,c.snapshot,c.frontier
}
func EngineExportCount(e *Engine) int { e.Mu.Lock();defer e.Mu.Unlock();return len(e.Cluster.snapshots) }
func EngineExpireSnapshots(e *Engine,ref ids.TxRef) { e.Mu.Lock();defer e.Mu.Unlock();e.expireSnapshotsLocked(ref) }
