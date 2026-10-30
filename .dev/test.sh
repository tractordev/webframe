#!/usr/bin/env bash
# Smoke tests for webframe in a guest. Run via `.dev/vm test [os...]`, which
# builds and copies the binary first and sets VM and VM_OS.
set -uo pipefail

: "${VM:?run via .dev/vm test}" "${VM_OS:?run via .dev/vm test}"
pass=0 fail=0

# run <name> <want-exit> <stdin> [webframe args...]; sets $out, $err.
run() {
	local name=$1 want=$2 input=$3; shift 3
	local o e code
	o=$(mktemp) e=$(mktemp)
	printf '%s' "$input" | "$VM" _run "$VM_OS" "$@" >"$o" 2>"$e"
	code=$?
	out=$(tr -d '\r' <"$o") err=$(tr -d '\r' <"$e")
	rm -f "$o" "$e"
	if [[ $code -ne $want ]]; then
		bad "$name: exit $code, want $want"
		return 1
	fi
	return 0
}

ok() { echo "  ok   $1"; pass=$((pass + 1)); }
bad() {
	echo "  FAIL $1"
	[[ -n "${out:-}" ]] && sed 's/^/       out| /' <<<"$out" | head -20
	[[ -n "${err:-}" ]] && sed 's/^/       err| /' <<<"$err" | head -20
	fail=$((fail + 1))
}

has() { grep -qF -- "$2" <<<"$1"; }

# expect <name> <haystack> <needle>...
expect() {
	local name=$1 hay=$2 n; shift 2
	for n in "$@"; do
		has "$hay" "$n" || { bad "$name: missing '$n'"; return; }
	done
	ok "$name"
}

out= err=
run version 0 "" -v && expect "version" "$out" "dev"

out= err=
run "usage without url" 2 "" && expect "usage without url" "$err" "usage: webframe"

out= err=
if run "smoke page" 0 "" -E TEST=yes smoke.html; then
	expect "console.log formatting" "$out" 'hello 1 {"a":[1,2]} null undefined' '"self":"[Circular]"'
	expect "console prefixes" "$out" "[warn] careful"
	expect "-E env" "$out" "env yes"
	expect "window.close" "$out" "ticks done"
	n=$(grep -E '^n[0-9]+$' <<<"$out" | tr '\n' ' ')
	want=$(for i in $(seq 0 49); do printf 'n%d ' "$i"; done)
	[[ "$n" == "$want" ]] && ok "console ordering" || bad "console ordering: got '$n'"
	expect "exceptions to stderr" "$err" "Unhandled promise rejection: TypeError: nope" "Uncaught"
fi

out= err=
run "-e exits on exception" 1 "" -e smoke.html && expect "-e exits on exception" "$err" "nope"

out= err=
if run "-s silences exceptions" 1 "" -s -e smoke.html; then
	has "$out" "hello 1" && ! has "$err" "nope" && ! has "$err" "Uncaught" \
		&& ok "-s silences exceptions" || bad "-s silences exceptions"
fi

out= err=
if run "-S silences all output" 1 "" -S -e smoke.html; then
	[[ -z "$out$err" ]] && ok "-S silences all output" || bad "-S silences all output"
fi

out= err=
if run "console mode" 0 $'var x = 40\nx + 2\nPromise.resolve("later")\nnope.nope\nlocation.href\n' -c about:blank; then
	expect "console mode" "$out" "42" '"later"' '"about:blank"'
	expect "console errors" "$err" "Uncaught ReferenceError"
fi

out= err=
if run "console multi-line" 0 $'function add(a, b) {\n  return a + b\n}\nadd(1,\n  2)\n1 +\n' -c about:blank; then
	expect "console multi-line" "$out" "3"
	expect "console incomplete at EOF" "$err" "SyntaxError"
fi

out= err=
run "-x and webframe.exit" 3 "" -x script.js -i init.js smoke.html \
	&& expect "-x and webframe.exit" "$out" "init yes" "loaded yes" "confirm false"

out= err=
run "-y accepts dialogs" 0 "" -y -x script.js smoke.html && expect "-y accepts dialogs" "$out" "confirm true"

out= err=
run "-t timeout" 124 "" -t 1 about:blank && expect "-t timeout" "$err" "timed out"

out= err=
run "-t duration, flags after url" 124 "" about:blank -t 500ms && expect "-t duration, flags after url" "$err" "timed out after 500ms"

out= err=
run "-x throws exits 1" 1 "" -t 30 -x throw.js about:blank && expect "-x throws exits 1" "$err" "Uncaught Error: bad"

out= err=
run "-x repeatable, in order" 3 "" -x first.js -x script.js smoke.html \
	&& expect "-x repeatable, in order" "$out" "first ran" "confirm false"

out= err=
if run "-w window size" 0 $'innerWidth + "x" + innerHeight\n' -w 800x600 -c about:blank; then
	if [[ "$VM_OS" == windows ]]; then
		# glaze sizes the outer window on Windows, so the viewport is smaller.
		[[ "$out" =~ \"(7[0-9][0-9]|800)x(5[0-9][0-9]|600)\" ]] && ok "-w window size" || bad "-w window size"
	else
		expect "-w window size" "$out" '"800x600"'
	fi
fi

# timers.html measures timer and frame rates hidden; a throttled page runs a
# handful per second, or no frames at all. Thresholds are loose for slow VMs.
out= err=
if run "hidden page not throttled" 0 "" -t 60 -E SECONDS=2 timers.html; then
	rate() { awk -v k="$1" 'index($0, k) == 1 { print $(NF-1) }' <<<"$out"; }
	vis=$(awk '/^visibility/ { print $2 }' <<<"$out")
	vp=$(awk '/^viewport/ { print $2 }' <<<"$out")
	to=$(rate "setTimeout(0)/s") iv=$(rate "setInterval(10)/s") fr=$(rate "animation frames/s")
	if [[ "$vis" == visible && "$vp" != 0x0 && "${to:-0}" -ge 100 && "${iv:-0}" -ge 50 && "${fr:-0}" -ge 20 ]]; then
		ok "hidden page not throttled ($vis $vp, timeout $to/s, interval $iv/s, frames $fr/s)"
	else
		bad "hidden page not throttled ($vis $vp, timeout $to/s, interval $iv/s, frames $fr/s)"
	fi
fi

# glaze does not report navigation failures on Windows.
if [[ "$VM_OS" != windows ]]; then
	out= err=
	run "load failure" 1 "" -e http://webframe.invalid/ && expect "load failure" "$err" "Failed to load"

	out= err=
	run "load failure without -e" 1 "" -t 30 http://webframe.invalid/ && expect "load failure without -e" "$err" "Failed to load"
fi

echo "  $pass passed, $fail failed"
[[ $fail -eq 0 ]]
