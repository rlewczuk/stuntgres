package session

const NotificationQueuePages uint64 = 1048576
const notificationPageBytes uint32 = 8192

// NotificationQueuePosition is a global layout boundary. Published positions
// and listener states are immutable; their owning cluster mutex guards roots.
type NotificationQueuePosition struct {
	Page   uint64
	Offset uint32
}

func (p NotificationQueuePosition) Before(other NotificationQueuePosition) bool {
	return p.Page < other.Page || p.Page == other.Page && p.Offset < other.Offset
}

// Advance packs one deduplicated transaction batch without encoding or copying
// strings. Entries never straddle pages; a remainder below the minimum aligned
// entry size is immediately consumed.
func (p NotificationQueuePosition) Advance(events []Notification) NotificationQueuePosition {
	for _, event := range events {
		size := uint32((16 + len(event.Channel) + 1 + len(event.Payload) + 1 + 3) &^ 3)
		if size > notificationPageBytes-p.Offset {
			p.Page++
			p.Offset = 0
		}
		p.Offset += size
		if notificationPageBytes-p.Offset < 20 {
			p.Page++
			p.Offset = 0
		}
	}
	return p
}

// OldestPrepared includes unchanged states: action-only publications must not
// release another busy listener's hold. No client-ready queue participates.
func (p *NotificationPublication) OldestPrepared(tail NotificationQueuePosition) NotificationQueuePosition {
	for _, c := range p.captured {
		state := c.next
		if state == nil {
			state = c.original
		}
		if !c.ended && state != nil && len(state.Channels) != 0 && state.Unread && state.Position.Before(tail) {
			tail = state.Position
		}
	}
	return tail
}
