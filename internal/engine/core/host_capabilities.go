package core

import "stuntgres/internal/engine/exec"

// The exec.Host capability groups. Like statementHost, each is a defined type
// over StatementScope, so an accessor is a free pointer conversion: the
// one-word interface it returns never allocates. A group's methods leave
// statementHost; code shared with other groups stays on the scope.
type (
	contribHost            StatementScope
	textSearchHost         StatementScope
	catalogInquiryHost     StatementScope
	sessionInquiryHost     StatementScope
	transactionInquiryHost StatementScope
)

var (
	_ exec.ContribRuntime            = (*contribHost)(nil)
	_ exec.TextSearchRuntime         = (*textSearchHost)(nil)
	_ exec.CatalogInquiryRuntime     = (*catalogInquiryHost)(nil)
	_ exec.SessionInquiryRuntime     = (*sessionInquiryHost)(nil)
	_ exec.TransactionInquiryRuntime = (*transactionInquiryHost)(nil)
)

func (h *statementHost) Contrib() exec.ContribRuntime       { return (*contribHost)(h) }
func (h *statementHost) TextSearch() exec.TextSearchRuntime { return (*textSearchHost)(h) }
func (h *statementHost) CatalogInquiry() exec.CatalogInquiryRuntime {
	return (*catalogInquiryHost)(h)
}
func (h *statementHost) SessionInquiry() exec.SessionInquiryRuntime {
	return (*sessionInquiryHost)(h)
}
func (h *statementHost) TransactionInquiry() exec.TransactionInquiryRuntime {
	return (*transactionInquiryHost)(h)
}

func (h *catalogInquiryHost) scope() *StatementScope     { return (*StatementScope)(h) }
func (h *sessionInquiryHost) scope() *StatementScope     { return (*StatementScope)(h) }
func (h *transactionInquiryHost) scope() *StatementScope { return (*StatementScope)(h) }

// host is the statement host behind a group, for the shared host methods
// (environments, notices) a group body calls.
func (h *contribHost) host() *statementHost    { return (*statementHost)(h) }
func (h *textSearchHost) host() *statementHost { return (*statementHost)(h) }
func (h *catalogInquiryHost) host() *statementHost {
	return (*statementHost)(h)
}
