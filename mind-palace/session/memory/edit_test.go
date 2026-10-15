package memory

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
)

func TestMarkdownRoundTrip(t *testing.T) {
	st := memsession.New()
	c := &clock{t: day1}
	svc := newService(st, c)
	pinned, _ := svc.Remember(ctx, project, "pinned", "Always run make check.", false)
	c.set(day1.Add(time.Minute))
	note, _ := svc.Remember(ctx, project, "note", "The daemon listens on 8085.", false)
	c.set(day1.Add(2 * time.Minute))
	gone, _ := svc.Remember(ctx, project, "handoff", "Forget me.", false)
	c.set(day1.Add(3 * time.Minute))
	private, _ := svc.Remember(ctx, project, "note", "my private reminder", true)

	md, err := svc.EditMarkdown(ctx, project)
	must(t, err)
	for _, want := range []string{"<!-- clued memory of " + project, `<!-- memory id="` + pinned.ID + `" kind="memory-pinned" edited -->` + "\nAlways run make check.", `kind="memory-note" private edited -->` + "\nmy private reminder"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown lacks %q:\n%s", want, md)
		}
	}
	// Unchanged Markdown changes nothing.
	rep, err := svc.ApplyMarkdown(ctx, project, md)
	must(t, err)
	if len(rep.Changed)+len(rep.Removed) != 0 || rep.Unchanged != 4 {
		t.Errorf("no-op apply = %+v", rep)
	}
	edited := strings.Replace(md, "The daemon listens on 8085.", "The daemon listens on 9090.", 1)
	edited = strings.Replace(edited, `<!-- memory id="`+gone.ID+`" kind="memory-handoff" edited -->`+"\nForget me.\n", "", 1)
	rep, err = svc.ApplyMarkdown(ctx, project, edited)
	must(t, err)
	if !reflect.DeepEqual(rep.Changed, []string{note.ID}) || !reflect.DeepEqual(rep.Removed, []string{gone.ID}) || rep.Unchanged != 2 {
		t.Errorf("apply = %+v", rep)
	}
	notes, _ := svc.Show(ctx, project, true)
	byID := map[string]Note{}
	for _, n := range notes {
		byID[n.ID] = n
	}
	if n := byID[note.ID]; n.Text != "The daemon listens on 9090." || !n.Edited {
		t.Errorf("changed note = %+v", n)
	}
	if _, left := byID[gone.ID]; left || len(notes) != 3 || !byID[private.ID].Private {
		t.Errorf("records after the edit = %+v", notes)
	}
	// An unknown id refuses the whole edit.
	bad := strings.Replace(edited, "9090", "7070", 1) + "\n<!-- memory id=\"memory-note/elsewhere/x\" kind=\"memory-note\" -->\nsneaky\n"
	if _, err := svc.ApplyMarkdown(ctx, project, bad); err == nil || !strings.Contains(err.Error(), "unknown memory id") {
		t.Errorf("unknown id: %v", err)
	}
	if n, _ := svc.Show(ctx, project, false, KindNote); len(n) != 1 || n[0].Text != "The daemon listens on 9090." {
		t.Errorf("a refused edit changed %+v", n)
	}
	if _, err := svc.ApplyMarkdown(ctx, project, strings.Replace(edited, "nothing is changed. -->\n", "nothing is changed. -->\nloose text\n", 1)); err == nil {
		t.Error("text outside a block must be refused")
	}
}

func TestRememberForgetDryRun(t *testing.T) {
	st := memsession.New()
	c := &clock{t: day1}
	svc := newService(st, c)
	a, _ := svc.Remember(ctx, project, "note", "deploy with make release", false)
	c.set(day1.Add(time.Minute))
	b, _ := svc.Remember(ctx, project, "note", "the staging box is flaky", false)
	c.set(day1.Add(2 * time.Minute))
	if _, err := svc.Remember(ctx, "/w/other", "note", "deploy elsewhere", false); err != nil {
		t.Fatal(err)
	}
	hit, err := svc.Forget(ctx, project, nil, "DEPLOY", true)
	must(t, err)
	if len(hit) != 1 || hit[0].ID != a.ID {
		t.Fatalf("dry run = %+v", hit)
	}
	if n, _ := svc.Show(ctx, project, false); len(n) != 2 {
		t.Errorf("a dry run deleted: %+v", n)
	}
	hit, err = svc.Forget(ctx, project, []string{b.ID}, "deploy", false)
	must(t, err)
	if len(hit) != 2 {
		t.Errorf("forget = %+v", hit)
	}
	if n, _ := svc.Show(ctx, project, true); len(n) != 0 {
		t.Errorf("left = %+v", n)
	}
	if n, _ := svc.Show(ctx, "/w/other", false); len(n) != 1 {
		t.Errorf("another project's note went: %+v", n)
	}
	if _, err := svc.Forget(ctx, project, nil, "", false); err == nil {
		t.Error("forget without ids or pattern must fail")
	}
	if _, err := svc.Forget(ctx, project, nil, "(", true); err == nil {
		t.Error("a bad pattern must fail")
	}
}

func TestExportSkipsPrivateImportDedupes(t *testing.T) {
	st := memsession.New()
	c := &clock{t: day1}
	svc := newService(st, c)
	addSession(t, st, account, "s1", project, "", day1)
	c.set(day1.AddDate(0, 0, 1))
	if _, err := svc.Run(ctx, project, &fakeEngine{}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Remember(ctx, project, "pinned", "public pinned", false); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Remember(ctx, project, "note", "PRIVATE-NOTE", true); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	must(t, svc.Export(ctx, &out, project))
	if strings.Contains(out.String(), "PRIVATE-NOTE") {
		t.Errorf("private record exported:\n%s", out.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 4 { // the note, the day, recent and pinned
		t.Fatalf("%d lines:\n%s", len(lines), out.String())
	}

	other := memsession.New()
	restored := &Service{Store: other, AccountID: "acc-2", Now: c.now}
	n, err := restored.Import(ctx, bytes.NewReader(out.Bytes()))
	must(t, err)
	if n != 4 {
		t.Errorf("imported %d, want 4", n)
	}
	events, _ := other.MemoryEvents(ctx, session.MemoryQuery{AccountID: "acc-2"})
	if len(events) != 4 || events[0]["created_at"] == nil {
		t.Errorf("restored = %v", events)
	}
	if n, _ := restored.Import(ctx, bytes.NewReader(out.Bytes())); n != 0 {
		t.Errorf("a second import stored %d", n)
	}
	if again, _ := other.MemoryEvents(ctx, session.MemoryQuery{AccountID: "acc-2"}); len(again) != 4 {
		t.Errorf("duplicates after a second import: %d", len(again))
	}
	if _, err := restored.Import(ctx, strings.NewReader(`{"hook_event_name":"Stop"}`+"\n")); err == nil {
		t.Error("a line that is no memory record must fail")
	}
	privateLine := `{"hook_event_name":"MemorySaved","memory":{"id":"memory-note/p/x","kind":"memory-note","text":"secret","hash":"h1","private":true}}`
	if n, err := restored.Import(ctx, strings.NewReader(privateLine+"\n")); err != nil || n != 0 {
		t.Errorf("a private record was imported: %d, %v", n, err)
	}
}
