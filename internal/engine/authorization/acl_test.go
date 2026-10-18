package authorization

import "testing"

func TestACLItemBits(t *testing.T) {
	for _, tc := range []struct {
		mask Privileges
		want uint32
	}{{SetPrivilege, 1 << 12}, {AlterSystemPrivilege, 1 << 13}, {SetPrivilege | AlterSystemPrivilege, 1<<12 | 1<<13}} {
		got, err := ACLItemBits(tc.mask)
		if err != nil || got != tc.want {
			t.Fatalf("parameter ACLITEM transport: %x, %v; want %x", got, err, tc.want)
		}
	}
	if _, err := ACLItemBits(Privileges(1 << 31)); err == nil {
		t.Fatal("unknown privilege silently discarded")
	}
}
