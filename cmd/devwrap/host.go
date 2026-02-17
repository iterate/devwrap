package main

import (
	"errors"
	"strings"
)

func hostForApp(name, customHost string) (string, error) {
	if customHost == "" {
		return name + ".localhost", nil
	}
	host, err := normalizeHost(customHost)
	if err != nil {
		return "", err
	}
	return host, nil
}

func normalizeHost(raw string) (string, error) {
	host := strings.ToLower(strings.TrimSpace(raw))
	if host == "" {
		return "", errors.New("host cannot be empty")
	}
	if strings.Contains(host, "://") {
		return "", errors.New("host must be a hostname without scheme")
	}
	if strings.Contains(host, "/") {
		return "", errors.New("host must not include a path")
	}
	if strings.Contains(host, ":") {
		return "", errors.New("host must not include a port")
	}
	if strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") || strings.Contains(host, "..") {
		return "", errors.New("host format is invalid")
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" {
			return "", errors.New("host format is invalid")
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return "", errors.New("host labels cannot start or end with '-'")
		}
		for _, r := range label {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
				continue
			}
			return "", errors.New("host can use lowercase letters, numbers, dots, and dashes")
		}
	}
	return host, nil
}
