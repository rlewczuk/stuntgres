package engine

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// newPLTriggerFrame owns one invocation. OLD and NEW are both typed records even
// when their tuple is absent; no table shape is written into the shared program.
func newPLTriggerFrame(d *catalog.Procedure, read catalog.Read, relation *catalog.Relation, name, event string, args []string, old, next []sqltype.Value) (*proc.PLFrame, error) {
	if d.SQL == nil || !d.SQL.Trigger || d.SQL.PL == nil || !d.SQL.PL.Trigger {
		return nil, fmt.Errorf("stuntgres: missing checked trigger program")
	}
	shape, ok := read.LookupType(relation.RowType)
	if !ok || !shape.IsComposite() {
		return nil, fmt.Errorf("stuntgres: missing trigger relation row type")
	}
	f := proc.NewPLFrame(d.SQL.PL, d.SQL, &proc.Frame{}, sqltype.CollationRef{})
	fail := func(err error) (*proc.PLFrame, error) { f.Close(); return nil, err }
	for i, row := range [2][]sqltype.Value{old, next} {
		if row == nil {
			f.Values[i] = sqltype.Null(shape)
		} else {
			// CompositeValue detaches the producer's scratch row.
			value, err := sqltype.CompositeValue(shape, row)
			if err != nil {
				return fail(err)
			}
			f.Values[i] = value
		}
	}
	ns, ok := read.Get(relation.Namespace()).(*catalog.Namespace)
	if !ok {
		return fail(fmt.Errorf("stuntgres: missing trigger namespace"))
	}
	for _, entry := range [...]struct {
		slot int
		text string
	}{{2, name}, {3, "BEFORE"}, {4, "ROW"}, {5, strings.ToUpper(event)}, {7, relation.Name()}, {8, ns.Name()}, {9, relation.Name()}} {
		f.Values[entry.slot] = sqltype.String(f.Program.Variables[entry.slot].Usage.Type, entry.text)
	}
	var err error
	f.Values[6], err = sqltype.OIDValue(f.Program.Variables[6].Usage.Type, uint32(relation.Address().ObjectID))
	if err != nil {
		return fail(err)
	}
	f.Values[10], err = sqltype.Integer(f.Program.Variables[10].Usage.Type, int64(len(args)))
	if err != nil {
		return fail(err)
	}
	array := f.Program.Variables[11].Usage.Type
	f.Values[11] = sqltype.Null(array)
	if len(args) != 0 {
		values := make([]sqltype.Value, len(args))
		for i, arg := range args {
			values[i] = sqltype.String(array.Element(), arg)
		}
		f.Values[11], err = sqltype.OwnedArrayValue(array, []sqltype.ArrayDimension{{Length: int32(len(args)), Lower: 0}}, values)
		if err != nil {
			return fail(err)
		}
	}
	return f, nil
}

// invokePLTrigger is reached only by the admitted trigger payload entry, never
// by ordinary SQL routine dispatch. Its caller owns depth and live provenance.
func (s *StatementScope) invokePLTrigger(d *catalog.Procedure, f *proc.PLFrame, read catalog.Read, relation *catalog.Relation) (sqltype.Value, error) {
	result, flow, err := s.executePLBlock(d, f, d.SQL.PL.Root, read)
	if err != nil {
		return sqltype.Value{}, err
	}
	identity := "PL/pgSQL function " + plRoutineDiagnosticIdentity(read, d)
	if flow.Kind != proc.PLReturn {
		return sqltype.Value{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2F005", Message: "control reached end of trigger procedure without RETURN", Context: identity}
	}
	shape, ok := read.LookupType(relation.RowType)
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: trigger row type disappeared")
	}
	result, err = proc.TriggerReturn(shape, result)
	if diagnostic, ok := err.(*sqlstate.Error); ok {
		copy := *diagnostic
		copy.Context = identity + " during function exit"
		err = &copy
	}
	return result, err
}
