package main

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/smallstep/truststore"
)

type App struct {
	Name      string `json:"name"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	PID       int    `json:"pid"`
	StartedAt string `json:"started_at"`
}

func (a App) HTTPSURL(httpsPort int) string {
	if httpsPort == 443 {
		return "https://" + a.Host
	}
	return "https://" + a.Host + ":" + strconv.Itoa(httpsPort)
}

type daemonState struct {
	Version     int            `json:"version"`
	CaddySource string         `json:"caddy_source"`
	Root        bool           `json:"root"`
	HTTPPort    int            `json:"http_port"`
	HTTPSPort   int            `json:"https_port"`
	Apps        map[string]App `json:"apps"`
}

func startDaemon() error {
	if checkSystemCaddyReachable() {
		return errors.New("caddy admin already running; daemon not needed")
	}

	httpPort, httpsPort, _, err := chooseProxyPorts(os.Geteuid() == 0)
	if err != nil {
		return err
	}
	if err := startEmbeddedCaddy(httpPort, httpsPort); err != nil {
		return err
	}

	if err := withStateLock(func() error {
		state, err := loadLocalState()
		if err != nil {
			return err
		}
		for name, app := range state.Apps {
			if !processAlive(app.PID) {
				delete(state.Apps, name)
			}
		}
		state.Version = 1
		state.CaddySource = "managed"
		state.HTTPPort = httpPort
		state.HTTPSPort = httpsPort
		state.Root = httpPort == 80 && httpsPort == 443
		if err := saveLocalState(state); err != nil {
			return err
		}
		if _, _, err := applyRoutesViaAdmin(state.Apps); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return err
	}

	pid, err := pidPath()
	if err != nil {
		return err
	}
	if err := os.WriteFile(pid, []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		return err
	}
	defer os.Remove(pid)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(quit)
	<-quit
	return stopSpawnedCaddy()
}

func stopSpawnedCaddy() error {
	if err := stopEmbeddedCaddy(); err != nil {
		return err
	}
	return withStateLock(func() error {
		state, err := loadLocalState()
		if err != nil {
			return err
		}
		state.CaddySource = "unmanaged"
		return saveLocalState(state)
	})
}

func chooseProxyPorts(isRoot bool) (int, int, bool, error) {
	if isRoot {
		if portsAvailable(80, 443) {
			return 80, 443, true, nil
		}
		if portsAvailable(8080, 8443) {
			return 8080, 8443, false, nil
		}
		return 0, 0, false, errors.New("no available proxy ports: 80/443 and 8080/8443 are in use")
	}
	if portsAvailable(8080, 8443) {
		return 8080, 8443, false, nil
	}
	if portsAvailable(9080, 9443) {
		return 9080, 9443, false, nil
	}
	return 0, 0, false, errors.New("no available proxy ports: 8080/8443 and 9080/9443 are in use")
}

func portsAvailable(httpPort, httpsPort int) bool {
	return isPortAvailable(httpPort) && isPortAvailable(httpsPort)
}

func isPortAvailable(port int) bool {
	ln, err := net.Listen("tcp", ":"+strconv.Itoa(port))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

func isCertTrusted() bool {
	cert, err := rootCertFromAdmin("local")
	if err != nil {
		return false
	}
	chains, err := cert.Verify(x509.VerifyOptions{})
	return err == nil && len(chains) > 0
}

func trustLocalCA() error {
	cert, err := rootCertFromAdmin("local")
	if err != nil {
		return fmt.Errorf("failed to fetch caddy local CA from admin API: %w", err)
	}
	if isCertTrusted() {
		return nil
	}
	if err := truststore.Install(cert,
		truststore.WithDebug(),
		truststore.WithFirefox(),
		truststore.WithJava(),
	); err != nil {
		return fmt.Errorf("trust install failed: %w", err)
	}
	return nil
}

func rootCertFromAdmin(caID string) (*x509.Certificate, error) {
	if caID == "" {
		caID = "local"
	}
	res, err := adminGet("/pki/ca/" + caID)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return nil, fmt.Errorf("admin API returned %d", res.StatusCode)
	}
	var payload struct {
		RootCert string `json:"root_certificate"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		return nil, err
	}
	block, _ := pem.Decode([]byte(payload.RootCert))
	if block == nil {
		return nil, errors.New("failed to decode root certificate PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}
	return cert, nil
}
