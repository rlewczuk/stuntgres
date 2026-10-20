package sequence

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/storage"
)

// CallPrivileges is the privilege mask of which any one admits a sequence
// function call: USAGE or SELECT for currval/lastval, USAGE or UPDATE for
// nextval, only UPDATE for setval.
func CallPrivileges(name string) authorization.Privileges {
	switch name {
	case "nextval":
		return authorization.UsagePrivilege | authorization.UpdatePrivilege
	case "setval":
		return authorization.UpdatePrivilege
	}
	return authorization.UsagePrivilege | authorization.SelectPrivilege
}

// ReadOnlyCallError is the read-only transaction rejection of a modifying
// sequence function, or nil for currval and lastval. The engine exempts the
// session's own TEMP sequences.
func ReadOnlyCallError(name string) error {
	if name == "nextval" || name == "setval" {
		return sqlError("25006", "cannot execute "+name+"() in a read-only transaction")
	}
	return nil
}

// CallLocked runs one admitted sequence function against the opened relation,
// its shared generator and the calling physical session's memory. The caller
// holds Engine.mu.
func CallLocked(request functions.SequenceRequest, d *catalog.Relation, g *storage.SequenceGenerator, memories *SessionMemory) (int64, error) {
	m := memories.DemandLocked(d.Address())
	m.ObserveLocked(g.Generation)
	var result int64
	var err error
	switch request.Name {
	case "currval", "lastval":
		value, valid := m.Current()
		if !valid {
			msg := fmt.Sprintf(`currval of sequence "%s" is not yet defined in this session`, d.Name())
			if request.Name == "lastval" {
				msg = "lastval is not yet defined in this session"
			}
			return 0, sqlError("55000", msg)
		}
		result = value
	case "nextval":
		result, err = m.NextLocked(*d.Sequence, g, d.Name())
		if err != nil {
			return 0, err
		}
		memories.RecordLastLocked(d.Address())
	case "setval":
		result, err = request.Value.Integer()
		if err != nil {
			return 0, err
		}
		if result < d.Sequence.Min || result > d.Sequence.Max {
			return 0, sqlError("22003", fmt.Sprintf(`setval: value %d is out of bounds for sequence "%s" (%d..%d)`, result, d.Name(), d.Sequence.Min, d.Sequence.Max))
		}
		called := true
		if request.HasCalled {
			called, err = request.Called.Boolean()
			if err != nil {
				return 0, err
			}
		}
		g.Last, g.Called = result, called
		m.SetLocked(result, called)
	}
	return result, nil
}
