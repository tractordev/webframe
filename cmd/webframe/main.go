package main

import (
	"bufio"
	"errors"
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
	"time"

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

// listFlag is a repeatable string flag.
type listFlag []string

func (l *listFlag) String() string     { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error { *l = append(*l, v); return nil }

// durationFlag is a time.Duration flag that also takes plain seconds.
type durationFlag time.Duration

func (d *durationFlag) String() string {
	if d == nil || *d == 0 {
		return ""
	}
	return time.Duration(*d).String()
}

func (d *durationFlag) Set(v string) error {
	if secs, err := strconv.ParseFloat(v, 64); err == nil {
		*d = durationFlag(secs * float64(time.Second))
	} else if dur, err := time.ParseDuration(v); err == nil {
		*d = durationFlag(dur)
	} else {
		return fmt.Errorf("want seconds or a duration like 2m, got %q", v)
	}
	if *d < 0 {
		return fmt.Errorf("negative duration %q", v)
	}
	return nil
}

// exitTimeout is the exit status when -t elapses, as with timeout(1).
const exitTimeout = 124

func main() {
	var (
		window  windowFlag
		envs    listFlag
		inits   listFlag
		scripts listFlag
		timeout durationFlag
	)
	quiet := flag.Bool("s", false, "silence exceptions")
	silent := flag.Bool("S", false, "silence all output on stdout and stderr")
	exitOnErr := flag.Bool("e", false, "exit 1 on the first exception")
	console := flag.Bool("c", false, "console: evaluate statements from stdin in the page after it loads")
	debug := flag.Bool("d", false, "enable the web inspector")
	version := flag.Bool("v", false, "print version and exit")
	private := flag.Bool("p", false, "private: keep cookies, storage and cache in memory only")
	accept := flag.Bool("y", false, "accept alert/confirm/prompt instead of dismissing them")
	flag.Var(&scripts, "x", "run `FILE` in the page after it loads; exit 1 if it throws (repeatable, in order)")
	flag.Var(&timeout, "t", "exit 124 if still running after `DURATION`: seconds, or like 500ms or 2m")
	flag.Var(&inits, "i", "inject `FILE` before page scripts on every page, top frame only (repeatable)")
	flag.Var(&envs, "E", "expose environment variable `NAME[=VALUE]` as webframe.env.NAME; NAME must be set (repeatable)")
	flag.Var(&window, "w", "show a window, optionally sized `WIDTHxHEIGHT`")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: webframe [flags] <url|file>")
		flag.PrintDefaults()
		fmt.Fprintln(os.Stderr, "\nFlags can come before or after the URL. Pages get a read-only webframe")
		fmt.Fprintln(os.Stderr, "global with env (from -E) and exit(code).")
		fmt.Fprintln(os.Stderr, "\nExit status: 0 on window.close(), signals or the end of stdin with -c;")
		fmt.Fprintln(os.Stderr, "the code from webframe.exit(code); 1 on an exception with -e, an -x script")
		fmt.Fprintln(os.Stderr, "that throws or a failed first load; 2 on bad usage; 124 when -t elapses.")
		if runtime.GOOS == "linux" {
			fmt.Fprintln(os.Stderr, "Without DISPLAY or WAYLAND_DISPLAY, a private Xvfb is started if installed.")
		}
	}
	args := parseInterspersed(flag.CommandLine, joinWindowDims(os.Args[1:]))
	if *version {
		if Version == "" {
			Version = "dev"
		}
		fmt.Println(Version)
		return
	}
	if len(args) != 1 {
		flag.Usage()
		os.Exit(2)
	}
	target := args[0]

	env, err := envValues(envs)
	if err != nil {
		fmt.Fprintln(os.Stderr, "webframe:", err)
		os.Exit(2)
	}
	initSrc, err := readFiles(inits)
	if err != nil {
		fmt.Fprintln(os.Stderr, "webframe:", err)
		os.Exit(1)
	}
	scriptSrc, err := readFiles(scripts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "webframe:", err)
		os.Exit(1)
	}

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
	// exit holds the output lock so nothing is cut off mid-line.
	exit := func(code int) {
		mu.Lock()
		os.Exit(code)
	}
	exception := func(text string) {
		println(exceptions, text)
		if *exitOnErr {
			exit(1)
		}
	}

	if timeout > 0 {
		time.AfterFunc(time.Duration(timeout), func() {
			println(stderr, "webframe: timed out after "+timeout.String())
			exit(exitTimeout)
		})
	}

	if err := ensureDisplay(); err != nil {
		fmt.Fprintln(os.Stderr, "webframe:", err)
		os.Exit(1)
	}

	var (
		loadOnce sync.Once
		loaded   = make(chan struct{})
	)
	var frame *webframe.Frame
	frame, err = webframe.New(webframe.Options{
		Debug:         *debug,
		Window:        window.set,
		Width:         window.w,
		Height:        window.h,
		Env:           env,
		Init:          initSrc,
		AcceptDialogs: *accept,
		Ephemeral:     *private,
		Console: func(level, text string) {
			if level != "log" {
				text = "[" + level + "] " + text
			}
			println(stdout, text)
		},
		Exception: exception,
		Closed:    func() { frame.Terminate() },
		Exit:      exit,
		Loaded:    func() { loadOnce.Do(func() { close(loaded) }) },
		LoadFailed: func(string, error) {
			// Without the first page there is nothing to run, so don't wait
			// forever. Exception has already reported it.
			select {
			case <-loaded:
			default:
				exit(1)
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
	if len(scripts) > 0 || *console {
		go func() {
			<-loaded
			for _, src := range scriptSrc {
				done := make(chan struct{})
				frame.Eval(src, func(_ string, err error) {
					if err != nil {
						// A script that fails can't have done its job, so
						// don't wait for an exit that won't come.
						println(exceptions, err.Error())
						exit(1)
					}
					close(done)
				})
				<-done
			}
			if *console {
				repl(frame, func(s string) { println(stdout, s) }, func(s string) { println(stderr, s) })
			}
		}()
	}

	frame.Run()
}

// parseInterspersed parses flags that may come before or after positional
// arguments, which it returns. Everything after "--" is positional.
func parseInterspersed(fs *flag.FlagSet, args []string) []string {
	var pos []string
	for {
		_ = fs.Parse(args)
		rest := fs.Args()
		if n := len(args) - len(rest); n > 0 && args[n-1] == "--" {
			return append(pos, rest...)
		}
		if len(rest) == 0 {
			return pos
		}
		pos = append(pos, rest[0])
		args = rest[1:]
	}
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

// envValues resolves -E arguments: NAME=VALUE as given, NAME from the
// environment.
func envValues(args []string) (map[string]string, error) {
	env := map[string]string{}
	for _, a := range args {
		name, v, ok := strings.Cut(a, "=")
		if name == "" {
			return nil, fmt.Errorf("-E %q: missing name", a)
		}
		if !ok {
			if v, ok = os.LookupEnv(name); !ok {
				return nil, fmt.Errorf("-E %s: not set in the environment", name)
			}
		}
		env[name] = v
	}
	return env, nil
}

func readFiles(paths []string) ([]string, error) {
	var out []string
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		out = append(out, string(b))
	}
	return out, nil
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

// repl evaluates stdin in the page one statement at a time, printing results
// to out and errors to errout. A statement continues over lines until it
// parses. On a terminal, a blank line ends it early to show the error. It
// exits the process on EOF.
func repl(frame *webframe.Frame, out, errout func(string)) {
	tty := false
	if fi, err := os.Stdin.Stat(); err == nil {
		tty = fi.Mode()&os.ModeCharDevice != 0
	}
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	var buf []string
	for {
		if tty {
			if len(buf) == 0 {
				fmt.Fprint(os.Stdout, "> ")
			} else {
				fmt.Fprint(os.Stdout, "... ")
			}
		}
		final := false
		if !sc.Scan() {
			if len(buf) == 0 {
				break
			}
			final = true
		} else {
			line := sc.Text()
			blank := strings.TrimSpace(line) == ""
			if blank && len(buf) == 0 {
				continue
			}
			if blank && tty {
				final = true
			} else {
				buf = append(buf, line)
			}
		}
		var (
			result string
			err    error
			done   = make(chan struct{})
		)
		frame.Eval(strings.Join(buf, "\n"), func(r string, e error) {
			result, err = r, e
			close(done)
		})
		<-done
		if errors.Is(err, webframe.ErrIncomplete) && !final {
			continue
		}
		buf = nil
		if err != nil {
			errout(err.Error())
		} else {
			out(result)
		}
		if final && !tty {
			break
		}
	}
	if err := sc.Err(); err != nil {
		errout("webframe: " + err.Error())
	}
	frame.Terminate()
}
