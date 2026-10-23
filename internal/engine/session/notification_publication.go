package session

import "maps"

type notificationCapture struct {
	resource    *NotificationListener
	original    *NotificationState
	busy, ended bool
	next        *NotificationState
}

// NotificationPublication is one transaction's notification fanout: the
// listeners captured under Engine.mu (CaptureLocked), their immutable
// replacement states prepared without the lock (Prepare), and a replacement
// registry when the sender's registration changes. The publisher checks it
// with UnchangedLocked and installs it with InstallLocked under the same
// cluster-shared Engine.mu window as rows, catalog and the queue head.
type NotificationPublication struct {
	captured []notificationCapture
	registry NotificationRegistry
	count    int
	own      *NotificationListener
}

// CaptureLocked records every registered listener and, when nonnil, the local
// sender's own listener, with immutable state roots and busy/ended flags.
func (r *NotificationRegistry) CaptureLocked(p *NotificationPublication, own *NotificationListener) {
	p.own = own
	p.count = len(*r)
	capacity := p.count
	if own != nil && (*r)[own.Session.SessionToken] != own {
		capacity++
	}
	p.captured = make([]notificationCapture, 0, capacity)
	ownFound := false
	for _, n := range *r {
		p.captured = append(p.captured, notificationCapture{resource: n, original: n.State, busy: n.Busy, ended: n.Ended})
		ownFound = ownFound || n == own
	}
	if own != nil && !ownFound {
		p.captured = append(p.captured, notificationCapture{resource: own, original: own.State, busy: own.Busy, ended: own.Ended})
	}
}

// Prepare computes immutable replacements without Engine.mu. start is the
// cluster head before this batch. Every busy registered listener retains that
// boundary; local gates payload links and delivery, not global accounting.
// Sender actions change only the local registry.
func (p *NotificationPublication) Prepare(actions []NotificationAction, events []Notification, start NotificationQueuePosition, local bool) {
	own := p.own
	batch := events[:len(events):len(events)]
	for i := range p.captured {
		c := &p.captured[i]
		if c.ended {
			continue
		}
		var next NotificationState
		if c.original != nil {
			next = *c.original
		}
		wasRegistered := len(next.Channels) != 0
		changed := c.resource == own && len(actions) != 0
		if c.resource == own && len(actions) != 0 {
			next.Channels = maps.Clone(next.Channels)
			for _, action := range actions {
				if action.Listen {
					if next.Channels == nil {
						next.Channels = make(map[string]struct{})
					}
					next.Channels[action.Channel] = struct{}{}
					next.History = true
				} else if action.All {
					clear(next.Channels)
				} else {
					delete(next.Channels, action.Channel)
				}
			}
			if len(next.Channels) == 0 {
				next.Pending = nil
				next.Unread = false
				next.Position = NotificationQueuePosition{}
			}
			if wasRegistered != (len(next.Channels) != 0) {
				p.registry = make(NotificationRegistry, p.count+1)
				for _, entry := range p.captured {
					if entry.resource != own && !entry.ended && entry.original != nil && len(entry.original.Channels) != 0 {
						p.registry[entry.resource.Session.SessionToken] = entry.resource
					}
				}
				if len(next.Channels) != 0 {
					p.registry[own.Session.SessionToken] = own
				}
			}
		}
		if len(batch) != 0 && len(next.Channels) != 0 {
			if c.busy {
				if !next.Unread {
					next.Unread = true
					next.Position = start
				}
				if local {
					next.Pending = &NotificationLink{Batch: batch, Next: next.Pending}
				}
				changed = true
			} else {
				if local {
					ready := next.Ready.Append(FilterNotificationBatch(batch, next.Channels))
					changed = changed || ready != next.Ready
					next.Ready = ready
				}
			}
		}
		if changed {
			c.next = new(NotificationState)
			*c.next = next
		}
	}
}

// UnchangedLocked reports whether every listener p captured still has its
// captured state, busy and ended flags and registration, and the registration
// count is the captured one. A nil publication is unchanged.
func (r *NotificationRegistry) UnchangedLocked(p *NotificationPublication) bool {
	if p == nil {
		return true
	}
	if len(*r) != p.count {
		return false
	}
	for _, c := range p.captured {
		n := c.resource
		if n.State != c.original || n.Busy != c.busy || n.Ended != c.ended {
			return false
		}
		registered := c.original != nil && len(c.original.Channels) != 0 && !c.ended
		if ((*r)[n.Session.SessionToken] == n) != registered {
			return false
		}
	}
	return true
}

// InstallLocked installs p's replacement registry, if any, and every changed
// listener state, marking the listener used and waking its waiter. A nil
// publication installs nothing.
func (r *NotificationRegistry) InstallLocked(p *NotificationPublication) {
	if p == nil {
		return
	}
	if p.registry != nil {
		*r = p.registry
	}
	for _, c := range p.captured {
		if c.next != nil {
			c.resource.State = c.next
			c.resource.Used.Store(true)
			c.resource.SignalLocked()
		}
	}
}
