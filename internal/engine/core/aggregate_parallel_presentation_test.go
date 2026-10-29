package core_test

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/sqltype"
)

// Exercise every real captured formatter family, including lazy cache misses,
// while the sibling's SQL runner owns and replaces the live lexical scratch.
func exerciseParallelPresentation(c *exec.Context, procedure uint32, acl *exec.TransitionAggregate) error {
	if c.ClosedEvaluation == nil || c.FunctionInput().XMLMode != c.Input.XMLMode {
		return fmt.Errorf("captured XML mode lost authority")
	}
	p := c.Presentation()
	for _, format := range []func(uint32) (string, error){p.FormatRegclass, p.FormatRegtype,
		p.FormatRegproc, p.FormatRegprocedure, p.FormatRegrole, p.FormatRegoper,
		p.FormatRegoperator, p.FormatRegcollation, p.FormatRegnamespace,
		p.FormatRegconfig, p.FormatRegdictionary} {
		text, err := format(0)
		if err != nil || text == "" {
			return fmt.Errorf("captured formatter: %q: %v", text, err)
		}
	}
	if _, err := p.FormatACLRole(c.Catalog.Owner().ACLAuthority, 0); err != nil {
		return err
	}
	classID := uint32(c.Query.Source.Ref.Address.ObjectID)
	if text, err := p.FormatRegclass(classID); err != nil || text != "ac_snapshot_input" {
		return fmt.Errorf("class spelling %q: %v", text, err)
	}
	if text, err := p.FormatRegproc(procedure); err != nil || text != "ac_snapshot_step" {
		return fmt.Errorf("routine spelling %q: %v", text, err)
	}
	class, ok := c.Catalog.LookupType(sqltype.Regclass)
	if !ok {
		return fmt.Errorf("missing captured class type")
	}
	text, ok := c.Catalog.LookupType(sqltype.Text)
	if !ok {
		return fmt.Errorf("missing captured text type")
	}
	value, err := class.Input(c.Context, strconv.FormatUint(uint64(classID), 10), -1)
	if err != nil {
		return err
	}
	conversion, err := sqltype.PlanCoercion(class.Usage(), text.Usage(), sqltype.Explicit)
	if err != nil {
		return err
	}
	converted, err := c.ApplyCoercion(conversion, value, false)
	if err != nil {
		return err
	}
	spelling, err := converted.Text()
	if err != nil || spelling != "ac_snapshot_input" {
		return fmt.Errorf("captured coercion %q: %v", spelling, err)
	}
	xml, ok := c.Catalog.LookupType(sqltype.XML)
	if !ok {
		return fmt.Errorf("missing XML type")
	}
	in := c.FunctionInput()
	in.XMLMode = sqltype.XMLDocument
	in = in.LiteralInput(31)
	if _, err := xml.InputWithEnvironment(c.Context, `<?xml version="1.1"?><a>`, -1, in); err == nil {
		return fmt.Errorf("malformed XML input accepted")
	}
	query, ok := c.Catalog.LookupType(sqltype.TSQuery)
	if !ok {
		return fmt.Errorf("missing tsquery type")
	}
	in.FTS = in.FTS.FTSLiteral(37)
	if _, err := query.InputWithEnvironment(c.Context, "", -1, in); err != nil {
		return err
	}
	if _, err := in.Regtype.ResolveRegtype(c.Context, strings.Repeat("a", 70)); err == nil {
		return fmt.Errorf("missing truncated type accepted")
	}
	// These are actual selected ACL-state initializers, not direct calls to the
	// warning hook. Initialization is deliberately outside the SQL routine gate.
	for _, input := range []sqltype.InputEnvironment{c.FunctionInput().LiteralInput(43), c.FunctionInput().ParameterInput(2, false)} {
		local := *c
		local.Input = input
		state, err := acl.InitWithContext(c.Context, &local)
		if err != nil {
			return err
		}
		value, err := acl.FinalizeWithContext(c.Context, state, nil, &local)
		if err != nil {
			return err
		}
		authority, grantee, grantor, privileges, options, err := value.ACLItem()
		// Released ACL_SELECT is bit 1; "=r" names PUBLIC and default grantor 10.
		if err != nil || authority != c.Catalog.Owner().ACLAuthority || grantee != 0 || grantor != 10 || privileges != 2 || options != 0 {
			return fmt.Errorf("captured ACL seed authority/value: %p %d %d %d %d: %v", authority, grantee, grantor, privileges, options, err)
		}
	}
	// Explicit names actually consult the retained immutable role snapshot and
	// must not generate a default-grantor warning.
	explicit, err := acl.StateUsage.Type.InputWithEnvironment(c.Context, "postgres=r/postgres", acl.StateUsage.Typmod, c.FunctionInput().ParameterInput(2, false))
	if err != nil {
		return err
	}
	authority, grantee, grantor, privileges, options, err := explicit.ACLItem()
	if err != nil || authority != c.Catalog.Owner().ACLAuthority || grantee != 10 || grantor != 10 || privileges != 2 || options != 0 {
		return fmt.Errorf("explicit captured ACL authority/roles: %p %d %d %d %d: %v", authority, grantee, grantor, privileges, options, err)
	}
	canceled, cancel := context.WithCancel(c.Context)
	cancel()
	local := *c
	local.Context = canceled
	if _, err := acl.InitWithContext(canceled, &local); !errors.Is(err, context.Canceled) {
		return fmt.Errorf("already-canceled ACL initializer: %v", err)
	}
	// Missing captured resolvers/presentation must never borrow host scratch.
	missing := *c
	missing.Input.Regrole, missing.Input.Regcollation, missing.Input.Regnamespace = nil, nil, nil
	missing.ClosedEvaluation = &exec.EvaluationCapture{}
	missing.FunctionInput()
	if _, ok := missing.Presentation().(exec.DetachedPresentation); !ok {
		return fmt.Errorf("nil presentation fell through to live host")
	}
	for _, id := range []sqltype.ID{sqltype.Regrole, sqltype.Regcollation, sqltype.Regnamespace} {
		target, ok := c.Catalog.LookupType(id)
		if !ok {
			return fmt.Errorf("missing alias descriptor %d", id)
		}
		conversion, err := sqltype.PlanCoercion(text.Usage(), target.Usage(), sqltype.Explicit)
		if err != nil {
			return err
		}
		_, err = missing.ApplyCoercion(conversion, sqltype.String(text, "ac_missing_alias"), false)
		if err == nil || err.Error() != target.DiagnosticName()+" input lacks catalog authority" {
			return fmt.Errorf("authoritative nil alias resolver %s: %v", target.DiagnosticName(), err)
		}
	}
	return nil
}
