package reference_test

import (
	"database/sql/driver"
	"errors"
	"runtime"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGNamespaceTemporarySuperuserAuthority(t *testing.T) {
	pgNamespaceScenario(t, nil, []string{"ns18_authority_creator", "ns18_authority_reader"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		creator := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		peer := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		var pid int
		if b.PG {
			if err := creator.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
				t.Fatal(err)
			}
		}
		privilege := fixture.Select("SELECT rolsuper FROM pg_roles WHERE rolname=current_user", []string{"rolsuper"}, []string{"BOOL"}, [][]any{{true}})
		for _, db := range []fixture.Executor{creator, peer} {
			pgNamespaceCheck(t, p, b, db, privilege)
			pgNamespaceCheck(t, p, b, db, fixture.Prepared(privilege))
		}
		pgNamespaceExec(t, p, b, creator, "CREATE ROLE ns18_authority_creator", "CREATE ROLE ns18_authority_reader", "SET ROLE ns18_authority_creator", "CREATE TEMP TABLE ns18_authority_object(i INT)")
		actor := pgNamespaceRoleID(t, p, creator, "ns18_authority_creator")
		name, id, toast, toastID := pgNamespaceTempPair(t, p, b, creator, "ns18_authority_object", actor)
		fixture.RunStep(t, p.Ctx, creator, b.Recorder, fixture.Exec("INSERT INTO ns18_authority_object VALUES(18)", 1))
		pgNamespaceExec(t, p, b, creator, "GRANT SELECT ON ns18_authority_object TO PUBLIC")
		live := fixture.Select("SELECT i FROM "+name+".ns18_authority_object", []string{"i"}, []string{"INT4"}, [][]any{{int64(18)}})
		var objectID int64
		if err := creator.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relnamespace=$1 AND relname='ns18_authority_object'", id).Scan(&objectID); err != nil || objectID <= 0 {
			t.Fatal("actual TEMP object identity", objectID, err)
		}
		// Resolve the qualified identity, not another backend's physical storage.
		identity := fixture.Select("SELECT '"+name+".ns18_authority_object'::regclass::oid=$1::oid AS same_identity", []string{"same_identity"}, []string{"BOOL"}, [][]any{{true}}, objectID)
		for _, prepared := range []bool{false, true} {
			ownRead := live
			ownRead.Prepared = prepared
			pgNamespaceCheck(t, p, b, creator, ownRead)
			s := identity
			s.Prepared = prepared
			pgNamespaceCheck(t, p, b, peer, s)
			foreignCreate := fixture.SchemaReferenceCreationError("CREATE TABLE "+name+".ns18_foreign_create(i INT)", "42P16", "cannot create relations in temporary schemas of other sessions", prepared, "14")
			fixture.RunStep(t, p.Ctx, peer, b.Recorder, foreignCreate)
			ownCreate := fixture.Exec("CREATE TABLE "+name+".ns18_own_create(i INT)", 0)
			ownCreate.Prepared = prepared
			fixture.RunStep(t, p.Ctx, creator, b.Recorder, ownCreate)
			pgNamespaceExec(t, p, b, creator, "DROP TABLE "+name+".ns18_own_create")
		}
		deniedRead := func() {
			pgNamespaceExec(t, p, b, peer, "SET ROLE ns18_authority_reader")
			ordinary := fixture.Select("SELECT rolsuper FROM pg_roles WHERE rolname=current_user", []string{"rolsuper"}, []string{"BOOL"}, [][]any{{false}})
			pgNamespaceCheck(t, p, b, peer, ordinary)
			pgNamespaceCheck(t, p, b, peer, fixture.Prepared(ordinary))
			for _, prepared := range []bool{false, true} {
				s := fixture.SchemaReferenceError(live.Query, "42501", "permission denied for schema "+name, "", prepared, "execute", "15")
				s.SelectRows = true
				if prepared {
					s.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, peer, b.Recorder, s)
			}
			pgNamespaceExec(t, p, b, peer, "RESET ROLE")
		}
		deniedRead()
		pgNamespaceExec(t, p, b, creator, "RESET ROLE")
		if err := creator.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
			t.Fatal("physical retirement", err)
		}
		if b.PG {
			for {
				var live bool
				if err := peer.QueryRowContext(p.Ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1)", pid).Scan(&live); err != nil {
					t.Fatal("observing physical exit", err)
				}
				if !live {
					break
				}
				runtime.Gosched()
			}
		}
		pgNamespaceRow(t, p, b, peer, name, id, 10, nil)
		pgNamespaceRow(t, p, b, peer, toast, toastID, 10, nil)
		pgNamespaceCheck(t, p, b, peer, fixture.Select("SELECT count(*) FROM pg_class WHERE relnamespace=$1", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, id))
		for _, prepared := range []bool{false, true} {
			missing := fixture.SchemaReferenceError(live.Query, "42P01", `relation "`+name+`.ns18_authority_object" does not exist`, "", prepared, "execute", "15")
			missing.SelectRows = true
			if prepared {
				missing.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, peer, b.Recorder, missing)
			foreignCreate := fixture.SchemaReferenceCreationError("CREATE TABLE "+name+".ns18_retired_create(i INT)", "42P16", "cannot create relations in temporary schemas of other sessions", prepared, "14")
			fixture.RunStep(t, p.Ctx, peer, b.Recorder, foreignCreate)
		}
		deniedRead()
		pgNamespaceExec(t, p, b, peer, "CREATE TEMP TABLE ns18_authority_peer(i INT)")
		_, peerActor := pgNamespaceCurrentRole(t, p, b, peer)
		peerName, peerID, _, _ := pgNamespaceTempPair(t, p, b, peer, "ns18_authority_peer", peerActor)
		if peerID == id || peerName == name {
			t.Fatal("peer adopted retired namespace", peerID, id, peerName, name)
		}
		pgNamespaceRow(t, p, b, peer, name, id, 10, nil)
		pgNamespaceRow(t, p, b, peer, toast, toastID, 10, nil)
	})
}
