package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func run(args []string) error {
	if os.Geteuid() == 0 && !wantsJSONArgs(args) && !(len(args) >= 2 && args[0] == "proxy" && args[1] == "daemon") {
		fmt.Fprintln(os.Stderr, "warning: running devwrap with sudo is discouraged; use `devwrap proxy start --privileged` instead")
	}

	root := newRootCommand()
	root.SetArgs(args)
	return root.Execute()
}

func newRootCommand() *cobra.Command {
	var name string
	var host string
	var privileged bool

	root := &cobra.Command{
		Use:           "devwrap --name <name> -- <cmd...>",
		Short:         "Local dev reverse proxy helper",
		Long:          "Run local apps behind Caddy and map routes to local app ports. Use @PORT in your command arguments to inject the allocated app port.",
		Example:       "  devwrap --name myapp -- pnpm dev\n  devwrap --name api -- uvicorn app:app --port @PORT\n  devwrap --name web --host web.dev.test -- pnpm dev\n  devwrap -p",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if privileged && name == "" && len(args) == 0 {
				return runProxyStart(true)
			}
			if name == "" {
				if !outputJSON {
					_ = cmd.Help()
				}
				return errors.New("--name is required")
			}
			if len(args) == 0 {
				if !outputJSON {
					_ = cmd.Help()
				}
				return errors.New("missing command after '--'")
			}
			return runApp(name, host, args, privileged)
		},
	}

	root.PersistentPreRun = func(cmd *cobra.Command, args []string) {
		outputJSON, _ = cmd.Flags().GetBool("json")
	}

	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		if !outputJSON {
			_ = cmd.Help()
		}
		return err
	})

	root.Flags().StringVar(&name, "name", "", "App route name (e.g. myapp)")
	root.Flags().StringVar(&host, "host", "", "Custom hostname (default: <name>.localhost)")
	root.Flags().BoolVarP(&privileged, "privileged", "p", false, "Use sudo to spawn proxy if Caddy is not already running")
	root.PersistentFlags().BoolVar(&outputJSON, "json", false, "Output JSON for scripting")

	root.AddCommand(newProxyCommand())
	root.AddCommand(newListCommand())
	root.AddCommand(newRemoveCommand())
	root.AddCommand(newDoctorCommand())

	return root
}

func newProxyCommand() *cobra.Command {
	proxy := &cobra.Command{
		Use:   "proxy",
		Short: "Manage proxy lifecycle",
	}

	var privileged bool
	start := &cobra.Command{
		Use:   "start",
		Short: "Start proxy if needed (managed mode)",
		Args:  helpOnArgValidationError(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runProxyStart(privileged)
		},
	}
	start.Flags().BoolVarP(&privileged, "privileged", "p", false, "Spawn proxy with sudo")

	stop := &cobra.Command{Use: "stop", Short: "Stop devwrap-managed proxy", Args: helpOnArgValidationError(cobra.NoArgs), RunE: func(cmd *cobra.Command, args []string) error { return runProxyStop() }}
	status := &cobra.Command{Use: "status", Short: "Show proxy status", Args: helpOnArgValidationError(cobra.NoArgs), RunE: func(cmd *cobra.Command, args []string) error { return runProxyStatus() }}
	trust := &cobra.Command{Use: "trust", Short: "Trust Caddy local CA", Args: helpOnArgValidationError(cobra.NoArgs), RunE: func(cmd *cobra.Command, args []string) error { return runProxyTrust() }}
	logs := &cobra.Command{Use: "logs", Short: "Show proxy logs", Args: helpOnArgValidationError(cobra.NoArgs), RunE: func(cmd *cobra.Command, args []string) error { return runProxyLogs() }}
	daemon := &cobra.Command{Use: "daemon", Hidden: true, Args: helpOnArgValidationError(cobra.NoArgs), RunE: func(cmd *cobra.Command, args []string) error { return runProxyDaemon() }}

	proxy.AddCommand(start, stop, status, trust, logs, daemon)
	return proxy
}

func newDoctorCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Show environment and health diagnostics",
		Args:  helpOnArgValidationError(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDoctor()
		},
	}
}

func newListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List registered apps",
		Args:  helpOnArgValidationError(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runList()
		},
	}
}

func newRemoveCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <name>",
		Short: "Remove app route",
		Args:  helpOnArgValidationError(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRemove(args[0])
		},
	}
}

func helpOnArgValidationError(next cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		err := next(cmd, args)
		if err != nil && !outputJSON {
			_ = cmd.Help()
		}
		return err
	}
}

func runApp(name, host string, cmdArgs []string, privileged bool) error {
	if err := validateName(name); err != nil {
		return err
	}

	resolvedHost, err := hostForApp(name, host)
	if err != nil {
		return err
	}

	if err := ensureCaddyOrDaemon(privileged); err != nil {
		return err
	}

	lease, err := acquireLease(name, resolvedHost, os.Getpid())
	if err != nil {
		if checkDaemonReachable() {
			if path, logErr := daemonLogPath(); logErr == nil {
				return fmt.Errorf("%w (logs: %s)", err, path)
			}
		}
		return err
	}

	if !lease.Trusted {
		if outputJSON {
			_ = emitJSON(map[string]any{
				"ok":        true,
				"action":    "run",
				"name":      name,
				"port":      lease.Port,
				"https_url": lease.HTTPSURL,
				"http_url":  lease.HTTPURL,
				"trusted":   lease.Trusted,
				"warnings": []string{
					"HTTPS cert is issued by Caddy Local Authority and is not trusted yet",
					"run: devwrap proxy trust",
					"or: sudo devwrap proxy trust",
				},
			})
		} else {
			fmt.Println("warning: HTTPS cert is issued by Caddy Local Authority and is not trusted yet")
			fmt.Println("run: devwrap proxy trust")
			fmt.Println("or:  sudo devwrap proxy trust")
		}
	} else if outputJSON {
		_ = emitJSON(map[string]any{
			"ok":        true,
			"action":    "run",
			"name":      name,
			"port":      lease.Port,
			"https_url": lease.HTTPSURL,
			"http_url":  lease.HTTPURL,
			"trusted":   lease.Trusted,
		})
	}

	if !outputJSON {
		fmt.Printf("%s -> %s\n", name, lease.HTTPSURL)
		fmt.Printf("http fallback: %s\n", lease.HTTPURL)
	}

	release := func() {
		releaseLeaseSelected(name, os.Getpid())
	}
	return runChild(name, cmdArgs, lease.Port, normalizeDevwrapHostURL(lease.HTTPSURL), release)
}

func wantsJSONArgs(args []string) bool {
	for _, a := range args {
		if a == "--json" {
			return true
		}
	}
	return false
}

func validateName(name string) error {
	if name == "" {
		return errors.New("app name cannot be empty")
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			continue
		}
		return errors.New("app name must use lowercase letters, numbers, or dashes")
	}
	if name[0] == '-' || name[len(name)-1] == '-' {
		return errors.New("app name cannot start or end with a dash")
	}
	return nil
}
