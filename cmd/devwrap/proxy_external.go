package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

const caddyAdminBase = "http://127.0.0.1:2019"
const devwrapInternalTLSPolicyID = "devwrap-internal-policy"

type externalCaddyInfo struct {
	Available bool
	HTTPPort  int
	HTTPSPort int
	Managed   bool
}

func inspectExternalCaddy() (externalCaddyInfo, error) {
	servers, err := fetchExternalServers()
	if err != nil {
		return externalCaddyInfo{}, err
	}
	httpPort, httpsPort, _, _, err := parseExternalServers(servers)
	if err != nil {
		return externalCaddyInfo{}, err
	}
	_, managed := servers["devwrap-http"]
	return externalCaddyInfo{Available: true, HTTPPort: httpPort, HTTPSPort: httpsPort, Managed: managed}, nil
}

func applyRoutesViaAdmin(apps map[string]App) (int, int, error) {
	servers, err := fetchExternalServers()
	if err != nil {
		return 0, 0, err
	}
	httpPort, httpsPort, httpName, httpsName, err := parseExternalServers(servers)
	if err != nil {
		return 0, 0, err
	}

	devwrapRoutes := makeDevwrapRoutes(apps)

	httpRoutes, err := mergeExternalRoutes(servers[httpName], devwrapRoutes)
	if err != nil {
		return 0, 0, err
	}
	if err := putExternalRoutes(httpName, httpRoutes); err != nil {
		return 0, 0, err
	}

	if httpsName != "" {
		httpsRoutes, err := mergeExternalRoutes(servers[httpsName], devwrapRoutes)
		if err != nil {
			return 0, 0, err
		}
		if err := putExternalRoutes(httpsName, httpsRoutes); err != nil {
			return 0, 0, err
		}
	}

	if err := syncDevwrapInternalTLSPolicy(apps); err != nil {
		return 0, 0, err
	}

	return httpPort, httpsPort, nil
}

func syncDevwrapInternalTLSPolicy(apps map[string]App) error {
	subjectSet := make(map[string]struct{}, len(apps))
	for _, app := range apps {
		subject := tlsSubjectForHost(app.Host)
		subjectSet[subject] = struct{}{}
	}
	subjects := make([]string, 0, len(subjectSet))
	for subject := range subjectSet {
		subjects = append(subjects, subject)
	}
	sort.Strings(subjects)

	policies, found, err := fetchTLSAutomationPolicies()
	if err != nil {
		return err
	}

	merged := mergeDevwrapInternalTLSPolicy(policies, subjects)
	if found {
		return putTLSAutomationPolicies(merged)
	}
	if len(subjects) == 0 {
		return nil
	}
	return createTLSAppWithPolicies(merged)
}

func tlsSubjectForHost(host string) string {
	h := strings.ToLower(strings.TrimSpace(host))
	if i := strings.IndexByte(h, '.'); i > 0 && i < len(h)-1 {
		return "*." + h[i+1:]
	}
	return h
}

func fetchTLSAutomationPolicies() ([]any, bool, error) {
	res, err := adminGet("/config/apps/tls/automation/policies")
	if err != nil {
		return nil, false, err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return nil, false, nil
	}
	if res.StatusCode >= 300 {
		return nil, false, fmt.Errorf("caddy TLS policy query failed: %s", adminReadBody(res))
	}
	var policies []any
	if err := json.NewDecoder(res.Body).Decode(&policies); err != nil {
		return nil, false, err
	}
	return policies, true, nil
}

func mergeDevwrapInternalTLSPolicy(existing []any, hosts []string) []any {
	out := make([]any, 0, len(existing)+1)
	if len(hosts) > 0 {
		out = append(out, map[string]any{
			"@id":      devwrapInternalTLSPolicyID,
			"subjects": hosts,
			"issuers":  []map[string]any{{"module": "internal"}},
		})
	}
	for _, policyAny := range existing {
		policy, ok := policyAny.(map[string]any)
		if !ok {
			out = append(out, policyAny)
			continue
		}
		id, _ := policy["@id"].(string)
		if id == devwrapInternalTLSPolicyID {
			continue
		}
		out = append(out, policyAny)
	}
	return out
}

func putTLSAutomationPolicies(policies []any) error {
	path := "/config/apps/tls/automation/policies"
	res, err := adminDoJSON(http.MethodPatch, path, policies)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		body := adminReadBody(res)

		if deleteReq, deleteErr := http.NewRequest(http.MethodDelete, adminURL(path), nil); deleteErr == nil {
			if deleteRes, doErr := apiClient().Do(deleteReq); doErr == nil {
				_ = deleteRes.Body.Close()
			}
		}

		createRes, createErr := adminDoJSON(http.MethodPut, path, policies)
		if createErr == nil {
			defer createRes.Body.Close()
			if createRes.StatusCode < 300 {
				return nil
			}
			return fmt.Errorf("caddy TLS policy update failed: %s", adminReadBody(createRes))
		}

		return fmt.Errorf("caddy TLS policy update failed: %s", body)
	}
	return nil
}

func createTLSAppWithPolicies(policies []any) error {
	res, err := adminDoJSON(http.MethodPut, "/config/apps/tls", map[string]any{
		"automation": map[string]any{"policies": policies},
	})
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return fmt.Errorf("caddy TLS app create failed: %s", adminReadBody(res))
	}
	return nil
}

func makeDevwrapRoutes(apps map[string]App) []map[string]any {
	names := make([]string, 0, len(apps))
	for name := range apps {
		names = append(names, name)
	}
	sort.Strings(names)

	routes := make([]map[string]any, 0, len(names))
	for _, name := range names {
		app := apps[name]
		routes = append(routes, map[string]any{
			"@id":   "devwrap-" + app.Name,
			"match": []map[string]any{{"host": []string{app.Host}}},
			"handle": []map[string]any{{
				"handler":   "reverse_proxy",
				"upstreams": []map[string]any{{"dial": fmt.Sprintf("127.0.0.1:%d", app.Port)}},
			}},
		})
	}
	return routes
}

func mergeExternalRoutes(server map[string]any, devwrapRoutes []map[string]any) ([]any, error) {
	existingAny := server["routes"]
	existing, _ := existingAny.([]any)
	out := make([]any, 0, len(existing)+len(devwrapRoutes))
	for _, route := range existing {
		routeMap, ok := route.(map[string]any)
		if !ok {
			out = append(out, route)
			continue
		}
		id, _ := routeMap["@id"].(string)
		if strings.HasPrefix(id, "devwrap-") {
			continue
		}
		out = append(out, route)
	}
	for _, route := range devwrapRoutes {
		out = append(out, route)
	}
	return out, nil
}

func fetchExternalServers() (map[string]map[string]any, error) {
	res, err := adminGet("/config/apps/http/servers")
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		b, _ := io.ReadAll(res.Body)
		return nil, fmt.Errorf("caddy admin query failed: %s", strings.TrimSpace(string(b)))
	}
	var raw map[string]any
	if err := json.NewDecoder(res.Body).Decode(&raw); err != nil {
		return nil, err
	}
	servers := make(map[string]map[string]any, len(raw))
	for k, v := range raw {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		servers[k] = m
	}
	if len(servers) == 0 {
		return nil, errors.New("caddy has no HTTP servers configured")
	}
	return servers, nil
}

func parseExternalServers(servers map[string]map[string]any) (int, int, string, string, error) {
	if server, ok := servers["devwrap-http"]; ok {
		httpPort := firstListenPort(server)
		if httpPort == 0 {
			httpPort = 80
		}
		httpsPort := 443
		httpsName := ""
		if httpsServer, ok := servers["devwrap-https"]; ok {
			httpsName = "devwrap-https"
			if p := firstListenPort(httpsServer); p > 0 {
				httpsPort = p
			}
		}
		if httpsName == "" {
			httpsPort = httpPort
		}
		return httpPort, httpsPort, "devwrap-http", httpsName, nil
	}

	httpName := ""
	httpsName := ""
	httpPort := 80
	httpsPort := 443

	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		server := servers[name]
		port := firstListenPort(server)
		if isTLSServer(server) || hasListenPort(server, 443) {
			if httpsName == "" {
				httpsName = name
				if port > 0 {
					httpsPort = port
				}
			}
			continue
		}
		if httpName == "" {
			httpName = name
			if port > 0 {
				httpPort = port
			}
		}
	}

	if httpName == "" && httpsName == "" {
		return 0, 0, "", "", errors.New("unable to determine caddy server ports")
	}
	if httpName == "" {
		httpName = httpsName
	}
	if httpsName == "" {
		httpsName = httpName
	}
	return httpPort, httpsPort, httpName, httpsName, nil
}

func isTLSServer(server map[string]any) bool {
	v, ok := server["tls_connection_policies"]
	if !ok {
		return false
	}
	arr, ok := v.([]any)
	return ok && len(arr) > 0
}

func firstListenPort(server map[string]any) int {
	listenAny, ok := server["listen"]
	if !ok {
		return 0
	}
	listen, ok := listenAny.([]any)
	if !ok || len(listen) == 0 {
		return 0
	}
	s, ok := listen[0].(string)
	if !ok {
		return 0
	}
	if strings.HasPrefix(s, ":") {
		n, _ := strconv.Atoi(strings.TrimPrefix(s, ":"))
		return n
	}
	parts := strings.Split(s, ":")
	if len(parts) == 0 {
		return 0
	}
	n, _ := strconv.Atoi(parts[len(parts)-1])
	return n
}

func hasListenPort(server map[string]any, want int) bool {
	listenAny, ok := server["listen"]
	if !ok {
		return false
	}
	listen, ok := listenAny.([]any)
	if !ok {
		return false
	}
	for _, item := range listen {
		s, ok := item.(string)
		if !ok {
			continue
		}
		if parseListenPort(s) == want {
			return true
		}
	}
	return false
}

func parseListenPort(s string) int {
	if strings.HasPrefix(s, ":") {
		n, _ := strconv.Atoi(strings.TrimPrefix(s, ":"))
		return n
	}
	parts := strings.Split(s, ":")
	if len(parts) == 0 {
		return 0
	}
	n, _ := strconv.Atoi(parts[len(parts)-1])
	return n
}

func putExternalRoutes(serverName string, routes []any) error {
	path := "/config/apps/http/servers/" + serverName + "/routes"
	res, err := adminDoJSON("PATCH", path, routes)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		body := adminReadBody(res)

		if deleteReq, deleteErr := http.NewRequest("DELETE", adminURL(path), nil); deleteErr == nil {
			if deleteRes, doErr := apiClient().Do(deleteReq); doErr == nil {
				_ = deleteRes.Body.Close()
			}
		}

		createRes, createErr := adminDoJSON("PUT", path, routes)
		if createErr == nil {
			defer createRes.Body.Close()
			if createRes.StatusCode < 300 {
				return nil
			}
			return fmt.Errorf("caddy routes update failed: %s", adminReadBody(createRes))
		}

		return fmt.Errorf("caddy routes update failed: %s", body)
	}
	return nil
}
