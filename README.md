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
webframe [-s|-S] [-e] [-c] [-d] [-v] [-w [WIDTHxHEIGHT]] <url|file>
```

The argument is required. It can be a URL or a path to a local file. Use
`about:blank` for an empty page.

| Flag | |
|---|---|
| `-c` | Console: evaluate each line of stdin in the page and print the result |
| `-e` | Exit with status 1 on the first exception |
| `-s` | Silence exceptions |
| `-S` | Silence all output (stdout and stderr, including native libraries) |
| `-w [WxH]` | Show a window, optionally sized, e.g. `-w 1024x768` |
| `-d` | Enable the web inspector |
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
  Windows)

```
Unhandled promise rejection: TypeError: nope
    @http://localhost:8000/page.html:8:44
Failed to load resource: <img> http://localhost:8000/missing.png
```

Errors from scripts on `file://` pages and from cross-origin scripts without
CORS show only `Script error.`. This is WebKit hiding details, not webframe.
Serve the page over http if you need the full error.

`alert`, `confirm` and `prompt` print as `[dialog] ...` and are answered as if
dismissed.

### Console mode

`-c` reads JavaScript from stdin, one line at a time. Each line is evaluated
as a global script, so `var` and function declarations stay defined between
lines. If the result is a promise, webframe waits for it and prints the
resolved value. Strings print quoted. Errors go to stderr and don't count
toward `-e`. webframe exits when stdin closes.

For a console on an empty page, use `about:blank`:

```
$ webframe -c about:blank
> var x = 40
undefined
> x + 2
42
> new Promise(r => setTimeout(() => r("later"), 100))
"later"
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

### Passing values in

Environment variables prefixed with `WF_` become string globals on `window`,
without the prefix, before any page script runs:

```
WF_API_KEY=abc webframe app.html    # window.API_KEY === "abc"
```

### Lifecycle

| Event | Effect |
|---|---|
| `window.close()` in the page | Exit 0 |
| Closing the window (with `-w`) | Exit 0 |
| `SIGHUP` | Reload the original URL |
| `SIGINT` / `SIGTERM` | Exit 0 |
| stdin closes (with `-c`) | Exit 0 |
| Exception (with `-e`) | Exit 1 |
| Bad usage | Exit 2 |

Throttling on macOS is turned off, so timers and animation keep running at full
speed while hidden. Other platforms may be throttled.

## Docker

The [Dockerfile](Dockerfile) builds an Alpine image with WebKitGTK and Xvfb:

```
docker build -t webframe .
docker run --rm webframe https://example.com
docker run --rm -i webframe -c about:blank
docker run --rm -v "$PWD:/work" webframe page.html
```

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
		Ready: func() {
			f.Eval("navigator.userAgent", func(res string, err error) {
				fmt.Println(res, err)
			})
		},
		Globals: map[string]string{"MODE": "headless"},
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
| `Run()` | Run the event loop until `Terminate` |
| `Terminate()` | Stop `Run` |

Everything except `New` and `Run` is safe to call from any goroutine. The
callbacks can run on any goroutine.

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

Files in `.dev/testdata` are copied next to the binary in the guest. The macOS
and Windows VMs set up auto-login, since webframe needs a desktop session.

Lima's Windows support is experimental, and the first install needs some
manual steps in the VM display. They're listed in
[.dev/lima/windows.yaml](.dev/lima/windows.yaml).

## Examples

`make examples` serves the [examples](examples) directory on port 8000.

- [hello.html](examples/hello.html): prints and exits.
- [bench.html](examples/bench.html): a small JavaScript benchmark.
- [v86.html](examples/v86.html): boots Linux in the
  [v86](https://github.com/copy/v86) x86 emulator and connects its serial
  console to webframe. Run it with `-c` and type `send("ls\n")`. It needs
  `buildroot-bzimage68.bin` next to it; see the file for where to get it.

## License

MIT
