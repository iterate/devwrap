package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

func runProxyStart(privileged bool) error {
	if privileged && os.Geteuid() == 0 {
		return errors.New("do not run `devwrap proxy start --privileged` under sudo; run it as your normal user")
	}

	if checkDaemonReachable() {
		if outputJSON {
			return emitJSON(map[string]any{"ok": true, "action": "proxy_start", "result": "already_running"})
		}
		fmt.Println("proxy is already running")
		return nil
	}
	if checkSystemCaddyReachable() {
		if outputJSON {
			return emitJSON(map[string]any{"ok": true, "action": "proxy_start", "result": "using_unmanaged", "admin": caddyAdminBase})
		}
		fmt.Println("unmanaged caddy is already running at 127.0.0.1:2019")
		fmt.Println("devwrap will use it directly with file-based state")
		return nil
	}

	bin, err := os.Executable()
	if err != nil {
		return err
	}
	logPath, err := daemonLogPath()
	if err != nil {
		return err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer logFile.Close()

	cmdName := bin
	cmdArgs := []string{"proxy", "daemon"}
	if privileged {
		cmdName = "sudo"
		cmdArgs = append([]string{"--preserve-env=XDG_STATE_HOME,DEVWRAP_CADDY_DATA_DIR,CADDY_DATA_DIR", bin}, cmdArgs...)
	}
	cmd := exec.Command(cmdName, cmdArgs...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if privileged {
		cmd.Stdin = os.Stdin
	} else {
		cmd.Stdin = nil
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return err
	}
	if err := waitForDaemon(); err != nil {
		return fmt.Errorf("proxy failed to start (see %s): %w", logPath, err)
	}
	if outputJSON {
		return emitJSON(map[string]any{"ok": true, "action": "proxy_start", "result": "started", "privileged": privileged})
	}
	if privileged {
		fmt.Println("proxy started (privileged)")
	} else {
		fmt.Println("proxy started")
	}
	return nil
}

func runProxyStop() error {
	if checkSystemCaddyReachable() {
		if info, err := inspectExternalCaddy(); err == nil && info.Managed {
			if err := stopManagedCaddy(); err != nil {
				return err
			}
			if outputJSON {
				return emitJSON(map[string]any{"ok": true, "action": "proxy_stop", "result": "stopped"})
			}
			fmt.Println("proxy stopped")
			return nil
		}
	}

	pid, err := readDaemonPID()
	if err != nil || !processAlive(pid) {
		if checkSystemCaddyReachable() {
			if outputJSON {
				return emitJSON(map[string]any{"ok": true, "action": "proxy_stop", "result": "using_unmanaged"})
			}
			fmt.Println("using unmanaged caddy; nothing for devwrap to stop")
			return nil
		}
		if outputJSON {
			return emitJSON(map[string]any{"ok": true, "action": "proxy_stop", "result": "not_running"})
		}
		fmt.Println("proxy is not running")
		return nil
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return fmt.Errorf("stop failed: %w", err)
	}
	if outputJSON {
		return emitJSON(map[string]any{"ok": true, "action": "proxy_stop", "result": "signaled", "pid": pid})
	}
	fmt.Println("proxy stopped")
	return nil
}

func runProxyStatus() error {
	if !checkSystemCaddyReachable() {
		if outputJSON {
			return emitJSON(map[string]any{"ok": true, "running": false})
		}
		fmt.Println("proxy is not running")
		return nil
	}
	s, err := localStatusFromFiles()
	if err != nil {
		return err
	}
	owner := "unmanaged caddy"
	if s.CaddySource == "managed" {
		owner = "managed caddy"
	}
	if outputJSON {
		return emitJSON(map[string]any{"ok": true, "running": true, "status": s, "owner": owner})
	}
	mode := modeFromStatus(s)
	if s.CaddySource == "managed" {
		pid := "-"
		if s.PID > 0 {
			pid = strconv.Itoa(s.PID)
		}
		fmt.Printf("proxy running (pid %s, %s, %s)\n", pid, mode, owner)
	} else {
		fmt.Printf("proxy running (%s)\n", owner)
	}
	fmt.Printf("http: %d, https: %d\n", s.HTTPPort, s.HTTPSPort)
	fmt.Printf("ca trusted: %v\n", s.Trusted)
	if len(s.Apps) == 0 {
		fmt.Println("apps: none")
		return nil
	}
	fmt.Println("apps:")
	for _, app := range s.Apps {
		fmt.Printf("- %s -> https://%s%s (port %d, pid %d)\n", app.Name, app.Host, portSuffix(s.HTTPSPort), app.Port, app.PID)
	}
	return nil
}

func runProxyTrust() error {
	if err := ensureCaddyOrDaemon(false); err != nil {
		return err
	}
	if err := trustLocalCA(); err != nil {
		return err
	}
	if outputJSON {
		return emitJSON(map[string]any{"ok": true, "action": "proxy_trust", "trusted": true})
	}
	fmt.Println("trust complete")
	return nil
}

func runProxyLogs() error {
	managed := false
	if checkSystemCaddyReachable() {
		if info, err := inspectExternalCaddy(); err == nil {
			managed = info.Managed
		}
	}
	if !managed {
		if outputJSON {
			return emitJSON(map[string]any{"ok": true, "managed": false, "log_file": "", "content": ""})
		}
		fmt.Println("no managed caddy logs (currently using unmanaged caddy)")
		return nil
	}

	path, err := daemonLogPath()
	if err != nil {
		return err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			if outputJSON {
				return emitJSON(map[string]any{"ok": true, "log_file": path, "content": ""})
			}
			fmt.Printf("no daemon logs yet (%s)\n", path)
			return nil
		}
		return err
	}
	if outputJSON {
		return emitJSON(map[string]any{"ok": true, "log_file": path, "content": string(b)})
	}
	fmt.Printf("log file: %s\n", path)
	if len(b) == 0 {
		fmt.Println("(empty)")
		return nil
	}
	fmt.Print(string(b))
	return nil
}

func runProxyDaemon() error {
	return startDaemon()
}

func runDoctor() error {
	runtimePath, err := runtimeDir()
	if err != nil {
		return err
	}
	stateP, _ := statePath()
	lockP, _ := stateLockPath()
	pidP, _ := pidPath()
	logP, _ := daemonLogPath()
	managed := false
	if checkSystemCaddyReachable() {
		if info, err := inspectExternalCaddy(); err == nil {
			managed = info.Managed
		}
	}

	if outputJSON {
		payload := map[string]any{
			"ok":          true,
			"runtime_dir": runtimePath,
			"state_file":  stateP,
			"state_lock":  lockP,
			"storage_dir": sharedCaddyStorageRoot(),
			"caddy_admin": checkSystemCaddyReachable(),
			"trusted":     isCertTrusted(),
		}
		if managed {
			payload["pid_file"] = pidP
			payload["log_file"] = logP
		}
		if checkSystemCaddyReachable() {
			if info, err := inspectExternalCaddy(); err == nil {
				source := "unmanaged"
				if info.Managed {
					source = "managed"
				}
				payload["caddy_source"] = source
				payload["http_port"] = info.HTTPPort
				payload["https_port"] = info.HTTPSPort
			} else {
				payload["caddy_inspect_error"] = err.Error()
			}
		}
		if s, err := localStatusFromFiles(); err == nil {
			payload["tracked_apps"] = len(s.Apps)
		} else {
			payload["tracked_apps_error"] = err.Error()
		}
		return emitJSON(payload)
	}

	fmt.Println("devwrap doctor")
	fmt.Printf("runtime dir: %s\n", runtimePath)
	fmt.Printf("state file: %s\n", stateP)
	fmt.Printf("state lock: %s\n", lockP)
	if managed {
		fmt.Printf("pid file:   %s\n", pidP)
		fmt.Printf("log file:   %s\n", logP)
	}
	fmt.Printf("storage dir: %s\n", sharedCaddyStorageRoot())

	fmt.Printf("caddy admin: %v\n", checkSystemCaddyReachable())
	if checkSystemCaddyReachable() {
		if info, err := inspectExternalCaddy(); err == nil {
			source := "unmanaged"
			if info.Managed {
				source = "managed"
			}
			fmt.Printf("caddy source: %s\n", source)
			fmt.Printf("http/https:   %d/%d\n", info.HTTPPort, info.HTTPSPort)
		} else {
			fmt.Printf("caddy inspect error: %v\n", err)
		}
	}

	fmt.Printf("trust (local CA): %v\n", isCertTrusted())
	if s, err := localStatusFromFiles(); err == nil {
		fmt.Printf("tracked apps: %d\n", len(s.Apps))
	} else {
		fmt.Printf("tracked apps: unknown (%v)\n", err)
	}

	return nil
}

func runList() error {
	if !checkSystemCaddyReachable() {
		if outputJSON {
			return emitJSON(map[string]any{"ok": true, "apps": []any{}})
		}
		fmt.Println("no apps registered (proxy not running)")
		return nil
	}
	s, err := localStatusFromFiles()
	if err != nil {
		return err
	}
	if outputJSON {
		return emitJSON(map[string]any{"ok": true, "apps": sortedApps(s.Apps), "https_port": s.HTTPSPort})
	}
	if len(s.Apps) == 0 {
		fmt.Println("no apps registered")
		return nil
	}
	for _, app := range s.Apps {
		fmt.Printf("%s -> %s (port %d, pid %d)\n", app.Name, app.HTTPSURL(s.HTTPSPort), app.Port, app.PID)
	}
	return nil
}

func runRemove(name string) error {
	if err := validateName(name); err != nil {
		return err
	}
	if !checkSystemCaddyReachable() {
		return errors.New("proxy is not running")
	}
	if err := removeDirect(name); err != nil {
		return err
	}
	if outputJSON {
		return emitJSON(map[string]any{"ok": true, "action": "remove", "name": name})
	}
	fmt.Printf("removed route for %q\n", name)
	return nil
}

func runChild(name string, cmdArgs []string, port int, hostURL string, release func()) error {
	templated := applyTemplates(cmdArgs, port)
	cmd := exec.Command(templated[0], templated[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	env := os.Environ()
	env = append(env, "PORT="+strconv.Itoa(port))
	env = append(env, "DEVWRAP_APP="+name)
	if hostURL != "" {
		env = append(env, "DEVWRAP_HOST="+hostURL)
	}
	cmd.Env = env

	if err := cmd.Start(); err != nil {
		return err
	}

	sigCh := make(chan os.Signal, 8)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	defer signal.Stop(sigCh)

	go func() {
		for sig := range sigCh {
			if cmd.Process != nil {
				_ = cmd.Process.Signal(sig)
			}
		}
	}()

	err := cmd.Wait()
	if release != nil {
		release()
	}
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
			if status.Signaled() {
				return childExitError{code: 128 + int(status.Signal())}
			}
			return childExitError{code: status.ExitStatus()}
		}
	}
	return err
}

func normalizeDevwrapHostURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return raw
	}
	hostname := u.Hostname()
	port := u.Port()
	if port == "" || port == "80" || port == "443" {
		return u.Scheme + "://" + hostname
	}
	return u.Scheme + "://" + hostname + ":" + port
}

type childExitError struct {
	code int
}

func (e childExitError) Error() string {
	return fmt.Sprintf("child exited with status %d", e.code)
}

func (e childExitError) ExitCode() int {
	return e.code
}

func modeFromStatus(s ProxyStatus) string {
	if s.Root {
		return "sudo"
	}
	return "unprivileged"
}

func sortedApps(apps []App) []App {
	out := make([]App, len(apps))
	copy(out, apps)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func stopManagedCaddy() error {
	res, err := adminDo(http.MethodPost, "/stop")
	if err != nil {
		return fmt.Errorf("stop failed: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return fmt.Errorf("stop failed: caddy admin returned %d", res.StatusCode)
	}
	return nil
}

func applyTemplates(args []string, port int) []string {
	out := make([]string, 0, len(args))
	portValue := strconv.Itoa(port)
	for _, arg := range args {
		out = append(out, strings.ReplaceAll(arg, "@PORT", portValue))
	}
	return out
}

func portSuffix(port int) string {
	if port == 443 {
		return ""
	}
	return ":" + strconv.Itoa(port)
}
