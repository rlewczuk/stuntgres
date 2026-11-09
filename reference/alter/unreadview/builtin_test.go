package unreadview_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAlterTypeUnreadViewBuiltinControl(t *testing.T) {
	routes(t, func(s *scenario, mode string) {
		s.exec("CREATE TABLE t(a INT,b INT)", 0)
		s.exec("INSERT INTO t VALUES(1,7)", 1)
		s.exec("CREATE VIEW v AS SELECT b FROM t", 0)
		view := fixture.Select("SELECT b FROM v", []string{"b"}, []string{"INT4"}, [][]any{{int64(7)}})
		held := s.prepare(s.conn, view.Query)
		s.run(view)
		s.heldRead(held, view)
		finish := s.begin(mode)
		s.exec("ALTER TABLE t ALTER a TYPE TEXT USING a", 0)
		finish(true)
		s.run(fixture.Select("SELECT a,b FROM t", []string{"a", "b"}, []string{"TEXT", "INT4"}, [][]any{{"1", int64(7)}}))
		s.run(view)
		s.heldRead(held, view)
	})
}
