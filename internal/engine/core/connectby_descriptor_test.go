package core_test

import (
	"errors"
	"fmt"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func connectbyUnitDiagnostic(t *testing.T, err error, code, message, detail string) {
	t.Helper()
	var d *sqlstate.Error
	if !errors.As(err, &d) || d.Code != code || d.Message != message || d.Detail != detail || d.Hint != "" || d.Position != "" || d.InternalPosition != "" || d.InternalQuery != "" || d.Context != "" {
		t.Fatal("connectby descriptor diagnostic", err, detail)
	}
}
func TestConnectbyReturnDescriptorOrder(t *testing.T) {
	e := engine.New()
	text, int4 := enginetest.Usage(e, sqltype.Text), enginetest.Usage(e, sqltype.Int4)
	shape := func(usages []sqltype.Usage) *sqltype.Descriptor {
		members := make([]sqltype.CompositeMember, len(usages))
		for i, u := range usages {
			members[i] = sqltype.CompositeMember{Attribute: int32(i + 1), Name: fmt.Sprintf("c%d", i), Usage: u}
		}
		d, err := sqltype.RecordShape(members)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	for _, ordered := range []bool{false, true} {
		for _, branch := range []bool{false, true} {
			usages := []sqltype.Usage{text, text, int4}
			if branch {
				usages = append(usages, text)
			}
			if ordered {
				usages = append(usages, int4)
			}
			r := functions.ConnectbyRequest{Ordered: ordered, Branch: branch, Result: shape(usages)}
			if err := core.ValidateConnectbyReturn(r); err != nil {
				t.Fatal(err)
			}
			bad := r
			bad.Result = shape([]sqltype.Usage{text, text, text, text, text, text})
			connectbyUnitDiagnostic(t, core.ValidateConnectbyReturn(bad), "42804", "invalid connectby return type", fmt.Sprintf("Return row must have %d columns, not 6.", len(usages)))
			badUsages := append([]sqltype.Usage{}, usages...)
			badUsages[2] = text
			if branch {
				badUsages[3] = int4
			}
			if ordered {
				badUsages[len(badUsages)-1] = text
			}
			bad.Result = shape(badUsages)
			connectbyUnitDiagnostic(t, core.ValidateConnectbyReturn(bad), "42804", "invalid connectby return type", "Third return column (depth) must be type integer.")
			if branch {
				badUsages[2] = int4
				bad.Result = shape(badUsages)
				connectbyUnitDiagnostic(t, core.ValidateConnectbyReturn(bad), "42804", "invalid connectby return type", "Fourth return column (branch) must be type text.")
				badUsages[3] = text
			}
			if ordered {
				badUsages[2] = int4
				bad.Result = shape(badUsages)
				ordinal := "Fourth"
				if branch {
					ordinal = "Fifth"
				}
				connectbyUnitDiagnostic(t, core.ValidateConnectbyReturn(bad), "42804", "invalid connectby return type", ordinal+" return column (serial) must be type integer.")
			}
		}
	}
}
func TestConnectbySourceDescriptorCompatibility(t *testing.T) {
	e := engine.New()
	text, int4 := enginetest.Usage(e, sqltype.Text), enginetest.Usage(e, sqltype.Int4)
	shape := func(key, parent sqltype.Usage) *sqltype.Descriptor {
		d, err := sqltype.RecordShape([]sqltype.CompositeMember{{Attribute: 1, Name: "key", Usage: key}, {Attribute: 2, Name: "parent", Usage: parent}, {Attribute: 3, Name: "level", Usage: int4}})
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	result := shape(int4, text)
	if err := core.ValidateConnectbySource(result, []catalog.Column{{Usage: int4}, {Usage: text}, {Usage: int4}}); err != nil {
		t.Fatal("independent key/parent types and ignored extra source column", err)
	}
	connectbyUnitDiagnostic(t, core.ValidateConnectbySource(result, []catalog.Column{{Usage: int4}}), "22023", "invalid connectby source data query", "The query must return at least two columns.")
	connectbyUnitDiagnostic(t, core.ValidateConnectbySource(result, []catalog.Column{{Usage: text}, {Usage: int4}}), "42804", "invalid connectby return type", "Source key type text does not match return key type integer.")
	connectbyUnitDiagnostic(t, core.ValidateConnectbySource(result, []catalog.Column{{Usage: int4}, {Usage: int4}}), "42804", "invalid connectby return type", "Source parent key type integer does not match return parent key type text.")
	varchar := enginetest.Usage(e, sqltype.VarChar)
	source := varchar
	source.Typmod = 9
	if err := core.ValidateConnectbySource(shape(varchar, varchar), []catalog.Column{{Usage: source}, {Usage: source}}); err != nil {
		t.Fatal("unconstrained typmod", err)
	}
	target := varchar
	target.Typmod = 10
	connectbyUnitDiagnostic(t, core.ValidateConnectbySource(shape(target, varchar), []catalog.Column{{Usage: source}, {Usage: source}}), "42804", "invalid connectby return type", "Source key type character varying(5) does not match return key type character varying(6).")
}
