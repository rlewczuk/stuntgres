package lock

import "stuntgres/internal/engine/ids"

// ActivityWait owns no ticket, queue, resource or callback. Capture is purely
// observational and must be entered without engine/session/transaction metadata.
type ActivityWait struct {
	Transaction ids.TxRef
	Event       string
}

func (m *Manager) ActivityWaits() map[ids.SessionToken]ActivityWait {
	m.mu.Lock()
	defer m.mu.Unlock()
	waits := make(map[ids.SessionToken]ActivityWait)
	for resource, entry := range m.entries {
		for _, request := range entry.pending {
			if !m.validInquiryRequest(request, entry, resource) {
				continue
			}
			ref := request.provenance
			if ref.Session().Private() != nil || ref.Session().ID() == 0 {
				continue
			}
			event := "relation"
			switch {
			case resource.Kind == AdvisoryBigintResource || resource.Kind == AdvisoryPairResource:
				event = "advisory"
			case resource.completion != nil:
				event = "transactionid"
				if request.policy.CompletionPurpose == CompletionVirtual {
					event = "virtualxid"
				}
			case resource.Row != 0:
				event = "tuple"
			case resource.Address.ClassID != ids.RelationClassID || resource.Name != (ids.NameKey{}):
				event = "object"
			}
			waits[ref.Session()] = ActivityWait{Transaction: ref, Event: event}
		}
	}
	return waits
}
