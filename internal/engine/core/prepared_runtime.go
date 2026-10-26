package core

import (
 "context"
 "fmt"
 "iter"
 "maps"
 "stuntgres/internal/engine/exec"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/engine/plan"
 "stuntgres/internal/engine/session"
 "stuntgres/internal/parser"
)

// PreparedDefinition is the actual SQL/protocol definition returned to callers.
// Its original syntax and public signature never become an executable plan.
type PreparedDefinition struct {
 Body parser.Statement
 Source string
 Description Description
 ParameterOIDs []uint32
}

// PreparedResource associates an accepted binding with its actual private graph
// and terminal close capability. The cursor consumes this very Root; there is
// no graph snapshot or observer token. Definitions never retain resources.
type PreparedResource struct {
 Root *plan.ProjectNode
 Closer interface { Close(context.Context) error }
}
func (r *PreparedResource) Close(ctx context.Context) error {
 if r == nil || r.Closer == nil { return nil }
 return r.Closer.Close(ctx)
}

// PreparedBinding owns the accepted metadata and typed group consumed by Execute.
// Name replacement only removes registry reachability, not this accepted value.
type PreparedBinding struct {
 Entry PreparedDefinition
 Transaction ids.TxRef
 Birth uint64
 Folds *exec.AcceptedFolds
 Resource *PreparedResource
 Retired bool
}
func (b *PreparedBinding) AcceptFolds(group *exec.FoldCaptureCandidate) error {
 if group == nil || !group.Sealed { return ids.ErrCatalogReplaced }
 accepted := exec.AcceptedFolds{Entries:group.Entries, Dependencies:group.Dependencies}
 if b.Entry.Description.Folds != nil && b.Entry.Description.Parameters.Count == 0 {
  accepted.Generation = b.Entry.Description.Folds.Current().Generation
 }
 b.Folds = &accepted
 return nil
}
func (b *PreparedBinding) Close(ctx context.Context) error {
 b.Retired = true
 return b.Resource.Close(ctx)
}

// PreparedRegistry is the single token-owned definition/binding registry.
// Physical-session admission serializes it; callbacks run in runtime cleanup lanes.
type PreparedRegistry struct {
 token ids.SessionToken
 definitions map[string]*PreparedDefinition
 bindings map[string]*PreparedBinding
}
func NewPreparedRegistry(token ids.SessionToken) *PreparedRegistry {
 return &PreparedRegistry{token:token, definitions:make(map[string]*PreparedDefinition)}
}
func (r *PreparedRegistry) Lookup(name string) *PreparedDefinition { return r.definitions[name] }
func (r *PreparedRegistry) Install(name string, definition *PreparedDefinition) {
 if old := r.definitions[name]; old != nil && old != definition { old.Description.Retire() }
 r.definitions[name] = definition
}
func (r *PreparedRegistry) Remove(name string) {
 if old := r.definitions[name]; old != nil { old.Description.Retire() }
 delete(r.definitions,name)
}
func (r *PreparedRegistry) ClearDefinitions() {
 for name := range r.Definitions() { r.Remove(name) }
}
func (r *PreparedRegistry) Definitions() iter.Seq2[string,*PreparedDefinition] { return maps.All(r.definitions) }
func (r *PreparedRegistry) Binding(name string) *PreparedBinding { return r.bindings[name] }
func (r *PreparedRegistry) Bind(name string, binding *PreparedBinding) {
 if r.bindings == nil { r.bindings = make(map[string]*PreparedBinding) }
 r.bindings[name] = binding
}
func (r *PreparedRegistry) RemoveBinding(name string) { delete(r.bindings,name) }
func (r *PreparedRegistry) Bindings() iter.Seq2[string,*PreparedBinding] { return maps.All(r.bindings) }
func (r *PreparedRegistry) EndSession(token ids.SessionToken, _ session.EndReason) error {
 if token != r.token { return fmt.Errorf("stuntgres: foreign SQL prepared attachment") }
 r.ClearDefinitions()
 var failure error
 for name, binding := range r.bindings {
  delete(r.bindings,name)
  if err := binding.Close(context.Background()); failure == nil { failure = err }
 }
 return failure
}
