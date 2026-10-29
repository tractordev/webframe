// Package webframe runs a web page in a hidden, unthrottled web view and
// reports its console output, exceptions and lifecycle to Go.
package webframe

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/crgimenes/glaze"
)

//go:embed hooks.js
var hooks string

// ErrReloaded is passed to a pending Eval callback when the page goes away
// before the evaluation finishes.
var ErrReloaded = errors.New("page reloaded")

// Options are the event callbacks of a Frame. Each is optional and may be
// called from any goroutine.
type Options struct {
	// Console receives each console call: level is the method name ("log",
	// "warn", "error", ...) and text its arguments, formatted and joined.
	Console func(level, text string)

	// Exception receives uncaught errors, unhandled promise rejections,
	// resource load errors and failed page loads, formatted with any detail
	// available.
	Exception func(text string)

	// Closed is called when the page calls window.close() or, with Window,
	// the window is closed.
	Closed func()

	// Ready is called when a page has started and its hooks are installed.
	Ready func()

	// Debug enables the web inspector.
	Debug bool

	// Window shows the web view in a window instead of keeping it hidden.
	// Width and Height size it, or the page's viewport when hidden; zero
	// leaves the default.
	Window        bool
	Width, Height int

	// Globals are set as string properties of window before any page
	// script runs.
	Globals map[string]string

	// Ephemeral keeps cookies, local storage and cache in memory only, so
	// nothing is shared with earlier or later runs.
	Ephemeral bool
}

// Frame is a hidden web view running one page.
type Frame struct {
	w    glaze.WebView
	opts Options

	mu    sync.Mutex
	url   string
	id    int
	evals map[int]func(string, error)
}

type message struct {
	Kind  string `json:"kind"`
	Level string `json:"level"`
	Text  string `json:"text"`
	ID    int    `json:"id"`
	CSP   bool   `json:"csp"`
}

// New creates a Frame. It must be called from the main goroutine, which then
// has to call Run.
func New(opts Options) (*Frame, error) {
	f := &Frame{opts: opts, evals: map[int]func(string, error){}}
	w, err := glaze.NewWithOptions(glaze.Options{
		Debug:             opts.Debug,
		Hidden:            !opts.Window,
		DisableThrottling: true,
		Ephemeral:         opts.Ephemeral,
		OnNavigation:      f.navigated,
		OnClose: func() {
			if opts.Closed != nil {
				opts.Closed()
			}
		},
		OnScriptDialog: func(d glaze.ScriptDialog) (bool, string) {
			if opts.Console != nil {
				opts.Console("dialog", d.Message)
			}
			return false, ""
		},
	})
	if err != nil {
		return nil, err
	}
	f.w = w
	if err := w.Bind("__webframe", f.receive); err != nil {
		w.Destroy()
		return nil, err
	}
	if opts.Width > 0 && opts.Height > 0 {
		w.SetSize(opts.Width, opts.Height, glaze.HintNone)
	}
	if len(opts.Globals) > 0 {
		g, _ := json.Marshal(opts.Globals)
		w.Init(fmt.Sprintf("Object.assign(window, %s);", g))
	}
	w.Init(hooks)
	return f, nil
}

func (f *Frame) navigated(ev glaze.NavigationEvent) {
	if ev.Kind == glaze.NavigationFailed && f.opts.Exception != nil {
		f.opts.Exception(fmt.Sprintf("Failed to load %s: %v", ev.URL, ev.Err))
	}
}

func (f *Frame) receive(batch []message) {
	for _, m := range batch {
		switch m.Kind {
		case "console":
			if f.opts.Console != nil {
				f.opts.Console(m.Level, m.Text)
			}
		case "exception":
			if f.opts.Exception != nil {
				f.opts.Exception(m.Text)
			}
		case "close":
			if f.opts.Closed != nil {
				f.opts.Closed()
			}
		case "ready":
			f.failEvals()
			if f.opts.Ready != nil {
				f.opts.Ready()
			}
		case "eval", "evalerr":
			if m.CSP {
				// The page's Content-Security-Policy forbids eval, so run the
				// source again as an expression injected by the web view,
				// which CSP does not apply to.
				f.evalExpr(m.ID, m.Text)
				continue
			}
			f.mu.Lock()
			cb := f.evals[m.ID]
			delete(f.evals, m.ID)
			f.mu.Unlock()
			if cb == nil {
				continue
			}
			if m.Kind == "eval" {
				cb(m.Text, nil)
			} else {
				cb("", errors.New(m.Text))
			}
		}
	}
}

func (f *Frame) failEvals() {
	f.mu.Lock()
	evals := f.evals
	f.evals = map[int]func(string, error){}
	f.mu.Unlock()
	for _, cb := range evals {
		cb("", ErrReloaded)
	}
}

// Navigate loads url. Safe to call from any goroutine.
func (f *Frame) Navigate(url string) {
	f.mu.Lock()
	f.url = url
	f.mu.Unlock()
	f.w.Dispatch(func() { f.w.Navigate(url) })
}

// Reload navigates again to the URL last passed to Navigate. Safe to call
// from any goroutine.
func (f *Frame) Reload() {
	f.mu.Lock()
	url := f.url
	f.mu.Unlock()
	f.w.Dispatch(func() { f.w.Navigate(url) })
}

// Eval evaluates src as a global script in the page and calls cb with the
// result formatted as text, after awaiting it if it is a promise. Errors
// thrown or rejected are passed to cb as an error. Safe to call from any
// goroutine; call it after Ready.
func (f *Frame) Eval(src string, cb func(result string, err error)) {
	f.mu.Lock()
	f.id++
	id := f.id
	f.evals[id] = cb
	f.mu.Unlock()
	js, _ := json.Marshal(src)
	f.w.Dispatch(func() {
		f.w.Eval(fmt.Sprintf("window.__webframe_eval(%d, %s)", id, js))
	})
}

// evalExpr evaluates src as an expression without eval, for pages whose CSP
// blocks eval. Statements that are not an expression fail to parse; the
// third script notices that the second never started and reports it.
func (f *Frame) evalExpr(id int, src string) {
	f.w.Dispatch(func() {
		f.w.Eval(fmt.Sprintf("window.__webframe_expr_begin(%d)", id))
		f.w.Eval(fmt.Sprintf("window.__webframe_expr_run(%d, (function(){ return (\n%s\n); }))", id, src))
		f.w.Eval(fmt.Sprintf("window.__webframe_expr_end(%d)", id))
	})
}

// Run runs the event loop until Terminate. Call it from the goroutine that
// called New.
func (f *Frame) Run() {
	f.w.Run()
	f.w.Destroy()
}

// Terminate stops Run. Safe to call from any goroutine.
func (f *Frame) Terminate() {
	f.w.Terminate()
}
