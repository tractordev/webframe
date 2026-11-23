package main

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveLeavesURLsAlone(t *testing.T) {
	for _, target := range []string{"https://example.com", "about:blank", "data:text/html,hi"} {
		if got := resolve(target); got != target {
			t.Errorf("resolve(%q) = %q, want it unchanged", target, got)
		}
	}
}

func TestResolveLeavesMissingPathAlone(t *testing.T) {
	target := "does-not-exist-webframe-test.html"
	if got := resolve(target); got != target {
		t.Errorf("resolve(%q) = %q, want it unchanged since the file does not exist", target, got)
	}
}

func TestResolveWrapsExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "page.html")
	if err := os.WriteFile(path, []byte("<html></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := resolve(path)
	if !strings.HasPrefix(got, "file://") {
		t.Errorf("resolve(%q) = %q, want a file:// URL", path, got)
	}
	if !strings.Contains(got, "page.html") {
		t.Errorf("resolve(%q) = %q, want it to name the file", path, got)
	}
}

func TestEnvValues(t *testing.T) {
	if _, err := envValues([]string{"=bar"}); err == nil {
		t.Error("envValues([\"=bar\"]) should fail: missing name")
	}

	const unset = "WEBFRAME_TEST_ENV_VAR_NOT_SET"
	os.Unsetenv(unset)
	if _, err := envValues([]string{unset}); err == nil {
		t.Errorf("envValues([%q]) should fail: not set in the environment", unset)
	}

	t.Setenv("WEBFRAME_TEST_ENV_VAR", "from-environ")
	env, err := envValues([]string{"WEBFRAME_TEST_ENV_VAR"})
	if err != nil || env["WEBFRAME_TEST_ENV_VAR"] != "from-environ" {
		t.Errorf("envValues([\"WEBFRAME_TEST_ENV_VAR\"]) = %v, %v, want value read from the environment", env, err)
	}

	env, err = envValues([]string{"FOO=bar", "EMPTY="})
	if err != nil {
		t.Fatalf("envValues returned an error: %v", err)
	}
	if env["FOO"] != "bar" {
		t.Errorf("env[FOO] = %q, want %q", env["FOO"], "bar")
	}
	if v, ok := env["EMPTY"]; !ok || v != "" {
		t.Errorf("env[EMPTY] = %q, %v, want an explicit empty value", v, ok)
	}
}

func TestJoinWindowDims(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"space-separated dims", []string{"-w", "300x300", "page.html"},
			[]string{"-w=300x300", "page.html"}},
		{"non-dims value is left alone", []string{"-w", "true", "page.html"},
			[]string{"-w", "true", "page.html"}},
		{"double dash stops rewriting", []string{"page.html", "--", "-w", "300x300"},
			[]string{"page.html", "--", "-w", "300x300"}},
		{"long flag spelling", []string{"--w", "640x480"},
			[]string{"--w=640x480"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := joinWindowDims(c.in)
			if !equalSlices(got, c.want) {
				t.Errorf("joinWindowDims(%v) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestParseInterspersedAllowsFlagsAfterPositional(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	s := fs.Bool("s", false, "")
	tv := fs.String("t", "", "")

	pos := parseInterspersed(fs, []string{"page.html", "-s", "-t", "5"})
	if !equalSlices(pos, []string{"page.html"}) {
		t.Errorf("positional args = %v, want [page.html]", pos)
	}
	if !*s {
		t.Error("-s after the positional arg was not parsed")
	}
	if *tv != "5" {
		t.Errorf("-t = %q, want %q", *tv, "5")
	}
}

func TestParseInterspersedStopsAtDoubleDash(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.Bool("s", false, "")

	pos := parseInterspersed(fs, []string{"-s", "--", "-s", "page.html"})
	if !equalSlices(pos, []string{"-s", "page.html"}) {
		t.Errorf("positional args = %v, want everything after -- taken literally", pos)
	}
}

func TestDurationFlag(t *testing.T) {
	var d durationFlag
	if err := d.Set("5"); err != nil || d.String() != "5s" {
		t.Errorf("Set(5) -> %v, %q, want 5s", err, d.String())
	}
	if err := d.Set("2m"); err != nil || d.String() != "2m0s" {
		t.Errorf("Set(2m) -> %v, %q, want 2m0s", err, d.String())
	}
	if err := d.Set("-5"); err == nil {
		t.Error("Set(-5) should reject a negative duration")
	}
	if err := d.Set("not-a-duration"); err == nil {
		t.Error("Set(not-a-duration) should fail")
	}
}

func TestWindowFlag(t *testing.T) {
	var w windowFlag
	if err := w.Set("true"); err != nil || !w.set {
		t.Errorf("Set(true) -> %v, set=%v, want set=true", err, w.set)
	}
	w = windowFlag{}
	if err := w.Set("1024x768"); err != nil || !w.set || w.w != 1024 || w.h != 768 {
		t.Errorf("Set(1024x768) -> %v, %+v, want set with w=1024 h=768", err, w)
	}
	if err := w.Set("not-dims"); err == nil {
		t.Error("Set(not-dims) should fail")
	}
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
