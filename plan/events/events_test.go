package events

import "testing"

func TestBusFilter(t *testing.T) {
	b := New()
	all := b.Subscribe(nil, 4)
	one := b.Subscribe([]string{"0001-aaa"}, 4)
	b.Publish(Event{Type: PlanUpdated, PlanID: "0001-aaa"})
	b.Publish(Event{Type: PlanUpdated, PlanID: "0002-bbb"})
	if len(all.C) != 2 || len(one.C) != 1 {
		t.Errorf("all=%d one=%d", len(all.C), len(one.C))
	}
	one.Add("0002-bbb")
	b.Publish(Event{Type: PlanDeleted, PlanID: "0002-bbb"})
	if len(one.C) != 2 {
		t.Error("Add did not widen filter")
	}
	one.Remove("0002-bbb")
	b.Publish(Event{Type: PlanDeleted, PlanID: "0002-bbb"})
	if len(one.C) != 2 {
		t.Error("Remove did not narrow filter")
	}
	// Full buffer drops instead of blocking.
	for i := 0; i < 10; i++ {
		b.Publish(Event{Type: PlanUpdated, PlanID: "0001-aaa"})
	}
	one.Close()
	one.Close()
	closed := false
	for range one.C {
	}
	closed = true
	if !closed {
		t.Error("closed subscription channel should be closed")
	}
	if b.Len() != 1 {
		t.Errorf("len = %d", b.Len())
	}
}
