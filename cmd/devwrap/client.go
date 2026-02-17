package main

import (
	"net/http"
	"time"
)

var adminHTTPClient = &http.Client{Timeout: 4 * time.Second}

type Lease struct {
	Name     string `json:"name"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	HTTPURL  string `json:"http_url"`
	HTTPSURL string `json:"https_url"`
	Trusted  bool   `json:"trusted"`
}

type ProxyStatus struct {
	Running     bool   `json:"running"`
	CaddySource string `json:"caddy_source"`
	Root        bool   `json:"root"`
	HTTPPort    int    `json:"http_port"`
	HTTPSPort   int    `json:"https_port"`
	Trusted     bool   `json:"trusted"`
	PID         int    `json:"pid"`
	Apps        []App  `json:"apps"`
}

func apiClient() *http.Client {
	return adminHTTPClient
}

func acquireLease(name, host string, pid int) (Lease, error) {
	return requestLeaseDirect(name, host, pid)
}

func releaseLeaseSelected(name string, pid int) {
	releaseLeaseDirect(name, pid)
}
