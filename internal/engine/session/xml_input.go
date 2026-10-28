package session

import (
	"fmt"
	"strconv"

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// XMLInput belongs only to the active operation. Specializations copy provenance;
// accepted XML values and retained presentations never retain this adapter.
type XMLInput struct {
	Op         *Operation
	position   int
	portal     string
	textSuffix string
	label      string
}

func (r XMLInput) XMLInputDiagnostic(d sqlstate.Error) sqlstate.Error {
	if r.position > 0 {
		d.Position = strconv.Itoa(r.position)
	}
	if r.portal != "" {
		if d.Context != "" {
			d.Context += "\n"
		}
		d.Context += r.portal
		if d.Code != "22021" {
			d.Context += r.textSuffix
		}
	}
	return d
}
func (r XMLInput) XMLInputWarning(d sqlstate.Error) {
	if r.Op != nil {
		r.Op.EmitNotice(LevelWarning, r.XMLInputDiagnostic(d))
	}
}
func (r XMLInput) XMLLiteral(position int) sqltype.XMLInputDiagnostics {
	r.position, r.portal, r.textSuffix = position, "", ""
	return r
}
func (r XMLInput) XMLParameter(slot int, empty bool) sqltype.XMLInputDiagnostics {
	r.position = 0
	r.portal = r.parameterLabel(slot)
	r.textSuffix = " = '...'"
	if empty {
		r.textSuffix = " = ''"
	}
	return r
}

// XMLBinaryParameter has no decoded text representation in its provenance.
func (r XMLInput) XMLBinaryParameter(slot int) sqltype.XMLInputDiagnostics {
	r.position = 0
	r.portal = r.parameterLabel(slot)
	r.textSuffix = ""
	return r
}

// WithPortal supplies the actual wire portal label before slot specialization.
func (r XMLInput) WithPortal(name string) XMLInput {
	if name != "" {
		r.label = fmt.Sprintf("portal \"%s\"", name)
	}
	return r
}
func (r XMLInput) parameterLabel(slot int) string {
	label := r.label
	if label == "" {
		label = "unnamed portal"
	}
	return fmt.Sprintf("%s parameter $%d", label, slot)
}
