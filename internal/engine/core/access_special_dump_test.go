package core_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

type specialDumpWriter struct{ calls int }

func (w *specialDumpWriter) Write(p []byte) (int, error) { w.calls++; return len(p), nil }

func TestAccessSpecialParameterDumpPreflight(t *testing.T) {
	for _, mode := range []string{"valid", "provider", "grantor", "mask"} {
		t.Run(mode, func(t *testing.T) {
			e := engine.New()
			owner := e.RoleStore.Domain.Principal(10)
			d := &catalog.Parameter{ObjectAddress: ids.ObjectAddress{ClassID: ids.ParameterClassID, ObjectID: 910292}, ObjectRevision: 2, ObjectName: "lc_messages", Owner: owner, ACL: authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.SetPrivilege}}}}
			switch mode {
			case "provider":
				d.ObjectName = "absent_nondotted_setting"
			case "grantor":
				d.ACL.Entries[0].Grantor = new(authorization.Domain).Principal(10)
			case "mask":
				d.ACL.Entries[0].Privileges = authorization.SelectPrivilege
			}
			e.Mu.Lock()
			state := catalog.CloneState(e.Catalog.State)
			state.SetDescriptor(d.Address(), d)
			state.BindName(catalog.DescriptorNameKey(d), d.Address())
			e.Catalog.PublishLocked(state)
			captured := e.Catalog.Read(state)
			e.Mu.Unlock()
			if mode != "valid" {
				w := new(specialDumpWriter)
				if err := engine.Dump(context.Background(), e, w); err == nil || w.calls != 0 {
					t.Fatal("invalid parameter authority reached writer", mode, err, w.calls)
				}
				return
			}
			var first, second bytes.Buffer
			if err := engine.Dump(context.Background(), e, &first); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(first.String(), "UPDATE pg_catalog.pg_parameter_acl SET paracl=") || !strings.Contains(first.String(), "WHERE parname=E'lc_messages'::pg_catalog.text;") {
				t.Fatal("missing bounded by-name typed parameter repair", first.String())
			}
			if err := engine.Dump(context.Background(), e, &second); err != nil || !bytes.Equal(first.Bytes(), second.Bytes()) {
				t.Fatal("nondeterministic dump", err)
			}
			if captured.Get(d.Address()) != d || !d.ACL.Explicit || len(d.ACL.Entries) != 1 {
				t.Fatal("dump mutated immutable capture")
			}
		})
	}
}
