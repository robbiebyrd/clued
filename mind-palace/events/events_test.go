package events

import "testing"

func TestBusFilter(t *testing.T) {
	b := New()
	all := b.Subscribe(Filter{}, 4)
	one := b.Subscribe(Filter{Kind: "plan", IDs: []string{"0001-aaa"}}, 4)
	stories := b.Subscribe(Filter{Kind: "story"}, 4)
	b.Publish(Event{Type: "plan.updated", Kind: "plan", ID: "0001-aaa"})
	b.Publish(Event{Type: "plan.updated", Kind: "plan", ID: "0002-bbb"})
	b.Publish(Event{Type: "story.updated", Kind: "story", ID: "0001-aaa"})
	if len(all.C) != 3 || len(one.C) != 1 || len(stories.C) != 1 {
		t.Errorf("all=%d one=%d stories=%d", len(all.C), len(one.C), len(stories.C))
	}
	one.Add("0002-bbb")
	b.Publish(Event{Type: "plan.deleted", Kind: "plan", ID: "0002-bbb"})
	if len(one.C) != 2 {
		t.Error("Add did not widen filter")
	}
	one.Remove("0002-bbb")
	b.Publish(Event{Type: "plan.deleted", Kind: "plan", ID: "0002-bbb"})
	if len(one.C) != 2 {
		t.Error("Remove did not narrow filter")
	}
	// Template events (no id) reach unfiltered subscriptions of the kind only.
	b.Publish(Event{Type: TemplateUpdated, Kind: "story", TemplateID: "default"})
	if len(stories.C) != 2 || len(one.C) != 2 || len(all.C) != 4 {
		t.Errorf("template event routing: all=%d one=%d stories=%d", len(all.C), len(one.C), len(stories.C))
	}
	// Full buffer drops instead of blocking.
	for i := 0; i < 10; i++ {
		b.Publish(Event{Type: "plan.updated", Kind: "plan", ID: "0001-aaa"})
	}
	one.Close()
	one.Close()
	for range one.C {
	}
	if b.Len() != 2 {
		t.Errorf("len = %d", b.Len())
	}
}
