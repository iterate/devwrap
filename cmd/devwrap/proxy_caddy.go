package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"time"

	"github.com/caddyserver/caddy/v2"
	_ "github.com/caddyserver/caddy/v2/modules/standard"
)

func startEmbeddedCaddy(httpPort, httpsPort int) error {
	storageRoot := sharedCaddyStorageRoot()
	cfg := map[string]any{
		"admin": map[string]any{"listen": "127.0.0.1:2019"},
		"storage": map[string]any{
			"module": "file_system",
			"root":   storageRoot,
		},
		"apps": map[string]any{
			"http": map[string]any{
				"servers": map[string]any{
					"devwrap-http": map[string]any{
						"listen": []string{fmt.Sprintf(":%d", httpPort)},
						"routes": []any{},
					},
					"devwrap-https": map[string]any{
						"listen":                  []string{fmt.Sprintf(":%d", httpsPort)},
						"tls_connection_policies": []map[string]any{{}},
						"routes":                  []any{},
					},
				},
			},
			"tls": map[string]any{
				"automation": map[string]any{
					"policies": []map[string]any{{
						"issuers": []map[string]any{{"module": "internal"}},
					}},
				},
			},
		},
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := caddy.Load(b, true); err != nil {
		return err
	}
	if err := waitForAdminReady(3 * time.Second); err != nil {
		return fmt.Errorf("embedded caddy started but admin API is unavailable")
	}
	return nil
}

func stopEmbeddedCaddy() error {
	return caddy.Stop()
}

func sharedCaddyStorageRoot() string {
	if dir := os.Getenv("DEVWRAP_CADDY_DATA_DIR"); dir != "" {
		return dir
	}
	if dir := os.Getenv("CADDY_DATA_DIR"); dir != "" {
		return dir
	}
	if os.Geteuid() == 0 {
		sudoUser := os.Getenv("SUDO_USER")
		if sudoUser != "" {
			u, err := user.Lookup(sudoUser)
			if err == nil && u.HomeDir != "" {
				return caddyDataDirForHome(u.HomeDir)
			}
		}
	}
	return caddy.AppDataDir()
}

func caddyDataDirForHome(home string) string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Caddy")
	case "windows":
		return filepath.Join(home, "AppData", "Roaming", "Caddy")
	default:
		return filepath.Join(home, ".local", "share", "caddy")
	}
}
