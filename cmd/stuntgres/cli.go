package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type serveOptions struct {
	listen, dumpdir, logdir, userfile, pidfile string
	restore, verbose, background               bool
}
type cliCommand struct {
	command   string
	options   serveOptions
	username  string
	databases []string
	grantsSet bool
}

const usage = `Usage:
  stuntgres serve|start [-l ADDR] [-d DUMPDIR] [-L LOGDIR] -u USERFILE [-p PIDFILE] [-r] [-v] [-b]
  stuntgres dump|stop [-p PIDFILE]
  stuntgres passwd -u USERFILE [--databases DB1,DB2] USER

serve runs in the foreground; start (or serve -b) reexecutes in the background.
Listen defaults to 127.0.0.1:5434; a bare port binds loopback. -L selects logs.
-r restores the newest native SQL dump per database and requires -d.
USR1/dump triggers asynchronous dumps; stop waits for shutdown and final dumps.
passwd prompts twice; existing grants are preserved unless --databases is given.
All long aliases: --listen, --dumpdir, --logdir, --userfile, --pidfile,
--restore, --verbose, --background. Filesystem paths resolve before daemonizing.

PostgreSQL protocol 3.0/3.2 simple and extended queries, prepared statements,
portals and streaming COPY over the bounded in-memory SQL engine; native dumps.
WARNING: cleartext password authentication without TLS. Use loopback or a
trusted protected network; non-loopback listening exposes passwords and data.
`

func parseCLI(args []string, out io.Writer) (cliCommand, error) {
	var command cliCommand
	if len(args) == 0 {
		_, _ = io.WriteString(out, usage)
		return command, errors.New("a command is required")
	}
	if args[0] == "--help" || args[0] == "-h" {
		_, err := io.WriteString(out, usage)
		if err != nil {
			return command, err
		}
		return command, flag.ErrHelp
	}
	command.command = args[0]
	flags := flag.NewFlagSet(command.command, flag.ContinueOnError)
	flags.SetOutput(out)
	flags.Usage = func() { _, _ = io.WriteString(out, usage) }
	options := &command.options
	cwd, err := os.Getwd()
	if err != nil {
		return command, err
	}
	options.pidfile = filepath.Join(cwd, "stuntgres.pid")
	pathFlag := func(short, long string, target *string, initial, description string) {
		flags.StringVar(target, short, initial, description)
		flags.StringVar(target, long, initial, description)
	}
	boolFlag := func(short, long string, target *bool, description string) {
		flags.BoolVar(target, short, false, description)
		flags.BoolVar(target, long, false, description)
	}
	switch command.command {
	case "serve", "start":
		pathFlag("l", "listen", &options.listen, "127.0.0.1:5434", "listen address")
		pathFlag("d", "dumpdir", &options.dumpdir, "", "native SQL dump directory")
		pathFlag("L", "logdir", &options.logdir, "", "JSONL log directory")
		pathFlag("u", "userfile", &options.userfile, "", "password and database grant file")
		pathFlag("p", "pidfile", &options.pidfile, options.pidfile, "PID ownership file")
		boolFlag("r", "restore", &options.restore, "restore newest dumps")
		boolFlag("v", "verbose", &options.verbose, "enable DEBUG logs")
		boolFlag("b", "background", &options.background, "run in background")
	case "dump", "stop":
		pathFlag("p", "pidfile", &options.pidfile, options.pidfile, "PID ownership file")
	case "passwd":
		pathFlag("u", "userfile", &options.userfile, "", "password and database grant file")
		var databaseList string
		flags.StringVar(&databaseList, "databases", "", "exact comma-separated database grants")
		if err := flags.Parse(args[1:]); err != nil {
			return command, err
		}
		flags.Visit(func(f *flag.Flag) {
			if f.Name == "databases" {
				command.grantsSet = true
			}
		})
		if options.userfile == "" || flags.NArg() != 1 {
			return command, errors.New("passwd requires -u USERFILE and exactly one USER")
		}
		command.username = flags.Arg(0)
		if err := validName(command.username, false); err != nil {
			return command, err
		}
		if command.grantsSet {
			command.databases = strings.Split(databaseList, ",")
			if err := validateGrants(command.databases); err != nil {
				return command, err
			}
		}
		options.userfile, err = filepath.Abs(options.userfile)
		return command, err
	default:
		_, _ = io.WriteString(out, usage)
		return command, fmt.Errorf("unknown command %q", command.command)
	}
	if err := flags.Parse(args[1:]); err != nil {
		return command, err
	}
	if flags.NArg() != 0 {
		return command, errors.New("unexpected positional arguments")
	}
	if command.command == "serve" || command.command == "start" {
		if options.userfile == "" {
			return command, errors.New("serve/start requires -u USERFILE")
		}
		if options.restore && options.dumpdir == "" {
			return command, errors.New("restore requires a dump directory")
		}
		options.listen, err = normalizeListen(options.listen)
		if err != nil {
			return command, err
		}
		options.background = options.background || command.command == "start"
	}
	for _, path := range []*string{&options.dumpdir, &options.logdir, &options.userfile, &options.pidfile} {
		if *path != "" {
			*path, err = filepath.Abs(*path)
			if err != nil {
				return command, err
			}
		}
	}
	return command, nil
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
	return args
}
func runCLI(args []string) error {
	command, err := parseCLI(args, os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
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
