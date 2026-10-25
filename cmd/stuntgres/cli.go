package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type serveOptions struct {
	listen, dumpdir, logdir, userfile, pidfile string
	restore, verbose, background, querylog     bool
	references                                 map[string]referenceConfig // nil unless dual-backend mode is configured
	referenceTimeout                           time.Duration
}
type cliCommand struct {
	command   string
	options   serveOptions
	username  string
	databases []string
	grantsSet bool
}

// errHelp reports that help was printed and no command should run.
var errHelp = errors.New("help requested")

const rootDescription = `stuntgres serves the embedded SQL engine over the PostgreSQL wire protocol:
protocol 3.0/3.2 simple and extended queries, prepared statements, portals and
streaming COPY over the bounded in-memory engine, with native SQL dumps.

Options resolve in this order: command-line flag, STUNTGRES_<OPTION>
environment variable (for example STUNTGRES_LISTEN or STUNTGRES_USERFILE),
--config FILE (YAML, TOML or JSON keyed by long option names), default.
Filesystem paths resolve before daemonizing.

WARNING: cleartext password authentication without TLS. Use loopback or a
trusted protected network; non-loopback listening exposes passwords and data.`

const serveDescription = `serve runs in the foreground; start (or serve -b) reexecutes in the background.
Listen defaults to 127.0.0.1:5434; a bare port binds loopback. -L selects logs.
-r restores the newest native SQL dump per database and requires -d.
-q writes every command cycle with its exact responses to query.log; requires -L.
--reference DB=URL (repeatable; requires -q) also sends every command for DB to
the PostgreSQL server at URL (postgres://user[:password]@host[:port][/dbname]
[?sslmode=disable|require|verify-full]) and logs both responses with their
differences. Without a URL password, PGPASSFILE or ~/.pgpass supplies it.`

// parseCLI resolves args, environment and the optional config file into a
// command without running it. Help output returns errHelp.
func parseCLI(args []string, out io.Writer) (cliCommand, error) {
	var command cliCommand
	cwd, err := os.Getwd()
	if err != nil {
		return command, err
	}
	root := newRootCommand(&command, filepath.Join(cwd, "stuntgres.pid"))
	root.SetArgs(args)
	root.SetOut(out)
	root.SetErr(out)
	if len(args) == 0 {
		_ = root.Help()
		return command, errors.New("a command is required")
	}
	if err := root.Execute(); err != nil {
		return command, err
	}
	if command.command == "" {
		return command, errHelp
	}
	return command, nil
}

func newRootCommand(command *cliCommand, pidfile string) *cobra.Command {
	root := &cobra.Command{
		Use:           "stuntgres",
		Short:         "PostgreSQL wire-protocol server for the stuntgres engine",
		Long:          rootDescription,
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return fmt.Errorf("%w (see %q)", err, cmd.CommandPath()+" --help")
	})
	for _, name := range []string{"serve", "start"} {
		root.AddCommand(newServeCommand(name, command, pidfile))
	}
	root.AddCommand(newControlCommand("dump", "Request an asynchronous dump from the running server (SIGUSR1)", command, pidfile))
	root.AddCommand(newControlCommand("stop", "Stop the running server; waits for shutdown and final dumps", command, pidfile))
	root.AddCommand(newPasswdCommand(command))
	return root
}

func newServeCommand(name string, command *cliCommand, pidfile string) *cobra.Command {
	short := "Serve in the foreground"
	if name == "start" {
		short = "Serve in the background and wait for readiness"
	}
	cmd := &cobra.Command{
		Use:   name + " -u USERFILE",
		Short: short,
		Long:  serveDescription,
		Args:  cobra.NoArgs,
	}
	flags := cmd.Flags()
	flags.StringP(keyListen, "l", "127.0.0.1:5434", "listen address")
	flags.StringP(keyDumpdir, "d", "", "native SQL dump directory")
	flags.StringP(keyLogdir, "L", "", "JSONL log directory")
	flags.StringP(keyUserfile, "u", "", "password and database grant file")
	flags.StringP(keyPidfile, "p", pidfile, "PID ownership file")
	flags.BoolP(keyRestore, "r", false, "restore newest dumps")
	flags.BoolP(keyVerbose, "v", false, "enable DEBUG logs")
	flags.BoolP(keyBackground, "b", false, "run in background")
	flags.BoolP(keyQuerylog, "q", false, "log queries with exact responses")
	flags.StringArray(keyReference, nil, "compare database DB with PostgreSQL at URL (DB=URL, repeatable)")
	flags.String(keyReferenceTimeout, defaultReferenceTimeout.String(), "longest wait for a reference response")
	addConfigFlag(flags)
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		options, err := resolveServeOptions(cmd.Flags())
		if err != nil {
			return err
		}
		options.background = options.background || name == "start"
		command.command, command.options = name, options
		return nil
	}
	return cmd
}

func newControlCommand(name, short string, command *cliCommand, pidfile string) *cobra.Command {
	cmd := &cobra.Command{Use: name, Short: short, Args: cobra.NoArgs}
	cmd.Flags().StringP(keyPidfile, "p", pidfile, "PID ownership file")
	addConfigFlag(cmd.Flags())
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		config, err := newConfig(cmd.Flags(), keyPidfile)
		if err != nil {
			return err
		}
		path, err := config.path(keyPidfile)
		if err != nil {
			return err
		}
		command.command, command.options.pidfile = name, path
		return nil
	}
	return cmd
}

func newPasswdCommand(command *cliCommand) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "passwd -u USERFILE [--databases DB1,DB2] USER",
		Short: "Set an account password and database grants",
		Long: `passwd prompts twice; existing grants are preserved unless --databases is given.
New accounts require --databases.`,
		Args: cobra.ExactArgs(1),
	}
	cmd.Flags().StringP(keyUserfile, "u", "", "password and database grant file")
	cmd.Flags().String("databases", "", "exact comma-separated database grants")
	addConfigFlag(cmd.Flags())
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		config, err := newConfig(cmd.Flags(), keyUserfile)
		if err != nil {
			return err
		}
		userfile, err := config.path(keyUserfile)
		if err != nil {
			return err
		}
		if userfile == "" {
			return errors.New("passwd requires -u USERFILE")
		}
		if err := validName(args[0], false); err != nil {
			return err
		}
		command.grantsSet = cmd.Flags().Changed("databases")
		if command.grantsSet {
			list, _ := cmd.Flags().GetString("databases")
			command.databases = strings.Split(list, ",")
			if err := validateGrants(command.databases); err != nil {
				return err
			}
		}
		command.command, command.username, command.options.userfile = "passwd", args[0], userfile
		return nil
	}
	return cmd
}

func addConfigFlag(flags *pflag.FlagSet) {
	flags.String(configFlag, "", "configuration file (YAML, TOML or JSON)")
}

func resolveServeOptions(flags *pflag.FlagSet) (serveOptions, error) {
	var options serveOptions
	config, err := newConfig(flags, serveKeys...)
	if err != nil {
		return options, err
	}
	for _, field := range []struct {
		key    string
		target *bool
	}{{keyRestore, &options.restore}, {keyVerbose, &options.verbose}, {keyBackground, &options.background}, {keyQuerylog, &options.querylog}} {
		if *field.target, err = config.boolean(field.key); err != nil {
			return options, err
		}
	}
	if options.listen, err = config.text(keyListen); err != nil {
		return options, err
	}
	for _, field := range []struct {
		key    string
		target *string
	}{{keyDumpdir, &options.dumpdir}, {keyLogdir, &options.logdir}, {keyUserfile, &options.userfile}, {keyPidfile, &options.pidfile}} {
		if *field.target, err = config.path(field.key); err != nil {
			return options, err
		}
	}
	if options.userfile == "" {
		return options, errors.New("serve/start requires -u USERFILE")
	}
	if options.restore && options.dumpdir == "" {
		return options, errors.New("restore requires a dump directory")
	}
	if options.querylog && options.logdir == "" {
		return options, errors.New("querylog requires a log directory")
	}
	specs, err := config.list(keyReference)
	if err != nil {
		return options, err
	}
	if options.references, err = parseReferences(specs); err != nil {
		return options, err
	}
	if options.references != nil && !options.querylog {
		return options, errors.New("reference requires querylog")
	}
	timeout, err := config.text(keyReferenceTimeout)
	if err != nil {
		return options, err
	}
	if options.referenceTimeout, err = time.ParseDuration(timeout); err != nil {
		return options, fmt.Errorf("invalid %s value %q", keyReferenceTimeout, timeout)
	}
	if err := validateReferenceTimeout(options.referenceTimeout); err != nil {
		return options, err
	}
	options.listen, err = normalizeListen(options.listen)
	return options, err
}

func normalizeListen(address string) (string, error) {
	if !strings.Contains(address, ":") {
		address = net.JoinHostPort("127.0.0.1", address)
	}
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return "", fmt.Errorf("invalid listen address: %w", err)
	}
	if port == "" {
		return "", errors.New("listen port is required")
	}
	for _, c := range port {
		if c < '0' || c > '9' {
			return "", errors.New("listen port must be numeric")
		}
	}
	n, err := strconv.Atoi(port)
	if err != nil || n > 65535 {
		return "", errors.New("listen port must be between 0 and 65535")
	}
	return address, nil
}

// serveArguments is the daemon child's complete configuration; the child
// environment carries no configuration variables (see childEnvironment).
func serveArguments(options serveOptions) []string {
	args := []string{"serve", "--listen", options.listen, "--userfile", options.userfile, "--pidfile", options.pidfile}
	if options.dumpdir != "" {
		args = append(args, "--dumpdir", options.dumpdir)
	}
	if options.logdir != "" {
		args = append(args, "--logdir", options.logdir)
	}
	if options.restore {
		args = append(args, "--restore")
	}
	if options.verbose {
		args = append(args, "--verbose")
	}
	if options.querylog {
		args = append(args, "--querylog")
	}
	for _, spec := range referenceSpecs(options.references) {
		args = append(args, "--reference", spec)
	}
	return append(args, "--reference-timeout", options.referenceTimeout.String())
}

func runCLI(args []string) error {
	command, err := parseCLI(args, os.Stderr)
	if errors.Is(err, errHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	switch command.command {
	case "passwd":
		password, err := readPasswordPair(os.Stdin, os.Stderr)
		if err != nil {
			return err
		}
		return updateAccount(command.options.userfile, command.username, password, command.databases, command.grantsSet)
	case "dump", "stop":
		return controlProcess(command.command, command.options.pidfile)
	case "serve", "start":
		if command.options.background {
			return background(command.options)
		}
		return runServe(command.options)
	}
	return errors.New("unreachable command")
}
