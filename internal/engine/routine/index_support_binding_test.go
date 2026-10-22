package routine

import (
	"errors"
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestExclusionOperatorFamilyMembership(t *testing.T) {
	state := catalog.NewState(1)
	catalog.InstallBuiltinFamilies(state, authorization.PrincipalRef{})
	catalog.InstallBuiltinClasses(state, authorization.PrincipalRef{})
	procedures := BuiltinProcedures(authorization.PrincipalRef{})
	for i := range procedures {
		state.SetDescriptor(procedures[i].Address(), &procedures[i])
	}
	operators := BuiltinOperatorDescriptors(authorization.PrincipalRef{})
	for i := range operators {
		state.SetDescriptor(operators[i].Address(), &operators[i])
	}
	view := catalog.NewRead(nil, state)
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, typ := range sqltype.Builtins() {
		types[typ.ID()] = typ
	}
	for _, method := range []ids.OID{403, 405} {
		for _, tc := range []struct {
			input                    sqltype.ID
			class, signature, family string
		}{
			{sqltype.Int4, "oid_ops", "=(integer,integer)", "oid_ops"},
			{sqltype.Text, "bpchar_ops", "=(text,text)", "bpchar_ops"},
			{sqltype.Int4, "int4_ops", "", ""},
			{sqltype.Text, "text_pattern_ops", "", ""},
		} {
			t.Run(fmt.Sprintf("%d/%s", method, tc.class), func(t *testing.T) {
				fact, ok := view.LookupOperatorClass(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, method, tc.class)
				if !ok {
					t.Fatal("missing catalog class", tc.class)
				}
				class, ok := view.IndexClass(fact.Address().ObjectID)
				if !ok || !class.Accepts(types[tc.input]) {
					t.Fatal("fixture must demonstrate actual index input admission", class)
				}
				predicate, err := BindExclusionOperator(view, types[tc.input].Usage(), class, "=")
				if tc.signature != "" {
					var diagnostic *sqlstate.Error
					message := fmt.Sprintf(`operator %s is not a member of operator family "%s"`, tc.signature, tc.family)
					if !errors.As(err, &diagnostic) || diagnostic.Code != "42809" || diagnostic.Message != message || diagnostic.Detail != "The exclusion operator must be related to the index operator class for the constraint." {
						t.Fatal("binary admission was mistaken for operator membership", err)
					}
					return
				}
				inputs, inputErr := predicate.OperatorInputs()
				if err != nil || inputErr != nil || inputs != [2]sqltype.ID{tc.input, tc.input} {
					t.Fatal("actual family member failed binding", inputs, err, inputErr)
				}
				strategy := int16(3)
				if method == 405 {
					strategy = 1
				}
				member, ok := view.FamilyOperator(fact.Family, ids.OID(tc.input), ids.OID(tc.input), strategy, 's')
				if !ok {
					t.Fatal("missing real family member")
				}
				without := catalog.CloneState(state)
				without.RemoveDescriptor(member.Address())
				_, err = BindExclusionOperator(catalog.NewRead(nil, without), types[tc.input].Usage(), class, "=")
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != "42809" {
					t.Fatal("missing live membership selected a fallback", err)
				}
			})
		}
	}
}
