package ids_test

import (
	"testing"

	"stuntgres/internal/engine/ids"
)

// owner stands in for the engine's transaction owner and session: it embeds
// the markers and has nonzero size, so distinct owners have distinct pointers.
type owner struct {
	ids.TxOwnerMarker
	ids.SessionOwnerMarker
	name string
}

var (
	_ ids.TxOwner      = (*owner)(nil)
	_ ids.SessionOwner = (*owner)(nil)
)

var relation = ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 16384}

func TestDomainIdentityIsPointerIdentity(t *testing.T) {
	a, b := new(ids.Domain), new(ids.Domain)
	if a == b {
		t.Fatal("distinct domains share one identity")
	}
	if a.Ref(1, relation) != a.Ref(1, relation) {
		t.Fatal("refs minted alike in one domain differ")
	}
	if a.Ref(1, relation) == b.Ref(1, relation) {
		t.Fatal("refs from different domains compare equal")
	}
	if a.Session(7) == b.Session(7) {
		t.Fatal("session tokens from different domains compare equal")
	}
	if a.Tx(a.Session(7), 1, 1, nil, 0) == b.Tx(a.Session(7), 1, 1, nil, 0) {
		t.Fatal("transaction refs from different domains compare equal")
	}
}

func TestDomainZeroIdentitiesAreInvalid(t *testing.T) {
	if (ids.ObjectRef{}).Domain() != nil || (ids.SessionToken{}).Domain() != nil || (ids.TxRef{}).Domain() != nil {
		t.Fatal("zero identity has a domain")
	}
	var none *ids.Domain
	live := new(ids.Domain)
	if none.Ref(1, relation).Domain() != nil || none.Ref(1, relation) == live.Ref(1, relation) {
		t.Fatal("nil-domain ref gained provenance")
	}
	if none.Session(1).Domain() != nil || none.Session(1) == live.Session(1) {
		t.Fatal("nil-domain token gained provenance")
	}
	if none.Tx(ids.SessionToken{}, 0, 0, nil, 0) != (ids.TxRef{}) {
		t.Fatal("all-zero constructor differs from the zero transaction ref")
	}
	if none.Session(0) != (ids.SessionToken{}) {
		t.Fatal("all-zero constructor differs from the zero token")
	}
}

func TestSessionTokenPrivateIdentity(t *testing.T) {
	d := new(ids.Domain)
	a, b := &owner{name: "a"}, &owner{name: "b"}
	public := d.Session(7)
	if public.ID() != 7 || public.Domain() != d || public.Private() != nil {
		t.Fatal("public token accessors", public.ID(), public.Domain(), public.Private())
	}
	private := d.PrivateSession(a)
	if private.ID() != 0 || private.Domain() != d || private.Private() != ids.SessionOwner(a) {
		t.Fatal("private token accessors", private.ID(), private.Domain(), private.Private())
	}
	if private != d.PrivateSession(a) || private == d.PrivateSession(b) || private == d.Session(0) {
		t.Fatal("private token identity is not owner pointer identity")
	}
	tokens := map[ids.SessionToken]string{public: "public", private: "private"}
	if tokens[d.Session(7)] != "public" || tokens[d.PrivateSession(a)] != "private" || len(tokens) != 2 {
		t.Fatal("tokens are not stable map keys", tokens)
	}
}

func TestTxRefIdentity(t *testing.T) {
	d := new(ids.Domain)
	a, b := &owner{name: "a"}, &owner{name: "b"}
	session := d.Session(3)
	ref := d.Tx(session, 5, 9, a, 11)
	if ref.Domain() != d || ref.Session() != session || ref.ID() != 5 || ref.Epoch() != 9 || ref.Attachment() != ids.TxOwner(a) || ref.Diagnostic() != 11 {
		t.Fatal("transaction ref accessors", ref.Domain(), ref.Session(), ref.ID(), ref.Epoch(), ref.Attachment(), ref.Diagnostic())
	}
	for name, other := range map[string]ids.TxRef{
		"owner":      d.Tx(session, 5, 9, b, 11),
		"unattached": d.Tx(session, 5, 9, nil, 11),
		"session":    d.Tx(d.Session(4), 5, 9, a, 11),
		"id":         d.Tx(session, 6, 9, a, 11),
		"epoch":      d.Tx(session, 5, 10, a, 11),
		"diagnostic": d.Tx(session, 5, 9, a, 12),
	} {
		if other == ref {
			t.Fatal("transaction ref equality ignores", name)
		}
	}
	refs := map[ids.TxRef]int{ref: 1}
	if refs[d.Tx(session, 5, 9, a, 11)] != 1 {
		t.Fatal("transaction ref is not a stable map key")
	}
}
