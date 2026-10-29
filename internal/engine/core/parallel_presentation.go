package core

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// parallelPresentation shares only immutable admitted roots and paths. The
// spelling cache belongs to one evaluator (including its synchronous children),
// never the live SQL host or another partition.
type parallelPresentation struct {
	exec.DetachedPresentation
	view             catalog.Read
	roles            authorization.Read
	path, collations []ids.ObjectAddress
	quoteAll         bool
	spellings        regobj.SpellingCache
}

func (h *statementHost) CaptureParallelEvaluation() func() *exec.EvaluationCapture {
	input, output := h.Input, h.Output
	facts := parallelPresentation{view: h.Inquiry.CommentCatalog, roles: h.Inquiry.commentRoles,
		path: h.Inquiry.DeparsePath, collations: h.Inquiry.collationPath, quoteAll: output.QuoteAllIdentifiers}
	// Domain checking is evaluator-owned, never an escaped StatementScope.
	// LockedDomainInput is a binder-only boundary, not an execution capability.
	input.LockedDomainInput, input.DomainChecks = nil, nil
	op := h.Op // Capture the original diagnostic owner, not a lexical host.
	return func() *exec.EvaluationCapture {
		p := facts
		out := output
		in := input
		type diagnostic struct {
			level session.NoticeLevel
			value sqlstate.Error
		}
		var notices []diagnostic
		emit := func(level session.NoticeLevel, value sqlstate.Error) {
			notices = append(notices, diagnostic{level, value})
		}
		regobj.CaptureEvaluationInput(&in, emit)
		if xml, ok := in.XMLInput.(session.XMLInput); ok {
			in.XMLInput = xml.WithNotice(func(d sqlstate.Error) { emit(session.LevelWarning, d) })
		}
		if fts, ok := in.FTS.(session.FTSInput); ok {
			in.FTS = fts.WithNotice(func(d sqlstate.Error) { emit(session.LevelNotice, d) })
		}
		out.Regclass, out.Regtype, out.Regproc = &p, &p, &p
		out.Regrole, out.Operator, out.ACLItem = &p, &p, &p
		out.XMLNotice = nil
		if in.XMLInput != nil {
			out.XMLNotice = in.XMLInput.XMLInputWarning
		}
		out.Regcollation, out.Regnamespace, out.RegTS = &p, &p, &p
		return &exec.EvaluationCapture{Input: in, Output: out, Presentation: &p,
			FlushDiagnostics: func() {
				if op != nil {
					for _, d := range notices {
						op.EmitNotice(d.level, d.value)
					}
				}
				notices = nil
			}}
	}
}

func (p *parallelPresentation) FormatRegclass(id uint32) (string, error) {
	if p.view.State() == nil {
		return p.DetachedPresentation.FormatRegclass(id)
	}
	return regobj.ClassSpelling(p.view, p.path, ids.OID(id)), nil
}
func (p *parallelPresentation) FormatRegtype(id uint32) (string, error) {
	if p.view.State() == nil {
		return p.DetachedPresentation.FormatRegtype(id)
	}
	return regobj.TypeSpelling(p.view, p.path, ids.OID(id)), nil
}
func (p *parallelPresentation) FormatRegproc(id uint32) (string, error) {
	if p.view.State() == nil {
		return p.DetachedPresentation.FormatRegproc(id)
	}
	return p.spellings.Regproc(p.view, p.path, id), nil
}
func (p *parallelPresentation) FormatRegprocedure(id uint32) (string, error) {
	if p.view.State() == nil {
		return p.DetachedPresentation.FormatRegprocedure(id)
	}
	return p.spellings.Regprocedure(p.view, p.path, id), nil
}
func (p *parallelPresentation) FormatRegrole(id uint32) (string, error) {
	return (regobj.RoleOutput{Owner: p.view.Owner(), Roles: p.roles, QuoteAll: p.quoteAll}).FormatRegrole(id)
}
func (p *parallelPresentation) FormatRegoper(id uint32) (string, error) {
	if p.view.State() == nil {
		return p.DetachedPresentation.FormatRegoper(id)
	}
	return p.spellings.Operator(p.view, p.path, id, sqltype.Regoper), nil
}
func (p *parallelPresentation) FormatRegoperator(id uint32) (string, error) {
	if p.view.State() == nil {
		return p.DetachedPresentation.FormatRegoperator(id)
	}
	return p.spellings.Operator(p.view, p.path, id, sqltype.Regoperator), nil
}
func (p *parallelPresentation) FormatRegcollation(id uint32) (string, error) {
	if p.view.State() == nil {
		return p.DetachedPresentation.FormatRegcollation(id)
	}
	return regobj.ObjectSpelling(p.view, p.collations, ids.CollationClassID, ids.OID(id)), nil
}
func (p *parallelPresentation) FormatRegnamespace(id uint32) (string, error) {
	if p.view.State() == nil {
		return p.DetachedPresentation.FormatRegnamespace(id)
	}
	return regobj.ObjectSpelling(p.view, nil, ids.NamespaceClassID, ids.OID(id)), nil
}
func (p *parallelPresentation) FormatRegconfig(id uint32) (string, error) {
	if p.view.State() == nil {
		return p.DetachedPresentation.FormatRegconfig(id)
	}
	return p.spellings.TextSearch(p.view, p.path, id, sqltype.Regconfig), nil
}
func (p *parallelPresentation) FormatRegdictionary(id uint32) (string, error) {
	if p.view.State() == nil {
		return p.DetachedPresentation.FormatRegdictionary(id)
	}
	return p.spellings.TextSearch(p.view, p.path, id, sqltype.Regdictionary), nil
}
func (p *parallelPresentation) FormatACLRole(authority *sqltype.ACLAuthority, id uint32) (string, error) {
	if p.view.Owner() == nil {
		return p.DetachedPresentation.FormatACLRole(authority, id)
	}
	return (regobj.ACLItemOutput{Owner: p.view.Owner(), Roles: p.roles}).FormatACLRole(authority, id)
}
