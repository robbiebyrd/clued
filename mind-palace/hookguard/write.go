package hookguard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/robbiebyrd/clued/mind-palace/kind"
	"github.com/robbiebyrd/clued/mind-palace/markdown"
	"github.com/robbiebyrd/clued/mind-palace/model"
)

// WriteInput is the part of a PreToolUse hook's JSON input the write guard
// reads.
type WriteInput struct {
	ToolName string `json:"tool_name"`
	Cwd      string `json:"cwd"`
	// ToolInput is the tool's own parameters; the guard reads the file path,
	// the strings an edit replaces and the shell command.
	ToolInput json.RawMessage `json:"tool_input"`
}

// toolInput is the union of the tool parameters the guard looks at.
type toolInput struct {
	FilePath     string `json:"file_path"`
	NotebookPath string `json:"notebook_path"`
	OldString    string `json:"old_string"`
	Edits        []struct {
		OldString string `json:"old_string"`
	} `json:"edits"`
	Command string `json:"command"`
}

// Dirs tells the guard where the managed documents live. Paths are
// absolute; an empty one disables that rule.
type Dirs struct {
	// Plans and Stories are the file-store roots of each kind (archive and
	// templates subdirectories included).
	Plans   string
	Stories string
}

// of returns the directory configured for a kind.
func (d Dirs) of(k *kind.Kind) string {
	if k.Name == kind.Story {
		return d.Stories
	}
	return d.Plans
}

// shortDir spells dir relative to cwd when it lies inside it, for messages.
func shortDir(cwd, dir string) string {
	if cwd != "" {
		if rel, err := filepath.Rel(cwd, dir); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(dir)
}

// SuperpowersPlansDir is the directory the superpowers writing-plans skill
// writes to; it is blocked wherever it appears in a path, since mind-palace
// stores those plans instead.
const SuperpowersPlansDir = "docs/superpowers/plans"

// PreToolDecision is the JSON a PreToolUse hook prints: a denial with its
// reason, or extra context for the assistant when the call may proceed.
type PreToolDecision struct {
	HookSpecificOutput PreToolOutput `json:"hookSpecificOutput"`
}

// PreToolOutput is the hookSpecificOutput block of a PreToolUse hook.
type PreToolOutput struct {
	HookEventName            string `json:"hookEventName"`
	PermissionDecision       string `json:"permissionDecision,omitempty"`
	PermissionDecisionReason string `json:"permissionDecisionReason,omitempty"`
	AdditionalContext        string `json:"additionalContext,omitempty"`
}

// Denied reports whether the decision blocks the tool call.
func (d *PreToolDecision) Denied() bool {
	return d != nil && d.HookSpecificOutput.PermissionDecision == "deny"
}

func deny(reason string) *PreToolDecision {
	return &PreToolDecision{HookSpecificOutput: PreToolOutput{
		HookEventName: "PreToolUse", PermissionDecision: "deny", PermissionDecisionReason: reason,
	}}
}

func withContext(ctx string) *PreToolDecision {
	return &PreToolDecision{HookSpecificOutput: PreToolOutput{HookEventName: "PreToolUse", AdditionalContext: ctx}}
}

// Tool names the guard distinguishes.
const (
	toolWrite         = "Write"
	toolEdit          = "Edit"
	toolMultiEdit     = "MultiEdit"
	toolNotebookEdit  = "NotebookEdit"
	toolBash          = "Bash"
	toolEnterPlanMode = "EnterPlanMode"
	toolExitPlanMode  = "ExitPlanMode"
)

// planModeContext is added when Claude Code's own plan mode starts or ends,
// so the plan it produces lands in mind-palace rather than in a loose file.
const planModeContext = "mind-palace manages plans: fetch the layout with plan_getTemplate (or `mind-palace plan template get`) before writing the plan, and when it is final save it with plan_create (or `mind-palace plan create --input plan.json`) instead of writing a Markdown file. Break the work into stories with story_create and link them with story_addPlanLink."

// CheckWrite decides whether a tool call may touch the plans and stories
// mind-palace manages. It returns nil when the call is none of the guard's
// business, a denial naming the mind-palace tool to use instead, or extra
// context when the call proceeds but a mind-palace operation would have been
// the better route.
func CheckWrite(in WriteInput, dirs Dirs) *PreToolDecision {
	var ti toolInput
	if len(in.ToolInput) > 0 {
		_ = json.Unmarshal(in.ToolInput, &ti) // a tool_input that is not an object carries nothing to check
	}
	switch in.ToolName {
	case toolEnterPlanMode, toolExitPlanMode:
		return withContext(planModeContext)
	case toolWrite, toolEdit, toolMultiEdit, toolNotebookEdit:
		path := ti.FilePath
		if path == "" {
			path = ti.NotebookPath
		}
		if path == "" {
			return nil
		}
		return checkFileWrite(in.ToolName, in.Cwd, absPath(in.Cwd, path), &ti, dirs)
	case toolBash:
		return checkBash(ti.Command, in.Cwd, dirs)
	}
	return nil
}

// CheckWriteJSON parses the hook's JSON input and runs CheckWrite.
func CheckWriteJSON(input []byte, dirs Dirs) (*PreToolDecision, error) {
	var in WriteInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, err
	}
	return CheckWrite(in, dirs), nil
}

// target is what the guard found out about a path under a managed directory.
type target struct {
	kind *kind.Kind
	// rel is the path inside the kind's directory, slash-separated.
	rel string
}

func (t target) isTemplate() bool {
	return strings.HasPrefix(t.rel, "templates/")
}

// locate reports which managed directory, if any, contains path.
func locate(path string, dirs Dirs) (target, bool) {
	for _, c := range []struct {
		k   *kind.Kind
		dir string
	}{{kind.Must(kind.Plan), dirs.Plans}, {kind.Must(kind.Story), dirs.Stories}} {
		if c.dir == "" {
			continue
		}
		rel, err := filepath.Rel(c.dir, path)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		return target{kind: c.k, rel: filepath.ToSlash(rel)}, true
	}
	return target{}, false
}

func isSuperpowersPlan(path string) bool {
	p := filepath.ToSlash(path)
	return strings.Contains(p, "/"+SuperpowersPlansDir+"/") || strings.HasPrefix(p, SuperpowersPlansDir+"/")
}

func absPath(cwd, p string) string {
	p = filepath.Clean(filepath.FromSlash(p))
	if filepath.IsAbs(p) {
		return p
	}
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	return filepath.Join(cwd, p)
}

// mcp names the MCP tool and the CLI command of one operation of a kind.
func mcp(k *kind.Kind, op, cli string) string {
	return fmt.Sprintf("%s_%s (MCP) or `mind-palace %s %s`", k.Name, op, k.Name, cli)
}

func checkFileWrite(tool, cwd, path string, ti *toolInput, dirs Dirs) *PreToolDecision {
	if isSuperpowersPlan(path) {
		return deny("Plans are stored by mind-palace, not written to " + SuperpowersPlansDir + "/. Save a new plan with the plan_create MCP tool, or change an existing plan's content with plan_update.")
	}
	t, ok := locate(path, dirs)
	if !ok {
		return nil
	}
	k := t.kind
	dir := shortDir(cwd, dirs.of(k))
	if t.isTemplate() {
		return deny(fmt.Sprintf("%s templates are managed by mind-palace, not edited in %s/templates/. Create one with %s, or change it with %s.",
			k.Title, dir, mcp(k, "createTemplate", "template create <id> --content template.md"), mcp(k, "updateTemplate", "template update <id> --content template.md")))
	}
	_, statErr := os.Stat(path)
	exists := statErr == nil
	if exists && !model.FileNameRe.MatchString(filepath.Base(path)) {
		return nil // an existing file that is not a document (a README, say) is not managed
	}
	if !exists {
		return deny(fmt.Sprintf("%ss are created by mind-palace, not written as files under %s/. Fetch the layout with %s and the input shape with %s, then create it with %s.",
			k.Title, dir, mcp(k, "getTemplate", "template get"), mcp(k, "getSchema", "schema"), mcp(k, "create", "create --input "+k.Name+".json")))
	}
	id := documentID(path)
	switch tool {
	case toolWrite, toolNotebookEdit:
		return deny(fmt.Sprintf("Cannot overwrite %s %s directly. Replace its content with %s; change front matter through the setters (%s, %s, …); use the Edit tool for a change inside the body.",
			k.Name, id, mcp(k, "update", "update "+id+" --content body.md"), mcp(k, "setStatus", "set-status "+id+" <status>"), mcp(k, "setTitle", "set-title "+id+" <title>")))
	}
	olds := []string{ti.OldString}
	if tool == toolMultiEdit {
		olds = olds[:0]
		for _, e := range ti.Edits {
			olds = append(olds, e.OldString)
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return deny(fmt.Sprintf("Cannot read %s %s to check the edit (%v); change it through mind-palace instead: %s.", k.Name, id, err, mcp(k, "update", "update "+id+" --content body.md")))
	}
	doc := strings.ReplaceAll(string(raw), "\r\n", "\n")
	for _, old := range olds {
		if reason := protectedEdit(k, id, doc, strings.ReplaceAll(old, "\r\n", "\n")); reason != "" {
			return deny(reason)
		}
	}
	return withContext(fmt.Sprintf("Editing the body of %s %s. Front matter changes go through mind-palace setters (%s); %s replaces the whole content and keeps progress keys consistent.",
		k.Name, id, mcp(k, "setStatus", "set-status "+id+" <status>"), mcp(k, "update", "update "+id+" --content body.md")))
}

// protectedEdit returns the reason an Edit replacing old in doc must be
// refused: old is empty (the edit could land anywhere), or an occurrence of
// it overlaps the front matter or content the service owns (the H1, and for
// kinds with them the acceptance criteria and work log sections).
func protectedEdit(k *kind.Kind, id, doc, old string) string {
	if old == "" {
		return fmt.Sprintf("An edit of %s %s needs a non-empty old_string so the guard can tell front matter from body.", k.Name, id)
	}
	doc = strings.TrimPrefix(doc, "\xEF\xBB\xBF")
	_, content, err := markdown.Split(doc)
	if err != nil {
		return fmt.Sprintf("%s %s is not a valid mind-palace document (%v); repair it with %s.", k.Title, id, err, mcp(k, "update", "update "+id+" --content body.md"))
	}
	contentStart := len(doc) - len(content) // content is a suffix of doc
	ranges := protectedRanges(k, content, contentStart)
	for p := strings.Index(doc, old); p >= 0; {
		if p < contentStart {
			return fmt.Sprintf("Cannot edit the front matter of %s %s directly: status transitions follow the workflow and the managed fields (%s) are immutable. Use %s (see %s for the allowed moves), %s, %s, %s, or the link and progress tools (%s_addPlanLink, %s_setProgress, …).",
				k.Name, id, strings.Join(k.Immutable, ", "),
				mcp(k, "setStatus", "set-status "+id+" <status>"), mcp(k, "getTransitions", "transitions "+id),
				mcp(k, "setTitle", "set-title "+id+" <title>"), mcp(k, "setPriority", "set-priority "+id+" <priority>"), mcp(k, "setType", "set-type "+id+" <type>"),
				k.Name, k.Name)
		}
		for _, r := range ranges {
			if p < r.end && p+len(old) > r.start {
				return r.reason
			}
		}
		next := strings.Index(doc[p+1:], old)
		if next < 0 {
			break
		}
		p += 1 + next
	}
	return ""
}

// byteRange is a half-open span of doc the service owns and the denial it
// carries.
type byteRange struct {
	start, end int
	reason     string
}

// protectedRanges locates, as offsets into the whole document, the H1 line
// and the sections the service edits for this kind.
func protectedRanges(k *kind.Kind, content string, base int) []byteRange {
	lines := markdown.Lines(content)
	offsets := make([]int, len(lines)+1)
	for i, l := range lines {
		offsets[i+1] = offsets[i] + len(l.Text) + 1
	}
	span := func(s markdown.Section) (int, int) { return base + offsets[s.Start], base + offsets[s.End] }
	var out []byteRange
	for i, l := range lines {
		if l.InFence || strings.TrimSpace(l.Text) == "" {
			continue
		}
		if strings.HasPrefix(l.Text, "# ") {
			out = append(out, byteRange{base + offsets[i], base + offsets[i+1], fmt.Sprintf("The H1 of a %s is its title; change it with %s so the front matter stays in step.", k.Name, mcp(k, "setTitle", "set-title <id> <title>"))})
		}
		break
	}
	if k.CriteriaGate {
		if s, ok := markdown.FindSection(content, markdown.CriteriaHeading); ok {
			start, end := span(s)
			out = append(out, byteRange{start, end, fmt.Sprintf("The acceptance criteria of a %s are managed by mind-palace (they gate completion). Use %s to check one, %s to add one and %s to remove one.",
				k.Name, mcp(k, "setCriterion", "check <id> <position> [done|open|not_applicable]"), mcp(k, "addCriterion", "add-criterion <id> <text>"), mcp(k, "removeCriterion", "remove-criterion <id> <position>"))})
		}
	}
	if k.WorkLog {
		if s, ok := markdown.FindSection(content, markdown.WorkLogHeading); ok {
			start, end := span(s)
			out = append(out, byteRange{start, end, fmt.Sprintf("The work log of a %s is append-only and timestamped by mind-palace. Add an entry with %s.", k.Name, mcp(k, "appendWorkLog", "log <id> \"<entry>\""))})
		}
	}
	return out
}

// documentID returns the id encoded in a document's file name, or the file
// name when it has none.
func documentID(path string) string {
	name := filepath.Base(path)
	if m := model.FileNameRe.FindStringSubmatch(name); m != nil {
		return m[1]
	}
	return name
}

// shellWriteRe matches the commands that create, change, move or remove
// files (sed and perl only when editing in place); redirections are checked
// by their target instead.
var shellWriteRe = regexp.MustCompile(`(?:^|[\s;&|(])(?:(?:rm|mv|cp|tee|touch|truncate|dd|install|rsync|ln|shred|unlink|rmdir|mkdir|chmod|chown|git\s+(?:rm|mv|clean))(?:\s|$)|sed(?:\s+[^\s;&|]+)*?\s+-(?:[a-zA-Z]*i\S*|-in-place\S*)(?:\s|$)|perl(?:\s+[^\s;&|]+)*?\s+-[a-zA-Z0-9]*i\S*(?:\s|$))`)

// redirectRe captures the target of a > or >> redirection.
var redirectRe = regexp.MustCompile(`>>?\s*["']?([^\s"';&|<>]+)`)

// mindPalaceRe matches an invocation of the binary that manages the documents.
var mindPalaceRe = regexp.MustCompile(`(?:^|[\s;&|(/])(?:mind-palace|clued)(?:\s|$)`)

// closesStoryRe matches a commit message that closes a story by id.
var closesStoryRe = regexp.MustCompile(`(?i)\b(?:closes?|closed|completes?|completed|fix|fixes|fixed|resolves?|resolved)(?::\s*|\s+)#?([0-9]{4}-[a-z0-9]{3})\b`)

var gitCommitRe = regexp.MustCompile(`(?:^|[\s;&|(])git\s+(?:[^;&|]*?\s)?commit(?:\s|$)`)

func checkBash(cmd, cwd string, dirs Dirs) *PreToolDecision {
	if strings.TrimSpace(cmd) == "" {
		return nil
	}
	if gitCommitRe.MatchString(cmd) {
		if d := checkStoryCommit(cmd, dirs); d != nil {
			return d
		}
	}
	// A redirection into a managed directory bypasses the service even when
	// the command also runs mind-palace, so it is checked first.
	for _, m := range redirectRe.FindAllStringSubmatch(cmd, -1) {
		p := absPath(cwd, m[1])
		if isSuperpowersPlan(p) {
			return deny("Plans are stored by mind-palace, not written to " + SuperpowersPlansDir + "/. Save a new plan with the plan_create MCP tool, or change an existing plan's content with plan_update.")
		}
		if t, ok := locate(p, dirs); ok {
			return deny(fmt.Sprintf("Cannot write into %s/ from the shell. Create a %s with %s and change one with %s, %s or the other %s_* tools.",
				shortDir(cwd, dirs.of(t.kind)), t.kind.Name, mcp(t.kind, "create", "create --input "+t.kind.Name+".json"), mcp(t.kind, "update", "update <id> --content body.md"), mcp(t.kind, "setStatus", "set-status <id> <status>"), t.kind.Name))
		}
	}
	if !shellWriteRe.MatchString(cmd) || mindPalaceRe.MatchString(cmd) {
		return nil
	}
	if mentionsDir(cmd, cwd, SuperpowersPlansDir) {
		return deny("Plans are stored by mind-palace, not kept under " + SuperpowersPlansDir + "/. Save a new plan with the plan_create MCP tool, or change an existing plan's content with plan_update.")
	}
	for _, c := range []struct {
		k   *kind.Kind
		dir string
	}{{kind.Must(kind.Plan), dirs.Plans}, {kind.Must(kind.Story), dirs.Stories}} {
		if c.dir != "" && mentionsDir(cmd, cwd, c.dir) {
			return deny(fmt.Sprintf("Cannot change files under %s/ from the shell. Use %s for the content, %s for the workflow, %s to archive and %s to remove a %s; the other %s_* tools cover links, progress and criteria.",
				shortDir(cwd, c.dir), mcp(c.k, "update", "update <id> --content body.md"), mcp(c.k, "setStatus", "set-status <id> <status>"), mcp(c.k, "setStatus", "set-status <id> archived"), mcp(c.k, "delete", "delete <id>"), c.k.Name, c.k.Name))
		}
	}
	return nil
}

// mentionsDir reports whether cmd names dir (absolute, or relative to cwd)
// as a whole path component.
func mentionsDir(cmd, cwd, dir string) bool {
	candidates := []string{filepath.ToSlash(dir)}
	if cwd != "" && filepath.IsAbs(dir) {
		if rel, err := filepath.Rel(cwd, dir); err == nil && !strings.HasPrefix(rel, "..") {
			candidates = append(candidates, filepath.ToSlash(rel), "./"+filepath.ToSlash(rel))
		}
	}
	for _, c := range candidates {
		if c == "" || c == "." {
			continue
		}
		re := regexp.MustCompile(`(?:^|[\s"'=:(/])` + regexp.QuoteMeta(c) + `(?:/|\s|"|'|$)`)
		if re.MatchString(cmd) {
			return true
		}
	}
	return false
}

// checkStoryCommit refuses a commit whose message closes a story that is
// not complete, so the story's criteria gate cannot be skipped by a commit
// message.
func checkStoryCommit(cmd string, dirs Dirs) *PreToolDecision {
	if dirs.Stories == "" {
		return nil
	}
	k := kind.Must(kind.Story)
	for _, m := range closesStoryRe.FindAllStringSubmatch(cmd, -1) {
		id := m[1]
		status, ok := storyStatus(dirs.Stories, id)
		if !ok {
			continue
		}
		if status == "complete" || status == "archived" {
			continue
		}
		return deny(fmt.Sprintf("Story %s is %s, not complete, so the commit cannot close it. Check its criteria with %s, finish its steps with %s, then close it with %s; or reference it without a closing keyword.",
			id, status, mcp(k, "getCriteria", "criteria "+id), mcp(k, "setProgress", "set-progress "+id+" <step> complete"), mcp(k, "setStatus", "set-status "+id+" complete")))
	}
	return nil
}

// storyStatus reads the status of the story with the given id from the
// stories directory or its archive.
func storyStatus(dir, id string) (string, bool) {
	for _, d := range []string{dir, filepath.Join(dir, "archive")} {
		matches, _ := filepath.Glob(filepath.Join(d, id+"-*.md"))
		for _, p := range matches {
			raw, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			fm, _, err := markdown.Parse(string(raw))
			if err != nil || fm.ID != id {
				continue
			}
			return fm.Status, true
		}
	}
	return "", false
}
