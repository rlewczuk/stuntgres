package core

import (
 "context"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/exec"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/engine/session"
 "stuntgres/internal/engine/stored"
 "stuntgres/internal/parser"
)

// PreparedFolds owns definition qualification; the lower owner owns the actual
// typed backing, generation and publication rights. Callers serialize mutations.
type PreparedFolds struct {
 exec.FoldDefinition
 Analysis *Analysis
 Environment PreparedEnvironment
 RoleSensitive bool
}

func NewPreparedFolds() *PreparedFolds { return &PreparedFolds{FoldDefinition:*exec.NewFoldDefinition()} }

// FoldPublication is one actual planning operation, never a retained definition.
// Its exact description and accepted admission environment cannot be replaced by
// an ambient nested command's description or authorization.
type FoldPublication struct {
 Statement parser.Statement
 Description *Description
 Environment PreparedEnvironment
 Portal bool
 Group *exec.FoldCaptureCandidate
 owner *PreparedFolds
 attempt exec.FoldAttempt
 fixed exec.FoldCaptureCandidate
}

func FoldDependenciesValid(read catalog.Read, env PreparedEnvironment, dependencies []exec.FoldDependency) bool {
 for _, dependency := range dependencies {
  if dependency.Ref.Domain() != env.Domain || dependency.Ref.Epoch != env.Epoch { return false }
  current := read.Get(dependency.Ref.Address)
  if current == nil { return false }
  if DefinitionStamp(current, env) == dependency.Stamp { continue }
  switch original := dependency.Descriptor.(type) {
  case *catalog.Procedure:
   callable, ok := current.(*catalog.Procedure)
   if ok && exec.SameRoutineDeclaration(callable, original) { continue }
  case *catalog.Type:
   typ, ok := current.(*catalog.Type)
   if ok && !typ.UsesDomainPlanStamp() && typ.SQLType == original.SQLType { continue }
  }
  return false
 }
 return true
}

func BeginFoldPublication(description *Description, statement parser.Statement, env PreparedEnvironment, read catalog.Read, binding *PreparedBinding) (*FoldPublication, error) {
 if description == nil || binding == nil && description.Parameters.Count != 0 { return nil, nil }
 var owner *PreparedFolds
 if binding != nil {
  if binding.Retired || binding.Folds == nil || !FoldDependenciesValid(read, env, binding.Folds.Dependencies) { return nil, ids.ErrCatalogReplaced }
 } else {
  if description.Folds == nil { description.Folds = NewPreparedFolds() }
  owner = description.Folds
  if !owner.Live() { return nil, ids.ErrCatalogReplaced }
  roleChanged := owner.RoleSensitive && (owner.Environment.RoleStamp != env.RoleStamp || owner.Environment.Principal != env.Principal || owner.Environment.RowSecurity != env.RowSecurity)
  dependenciesValid := FoldDependenciesValid(read, env, owner.Current().Dependencies)
  if roleChanged || owner.Analysis != nil && (owner.Environment.Discard != env.Discard || !owner.Analysis.FoldsValid(read, env) || !dependenciesValid) {
   all := roleChanged || owner.Environment.Discard != env.Discard || !owner.Analysis.FoldEnvironmentValid(env) || !dependenciesValid
   owner.Invalidate(all)
  }
 }
 publication := &FoldPublication{Statement:statement, Description:description, Environment:env, owner:owner, Portal:binding != nil}
 if binding != nil {
  publication.fixed = binding.Folds.Candidate()
  publication.Group = &publication.fixed
 } else {
  publication.attempt = owner.Begin()
  publication.Group = &publication.attempt.Candidate
  stored.PruneFoldCaptures(publication.Group, read)
  exec.PruneFoldDependencies(publication.Group)
 }
 publication.Group.Publication = publication
 return publication, nil
}

// Accept follows final current binder/ACL and authorization capture under
// Engine.Mu, before executor startup. It owns retained-state validation and
// the exact-attempt transition, not statement effects.
func (p *FoldPublication) Accept(ctx context.Context, read catalog.Read, current PreparedEnvironment, nativeEpoch ids.CatalogEpoch) error {
 if err := ctx.Err(); err != nil { return err }
 if !p.Portal && p.Environment.Discard != current.Discard { return ids.ErrCatalogReplaced }
 analysis := p.Description.Analysis
 if analysis != nil {
  checked := *analysis
  checked.Path, checked.Namespace = p.Environment.Path, p.Environment.Namespace
  checked.NeedsReanalysis = false
  if p.Portal { checked.Private = p.Environment.Private }
  if !checked.FoldsValid(read, current) { return ids.ErrCatalogReplaced }
 }
 if analysis != nil && analysis.FoldRoleSensitive && (p.Environment.RoleStamp != current.RoleStamp || p.Environment.Principal != current.Principal || p.Environment.RowSecurity != current.RowSecurity) { return ids.ErrCatalogReplaced }
 if nativeEpoch != current.Epoch { return ids.ErrCatalogReplaced }
 exec.PruneFoldDependencies(p.Group)
 if !FoldDependenciesValid(read, current, p.Group.Dependencies) { return ids.ErrCatalogReplaced }
 if err := ctx.Err(); err != nil { return err }
 if p.Portal { p.Group.Sealed = true; return nil }
 if _, err := p.owner.FoldDefinition.Accept(&p.attempt); err != nil { return err }
 acceptedEnvironment := p.Environment
 acceptedEnvironment.Sequences = nil // Session memory is an operation input, never retained state.
 p.owner.Analysis, p.owner.Environment = analysis, acceptedEnvironment
 p.owner.RoleSensitive = analysis != nil && analysis.FoldRoleSensitive
 return nil
}

func (d *Description) Retire() {
 if d != nil && d.Folds != nil { d.Folds.Retire() }
}

func (e *Engine) prepareFoldAttemptLocked(s *StatementScope, statement parser.Statement, auth AuthorizationView, description *Description) (*FoldPublication, error) {
 if description == nil { return nil, nil }
 var binding *PreparedBinding
 // Only preparable bodies accept a fold group at Bind. Utility portals defer
 // planning to Execute and must not claim an accepted group's fixed lifetime.
 // Keep missing-group rejection for every command that Bind actually plans.
 if p := s.protocolOrZero().execute; p != nil && description == &p.Entry.Description && session.PreparableBody(p.Entry.Body) { binding = &p.PreparedBinding }
 if binding == nil && description.Parameters.Count != 0 { return nil, nil }
 env, err := e.PreparedEnvironmentLocked(auth,s.RowSecurity)
 if err != nil { return nil, err }
 return BeginFoldPublication(description,statement,env,auth.Catalog,binding)
}

// Final current authorization and exact-attempt publication share Engine.Mu.
func (e *Engine) publishFoldAttemptLocked(s *StatementScope, auth AuthorizationView, group *exec.FoldCaptureCandidate) error {
 if err := s.Op.Context().Err(); err != nil { return err }
 if group == nil { return nil }
 publication, _ := group.Publication.(*FoldPublication)
 if publication == nil { group.Sealed = true; return nil }
 current, err := s.statementAuthorizationLocked(publication.Statement)
 if err != nil { return err }
 auth = current
 env, err := e.PreparedEnvironmentLocked(auth,s.RowSecurity)
 if err != nil { return err }
 if e.Catalog.State.Epoch() != s.TxRef.Epoch() { return ids.ErrCatalogReplaced }
 return publication.Accept(s.Op.Context(),auth.Catalog,env,e.Catalog.State.Epoch())
}

// RetirePreparedDescription serializes Go handle closure with publication.
func (e *Engine) RetirePreparedDescription(description *Description) {
 e.Mu.Lock()
 defer e.Mu.Unlock()
 description.Retire()
}
