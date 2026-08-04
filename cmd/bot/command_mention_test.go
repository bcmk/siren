package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	texttemplate "text/template"
	"text/template/parse"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/bcmk/siren/v4/internal/db"
	"github.com/bcmk/siren/v4/lib/cmdlib"
)

// TestCommandRendersPerMention pins the mechanism: one parse, and command rebound per render,
// so the same translation names the bot only when the chat calls for it.
func TestCommandRendersPerMention(t *testing.T) {
	t.Parallel()
	base := filepath.Join("..", "..", "res", "translations")
	files := map[string][]string{"cb": {
		filepath.Join(base, "common.en.yaml"),
		filepath.Join(base, "chaturbate.en.yaml"),
	}}
	_, tpl := cmdlib.LoadAllTranslations(files)
	params := &renderParams{templates: tpl["cb"], key: "syntax_add"}
	got, want := params.render(""), "Specify a model, e.g., <code>/add alica_webcam</code>"
	if got != want {
		t.Errorf("without a mention = %q, want %q", got, want)
	}
	got, want = params.render("@sirenbot"), "Specify a model, e.g., <code>/add@sirenbot alica_webcam</code>"
	if got != want {
		t.Errorf("with a mention = %q, want %q", got, want)
	}
}

// unaddressedKeys never reach a chat as a message, so a command named in one can never work.
// Add a key here when you render one with no chat.
// One that slips the list is not silent: the render fails, naming the command.
// Some render for an API that takes no chat: the command menu, an inline button label,
// the invoice fields, a pre-checkout answer.
// The search keys are not rendered at all, but read as raw Str into the web app,
// where a mention would reach the user verbatim.
var unaddressedKeys = map[string]bool{
	"raw_commands":                 true,
	"buy_subs_package_button":      true,
	"buy_subs_invoice_title":       true,
	"buy_subs_invoice_description": true,
	"buy_subs_invoice_label":       true,
	"buy_invoice_expired":          true,
	"affiliate_command":            true,
	"search_button":                true,
	"search_header":                true,
	"search_placeholder":           true,
	"search_no_results":            true,
	"search_failed":                true,
	"search_failed_to_add":         true,
	"timezone_button":              true,
	"timezone_app_header":          true,
	"timezone_app_current":         true,
	"timezone_app_detected":        true,
	"timezone_app_placeholder":     true,
	"timezone_app_no_results":      true,
	"timezone_app_failed":          true,
}

// TestTranslationsMarkEveryCommand guards the one gap of the explicit form:
// a command written bare renders bare, which a channel drops
// and every bot in a group answers.
// Command calls are read off the parse tree, so every spelling the engine accepts is seen.
func TestTranslationsMarkEveryCommand(t *testing.T) {
	t.Parallel()
	dir := filepath.Join("..", "..", "res", "translations")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("cannot read translations: %v", err)
	}
	names := make([]string, 0, len(knownCommands))
	for name := range knownCommands {
		names = append(names, regexp.QuoteMeta(name))
	}
	// A map range is unordered, and a guard must not vary between runs.
	sort.Strings(names)
	// A slash never appears inside a call, so any of these is a bare mention.
	// Only < is excluded, for </code>: a URL is already blocked by the word character
	// before its slash, and excluding . would miss a command right after a full stop.
	bare := regexp.MustCompile(`(^|[^<\w])/(` + strings.Join(names, "|") + `)\b`)
	// A listing that bolds a command name writes no slash and no bot name,
	// which a channel drops and a group hands to every bot. The bare regexp cannot see it,
	// there being no slash to match.
	bold := regexp.MustCompile(`<b>(` + strings.Join(names, "|") + `)</b>`)
	topLevelKey := regexp.MustCompile(`^([a-z0-9_]+):`)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		// LoadAds is the one loader that takes a lone file without demanding completeness.
		for key, tr := range cmdlib.LoadAds([]string{path}) {
			names, problems := commandCalls(tr.Str)
			for _, problem := range problems {
				t.Errorf("%s: %s: %s", e.Name(), key, problem)
			}
			for _, name := range names {
				if _, ok := knownCommands[name]; !ok {
					t.Errorf("%s: %s names unknown command %q", e.Name(), key, name)
				}
				if unaddressedKeys[key] {
					t.Errorf("%s: %s never reaches a chat: do not name /%s here", e.Name(), key, name)
				}
			}
		}
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("cannot read %s: %v", e.Name(), err)
		}
		key := ""
		for i, line := range strings.Split(string(body), "\n") {
			if k := topLevelKey.FindStringSubmatch(line); k != nil {
				key = k[1]
			}
			if unaddressedKeys[key] {
				// The tree walk above already rejects a call here, so only a bare command is left.
				if m := bare.FindStringSubmatch(line); m != nil {
					t.Errorf("%s:%d %s never reaches a chat: do not name /%s here", e.Name(), i+1, key, m[2])
				}
				continue
			}
			if m := bare.FindStringSubmatch(line); m != nil {
				t.Errorf(`%s:%d writes /%s bare; use {{ command "%s" }}`, e.Name(), i+1, m[2], m[2])
			}
			if m := bold.FindStringSubmatch(line); m != nil {
				t.Errorf(`%s:%d bolds %s as a command; use {{ short_command "%s" }}`, e.Name(), i+1, m[1], m[1])
			}
		}
	}
}

// commandCalls returns every command name a template's parse tree calls for,
// however the action is spelled: quoting, spacing, folded lines, nesting and define blocks
// are the engine's business, not a regexp's. Each call it cannot vouch for,
// and a text that does not parse, comes back as a problem for the caller to report.
func commandCalls(text string) (names, problems []string) {
	tpl, err := texttemplate.New("t").Funcs(cmdlib.TemplateFuncs()).Parse(text)
	if err != nil {
		return nil, []string{fmt.Sprintf("does not parse: %v", err)}
	}
	var walk func(parse.Node)
	var walkPipe func(*parse.PipeNode)
	walkPipe = func(p *parse.PipeNode) {
		if p == nil {
			return
		}
		for _, cmd := range p.Cmds {
			if id, ok := cmd.Args[0].(*parse.IdentifierNode); ok &&
				(id.Ident == "command" || id.Ident == "short_command") {
				if len(cmd.Args) == 2 {
					if lit, ok := cmd.Args[1].(*parse.StringNode); ok {
						names = append(names, lit.Text)
						continue
					}
				}
				problems = append(problems, id.Ident+" takes one quoted name")
				continue
			}
			for _, arg := range cmd.Args {
				if pipe, ok := arg.(*parse.PipeNode); ok {
					walkPipe(pipe)
				}
			}
		}
	}
	walk = func(n parse.Node) {
		switch x := n.(type) {
		case *parse.ListNode:
			if x != nil {
				for _, child := range x.Nodes {
					walk(child)
				}
			}
		case *parse.ActionNode:
			walkPipe(x.Pipe)
		case *parse.IfNode:
			walkPipe(x.Pipe)
			walk(x.List)
			walk(x.ElseList)
		case *parse.RangeNode:
			walkPipe(x.Pipe)
			walk(x.List)
			walk(x.ElseList)
		case *parse.WithNode:
			walkPipe(x.Pipe)
			walk(x.List)
			walk(x.ElseList)
		case *parse.TemplateNode:
			walkPipe(x.Pipe)
		}
	}
	// A define block is its own tree in the set, so walk every associated one.
	for _, assoc := range tpl.Templates() {
		if assoc.Tree != nil {
			walk(assoc.Root)
		}
	}
	// The set's order is a map's, so pin the names for a caller that compares.
	sort.Strings(names)
	return names, problems
}

// TestCommandWalkSeesEveryForm pins the extraction to the engine, not to a spelling:
// whatever parses as a call is seen, however it is quoted, spaced, folded or nested,
// and a call the walk cannot vouch for is a problem, not a panic.
func TestCommandWalkSeesEveryForm(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		form     string
		want     []string
		problems bool
	}{
		{"canonical", `{{ command "add" }}`, []string{"add"}, false},
		{"no spaces", `{{command "add"}}`, []string{"add"}, false},
		{"extra spaces", `{{  command  "add"  }}`, []string{"add"}, false},
		{"both trims", `{{- command "add" -}}`, []string{"add"}, false},
		{"backticks", "{{ command `add` }}", []string{"add"}, false},
		{"uppercase passes to validation", `{{ command "ADD" }}`, []string{"ADD"}, false},
		{"nested in print", `{{ print (command "add") }}`, []string{"add"}, false},
		{"split across lines", "{{ command\n\"add\" }}", []string{"add"}, false},
		{"inside a branch", `{{ if . }}{{ command "add" }}{{ end }}`, []string{"add"}, false},
		{"inside a define", `{{ define "d" }}{{ command "add" }}{{ end }}{{ template "d" }}`, []string{"add"}, false},
		{"short form", `{{ short_command "list" }}`, []string{"list"}, false},
		{"no call", `plain text with {{ .field }}`, nil, false},
		{"no argument", `{{ command }}`, nil, true},
		{"piped argument", `{{ "add" | command }}`, nil, true},
		{"computed argument", `{{ command .name }}`, nil, true},
		{"broken action", `{{ command "add" `, nil, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			names, problems := commandCalls(tc.form)
			if !slices.Equal(names, tc.want) {
				t.Errorf("commandCalls(%q) = %v, want %v", tc.form, names, tc.want)
			}
			if (len(problems) > 0) != tc.problems {
				t.Errorf("commandCalls(%q) problems = %v, want problems: %v", tc.form, problems, tc.problems)
			}
		})
	}
}

// capturingMessage is a real messageParams, so it renders at dispatch as any message does,
// and records the text the send would carry.
type capturingMessage struct {
	*messageParams
	sent chan string
}

func (m *capturingMessage) send(context.Context, *bot.Bot) (*models.Message, error) {
	m.sent <- m.Text
	return nil, nil
}

// TestDispatchRendersForTheDestinationChat covers the wiring trySend leans on:
// a queued translation carries no text until dispatch renders it for the chat it resolved.
func TestDispatchRendersForTheDestinationChat(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		chatID int64
		want   string
	}{
		{"private chat renders bare", 500, "/add"},
		{"group renders the mention", -500, "/add@bot"},
		{"channel renders the mention", -1001234567890, "/add@bot"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newTestWorker()
			defer w.terminate()
			w.createDatabase()
			w.sender("test").cooling = false
			userID, _ := w.db.AddUser(tc.chatID, 3, 0, "")

			tpl := texttemplate.New("t").Funcs(cmdlib.TemplateFuncs())
			texttemplate.Must(tpl.Parse(`{{ command "add" }}`))
			params := &renderParams{templates: tpl, key: "t"}
			msg := &capturingMessage{
				messageParams: params.asDeferredText(false, false, cmdlib.ParseRaw),
				sent:          make(chan string, 1),
			}
			w.enqueueMessage(db.PriorityHigh, "test", msg, unprompted(db.MessagePacket), userID, 0)

			var got string
			timeout := time.After(10 * time.Second)
			for done := false; !done; {
				select {
				case got = <-msg.sent:
					done = true
				case r := <-w.sendResults:
					w.onSendDone(r.endpoint)
				case u := <-w.cooledUsers:
					w.onUserCooled(u)
				case <-timeout:
					t.Fatal("the message was never dispatched")
				}
			}
			if got != tc.want {
				t.Errorf("dispatched text = %q, want %q", got, tc.want)
			}
		})
	}
}
