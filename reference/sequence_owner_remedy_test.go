//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func sequenceOwnerReferenceCommand(t *testing.T,p *referencePair,c *sql.Conn,q string,prepared bool) error {
	t.Helper()
	if !prepared { _,err := c.ExecContext(p.ctx,q); return err }
	s,err := c.PrepareContext(p.ctx,q)
	if err != nil { t.Fatal("utility Prepare",q,err) }
	defer s.Close()
	_,err = s.ExecContext(p.ctx)
	return err
}

func sequenceOwnerReferenceIdentity(t *testing.T,p *referencePair,c *sql.Conn,q,want string) {
	t.Helper()
	var got string
	if err := c.QueryRowContext(p.ctx,q).Scan(&got); err != nil || got != want { t.Fatalf("%s owner=%q want=%q err=%v",q,got,want,err) }
}

func TestReferenceSequenceOwnerForms(t *testing.T) {
	for _, prepared := range []bool{false,true} {
		for _, tc := range []struct{operand,name string}{{"CURRENT_ROLE","current_role"},{"CURRENT_USER","current_user"},{"SESSION_USER","session_user"}} {
			t.Run(tc.operand+map[bool]string{false:"/Direct",true:"/Prepared"}[prepared],func(t *testing.T) {
				p := newReferencePair(t)
				c,err := p.postgres.Conn(p.ctx); if err != nil { t.Fatal(err) }; defer c.Close()
				exec := func(q string) { sequenceProbeExec(t,p.ctx,c,q) }
				defer func(){ exec("RESET SESSION AUTHORIZATION"); exec("DROP TABLE IF EXISTS owner_forms_t CASCADE"); exec("DROP SEQUENCE IF EXISTS owner_forms_s"); exec(`DROP ROLE IF EXISTS "`+tc.name+`"`) }()
				var actor string
				if err := c.QueryRowContext(p.ctx,"SELECT current_user").Scan(&actor); err != nil { t.Fatal(err) }
				exec(`CREATE ROLE "`+tc.name+`"`)
				exec("CREATE SEQUENCE owner_forms_s")
				ownerQuery := "SELECT pg_get_userbyid(relowner) FROM pg_class WHERE oid='owner_forms_s'::regclass"
				for _, target := range []string{`"`+tc.name+`"`,tc.operand} {
					if err := sequenceOwnerReferenceCommand(t,p,c,"ALTER SEQUENCE owner_forms_s OWNER TO "+target,prepared); err != nil { t.Fatal(target,err) }
					want := tc.name; if target == tc.operand { want = actor }
					sequenceOwnerReferenceIdentity(t,p,c,ownerQuery,want)
				}
				exec("CREATE TABLE owner_forms_t(c INT)")
				exec("ALTER SEQUENCE owner_forms_s OWNED BY owner_forms_t.c")
				if err := sequenceOwnerReferenceCommand(t,p,c,"ALTER SEQUENCE owner_forms_s OWNER TO "+tc.operand,prepared); err != nil { t.Fatal("linked noop",err) }
				err = sequenceOwnerReferenceCommand(t,p,c,`ALTER SEQUENCE owner_forms_s OWNER TO "`+tc.name+`"`,prepared)
				want := controlsReferenceDiagnostic("0A000",`cannot change owner of sequence "owner_forms_s"`)
				want.Detail = `Sequence "owner_forms_s" is linked to table "owner_forms_t".`
				referenceAssertDiagnostic(t,err,&want)
				sequenceOwnerReferenceIdentity(t,p,c,ownerQuery,actor)
				sequenceProbeValue(t,p.ctx,c,"SELECT nextval('owner_forms_s')",1)
			})
		}
	}
}

func TestReferenceSequenceOwnerTransitionAuthority(t *testing.T) {
	for _, prepared := range []bool{false,true} {
		t.Run(map[bool]string{false:"Direct",true:"Prepared"}[prepared],func(t *testing.T) {
			p := newReferencePair(t)
			c,err := p.postgres.Conn(p.ctx); if err != nil { t.Fatal(err) }; defer c.Close()
			exec := func(q string){ sequenceProbeExec(t,p.ctx,c,q) }
			defer func(){ exec("RESET SESSION AUTHORIZATION"); exec("DROP SCHEMA IF EXISTS owner_private CASCADE"); for _,name := range []string{"seq_owner","seq_target","seq_reader"} { exec("DROP ROLE IF EXISTS "+name) } }()
			for _,q := range []string{"CREATE ROLE seq_owner","CREATE ROLE seq_target","CREATE ROLE seq_reader","CREATE SCHEMA owner_private","REVOKE ALL ON SCHEMA owner_private FROM PUBLIC","GRANT USAGE,CREATE ON SCHEMA owner_private TO seq_owner","GRANT USAGE ON SCHEMA owner_private TO seq_target,seq_reader","SET SESSION AUTHORIZATION seq_owner","CREATE SEQUENCE owner_private.s CACHE 7","GRANT USAGE ON SEQUENCE owner_private.s TO seq_owner","GRANT SELECT ON SEQUENCE owner_private.s TO seq_reader","RESET SESSION AUTHORIZATION","REVOKE CREATE ON SCHEMA owner_private FROM seq_owner","SET SESSION AUTHORIZATION seq_owner"} { exec(q) }
			sequenceProbeValue(t,p.ctx,c,"SELECT nextval('owner_private.s')",1)
			if err := sequenceOwnerReferenceCommand(t,p,c,"ALTER SEQUENCE owner_private.s OWNER TO seq_owner",prepared); err != nil { t.Fatal("same-owner without CREATE",err) }
			sequenceOwnerReferenceIdentity(t,p,c,"SELECT pg_get_userbyid(relowner) FROM pg_class WHERE oid='owner_private.s'::regclass","seq_owner")
			sequenceProbeValue(t,p.ctx,c,"SELECT nextval('owner_private.s')",2)
			err = sequenceOwnerReferenceCommand(t,p,c,"ALTER SEQUENCE owner_private.s OWNER TO seq_target",prepared)
			want := controlsReferenceDiagnostic("42501",`must be able to SET ROLE "seq_target"`)
			referenceAssertDiagnostic(t,err,&want)
			exec("RESET SESSION AUTHORIZATION")
			if err := sequenceOwnerReferenceCommand(t,p,c,"ALTER SEQUENCE owner_private.s OWNER TO seq_target",prepared); err != nil { t.Fatal("superuser transfer without destination CREATE",err) }
			sequenceOwnerReferenceIdentity(t,p,c,"SELECT pg_get_userbyid(relowner) FROM pg_class WHERE oid='owner_private.s'::regclass","seq_target")
			// Check actual stored grantor and owner-grantee remapping, not merely
			// owner implicit access. Keep the unrelated reader grant intact.
			var count int
			q := "SELECT count(*) FROM pg_class c CROSS JOIN LATERAL aclexplode(c.relacl) a JOIN pg_roles grantor ON grantor.oid=a.grantor JOIN pg_roles grantee ON grantee.oid=a.grantee WHERE c.oid='owner_private.s'::regclass AND grantor.rolname='seq_target' AND ((grantee.rolname='seq_target' AND a.privilege_type='USAGE') OR (grantee.rolname='seq_reader' AND a.privilege_type='SELECT'))"
			if err := c.QueryRowContext(p.ctx,q).Scan(&count); err != nil || count != 2 { t.Fatal("ACL remap",count,err) }
			exec("SET SESSION AUTHORIZATION seq_target")
			sequenceProbeValue(t,p.ctx,c,"SELECT nextval('owner_private.s')",3)
			exec("RESET SESSION AUTHORIZATION")
			exec("SET SESSION AUTHORIZATION seq_reader")
			sequenceProbeValue(t,p.ctx,c,"SELECT last_value FROM owner_private.s",7)
		})
	}
}
