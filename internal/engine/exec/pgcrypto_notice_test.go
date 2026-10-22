package exec

import (
	"testing"

	"stuntgres/internal/sqlstate"
)

type pgcryptoNoticeUnitHost struct {
	Host
	notices []sqlstate.Error
}

func (h *pgcryptoNoticeUnitHost) PgcryptoNotice(n sqlstate.Error) { h.notices = append(h.notices, n) }

func TestPgcryptoNoticeCurrentHost(t *testing.T) {
	first, second := &pgcryptoNoticeUnitHost{}, &pgcryptoNoticeUnitHost{}
	c := &Context{EvalStatement: EvalStatement{Host: first}}
	n := sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "22003", Message: "rounds=10 is below supported value (1000), using 1000 instead"}
	c.FunctionPgcryptoNotice(n)
	c.Host = second
	c.FunctionPgcryptoNotice(n)
	if len(first.notices) != 1 || first.notices[0] != n || len(second.notices) != 1 || second.notices[0] != n {
		t.Fatal(first.notices, second.notices)
	}
	c.Host = nil
	c.FunctionPgcryptoNotice(n)
	var absent *Context
	absent.FunctionPgcryptoNotice(n)
}
