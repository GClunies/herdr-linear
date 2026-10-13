package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func attachFiles(t *testing.T) (dir, png, pdf string) {
	dir = t.TempDir()
	png, pdf = filepath.Join(dir, "screen shot.png"), filepath.Join(dir, "spec.pdf")
	os.WriteFile(png, []byte("\x89PNG\r\n\x1a\n"), 0o644)
	os.WriteFile(pdf, []byte("%PDF-1.4"), 0o644)
	return
}

// The Files field: each file, then "Add new…"; the arrows go through them,
// x takes one off.
func TestFilesField(t *testing.T) {
	_, png, pdf := attachFiles(t)
	m := press(demoModel(t), "ctrl+c")
	if view := ansi.Strip(m.View()); !strings.Contains(view, "Files") || !strings.Contains(view, addNew) || !strings.Contains(view, "^f attach") {
		t.Fatalf("no Files field:\n%s", view)
	}
	m.form.files = []string{png, pdf}
	m = press(press(m, "tab"), "tab") // → Files
	if m.form.focus != fieldAttach {
		t.Fatalf("focus %d", m.form.focus)
	}
	m.form.fileCursor = 0
	view := ansi.Strip(m.View())
	for _, want := range []string{"screen shot.png", "spec.pdf", addNew, "x remove"} {
		if !strings.Contains(view, want) {
			t.Errorf("Files lacks %q:\n%s", want, view)
		}
	}
	m = press(m, "down")
	if m.form.fileCursor != 1 || m.form.focus != fieldAttach {
		t.Fatalf("down: cursor %d focus %d", m.form.fileCursor, m.form.focus)
	}
	m = press(m, "x")
	if len(m.form.files) != 1 || m.form.files[0] != png {
		t.Fatalf("x took off %v", m.form.files)
	}
	m = press(m, "down") // past "Add new…": the next field
	if m.form.focus != fieldTeam {
		t.Errorf("focus %d after the files", m.form.focus)
	}
	// Up from below lands on "Add new…"; down from above, on the first file.
	if m = press(m, "up"); m.form.fileCursor != 1 {
		t.Errorf("up into Files: cursor %d", m.form.fileCursor)
	}
	m = press(press(press(m, "up"), "up"), "down")
	if m.form.focus != fieldAttach || m.form.fileCursor != 0 {
		t.Errorf("down into Files: focus %d cursor %d", m.form.focus, m.form.fileCursor)
	}
	// A click on a file selects it; on "Add new…", attaches (not in the demo).
	y := strings.Index(ansi.Strip(m.View()), "screen shot.png")
	y = strings.Count(ansi.Strip(m.View())[:y], "\n")
	if i, ok := m.fileRowAt(y); !ok || i != 0 {
		t.Errorf("row at the file's line: %d %v", i, ok)
	}
	if i, ok := m.fileRowAt(y + 1); !ok || i != 1 {
		t.Errorf("row at Add new: %d %v", i, ok)
	}
	if _, ok := m.fileRowAt(y + 2); ok {
		t.Error("a row below Add new")
	}
	// In the demo, there's nowhere to go for files.
	m = press(m, "ctrl+f")
	if m.screen != screenCreate || !strings.Contains(m.err, "demo") {
		t.Errorf("^f in the demo: screen %v err %q", m.screen, m.err)
	}
}

// ^f keeps the form and hands the popup to herdr-file-transfer's, which
// is told to come back here; coming back, the form is as it was, with the
// files.
func TestAttachHandsOverAndComesBack(t *testing.T) {
	_, png, pdf := attachFiles(t)
	t.Setenv("HERDR_PLUGIN_STATE_DIR", t.TempDir())
	var opened []popupRef
	oldH, oldP := handOver, hasPopup
	handOver = func(p popupRef) error { opened = append(opened, p); return nil }
	installed := false
	hasPopup = func(popupRef) bool { return installed }
	t.Cleanup(func() { handOver, hasPopup = oldH, oldP })

	m := press(demoModel(t), "ctrl+c")
	m = typeText(m, "Crash on launch")
	m.form.files = []string{png}
	m.form.priority = 2
	m.client, m.ws, m.invoked = &linearClient{}, &workspace{ID: "ws1"}, "/src/app"
	m.back = &popupRef{PluginID: "herdr-recap", Entrypoint: "picker"}

	// Not installed: says how to get it, and stays.
	m = press(m, "ctrl+f")
	if len(opened) != 0 || !strings.Contains(m.err, "herdr plugin install") {
		t.Fatalf("without herdr-file-transfer: opened %v, err %q", opened, m.err)
	}
	installed = true
	m = press(m, "ctrl+f")
	if len(opened) != 1 {
		t.Fatalf("opened %v (err %q)", opened, m.err)
	}
	p := opened[0]
	if p.PluginID != "herdr-file-transfer" || p.Env[pickEnv] != "attach" || p.Env[pickDir] != "/src/app" {
		t.Errorf("handed to %+v", p)
	}
	var self popupRef
	json.Unmarshal([]byte(p.Env[returnEnv]), &self)
	if self.PluginID != "herdr-linear" || self.Env[draftEnv] != "1" || self.Env["HERDR_LINEAR_CWD"] != "/src/app" || !strings.Contains(self.Env[returnEnv], "herdr-recap") {
		t.Errorf("to come back to %+v", self)
	}

	// Back, with a file picked (and one that's gone, and one twice).
	picked, _ := json.Marshal([]string{pdf, "/nowhere/gone.txt", png})
	d, err := takeDraft(string(picked))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(draftPath()); err == nil {
		t.Error("the draft is kept after it's taken")
	}
	if d.WorkspaceID != "ws1" || d.Title != "Crash on launch" || d.Priority != 2 || len(d.Files) != 2 || d.Files[1] != pdf {
		t.Errorf("draft %+v", d)
	}
	m, _ = demoModel(t).restoreDraft(*d)
	if m.screen != screenCreate || m.form.title.Value() != "Crash on launch" || m.form.priority != 2 || len(m.form.files) != 2 || m.form.focus != fieldTitle {
		t.Errorf("restored: screen %v title %q files %v focus %d", m.screen, m.form.title.Value(), m.form.files, m.form.focus)
	}
}

// Created with files: each uploaded, and in the description, an image
// shown and anything else linked.
func TestCreateWithAttachments(t *testing.T) {
	_, png, pdf := attachFiles(t)
	m := press(demoModel(t), "ctrl+c")
	m = typeText(m, "Broken layout")
	m.form.files = []string{png, pdf}
	m = press(m, "enter")
	if m.screen != screenCreated || m.created == nil {
		t.Fatalf("not created: screen %v err %q", m.screen, m.err)
	}
	desc := m.client.(*demoSource).details[m.created.ID].Description
	for _, want := range []string{"![screen shot.png](https://uploads.linear.app/demo/screen shot.png)", "[spec.pdf](https://uploads.linear.app/demo/spec.pdf)"} {
		if !strings.Contains(desc, want) {
			t.Errorf("description lacks %q:\n%s", want, desc)
		}
	}
	if strings.Contains(desc, "![spec.pdf]") {
		t.Error("a PDF shown as an image")
	}
}

// uploadFile asks Linear where to put the file, then puts it there with the
// headers it was given.
func TestUploadFile(t *testing.T) {
	_, png, _ := attachFiles(t)
	fakeStore(t, &tokens{AccessToken: "tok", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)})
	var put struct {
		method, ct, extra string
		body              []byte
	}
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		put.method, put.ct, put.extra = r.Method, r.Header.Get("Content-Type"), r.Header.Get("X-Goog-Meta")
		put.body, _ = io.ReadAll(r.Body)
	}))
	t.Cleanup(storage.Close)
	var asked map[string]any
	fakeLinear(t, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Variables map[string]any `json:"variables"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		asked = req.Variables
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"fileUpload": map[string]any{"success": true, "uploadFile": map[string]any{
			"uploadUrl": storage.URL + "/put", "assetUrl": "https://uploads.linear.app/x/screen.png",
			"headers": []any{map[string]any{"key": "X-Goog-Meta", "value": "v"}},
		}}}})
	})
	url, err := (&linearClient{}).uploadFile(context.Background(), png)
	if err != nil || url != "https://uploads.linear.app/x/screen.png" {
		t.Fatalf("got %q, %v", url, err)
	}
	if asked["ct"] != "image/png" || asked["name"] != "screen shot.png" || asked["size"] != float64(8) {
		t.Errorf("asked %v", asked)
	}
	if put.method != http.MethodPut || put.ct != "image/png" || put.extra != "v" || len(put.body) != 8 {
		t.Errorf("put %+v", put)
	}
}
