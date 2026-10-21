package session

// Each immutable link belongs to one recipient; event storage is shared by all
// recipients of a publication. Back lists are newest-first, front lists FIFO.
type NotificationLink struct {
	Batch []Notification
	Next  *NotificationLink
}

// NotificationFIFO is a persistent queue of notification batches: Append
// pushes onto Back, Normalize moves Back into Front when Front is empty, and
// Offset indexes the next event of Front's batch.
type NotificationFIFO struct {
	Front, Back *NotificationLink
	Offset      int
}

func (q NotificationFIFO) Append(batch []Notification) NotificationFIFO {
	if len(batch) != 0 {
		q.Back = &NotificationLink{Batch: batch, Next: q.Back}
	}
	return q
}

// ReverseNotificationLinks returns a reversed copy of the list at p.
func ReverseNotificationLinks(p *NotificationLink) *NotificationLink {
	var result *NotificationLink
	for ; p != nil; p = p.Next {
		result = &NotificationLink{Batch: p.Batch, Next: result}
	}
	return result
}
func (q NotificationFIFO) Normalize() NotificationFIFO {
	if q.Front == nil && q.Back != nil {
		if q.Back.Next == nil {
			q.Front = q.Back
		} else {
			q.Front = ReverseNotificationLinks(q.Back)
		}
		q.Back = nil
		q.Offset = 0
	}
	return q
}

// NotificationState is one immutable listener state: its channels, whether it
// has ever listened, batches held until its idle boundary (Pending,
// newest-first) and client-ready events.
type NotificationState struct {
	Channels map[string]struct{}
	History  bool
	Pending  *NotificationLink
	Ready    NotificationFIFO
}

// AppendNotificationEvents appends the events of added whose channel and
// payload are not already in base.
func AppendNotificationEvents(base, added []Notification) []Notification {
	if len(base) == 0 && len(added) <= 1 {
		return added
	}
	for _, event := range added {
		duplicate := false
		for _, old := range base {
			if old.Channel == event.Channel && old.Payload == event.Payload {
				duplicate = true
				break
			}
		}
		if !duplicate {
			base = append(base, event)
		}
	}
	return base
}

// FilterNotificationBatch keeps the events on channels; it returns batch itself
// when all match and nil when none do.
func FilterNotificationBatch(batch []Notification, channels map[string]struct{}) []Notification {
	count := 0
	for _, event := range batch {
		if _, ok := channels[event.Channel]; ok {
			count++
		}
	}
	if count == len(batch) {
		return batch
	}
	if count == 0 {
		return nil
	}
	filtered := make([]Notification, 0, count)
	for _, event := range batch {
		if _, ok := channels[event.Channel]; ok {
			filtered = append(filtered, event)
		}
	}
	return filtered
}

// ScanNotificationPending appends the pending batches, oldest first and
// filtered by channels, to ready.
func ScanNotificationPending(ready NotificationFIFO, pending *NotificationLink, channels map[string]struct{}) NotificationFIFO {
	if pending == nil {
		return ready
	}
	if pending.Next == nil {
		return ready.Append(FilterNotificationBatch(pending.Batch, channels))
	}
	count := 0
	for p := pending; p != nil; p = p.Next {
		count++
	}
	order := make([]*NotificationLink, 0, count)
	for p := pending; p != nil; p = p.Next {
		order = append(order, p)
	}
	for i := len(order) - 1; i >= 0; i-- {
		ready = ready.Append(FilterNotificationBatch(order[i].Batch, channels))
	}
	return ready
}
