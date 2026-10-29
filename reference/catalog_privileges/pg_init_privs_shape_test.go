package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGInitPrivsShape(t *testing.T) {
	ipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		ipRawSelf(t, p, b)
		for _, q := range []string{"SELECT * FROM pg_catalog.pg_init_privs WHERE FALSE", "SELECT * FROM pg_init_privs WHERE FALSE", "SELECT objoid,classoid,objsubid,privtype,initprivs FROM pg_init_privs WHERE objoid=$1::oid AND classoid=0"} {
			values := []any{nil}
			if q[len(q)-1:] == "0" {
				values = []any{int64(0), int64(2147483648), int64(4294967295)}
			}
			for _, v := range values {
				var args []any
				if v != nil {
					args = []any{v}
				}
				s := ipShapeStep(q, b.PG, args...)
				for _, plain := range []bool{false, true} {
					s.NonContext = plain
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
				}
				fixture.RunStep(t, p.Ctx, fixture.ViewHold(t, p, b, q), b.Recorder, s)
			}
		}
		var attributes [][]any
		for i, name := range ipColumns {
			dims := int64(0)
			if i == 4 {
				dims = 1
			}
			attributes = append(attributes, []any{name, int64(i + 1), true, dims, int64(0), int64(-1)})
		}
		ipCheck(t, p, b, b.DB, "SELECT attname,attnum,attnotnull,attndims,attcollation,atttypmod FROM pg_attribute WHERE attrelid=3394 AND attnum>0", []string{"attname", "attnum", "attnotnull", "attndims", "attcollation", "atttypmod"}, []string{"NAME", "INT2", "BOOL", "INT2", "OID", "INT4"}, attributes)
		owner := ipOwner(t, p, b)
		ipACL(t, p, b, b.DB, 1259, 3394, 0, "i", ipGrant{owner, owner, ipTableRights, false}, ipGrant{0, owner, []string{"SELECT"}, false})
	})
}

func TestReferencePGInitPrivsBootstrap(t *testing.T) {
	ipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		owner := ipOwner(t, p, b)
		for _, name := range []string{"pg_class", "pg_init_privs", "pg_authid", "pg_settings", "pg_shmem_allocations", "pg_shmem_allocations_numa", "pg_subscription"} {
			oid := ipOID(t, p, b, "SELECT oid FROM pg_class WHERE relname=$1 AND relnamespace=11", name)
			grants := []ipGrant{{owner, owner, ipTableRights, false}}
			switch name {
			case "pg_authid", "pg_subscription":
			case "pg_settings":
				grants = append(grants, ipGrant{0, owner, []string{"SELECT", "UPDATE"}, false})
			case "pg_shmem_allocations", "pg_shmem_allocations_numa":
				grants = append(grants, ipGrant{ipRole(t, p, b, "pg_read_all_stats"), owner, []string{"SELECT"}, false})
			default:
				grants = append(grants, ipGrant{0, owner, []string{"SELECT"}, false})
			}
			ipACL(t, p, b, b.DB, 1259, oid, 0, "i", grants...)
			if name == "pg_subscription" {
				var subids [][]any
				for sub := int64(0); sub <= 18; sub++ {
					if sub == 14 {
						continue
					}
					subids = append(subids, []any{sub})
					if sub > 0 {
						ipACL(t, p, b, b.DB, 1259, oid, sub, "i", ipGrant{0, owner, []string{"SELECT"}, false})
					}
				}
				ipCheck(t, p, b, b.DB, "SELECT objsubid FROM pg_init_privs WHERE classoid=1259 AND objoid=$1", []string{"objsubid"}, []string{"INT4"}, subids, oid)
			}
		}
		namespace := ipOID(t, p, b, "SELECT oid FROM pg_namespace WHERE nspname='pg_catalog'")
		ipACL(t, p, b, b.DB, 2615, namespace, 0, "i", ipGrant{owner, owner, []string{"USAGE", "CREATE"}, false}, ipGrant{0, owner, []string{"USAGE"}, false})
		// initdb captures initial privileges before creating information_schema.
		// Its explicit current namespace/relation ACLs are not historical seeds.
		informationSchema := ipOID(t, p, b, "SELECT oid FROM pg_namespace WHERE nspname='information_schema'")
		ipAbsent(t, p, b, b.DB, 2615, informationSchema)
		ipCheck(t, p, b, b.DB, "SELECT i.objoid FROM pg_init_privs i JOIN pg_class c ON c.oid=i.objoid WHERE i.classoid=1259 AND c.relnamespace=$1", []string{"objoid"}, []string{"OID"}, nil, informationSchema)
		for _, tc := range []struct {
			oid     int64
			special string
		}{
			{2625, ""}, {3297, ""}, {3353, "pg_monitor"}, {3354, "pg_monitor"},
			{6271, "pg_monitor"}, {6270, "pg_monitor"}, {6272, "pg_monitor"}, {6400, "pg_monitor"},
			{5031, "pg_monitor"}, {3826, ""}, {6208, ""}, {2624, ""}, {3293, ""},
			{3828, ""}, {6209, ""}, {3827, ""}, {3295, ""}, {2623, ""}, {3307, ""},
			{5029, "pg_monitor"}, {5030, "pg_monitor"}, {3800, "pg_monitor"}, {3801, "pg_monitor"},
			{5052, "pg_read_all_stats"}, {4100, "pg_read_all_stats"},
		} {
			grants := []ipGrant{{owner, owner, []string{"EXECUTE"}, false}}
			if tc.special != "" {
				grants = append(grants, ipGrant{ipRole(t, p, b, tc.special), owner, []string{"EXECUTE"}, false})
			}
			ipACL(t, p, b, b.DB, 1255, tc.oid, 0, "i", grants...)
		}
		for _, a := range [][2]int64{{2615, 99}, {1247, 23}, {2612, 12}, {1255, 65}} {
			ipAbsent(t, p, b, b.DB, a[0], a[1])
		}
	})
}
