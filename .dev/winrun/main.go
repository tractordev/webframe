// winrun runs webframe.exe in the Windows guest's desktop session for
// .dev/vm, without PowerShell (whose startup takes ~20s in the ARM64 VM).
//
//	winrun setup              enable auto-logon etc; prints "session" if a
//	                          desktop session is active
//	winrun run [args...]      run webframe.exe in the desktop session with
//	                          stdin from stdin.txt and WF_* from env.txt,
//	                          print its output, exit with its code
//	winrun exec               (internal) the scheduled task's side of run
//
// Processes started over ssh have no desktop, so run hands off to a scheduled
// task with an interactive logon, which runs `winrun exec` in the session.
// Files are exchanged in the directory winrun.exe lives in.
package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const task = "webframe-run"

var dir string

func main() {
	exe, err := os.Executable()
	if err != nil {
		fatal(err)
	}
	dir = filepath.Dir(exe)
	if len(os.Args) < 2 {
		fatal(fmt.Errorf("usage: winrun setup | run [args...]"))
	}
	switch os.Args[1] {
	case "setup":
		setup()
	case "run":
		os.Exit(run(os.Args[2:]))
	case "exec":
		os.Exit(execTask())
	default:
		fatal(fmt.Errorf("unknown command %q", os.Args[1]))
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "winrun:", err)
	os.Exit(1)
}

func path(name string) string { return filepath.Join(dir, name) }

// cmd runs a command, returning its combined output.
func cmd(name string, args ...string) (string, error) {
	c := exec.Command(name, args...)
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := c.CombinedOutput()
	return string(out), err
}

func reg(args ...string) error {
	out, err := cmd("reg", args...)
	if err != nil {
		return fmt.Errorf("reg %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return nil
}

func regQuery(key, value string) string {
	out, err := cmd("reg", "query", key, "/v", value)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) >= 3 && f[0] == value {
			return strings.Join(f[2:], " ")
		}
	}
	return ""
}

// setup enables auto-logon (Lima's unattended install only logs on once) and
// quiets the guest for testing, then reports whether a desktop session exists.
func setup() {
	user := os.Getenv("USERNAME")
	winlogon := `HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon`
	if regQuery(winlogon, "AutoAdminLogon") != "1" {
		pw, err := os.ReadFile(filepath.Join(os.Getenv("USERPROFILE"), "password.txt"))
		if err != nil {
			fatal(err)
		}
		for _, kv := range [][2]string{
			{"AutoAdminLogon", "1"},
			{"DefaultUserName", user},
			{"DefaultPassword", strings.TrimSpace(string(pw))},
		} {
			if err := reg("add", winlogon, "/v", kv[0], "/t", "REG_SZ", "/d", kv[1], "/f"); err != nil {
				fatal(err)
			}
		}
		_, _ = cmd("reg", "delete", winlogon, "/v", "AutoLogonCount", "/f")
		fmt.Fprintln(os.Stderr, "auto-logon enabled")
	}

	// Background Windows Update work slows the guest to a crawl.
	au := `HKLM\SOFTWARE\Policies\Microsoft\Windows\WindowsUpdate\AU`
	if regQuery(au, "NoAutoUpdate") != "0x1" {
		_ = reg("add", au, "/v", "NoAutoUpdate", "/t", "REG_DWORD", "/d", "1", "/f")
		_, _ = cmd("sc", "stop", "wuauserv")
		_, _ = cmd("sc", "config", "wuauserv", "start=", "disabled")
		fmt.Fprintln(os.Stderr, "windows update disabled")
	}
	_, _ = cmd("powercfg", "/change", "monitor-timeout-ac", "0")
	_, _ = cmd("powercfg", "/change", "standby-timeout-ac", "0")

	out, _ := cmd("tasklist", "/fi", "imagename eq explorer.exe", "/fi", "username eq "+user, "/fo", "csv", "/nh")
	if strings.Contains(strings.ToLower(out), "explorer.exe") {
		fmt.Println("session")
	}
}

// run stages args, starts the task and waits for it, then prints the output
// and returns webframe's exit code.
func run(args []string) int {
	timeout := 120 * time.Second
	if s := os.Getenv("VM_TIMEOUT"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			timeout = time.Duration(n) * time.Second
		}
	}
	if err := os.WriteFile(path("args.txt"), []byte(strings.Join(args, "\n")), 0o644); err != nil {
		fatal(err)
	}
	for _, f := range []string{"out.txt", "err.txt", "code.txt"} {
		_ = os.Remove(path(f))
	}

	exe := path("winrun.exe")
	// /it: run only when the user is logged on, in their session.
	out, err := cmd("schtasks", "/create", "/f", "/tn", task, "/sc", "once", "/st", "00:00",
		"/it", "/tr", fmt.Sprintf(`"%s" exec`, exe))
	if err != nil {
		fatal(fmt.Errorf("schtasks /create: %v: %s", err, out))
	}
	defer cmd("schtasks", "/delete", "/f", "/tn", task)
	if out, err := cmd("schtasks", "/run", "/tn", task); err != nil {
		fatal(fmt.Errorf("schtasks /run: %v: %s", err, out))
	}

	deadline := time.Now().Add(timeout)
	for {
		if _, err := os.Stat(path("code.txt")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_, _ = cmd("schtasks", "/end", "/tn", task)
			_, _ = cmd("taskkill", "/f", "/t", "/im", "webframe.exe")
			time.Sleep(500 * time.Millisecond)
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if b, err := os.ReadFile(path("out.txt")); err == nil {
		os.Stdout.Write(b)
	}
	if b, err := os.ReadFile(path("err.txt")); err == nil {
		os.Stderr.Write(b)
	}
	b, err := os.ReadFile(path("code.txt"))
	if err != nil {
		return 124
	}
	code, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 1
	}
	return code
}

// execTask runs in the desktop session: it starts webframe.exe with the staged
// stdin, env and args, and writes its output and exit code.
func execTask() int {
	// The task gets a console window in the session; hide it.
	if h, _, _ := syscall.NewLazyDLL("kernel32.dll").NewProc("GetConsoleWindow").Call(); h != 0 {
		_, _, _ = syscall.NewLazyDLL("user32.dll").NewProc("ShowWindow").Call(h, 0)
	}
	write := func(code int) int {
		_ = os.WriteFile(path("code.txt.tmp"), []byte(strconv.Itoa(code)), 0o644)
		_ = os.Rename(path("code.txt.tmp"), path("code.txt"))
		return code
	}
	var args []string
	if b, err := os.ReadFile(path("args.txt")); err == nil && len(b) > 0 {
		args = strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	}
	env := os.Environ()
	if f, err := os.Open(path("env.txt")); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if line := strings.TrimSpace(sc.Text()); line != "" {
				env = append(env, line)
			}
		}
		f.Close()
	}

	open := func(name string, flag int) *os.File {
		f, err := os.OpenFile(path(name), flag, 0o644)
		if err != nil {
			return nil
		}
		return f
	}
	stdin := open("stdin.txt", os.O_RDONLY)
	stdout := open("out.txt", os.O_CREATE|os.O_TRUNC|os.O_WRONLY)
	stderr := open("err.txt", os.O_CREATE|os.O_TRUNC|os.O_WRONLY)
	defer func() {
		for _, f := range []*os.File{stdin, stdout, stderr} {
			if f != nil {
				f.Close()
			}
		}
	}()

	c := exec.Command(path("webframe.exe"), args...)
	c.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	c.Dir = dir
	c.Env = env
	if stdin != nil {
		c.Stdin = stdin
	}
	c.Stdout, c.Stderr = stdout, stderr
	if err := c.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return write(ee.ExitCode())
		}
		if stderr != nil {
			fmt.Fprintln(stderr, "winrun:", err)
		}
		return write(1)
	}
	return write(0)
}
