package core

import (
 "stuntgres/internal/engine/access"
 "stuntgres/internal/engine/analyze"
 "stuntgres/internal/engine/authorization"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/exec"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/engine/sequence"
 "stuntgres/internal/engine/session"
 "stuntgres/internal/parser"
 "stuntgres/internal/sqltype"
)

// PreparedEnvironment is captured coherently under the engine mutex. It contains
// identity and effective validity stamps, never retained authority or data roots.
type PreparedEnvironment struct {
 Domain *ids.Domain
 Epoch ids.CatalogEpoch
 Session ids.SessionToken
 Private *session.Journal
 Path sequence.Environment
 Namespace uint64
 Discard uint64
 RoleStamp access.Stamp
 Principal authorization.PrincipalRef
 RowSecurity bool
 Sequences *sequence.SessionMemory
}

func DefinitionStamp(d catalog.Descriptor, env PreparedEnvironment) ids.DefinitionRevision {
 if domain, ok := d.(*catalog.Type); ok && domain.UsesDomainPlanStamp() {
  return domain.EffectiveDomainPlanStamp()
 }
 return sequence.DefinitionStamp(d, env.Sequences)
}

// Only immutable analysis identities and definition stamps survive a statement.
// Engine.mu and physical-Session admission own this cell; no data or grants do.
type Analysis struct {
	Domain            *ids.Domain
	Epoch             ids.CatalogEpoch
	Session           ids.SessionToken
	Private           *session.Journal
	Path              sequence.Environment
	Namespace         uint64
	Target            ids.ObjectRef
	Targets           map[parser.Statement]ids.ObjectRef
	Sources           map[*parser.TableReference]analyze.PreparedSource
	Dependencies      []catalog.ExpressionDependency
	Types             map[ids.ObjectAddress]*catalog.Type
	FoldTypes         map[ids.ObjectAddress]*sqltype.Descriptor // Executable codec identity, separate from analysis revisions.
	FoldCallables     map[ids.ObjectAddress]*catalog.Procedure
	FoldRoleSensitive bool // Represented RLS relations, not a global role Epoch.
	Casts             map[catalog.CastPair]*catalog.Cast
	NeedsReanalysis   bool // A granted definition evolved after this attempt resolved names.
}

func (a *Analysis) EnvironmentValid(env PreparedEnvironment) bool {
 return a != nil && !a.NeedsReanalysis && a.Domain == env.Domain && a.Epoch == env.Epoch && a.Session == env.Session && a.Private == env.Private && a.Namespace == env.Namespace && sequence.SameEnvironment(a.Path, env.Path)
}
func (a *Analysis) Valid(read catalog.Read, env PreparedEnvironment) bool {
 return a.DefinitionsValid(read, env, false)
}
func (a *Analysis) FoldEnvironmentValid(env PreparedEnvironment) bool {
 if a == nil { return false }
 checked := *a
 checked.NeedsReanalysis = false
 return checked.EnvironmentValid(env)
}
// The rebind mode validates nominal installed-scalar occurrences only, never
// reuse. Owner/ACL changes still require fresh analysis and authority checks.
func (analysis *Analysis) DefinitionsValid(read catalog.Read, env PreparedEnvironment, installedScalarAuthorityRebind bool) bool {
	if !analysis.EnvironmentValid(env) {
		return false
	}
	for pair, observed := range analysis.Casts {
		current, _ := read.Cast(pair.Source, pair.Target)
		if current != observed {
			return false
		}
	}
	for _, dep := range analysis.Dependencies {
		d := read.Get(dep.Ref.Address)
		if d == nil || dep.Ref.Domain() != env.Domain || dep.Ref.Epoch != read.Epoch() {
			return false
		}
		if DefinitionStamp(d, env) != dep.Revision {
			if dep.Purpose == catalog.AggregateSupportDependency {
				return false
			}
			current, ok := d.(*catalog.Type)
			if !ok {
				return false
			}
			if installedScalarAuthorityRebind && !current.SQLType.IsHstore() && !current.SQLType.IsLquery() && (!current.SQLType.IsLtree() && !current.SQLType.IsLtxtquery()) && !current.SQLType.IsEan13() && !current.SQLType.IsIsbn() && !current.SQLType.IsIsbn13() && !current.SQLType.IsIsmn() && !current.SQLType.IsIsmn13() && !current.SQLType.IsIssn() && !current.SQLType.IsIssn13() && !current.SQLType.IsUpc() {
				return false
			}
			old := analysis.Types[dep.Ref.Address]
			if catalog.SamePreparedTypeDefinition(old, current) {
				continue
			}
			if !installedScalarAuthorityRebind || old == nil || old.SQLType.IsHstore() != current.SQLType.IsHstore() || old.SQLType.IsLquery() != current.SQLType.IsLquery() || (old.SQLType.IsLtree() != current.SQLType.IsLtree() || old.SQLType.IsLtxtquery() != current.SQLType.IsLtxtquery()) || old.SQLType.IsEan13() != current.SQLType.IsEan13() || old.SQLType.IsIsbn() != current.SQLType.IsIsbn() || old.SQLType.IsIsbn13() != current.SQLType.IsIsbn13() || old.SQLType.IsIsmn() != current.SQLType.IsIsmn() || old.SQLType.IsIsmn13() != current.SQLType.IsIsmn13() || old.SQLType.IsIssn() != current.SQLType.IsIssn() || old.SQLType.IsIssn13() != current.SQLType.IsIssn13() || old.SQLType.IsUpc() != current.SQLType.IsUpc() || !old.SQLType.IsHstore() && !old.SQLType.IsLquery() && (!old.SQLType.IsLtree() && !old.SQLType.IsLtxtquery()) && !old.SQLType.IsEan13() && !old.SQLType.IsIsbn() && !old.SQLType.IsIsbn13() && !old.SQLType.IsIsmn() && !old.SQLType.IsIsmn13() && !old.SQLType.IsIssn() && !old.SQLType.IsIssn13() && !old.SQLType.IsUpc() {
				return false
			}
			// Compare the same immutable definition while deliberately leaving
			// authority to the fresh binder, not to retained executable analysis.
			nominal := *old
			nominal.Owner, nominal.ACL = current.Owner, current.ACL
			if !catalog.SamePreparedTypeDefinition(&nominal, current) {
				return false
			}
		}
	}
	return true
}

// A separate projection preserves the complete analysis manifest. Incidental
// operand definition revisions still force analysis, but are not executable
// dependencies. An executable purpose at the same address always wins.
func (a *Analysis) FoldsValid(read catalog.Read, env PreparedEnvironment) bool {
	if !a.EnvironmentValid(env) {
		return false
	}
	for pair, observed := range a.Casts {
		current, _ := read.Cast(pair.Source, pair.Target)
		if current != observed {
			return false
		}
	}
	for _, dep := range a.Dependencies {
		d := read.Get(dep.Ref.Address)
		if d == nil || dep.Ref.Domain() != env.Domain || dep.Ref.Epoch != read.Epoch() {
			return false
		}
		if dep.Purpose == catalog.OperandDependency {
			continue
		}
		if DefinitionStamp(d, env) == dep.Revision {
			continue
		}
		if dep.Purpose == catalog.AggregateSupportDependency {
			return false
		}
		if current, ok := d.(*catalog.Procedure); ok && exec.SameRoutineDeclaration(current, a.FoldCallables[dep.Ref.Address]) {
			continue
		}
		if current, ok := d.(*catalog.Type); ok {
			// A presentation/ACL revision of an unchanged immutable codec is
			// still an analysis dependency, not a changed executable provider.
			// Domain semantic stamps remain independent even with the same codec.
			if !current.UsesDomainPlanStamp() && a.FoldTypes[dep.Ref.Address] == current.SQLType {
				continue
			}
			if catalog.SamePreparedTypeDefinition(a.Types[dep.Ref.Address], current) {
				continue
			}
		}
		return false
	}
	return true
}

