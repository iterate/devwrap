package main

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/gofrs/flock"
)

const (
	stateFile = "state.json"
	pidFile   = "daemon.pid"
	logFile   = "daemon.log"
	lockFile  = "state.lock"
)

func runtimeDir() (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := runtimeHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "state")
	}
	dir := filepath.Join(base, "devwrap")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

func runtimeHomeDir() (string, error) {
	if os.Geteuid() == 0 {
		sudoUser := os.Getenv("SUDO_USER")
		if sudoUser != "" {
			u, err := user.Lookup(sudoUser)
			if err == nil && u.HomeDir != "" {
				return u.HomeDir, nil
			}
		}
	}
	return os.UserHomeDir()
}

func pidPath() (string, error) {
	dir, err := runtimeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, pidFile), nil
}

func statePath() (string, error) {
	dir, err := runtimeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, stateFile), nil
}

func daemonLogPath() (string, error) {
	dir, err := runtimeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, logFile), nil
}

func stateLockPath() (string, error) {
	dir, err := runtimeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, lockFile), nil
}

func withStateLock(fn func() error) error {
	path, err := stateLockPath()
	if err != nil {
		return err
	}
	fileLock := flock.New(path)
	if err := fileLock.Lock(); err != nil {
		return fmt.Errorf("acquire state lock: %w", err)
	}
	defer func() { _ = fileLock.Unlock() }()
	return fn()
}

func checkDaemonReachable() bool {
	pid, err := readDaemonPID()
	if err != nil {
		return false
	}
	if !processAlive(pid) {
		_ = clearDaemonPIDFile()
		return false
	}
	if !checkSystemCaddyReachable() {
		return false
	}
	info, err := inspectExternalCaddy()
	if err != nil {
		return false
	}
	if !info.Managed {
		_ = clearDaemonPIDFile()
		return false
	}
	return info.Managed
}

func clearDaemonPIDFile() error {
	path, err := pidPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func checkSystemCaddyReachable() bool {
	return adminHealthy()
}

func readDaemonPID() (int, error) {
	path, err := pidPath()
	if err != nil {
		return 0, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	p, err := strconv.Atoi(string(b))
	if err != nil {
		return 0, err
	}
	return p, nil
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil
}

func waitForDaemon() error {
	return waitForAdminReady(5 * time.Second)
}
