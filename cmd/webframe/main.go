package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"tractor.dev/webframe"
)

var Version string

func init() {
	// The web view's event loop must run on the main OS thread.
	runtime.LockOSThread()
}

var dimsRe = regexp.MustCompile(`^(\d+)x(\d+)$`)

// windowFlag is a boolean flag that optionally takes WIDTHxHEIGHT.
type windowFlag struct {
	set  bool
	w, h int
}

func (f *windowFlag) IsBoolFlag() bool { return true }

func (f *windowFlag) String() string {
	if f == nil || f.w == 0 {
		return ""
	}
	return fmt.Sprintf("%dx%d", f.w, f.h)
}

func (f *windowFlag) Set(v string) error {
	if b, err := strconv.ParseBool(v); err == nil {
		f.set = b
		return nil
	}
	m := dimsRe.FindStringSubmatch(v)
	if m == nil {
		return fmt.Errorf("want WIDTHxHEIGHT, got %q", v)
	}
	f.set = true
	f.w, _ = strconv.Atoi(m[1])
	f.h, _ = strconv.Atoi(m[2])
	return nil
}

func main() {
	var window windowFlag
	quiet := flag.Bool("s", false, "silence exceptions")
	silent := flag.Bool("S", false, "silence all output on stdout and stderr")
	exitOnErr := flag.Bool("e", false, "exit non-zero on the first exception")
	console := flag.Bool("c", false, "interactive console: evaluate stdin lines in the page")
	debug := flag.Bool("d", false, "enable the web inspector")
	version := flag.Bool("v", false, "print version and exit")
	private := flag.Bool("p", false, "private: keep cookies, storage and cache in memory only")
	flag.Var(&window, "w", "show a window, optionally sized `WIDTHxHEIGHT`")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: webframe [-s|-S] [-e] [-c] [-d] [-p] [-v] [-w [WIDTHxHEIGHT]] <url|file>")
		flag.PrintDefaults()
		fmt.Fprintln(os.Stderr, "\nWF_* environment variables are set as page globals without the prefix.")
		if runtime.GOOS == "linux" {
			fmt.Fprintln(os.Stderr, "Without DISPLAY or WAYLAND_DISPLAY, a private Xvfb is started if installed.")
		}
	}
	_ = flag.CommandLine.Parse(joinWindowDims(os.Args[1:]))
	if *version {
		if Version == "" {
			Version = "dev"
		}
		fmt.Println(Version)
		return
	}
	args := flag.Args()
	if len(args) != 1 {
		flag.Usage()
		os.Exit(2)
	}
	target := args[0]

	var (
		mu     sync.Mutex
		stdout io.Writer = os.Stdout
		stderr io.Writer = os.Stderr
	)
	exceptions := stderr
	if *quiet {
		exceptions = io.Discard
	}
	if *silent {
		stdout, stderr, exceptions = io.Discard, io.Discard, io.Discard
		silence()
	}
	println := func(w io.Writer, s string) {
		mu.Lock()
		defer mu.Unlock()
		fmt.Fprintln(w, s)
	}

	if err := ensureDisplay(); err != nil {
		fmt.Fprintln(os.Stderr, "webframe:", err)
		os.Exit(1)
	}

	ready := make(chan struct{}, 1)
	var frame *webframe.Frame
	frame, err := webframe.New(webframe.Options{
		Debug:     *debug,
		Window:    window.set,
		Width:     window.w,
		Height:    window.h,
		Globals:   envGlobals(),
		Ephemeral: *private,
		Console: func(level, text string) {
			if level != "log" {
				text = "[" + level + "] " + text
			}
			println(stdout, text)
		},
		Exception: func(text string) {
			println(exceptions, text)
			if *exitOnErr {
				os.Exit(1)
			}
		},
		Closed: func() { frame.Terminate() },
		Ready: func() {
			select {
			case ready <- struct{}{}:
			default:
			}
		},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "webframe:", err)
		os.Exit(1)
	}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGHUP, os.Interrupt, syscall.SIGTERM)
	go func() {
		for sig := range sigs {
			if sig == syscall.SIGHUP {
				frame.Reload()
				continue
			}
			frame.Terminate()
		}
	}()

	frame.Navigate(resolve(target))
	if *console {
		go repl(frame, ready, func(s string) { println(stdout, s) }, func(s string) { println(stderr, s) })
	}

	frame.Run()
}

// joinWindowDims rewrites "-w WIDTHxHEIGHT" as "-w=WIDTHxHEIGHT", since a
// boolean flag never takes a separate value.
func joinWindowDims(args []string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			return append(out, args[i:]...)
		}
		if (a == "-w" || a == "--w") && i+1 < len(args) && dimsRe.MatchString(args[i+1]) {
			a += "=" + args[i+1]
			i++
		}
		out = append(out, a)
	}
	return out
}

// envGlobals returns WF_* environment variables keyed without the prefix.
func envGlobals() map[string]string {
	g := map[string]string{}
	for _, kv := range os.Environ() {
		k, v, _ := strings.Cut(kv, "=")
		if name, ok := strings.CutPrefix(k, "WF_"); ok && name != "" {
			g[name] = v
		}
	}
	return g
}

// resolve turns a path to an existing file into a file URL and leaves
// anything else as given.
func resolve(target string) string {
	if u, err := url.Parse(target); err == nil && u.Scheme != "" && len(u.Scheme) > 1 {
		return target
	}
	if _, err := os.Stat(target); err != nil {
		return target
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return target
	}
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}).String()
}

// repl evaluates each line of stdin in the page, one at a time, printing
// results to out and errors to errout. It exits the process on EOF.
func repl(frame *webframe.Frame, ready <-chan struct{}, out, errout func(string)) {
	<-ready
	tty := false
	if fi, err := os.Stdin.Stat(); err == nil {
		tty = fi.Mode()&os.ModeCharDevice != 0
	}
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for {
		if tty {
			fmt.Fprint(os.Stdout, "> ")
		}
		if !sc.Scan() {
			break
		}
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		done := make(chan struct{})
		frame.Eval(line, func(result string, err error) {
			if err != nil {
				errout(err.Error())
			} else {
				out(result)
			}
			close(done)
		})
		<-done
	}
	if err := sc.Err(); err != nil {
		errout("webframe: " + err.Error())
	}
	frame.Terminate()
}
