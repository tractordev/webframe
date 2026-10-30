// Package webframe runs a web page in a hidden, unthrottled web view and
// reports its console output, exceptions and lifecycle to Go.
package webframe

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"sync"

	"github.com/crgimenes/glaze"
)

//go:embed hooks.js
var hooks string

// ErrReloaded is passed to a pending Eval callback when the page goes away
// before the evaluation finishes.
var ErrReloaded = errors.New("page reloaded")

// ErrIncomplete wraps the syntax error passed to an Eval callback when the
// source ended before a complete script, as when a console line opens a block
// that later lines would close.
var ErrIncomplete = errors.New("incomplete script")

type incompleteError struct{ msg string }

func (e incompleteError) Error() string { return e.msg }
func (e incompleteError) Unwrap() error { return ErrIncomplete }

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

	// Ready is called when a page has started and its hooks are installed,
	// before the page's own scripts run.
	Ready func()

	// Loaded is called when a page has finished loading (its load event).
	Loaded func()

	// LoadFailed is called when a main-frame navigation fails, as for DNS or
	// connection errors, after Exception reports it. Not called on Windows.
	LoadFailed func(url string, err error)

	// Exit is called when the page calls webframe.exit(code).
	Exit func(code int)

	// Debug enables the web inspector.
	Debug bool

	// Window shows the web view in a window instead of keeping it hidden.
	// Width and Height size it, or the page's viewport when hidden; zero
	// leaves the default.
	Window        bool
	Width, Height int

	// Env is exposed to the page as the frozen object webframe.env before
	// any page script runs.
	Env map[string]string

	// Init scripts run in every page after the hooks are installed and
	// before the page's own scripts, as global scripts in order.
	Init []string

	// AcceptDialogs answers alert, confirm and prompt as if accepted, with a
	// prompt's default text, instead of as if dismissed.
	AcceptDialogs bool

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
	evals map[int]*pending
}

type pending struct {
	cb   func(string, error)
	stop func() bool
}

type message struct {
	Kind  string `json:"kind"`
	Level string `json:"level"`
	Text  string `json:"text"`
	ID    int    `json:"id"`
	CSP   bool   `json:"csp"`
}

// config is passed to hooks.js.
type config struct {
	Env map[string]string `json:"env"`
	// Dialogs makes the hooks answer alert, confirm and prompt themselves,
	// where the web view doesn't let Go answer them.
	Dialogs bool `json:"dialogs"`
	Accept  bool `json:"accept"`
}

// New creates a Frame. It must be called from the main goroutine, which then
// has to call Run.
func New(opts Options) (*Frame, error) {
	f := &Frame{opts: opts, evals: map[int]*pending{}}
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
			if opts.AcceptDialogs {
				return true, d.Text
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
	env := opts.Env
	if env == nil {
		env = map[string]string{}
	}
	cfg, _ := json.Marshal(config{
		Env: env,
		// glaze never calls OnScriptDialog on Windows.
		Dialogs: runtime.GOOS == "windows",
		Accept:  opts.AcceptDialogs,
	})
	w.Init(hooks + "(" + string(cfg) + ");")
	for _, js := range opts.Init {
		w.Init(js)
	}
	return f, nil
}

func (f *Frame) navigated(ev glaze.NavigationEvent) {
	if ev.Kind != glaze.NavigationFailed {
		return
	}
	if f.opts.Exception != nil {
		f.opts.Exception(fmt.Sprintf("Failed to load %s: %v", ev.URL, ev.Err))
	}
	if f.opts.LoadFailed != nil {
		f.opts.LoadFailed(ev.URL, ev.Err)
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
		case "load":
			if f.opts.Loaded != nil {
				f.opts.Loaded()
			}
		case "exit":
			if f.opts.Exit != nil {
				f.opts.Exit(m.ID)
			}
		case "eval", "evalerr":
			if m.CSP {
				// The page's Content-Security-Policy forbids eval, so run the
				// source again as an expression injected by the web view,
				// which CSP does not apply to.
				f.evalExpr(m.ID, m.Text)
				continue
			}
			cb := f.take(m.ID)
			if cb == nil {
				continue
			}
			if m.Kind == "eval" {
				cb(m.Text, nil)
			} else if m.Level == "incomplete" {
				cb("", incompleteError{m.Text})
			} else {
				cb("", errors.New(m.Text))
			}
		}
	}
}

// take removes a pending evaluation and returns its callback, or nil if it
// has already finished or been cancelled.
func (f *Frame) take(id int) func(string, error) {
	f.mu.Lock()
	p := f.evals[id]
	delete(f.evals, id)
	f.mu.Unlock()
	if p == nil {
		return nil
	}
	if p.stop != nil {
		p.stop()
	}
	return p.cb
}

func (f *Frame) failEvals() {
	f.mu.Lock()
	evals := f.evals
	f.evals = map[int]*pending{}
	f.mu.Unlock()
	for _, p := range evals {
		if p.stop != nil {
			p.stop()
		}
		p.cb("", ErrReloaded)
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
	f.EvalContext(context.Background(), src, cb)
}

// EvalContext is like Eval, but stops waiting for the result when ctx is
// done and calls cb with ctx.Err(). The script itself keeps running, and its
// result is dropped.
func (f *Frame) EvalContext(ctx context.Context, src string, cb func(result string, err error)) {
	if err := ctx.Err(); err != nil {
		cb("", err)
		return
	}
	f.mu.Lock()
	f.id++
	id := f.id
	p := &pending{cb: cb}
	f.evals[id] = p
	if ctx.Done() != nil {
		p.stop = context.AfterFunc(ctx, func() {
			if cb := f.take(id); cb != nil {
				cb("", ctx.Err())
			}
		})
	}
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
