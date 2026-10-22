package exec

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// RLSProgram owns execution-local imported conditions and source lifetimes.
// It is never installed in catalog payloads or retained prepared analysis.
type RLSProgram struct {
	Groups   []RLSRuntimeGroup
	Table    string
	Volatile bool // Rewinds must not reuse truth from volatile policy scans.
}

func (p *RLSProgram) Denied() bool {
	if p == nil {
		return false
	}
	for i := range p.Groups {
		if len(p.Groups[i].Permissive) == 0 {
			return true
		}
	}
	return false
}

type RLSRuntimeGroup struct {
	Permissive  []RLSRuntimeClause
	Restrictive []RLSRuntimeClause
}
type RLSRuntimeClause struct {
	Name       string
	Expression expr.Expr
	Evaluation Context
	State      State
}

// FinishSources settles reached producers before the owning command publishes.
// Clauses own separate captures; ordinary source graphs only borrow programs.
func (p *RLSProgram) FinishSources(primary error) error {
	if p == nil {
		return primary
	}
	for i := range p.Groups {
		for j := range p.Groups[i].Permissive {
			primary = p.Groups[i].Permissive[j].State.FinishSources(primary)
		}
		for j := range p.Groups[i].Restrictive {
			primary = p.Groups[i].Restrictive[j].State.FinishSources(primary)
		}
	}
	return primary
}

func (p *RLSProgram) Close() {
	if p == nil {
		return
	}
	for i := range p.Groups {
		for j := range p.Groups[i].Permissive {
			p.Groups[i].Permissive[j].close()
		}
		for j := range p.Groups[i].Restrictive {
			p.Groups[i].Restrictive[j].close()
		}
	}
}
func (c *RLSRuntimeClause) close() {
	c.State.ReleaseMemberships()
	if c.State.Source != nil {
		c.State.Source.Close()
		c.State.Source = nil
	}
}
func (c *RLSRuntimeClause) evaluate(parent *Context, row []sqltype.Value) (bool, error) {
	// Each admitted occurrence owns this scratch. Guarded sources cannot enter
	// parallel raw paths, and nested policy expansion rejects reentrant cycles.
	local := &c.Evaluation
	local.Context = parent.Context
	local.Host = parent.LiveHost()
	local.EvalFrame = EvalFrame{Row: row, Scope: 1, Frame: 1, PhysicalTable: parent.PhysicalTable}
	if c.State.Source != nil {
		local.Query = &c.State
	}
	defer func() { local.EvalFrame = EvalFrame{}; local.Context = nil; local.Host = nil }()
	if err := EvaluationContext(local); err != nil {
		return false, err
	}
	value, err := c.Expression.Eval(local)
	if err != nil {
		return false, err
	}
	truth, err := plan.TruthOf(value)
	return truth == plan.True, err
}

// EvaluateRowSecurity is the sole USING / WITH CHECK Boolean engine. The empty
// permissive group denies without demanding restrictive expressions. A named
// failure is exclusively a restrictive failure; permissive OR is unnamed.
func EvaluateRowSecurity(p *RLSProgram, c *Context, row []sqltype.Value) (pass bool, policy string, err error) {
	if p == nil {
		return true, "", nil
	}
	for i := range p.Groups {
		group := &p.Groups[i]
		granted := false
		for j := range group.Permissive {
			yes, err := group.Permissive[j].evaluate(c, row)
			if err != nil {
				return false, "", err
			}
			if yes {
				granted = true
				break
			}
		}
		if !granted {
			return false, "", nil
		}
		for j := range group.Restrictive {
			clause := &group.Restrictive[j]
			yes, err := clause.evaluate(c, row)
			if err != nil {
				return false, "", err
			}
			if !yes {
				return false, clause.Name, nil
			}
		}
	}
	return true, "", nil
}
func CheckRowSecurity(p *RLSProgram, c *Context, row []sqltype.Value) error {
	pass, name, err := EvaluateRowSecurity(p, c, row)
	if err != nil || pass {
		return err
	}
	message := fmt.Sprintf(`new row violates row-level security policy for table "%s"`, p.Table)
	if name != "" {
		message = fmt.Sprintf(`new row violates row-level security policy "%s" for table "%s"`, name, p.Table)
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: message}
}
