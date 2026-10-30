# webframe

Run a web page like a program. webframe loads a URL in a hidden webview,
keeps it running at full speed, and connects the page to your terminal: the
`console` goes to stdout, exceptions go to stderr, and you can evaluate
JavaScript in the page from stdin.

```
$ webframe examples/hello.html
Hello, world!
```

It uses the webview the OS already has (WebKit on macOS, WebKitGTK on Linux,
WebView2 on Windows) through [glaze](https://github.com/progrium/glaze), so it
is a small single binary with no bundled browser and, on most platforms, no
cgo.

## Uses

- Run browser-only code (WASM, WebGPU, Web Audio, DOM libraries) from scripts,
  CI, or cron without a full browser automation stack.
- Keep a page running in the background as a long-lived process, like a
  daemon or worker.
- Poke at a live page from a REPL with `-c`.
- Script and test pages: inject stubs with `-i`, run a test with `-x`, and
  set the exit status from the page with `webframe.exit(code)`.
- Run a single-page app or emulator headless, such as
  [v86](examples/v86.html), and talk to it over stdio or WebSocket.
- Use it as a library for cross-platform isolation

## Install

Download a zip for your platform from the releases, or build from source with
Go 1.27+:

```
go install tractor.dev/webframe/cmd/webframe@latest
```

Runtime requirements:

| Platform | Needs |
|---|---|
| macOS | Nothing |
| Windows | Edge WebView2 Runtime (preinstalled on Windows 10/11) |
| Linux (glibc) | WebKitGTK 4.1 or 6.0, plus a display or Xvfb |
| Linux (musl, Alpine) | `apk add webkit2gtk-4.1 xvfb`, and the `linux-musl` build |

On Linux, if neither `DISPLAY` nor `WAYLAND_DISPLAY` is set, webframe starts
its own private Xvfb and shuts it down when it exits. If you run it under
`xvfb-run` or in a desktop session, it uses that display instead.

Debian/Ubuntu: `apt install libwebkit2gtk-4.1-0 xvfb`

## Usage

```
webframe [flags] <url|file>
```

The argument is required. It can be a URL or a path to a local file. Use
`about:blank` for an empty page. Flags can come before or after it.

| Flag | |
|---|---|
| `-c` | Console: evaluate stdin in the page after it loads and print the results |
| `-x FILE` | Run a script file in the page after it loads; exit 1 if it throws (repeatable) |
| `-i FILE` | Inject a script before the page's scripts, on every page (repeatable) |
| `-E NAME[=VALUE]` | Expose an environment variable as `webframe.env.NAME` (repeatable) |
| `-t DURATION` | Exit with status 124 if still running after this long: seconds, or like `500ms` or `2m`. `0`, the default, means no limit. |
| `-e` | Exit with status 1 on the first exception |
| `-y` | Accept `alert`, `confirm` and `prompt` instead of dismissing them |
| `-s` | Silence exceptions |
| `-S` | Silence all output (stdout and stderr, including native libraries) |
| `-w [WxH]` | Show a window, optionally sized, e.g. `-w 1024x768` |
| `-d` | Enable the web inspector |
| `-p` | Private: keep cookies, storage and cache in memory only |
| `-v` | Print version |

### Output

`console.log` prints to stdout as-is. Other console methods are prefixed:

```
hello 1 {"a":[1,2]} null undefined
[warn] careful
[error] Error: boom
    global code@file:///tmp/page.html:4:49
```

Arguments are formatted much like Node: strings print raw, other values as
JSON, with handling for cycles (`[Circular]`), errors (with stacks),
functions, `undefined`, symbols, bigints and DOM nodes. Output keeps the
page's order, even in fast bursts.

Exceptions print to stderr with as much detail as the engine gives:

- uncaught errors
- unhandled promise rejections
- resources that fail to load, like `<img>` and `<script>`
- page loads that fail, like DNS or connection errors (not reported on
  Windows). If the first page fails to load, webframe exits with status 1.

```
Unhandled promise rejection: TypeError: nope
    @http://localhost:8000/page.html:8:44
Failed to load resource: <img> http://localhost:8000/missing.png
```

Errors from scripts on `file://` pages and from cross-origin scripts without
CORS show only `Script error.`. This is WebKit hiding details, not webframe.
Serve the page over http if you need the full error.

`alert`, `confirm` and `prompt` print as `[dialog] ...` and are answered as if
dismissed: `confirm` returns `false` and `prompt` returns `null`. With `-y`
they are accepted instead: `confirm` returns `true` and `prompt` its default
text.

### Console mode

`-c` reads JavaScript from stdin once the page has loaded and evaluates one
statement at a time. A statement can span lines: a line that leaves a block,
call or string open is joined with the next ones until it parses. On a
terminal, an empty line ends it early. Each statement is evaluated as a
global script, so `var` and function declarations stay defined between them.
If the result is a promise, webframe waits for it and prints the resolved
value. Strings print quoted. Errors go to stderr and don't count toward `-e`.
webframe exits when stdin closes, after the last result.

For a console on an empty page, use `about:blank`:

```
$ webframe -c about:blank
> var x = 40
undefined
> x + 2
42
> new Promise(r => setTimeout(() => r("later"), 100))
"later"
> function add(a, b) {
...   return a + b
... }
undefined
> add(1, 2)
3
> nope
Uncaught ReferenceError: Can't find variable: nope
```

`about:blank` has an opaque origin, so things like cross-origin `fetch` and
`localStorage` need a real page. Console mode works with any page, so you can
drive a page while it runs:

```
$ webframe -c examples/v86.html
```

Without a terminal it reads stdin as a stream, which is useful for scripts:

```
echo 'document.title' | webframe -c https://example.com
```

### Scripts

`-x` runs a file in the page once it has loaded, after the page's own `load`
handlers. It's evaluated like a console statement, so it can be many lines,
and if it ends in a promise webframe waits for it. If it throws or rejects,
webframe prints the error and exits 1, with or without `-e`. Repeat `-x` to
run several files in order, each after the last has settled. With `-c`, the
console starts after they have.

The page doesn't exit when the script finishes. Call `webframe.exit(code)` to
exit with a status, or `window.close()` to exit 0, and use `-t` so a test
that hangs can't block CI:

```js
// test.js
(async () => {
  document.querySelector("#save").click();
  await new Promise(r => setTimeout(r, 100));
  webframe.exit(document.querySelector(".saved") ? 0 : 1);
})();
```

```
webframe -t 30 -x test.js app.html
```

`-i` injects a file into every page before the page's scripts run, including
after navigations and reloads. Use it for stubs, `fetch` hooks and test
helpers. Files run in order, after webframe's own hooks, so `console` and
`webframe` are already set up. They run in the top frame only, not in
iframes. An `-i` script that throws is reported like any page exception: it
exits only with `-e`, since it runs again on every page.

Putting it together, [todo.html](examples/todo.html) is tested without a
server by stubbing `fetch` with [mock-fetch.js](examples/mock-fetch.js) and
accepting its delete confirmation with `-y`:

```
$ webframe -y -t 30 -i examples/mock-fetch.js -x examples/todo.test.js examples/todo.html
ok   loads saved todos
ok   adds a todo
[dialog] Delete "Buy milk"?
ok   ignores blank input
ok   deletes after confirm
ok   saves each change
$ echo $?
0
```

On a page whose Content-Security-Policy blocks `eval`, an `-x` script has to
be a single expression, like the async function above; `-i` scripts aren't
affected.

### Passing values in

`-E` exposes environment variables to the page as strings on
`webframe.env`, before any page script runs. Only the variables you name are
exposed, so secrets can stay in the environment instead of in scripts:

```
API_KEY=abc webframe -E API_KEY app.html    # webframe.env.API_KEY === "abc"
webframe -E MODE=test app.html              # webframe.env.MODE === "test"
```

`-E NAME` without a value exits with status 2 if `NAME` isn't set, so a
missing secret fails loudly instead of showing up as `undefined`.

Anything in `webframe.env` can be read by every script on the page, including
third-party ones.

### The webframe object

Pages get a read-only `webframe` global:

| | |
|---|---|
| `webframe.env` | Variables passed with `-E` |
| `webframe.exit(code)` | Exit with `code`, 0 if omitted. A code that isn't an integer exits 1; the OS keeps only the low 8 bits. |

`webframe` can't be replaced or redefined, so a page that declares its own
top-level `webframe` variable or class has that script fail to run.

### Lifecycle

| Event | Effect |
|---|---|
| `window.close()` in the page | Exit 0 |
| `webframe.exit(code)` in the page | Exit `code` |
| Closing the window (with `-w`) | Exit 0 |
| `SIGHUP` | Reload the original URL |
| `SIGINT` / `SIGTERM` | Exit 0 |
| stdin closes (with `-c`) | Exit 0 |
| Exception (with `-e`) | Exit 1 |
| An `-x` script throws or rejects | Exit 1 |
| The first page fails to load (not on Windows) | Exit 1 |
| Bad usage | Exit 2 |
| `-t` elapses | Exit 124 |

Throttling is turned off on every platform, so timers and
`requestAnimationFrame` keep running at full speed while hidden, and the page
sees itself as visible with a real viewport. Run
[timers.html](examples/timers.html) to check.

On Windows, failed page loads aren't reported, so use `-t` to avoid waiting
forever on a page that doesn't load. Script dialogs are answered by
replacing `alert`, `confirm` and `prompt` in the page, so a page can see or
undo that.

## Docker

The [Dockerfile](Dockerfile) builds an Alpine image with WebKitGTK and Xvfb:

```
docker build -t webframe .
docker run --rm webframe https://example.com
docker run --rm -i webframe -c about:blank
docker run --rm -v "$PWD:/work" webframe page.html
docker run --rm -v "$PWD:/work" -e API_KEY webframe -E API_KEY -t 30 -x test.js page.html
```

Files for `-x` and `-i` are read inside the container, so mount them, and
pass variables for `-E` into the container with `docker run -e`.

It runs as a non-root user in `/work`, keeps WebKit's sandbox on, and responds
to `docker kill -s HUP` (reload) and `docker stop`. The image is about 580 MB,
almost all of it WebKitGTK.

## Go library

The CLI is a thin wrapper around the `webframe` package, which reports events
and leaves policy, like output formatting and exit behavior, to you.

```go
package main

import (
	"fmt"
	"os"
	"runtime"

	"tractor.dev/webframe"
)

func init() { runtime.LockOSThread() } // the webview runs on the main thread

func main() {
	var f *webframe.Frame
	f, err := webframe.New(webframe.Options{
		Console:   func(level, text string) { fmt.Println(level, text) },
		Exception: func(text string) { fmt.Println("exception:", text) },
		Closed:    func() { f.Terminate() },
		Loaded: func() {
			f.Eval("navigator.userAgent", func(res string, err error) {
				fmt.Println(res, err)
			})
		},
		Exit: func(code int) { os.Exit(code) },
		Env:  map[string]string{"MODE": "headless"},
	})
	if err != nil {
		panic(err)
	}
	f.Navigate("https://example.com")
	f.Run() // blocks until Terminate
}
```

| API | |
|---|---|
| `New(Options)` | Create a frame. Call it from the main goroutine, which must then call `Run`. |
| `Navigate(url)` | Load a URL |
| `Reload()` | Load the last `Navigate` URL again |
| `Eval(src, cb)` | Evaluate in the page and get the formatted result, after awaiting promises. Call after `Ready`. |
| `EvalContext(ctx, src, cb)` | `Eval`, but stop waiting when `ctx` is done, as for a timeout |
| `Run()` | Run the event loop until `Terminate` |
| `Terminate()` | Stop `Run` |

Everything except `New` and `Run` is safe to call from any goroutine. The
callbacks can run on any goroutine.

`Options` also has `Init` scripts (`-i`), `AcceptDialogs` (`-y`), and the
`Loaded`, `LoadFailed` and `Exit` callbacks. Errors passed to `Eval` callbacks
wrap `ErrReloaded` if the page went away first, and `ErrIncomplete` if the
source ended mid-statement.

## Building

```
make build                      # ./webframe for this machine
make build OS=linux-musl GOARCH=arm64
make dist VERSION=0.2           # dist/webframe_v0.2_<os>_<arch>.zip for all targets
```

Builds use `CGO_ENABLED=0` and cross-compile from anywhere with plain Go. glaze
loads the system webview at runtime via [purego](https://github.com/ebitengine/purego).
The Linux binary still depends on the system's glibc and its loader, which
every glibc distro provides.

The exception is `linux-musl` for Alpine. purego without cgo only works with
glibc, so that build uses cgo against musl and cross-compiles with
[zig](https://ziglang.org) (`zig cc`). You only need zig to build those
targets.

## Testing

`.dev/vm` uses [Lima](https://lima-vm.io) to run webframe and a set of smoke
tests on real VMs: Ubuntu, Alpine, macOS 15, and Windows 11 ARM64.

```
.dev/vm up linux               # create/start webframe-linux
.dev/vm test                   # build, copy, and run the tests on every running VM
.dev/vm run macos -c           # run webframe in a VM; stdin and WF_* pass through
.dev/vm status | shell | down | rm
```

Files in `.dev/testdata`, and `examples/timers.html` for the check that hidden
pages aren't throttled, are copied next to the binary in the guest. The macOS
and Windows VMs set up auto-login, since webframe needs a desktop session. On
Windows, a small Go helper ([.dev/winrun](.dev/winrun)) runs webframe in that
session through a scheduled task. It's used instead of PowerShell, which
takes about 20 seconds to start in the ARM64 guest.

Lima's Windows support is experimental, and the first install needs some
manual steps in the VM display. They're listed in
[.dev/lima/windows.yaml](.dev/lima/windows.yaml).

## Examples

`make examples` serves the [examples](examples) directory on port 8000.

- [hello.html](examples/hello.html): prints and exits.
- [bench.html](examples/bench.html): a small JavaScript benchmark.
- [timers.html](examples/timers.html): measures how often timers and
  animation frames run, to check that a hidden page isn't throttled. Pass
  `-E SECONDS=5` to change how long it measures (default 2).
- [csp.html](examples/csp.html): a page whose Content-Security-Policy
  blocks `eval`. Run it with `-c`; each line is evaluated as a single
  expression instead, so `counter.next()` works but `let x = 1` doesn't.
- [todo.html](examples/todo.html): a small todo app with a test,
  [todo.test.js](examples/todo.test.js), that runs without a server by
  stubbing `fetch` with [mock-fetch.js](examples/mock-fetch.js). See
  [Scripts](#scripts).
- [links.js](examples/links.js): prints every link on any page:
  `webframe -t 30 -x examples/links.js https://example.com`.
- [status.js](examples/status.js): a post-deploy smoke test that fails on
  page errors or an unexpected title:
  `webframe -e -p -t 30 -E EXPECT=Example -x examples/status.js https://example.com`.
- [v86.html](examples/v86.html): boots Linux in the
  [v86](https://github.com/copy/v86) x86 emulator and connects its serial
  console to webframe. Run it with `-c` and type `send("ls\n")`. It needs
  `buildroot-bzimage68.bin` next to it; see the file for where to get it.

## License

MIT
