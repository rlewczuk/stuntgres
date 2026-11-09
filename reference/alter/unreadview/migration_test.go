package unreadview_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

type applicationView struct{ name, base, key, prefix string }

var applicationViews = []applicationView{
	{"template_with_users", "templates", "created_by", "created_by"},
	{"workspace_build_with_user", "workspace_builds", "initiator_id", "initiator_by"},
	{"template_version_with_user", "template_versions", "created_by", "created_by"},
}

func TestReferenceAlterTypeUnreadViewMigrationShape(t *testing.T) {
	for _, preexisting := range []bool{false, true} {
		name := "same_transaction"
		if preexisting {
			name = "preexisting"
		}
		t.Run(name, func(t *testing.T) {
			routes(t, func(s *scenario, mode string) {
				if preexisting {
					applicationSetup(s)
				}
				handles := map[string]*sql.Stmt{}
				if preexisting {
					handles = applicationHandles(s)
				}
				finish := s.begin(mode)
				if !preexisting {
					applicationSetup(s)
					handles = applicationHandles(s)
				}
				old := s.oid("user_status")
				applicationAssert(s, handles, false, false)
				applicationReplace(s)
				replacement := s.oid("user_status")
				if replacement == old {
					s.t.Fatal("migration did not replace enum identity")
				}
				finish(true)
				applicationAssert(s, handles, false, false)
				applicationWrite(s)
				applicationAssert(s, handles, true, false)
				applicationLinks(s)
				applicationDropRestriction(s)
			})
		})
	}
}

func TestReferenceAlterTypeUnreadViewFeatureSmoke(t *testing.T) {
	backends(t, func(s *scenario) {
		// Permanent public fixture/Open(database/sql) end-to-end smoke. This is
		// deliberately a real scenario, not an alias for a selector or a print.
		applicationSetup(s)
		handles := applicationHandles(s)
		applicationAssert(s, handles, false, false)
		finish := s.begin("go")
		applicationReplace(s)
		finish(true)
		applicationWrite(s)
		applicationAssert(s, handles, true, false)
		applicationLinks(s)
		applicationDropRestriction(s)
		if !s.b.PG {
			applicationRoundTrip(s)
		}
	})
}

func applicationSetup(s *scenario) {
	s.t.Helper()
	s.exec("CREATE TYPE user_status AS ENUM ('active','suspended')", 0)
	s.exec("CREATE TABLE users(id INT,username TEXT,avatar_url TEXT,status user_status DEFAULT 'active')", 0)
	s.exec("INSERT INTO users VALUES(1,'alice','/a','active'),(2,'bob',NULL,NULL)", 2)
	for _, view := range applicationViews {
		s.exec("CREATE TABLE "+view.base+"(id INT,"+view.key+" INT)", 0)
		s.exec("INSERT INTO "+view.base+" VALUES(10,1),(20,2),(30,99)", 3)
	}
	// The138/141 definitions preserve their actual base stars, LEFT JOIN,
	// COALESCE and aliases (including the original initiator_by_* spelling).
	s.exec("CREATE VIEW visible_users AS SELECT id,username,avatar_url FROM users", 0)
	for _, view := range applicationViews {
		s.exec("CREATE VIEW "+view.name+" AS SELECT "+view.base+".*,coalesce(visible_users.avatar_url,'') AS "+view.prefix+"_avatar_url,coalesce(visible_users.username,'') AS "+view.prefix+"_username FROM "+view.base+" LEFT JOIN visible_users ON "+view.base+"."+view.key+"=visible_users.id", 0)
	}
}

func applicationReplace(s *scenario) {
	s.t.Helper()
	s.exec("CREATE TYPE new_user_status AS ENUM ('active','suspended','dormant')", 0)
	replacement := s.oid("new_user_status")
	// Exactly the144-shaped action order, rather than a preemptive view rebuild
	// or a persistent initial-default workaround for the empty minimal case.
	s.exec("ALTER TABLE users ALTER COLUMN status DROP DEFAULT, ALTER COLUMN status TYPE new_user_status USING (status::text::new_user_status), ALTER COLUMN status SET DEFAULT 'active'::new_user_status", 0)
	s.exec("DROP TYPE user_status", 0)
	s.exec("ALTER TYPE new_user_status RENAME TO user_status", 0)
	if s.oid("user_status") != replacement {
		s.t.Fatal("migration rename changed type identity")
	}
}

func applicationHandles(s *scenario) map[string]*sql.Stmt {
	s.t.Helper()
	handles := map[string]*sql.Stmt{}
	handles["visible_users"] = s.prepare(s.conn, "SELECT * FROM visible_users ORDER BY id")
	for _, view := range applicationViews {
		handles[view.name] = s.prepare(s.conn, "SELECT * FROM "+view.name+" ORDER BY id")
	}
	return handles
}

func applicationWrite(s *scenario) {
	s.t.Helper()
	s.exec("INSERT INTO users(id,username,avatar_url) VALUES(3,'carol',NULL)", 1)
	s.exec("INSERT INTO users VALUES(4,'dana','/d','dormant')", 1)
	for _, view := range applicationViews {
		s.exec("INSERT INTO "+view.base+" VALUES(40,3),(50,4)", 2)
	}
}

func applicationAssert(s *scenario, handles map[string]*sql.Stmt, written, restoredWrite bool) {
	s.t.Helper()
	users := [][]any{{int64(1), "alice", "/a"}, {int64(2), "bob", nil}}
	statuses := [][]any{{int64(1), "active"}, {int64(2), nil}}
	joined := [][]any{{int64(10), int64(1), "/a", "alice"}, {int64(20), int64(2), "", "bob"}, {int64(30), int64(99), "", ""}}
	if written {
		users = append(users, []any{int64(3), "carol", nil}, []any{int64(4), "dana", "/d"})
		statuses = append(statuses, []any{int64(3), "active"}, []any{int64(4), "dormant"})
		joined = append(joined, []any{int64(40), int64(3), "", "carol"}, []any{int64(50), int64(4), "/d", "dana"})
	}
	if restoredWrite {
		users = append(users, []any{int64(5), "eve", nil}, []any{int64(6), "finn", "/f"})
		statuses = append(statuses, []any{int64(5), "active"}, []any{int64(6), "dormant"})
		joined = append(joined, []any{int64(60), int64(5), "", "eve"}, []any{int64(70), int64(6), "/f", "finn"})
	}
	visible := fixture.Select("SELECT * FROM visible_users ORDER BY id", []string{"id", "username", "avatar_url"}, []string{"INT4", "TEXT", "TEXT"}, users)
	s.run(visible)
	s.heldRead(handles["visible_users"], visible)
	for _, view := range applicationViews {
		step := fixture.Select("SELECT * FROM "+view.name+" ORDER BY id", []string{"id", view.key, view.prefix + "_avatar_url", view.prefix + "_username"}, []string{"INT4", "INT4", "TEXT", "TEXT"}, joined)
		s.run(step)
		s.heldRead(handles[view.name], step)
	}
	s.run(fixture.Select("SELECT id,status::text FROM users ORDER BY id", []string{"id", "status"}, []string{"INT4", "TEXT"}, statuses))
}

func applicationLinks(s *scenario) {
	s.t.Helper()
	if s.scalar("SELECT atttypid::BIGINT FROM pg_attribute WHERE attrelid='users'::regclass AND attname='status'") != s.oid("user_status") {
		s.t.Fatal("application status not current enum")
	}
	if s.scalar("SELECT atttypid::BIGINT FROM pg_attribute WHERE attrelid='visible_users'::regclass AND attname='id'") != 23 {
		s.t.Fatal("visible id lost INT4")
	}
	for _, view := range applicationViews {
		if s.scalar("SELECT count(*) FROM pg_depend d JOIN pg_rewrite r ON r.oid=d.objid JOIN pg_attribute a ON a.attrelid=d.refobjid AND a.attnum=d.refobjsubid WHERE d.classid='pg_rewrite'::regclass AND d.refclassid='pg_class'::regclass AND r.ev_class='"+view.name+"'::regclass AND d.refobjid='visible_users'::regclass AND a.attname='id'") < 1 {
			s.t.Fatal("missing application view dependency", view.name)
		}
	}
}

func applicationDropRestriction(s *scenario) {
	s.t.Helper()
	s.failure("DROP TABLE templates RESTRICT", "2BP01", "cannot drop table templates because other objects depend on it", "view template_with_users depends on table templates", dropHint)
}

func applicationRoundTrip(s *scenario) {
	s.t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &dump); err != nil {
		s.t.Fatal(err)
	}
	if err := stuntgres.Reset(s.p.Ctx, s.p.Native); err != nil {
		s.t.Fatal(err)
	}
	s.run(fixture.Select("SELECT count(*) FROM pg_class WHERE relnamespace='public'::regnamespace AND relname IN ('users','visible_users','template_with_users','workspace_build_with_user','template_version_with_user')", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
	if err := stuntgres.Restore(s.p.Ctx, s.p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		s.t.Fatal(err)
	}
	handles := applicationHandles(s)
	applicationAssert(s, handles, true, false)
	applicationLinks(s)
	applicationDropRestriction(s)
	s.exec("INSERT INTO users(id,username,avatar_url) VALUES(5,'eve',NULL)", 1)
	s.exec("INSERT INTO users VALUES(6,'finn','/f','dormant')", 1)
	for _, view := range applicationViews {
		s.exec("INSERT INTO "+view.base+" VALUES(60,5),(70,6)", 2)
	}
	applicationAssert(s, handles, true, true)
	applicationLinks(s)
}
