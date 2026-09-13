// Package webui serves a small HTML UI for managing custom LCD screens.
//
// Security note: adding a screen here means the daemon will run that shell
// command on every refresh cycle, typically as root (see the systemd unit).
// This is arbitrary command execution by design — the whole point is to let
// the operator wire up their own screens — so this server must never be
// exposed beyond a trusted network. Bind it to localhost and reach it over
// SSH port-forwarding, or put it behind a reverse proxy with auth, if
// remote access is needed.
package webui

import (
	"html/template"
	"log"
	"net/http"

	"github.com/arojas/qnap-led/internal/customscreens"
)

const pageTemplate = `<!doctype html>
<html>
<head>
<title>qnap-led custom screens</title>
<style>
body{font-family:sans-serif;max-width:640px;margin:2rem auto;padding:0 1rem;color:#222}
table{border-collapse:collapse;width:100%;margin:1rem 0}
td,th{border:1px solid #ccc;padding:.5rem;text-align:left}
code{background:#f0f0f0;padding:.1rem .3rem}
form.inline{display:inline}
input[type=text]{width:100%;box-sizing:border-box;padding:.3rem}
button{padding:.3rem .8rem}
.error{color:#b00020}
.hint{color:#555;font-size:.9rem}
</style>
</head>
<body>
<h1>qnap-led — custom LCD screens</h1>
<p class="hint">Each screen runs its command on the panel's refresh cycle
(as the user running qnap-led, typically root). One line of output becomes
the LCD's second row with Name as the label; two or more lines use the
first two directly, overriding Name.</p>

{{if .Error}}<p class="error">{{.Error}}</p>{{end}}

<table>
<tr><th>Name</th><th>Command</th><th></th></tr>
{{range .Screens}}
<tr>
  <td>{{.Name}}</td>
  <td><code>{{.Command}}</code></td>
  <td><form class="inline" method="post" action="/delete">
    <input type="hidden" name="id" value="{{.ID}}">
    <button type="submit">Delete</button>
  </form></td>
</tr>
{{else}}
<tr><td colspan="3">No custom screens yet.</td></tr>
{{end}}
</table>

<h2>Add screen</h2>
<form method="post" action="/add">
  <p><label>Name<br><input type="text" name="name" required placeholder="e.g. Backups"></label></p>
  <p><label>Command<br><input type="text" name="command" required placeholder="e.g. systemctl is-active restic-backup"></label></p>
  <p><button type="submit">Add</button></p>
</form>
</body>
</html>`

var tmpl = template.Must(template.New("page").Parse(pageTemplate))

// Server serves the custom-screens management UI.
type Server struct {
	registry *customscreens.Registry
}

// New builds a Server backed by the given registry.
func New(registry *customscreens.Registry) *Server {
	return &Server{registry: registry}
}

// Handler returns the UI's HTTP handler, e.g. for tests via httptest.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("POST /add", s.handleAdd)
	mux.HandleFunc("POST /delete", s.handleDelete)
	return mux
}

// ListenAndServe starts the web UI; it blocks until the server stops.
func (s *Server) ListenAndServe(addr string) error {
	log.Printf("webui: listening on %s", addr)
	return http.ListenAndServe(addr, s.Handler())
}

func (s *Server) render(w http.ResponseWriter, errMsg string) {
	data := struct {
		Screens []customscreens.Screen
		Error   string
	}{
		Screens: s.registry.List(),
		Error:   errMsg,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.Execute(w, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleIndex(w http.ResponseWriter, _ *http.Request) {
	s.render(w, "")
}

func (s *Server) handleAdd(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := s.registry.Add(r.FormValue("name"), r.FormValue("command")); err != nil {
		s.render(w, err.Error())
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.registry.Delete(r.FormValue("id")); err != nil {
		s.render(w, err.Error())
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
