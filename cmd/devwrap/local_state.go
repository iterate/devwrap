package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"sort"
	"strconv"
	"time"
)

func loadLocalState() (daemonState, error) {
	state := daemonState{
		Version:     1,
		CaddySource: "unmanaged",
		HTTPPort:    80,
		HTTPSPort:   443,
		Apps:        map[string]App{},
	}
	path, err := statePath()
	if err != nil {
		return state, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return state, nil
		}
		return state, err
	}
	if err := json.Unmarshal(b, &state); err != nil {
		return state, nil
	}
	if state.Apps == nil {
		state.Apps = map[string]App{}
	}
	if state.CaddySource == "" || state.CaddySource == "existing" {
		state.CaddySource = "unmanaged"
	}
	if state.CaddySource == "spawned" {
		state.CaddySource = "managed"
	}
	return state, nil
}

func saveLocalState(state daemonState) error {
	path, err := statePath()
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func localStatusFromFiles() (ProxyStatus, error) {
	var out ProxyStatus
	err := withStateLock(func() error {
		info, err := inspectExternalCaddy()
		if err != nil {
			return err
		}
		state, err := loadLocalState()
		if err != nil {
			return err
		}
		changed := false
		for name, app := range state.Apps {
			if !processAlive(app.PID) {
				delete(state.Apps, name)
				changed = true
			}
		}
		if changed {
			_, _, _ = applyRoutesViaAdmin(state.Apps)
			_ = saveLocalState(state)
		}
		apps := make([]App, 0, len(state.Apps))
		for _, app := range state.Apps {
			apps = append(apps, app)
		}
		sort.Slice(apps, func(i, j int) bool { return apps[i].Name < apps[j].Name })
		source := "unmanaged"
		pid := 0
		if info.Managed {
			source = "managed"
			if p, err := readDaemonPID(); err == nil && processAlive(p) {
				pid = p
			}
		}
		out = ProxyStatus{
			Running:     true,
			CaddySource: source,
			Root:        info.HTTPPort == 80 && info.HTTPSPort == 443,
			HTTPPort:    info.HTTPPort,
			HTTPSPort:   info.HTTPSPort,
			Trusted:     isCertTrusted(),
			PID:         pid,
			Apps:        apps,
		}
		return nil
	})
	if err != nil {
		return ProxyStatus{}, err
	}
	return out, nil
}

func requestLeaseDirect(name string, pid int) (Lease, error) {
	var lease Lease
	err := withStateLock(func() error {
		state, err := loadLocalState()
		if err != nil {
			return err
		}
		for appName, app := range state.Apps {
			if !processAlive(app.PID) {
				delete(state.Apps, appName)
			}
		}

		app, ok := state.Apps[name]
		if ok {
			app.PID = pid
			app.StartedAt = time.Now().UTC().Format(time.RFC3339)
		} else {
			port, err := allocatePortFromApps(state.Apps)
			if err != nil {
				return err
			}
			app = App{
				Name:      name,
				Host:      name + ".localhost",
				Port:      port,
				PID:       pid,
				StartedAt: time.Now().UTC().Format(time.RFC3339),
			}
		}
		state.Apps[name] = app

		httpPort, httpsPort, err := applyRoutesViaAdmin(state.Apps)
		if err != nil {
			return err
		}
		state.Version = 1
		state.CaddySource = "unmanaged"
		state.HTTPPort = httpPort
		state.HTTPSPort = httpsPort
		state.Root = httpPort == 80 && httpsPort == 443
		if err := saveLocalState(state); err != nil {
			return err
		}

		lease = leaseFromAppAndPorts(app, httpPort, httpsPort)
		return nil
	})
	if err != nil {
		return Lease{}, err
	}
	return lease, nil
}

func releaseLeaseDirect(name string, pid int) {
	_ = withStateLock(func() error {
		state, err := loadLocalState()
		if err != nil {
			return err
		}
		app, ok := state.Apps[name]
		if !ok {
			return nil
		}
		if pid > 0 && app.PID != pid {
			return nil
		}
		delete(state.Apps, name)
		if _, _, err := applyRoutesViaAdmin(state.Apps); err != nil {
			return err
		}
		return saveLocalState(state)
	})
}

func removeDirect(name string) error {
	return withStateLock(func() error {
		state, err := loadLocalState()
		if err != nil {
			return err
		}
		if _, ok := state.Apps[name]; !ok {
			return nil
		}
		delete(state.Apps, name)
		if _, _, err := applyRoutesViaAdmin(state.Apps); err != nil {
			return err
		}
		return saveLocalState(state)
	})
}

func allocatePortFromApps(apps map[string]App) (int, error) {
	used := make(map[int]struct{}, len(apps))
	for _, app := range apps {
		used[app.Port] = struct{}{}
	}
	for port := 11000; port <= 19999; port++ {
		if _, ok := used[port]; ok {
			continue
		}
		ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
		if err != nil {
			continue
		}
		_ = ln.Close()
		return port, nil
	}
	return 0, errors.New("no free ports in range 11000-19999")
}

func leaseFromAppAndPorts(app App, httpPort, httpsPort int) Lease {
	httpURL := "http://" + app.Host
	httpsURL := "https://" + app.Host
	if httpPort != 80 {
		httpURL += ":" + strconv.Itoa(httpPort)
	}
	if httpsPort != 443 {
		httpsURL += ":" + strconv.Itoa(httpsPort)
	}
	return Lease{
		Name:     app.Name,
		Host:     app.Host,
		Port:     app.Port,
		HTTPURL:  httpURL,
		HTTPSURL: httpsURL,
		Trusted:  isCertTrusted(),
	}
}

func ensureCaddyOrDaemon(privileged bool) error {
	if checkSystemCaddyReachable() {
		return nil
	}
	if err := runProxyStart(privileged); err != nil {
		return err
	}
	if checkSystemCaddyReachable() {
		return nil
	}
	return fmt.Errorf("caddy admin is still unavailable")
}
