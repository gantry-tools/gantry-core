// Package cli defines the common, side-effect-free Gantry command grammar.
package cli

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Output string

const (
	Table Output = "table"
	JSON  Output = "json"
	Plain Output = "plain"
)

const (
	ExitOK          = 0
	ExitFailure     = 1
	ExitUsage       = 2
	ExitAuth        = 3
	ExitNotFound    = 4
	ExitConflict    = 5
	ExitUnavailable = 6
	ExitPartial     = 7
)

type Invocation struct {
	Resource    string
	Verb        string
	Arguments   []string
	Input       string
	Output      Output
	Quiet       bool
	Confirm     bool
	Timeout     time.Duration
	RequestID   string
	URL         string
	TokenFile   string
	SessionFile string
	Query       []string
}

var ErrHelp = errors.New("help requested")

func Parse(args []string) (Invocation, error) {
	inv := Invocation{Output: Table, Timeout: 30 * time.Second}
	positionals := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--help" || arg == "-h":
			return Invocation{}, ErrHelp
		case arg == "--json":
			if inv.Output != Table && inv.Output != JSON {
				return Invocation{}, errors.New("--json conflicts with --output")
			}
			inv.Output = JSON
		case arg == "--quiet" || arg == "-q":
			inv.Quiet = true
		case arg == "--yes" || arg == "-y":
			inv.Confirm = true
		case strings.HasPrefix(arg, "--output="):
			if err := setOutput(&inv, strings.TrimPrefix(arg, "--output=")); err != nil {
				return Invocation{}, err
			}
		case arg == "--output":
			value, next, err := flagValue(args, i, arg)
			if err != nil {
				return Invocation{}, err
			}
			i = next
			if err := setOutput(&inv, value); err != nil {
				return Invocation{}, err
			}
		case takesValue(arg):
			name, inline, hasInline := strings.Cut(arg, "=")
			value := inline
			if !hasInline {
				var err error
				value, i, err = flagValue(args, i, name)
				if err != nil {
					return Invocation{}, err
				}
			}
			if err := applyValue(&inv, name, value); err != nil {
				return Invocation{}, err
			}
		case strings.HasPrefix(arg, "-"):
			return Invocation{}, fmt.Errorf("unknown option %s", arg)
		default:
			positionals = append(positionals, arg)
		}
	}
	if len(positionals) < 2 {
		return Invocation{}, errors.New("resource and verb are required")
	}
	inv.Resource, inv.Verb = positionals[0], positionals[1]
	inv.Arguments = append([]string(nil), positionals[2:]...)
	if !word(inv.Resource) || !word(inv.Verb) {
		return Invocation{}, errors.New("resource and verb must be lowercase words")
	}
	if inv.Quiet && inv.Output == Table {
		inv.Output = Plain
	}
	return inv, nil
}

func takesValue(arg string) bool {
	name, _, _ := strings.Cut(arg, "=")
	switch name {
	case "--input", "--timeout", "--request-id", "--url", "--token-file", "--session-file", "--query":
		return true
	default:
		return false
	}
}

func flagValue(args []string, index int, name string) (string, int, error) {
	if index+1 >= len(args) || strings.HasPrefix(args[index+1], "-") && args[index+1] != "-" {
		return "", index, fmt.Errorf("%s requires a value", name)
	}
	return args[index+1], index + 1, nil
}

func setOutput(inv *Invocation, value string) error {
	output := Output(value)
	if output != Table && output != JSON && output != Plain {
		return fmt.Errorf("invalid output %q", value)
	}
	if inv.Output == JSON && output != JSON {
		return errors.New("--output conflicts with --json")
	}
	inv.Output = output
	return nil
}

func applyValue(inv *Invocation, name, value string) error {
	switch name {
	case "--input":
		inv.Input = value
	case "--timeout":
		duration, err := time.ParseDuration(value)
		if err != nil || duration <= 0 {
			return fmt.Errorf("invalid timeout %q", value)
		}
		inv.Timeout = duration
	case "--request-id":
		if len(value) > 128 || value == "" {
			return errors.New("request id must contain 1-128 characters")
		}
		inv.RequestID = value
	case "--url":
		inv.URL = strings.TrimRight(value, "/")
		if !strings.HasPrefix(inv.URL, "http://") && !strings.HasPrefix(inv.URL, "https://") {
			return errors.New("URL must use http or https")
		}
	case "--token-file":
		if value == "-" {
			return errors.New("token file cannot be stdin; stdin is reserved for operation input")
		}
		if inv.SessionFile != "" {
			return errors.New("--token-file conflicts with --session-file")
		}
		inv.TokenFile = value
	case "--query":
		if !strings.Contains(value, "=") || strings.HasPrefix(value, "=") {
			return errors.New("--query requires key=value")
		}
		inv.Query = append(inv.Query, value)
	case "--session-file":
		if value == "-" {
			return errors.New("session file cannot be stdin")
		}
		if inv.TokenFile != "" {
			return errors.New("--session-file conflicts with --token-file")
		}
		inv.SessionFile = value
	default:
		return fmt.Errorf("unsupported option %s", name)
	}
	return nil
}

func word(value string) bool {
	if value == "" {
		return false
	}
	for i, r := range value {
		if r >= 'a' && r <= 'z' || i > 0 && r >= '0' && r <= '9' || i > 0 && r == '-' {
			continue
		}
		return false
	}
	return true
}

func Usage(program string) string {
	return strings.Join([]string{
		"Usage: " + program + " <resource> <verb> [arguments] [options]",
		"  --input <file|->       strict JSON input (use - for stdin)",
		"  --output <mode>        table, json or plain",
		"  --json                 shorthand for --output json",
		"  --quiet                suppress non-result output",
		"  --yes                  accept declared non-interactive confirmation",
		"  --timeout <duration>   operation timeout (default 30s)",
		"  --request-id <id>      caller-supplied correlation/idempotency seed",
		"  --query <key=value>     repeatable query parameter",
		"  --url <http(s)://...>  remote application origin",
		"  --token-file <path>    protected remote API-token file",
		"  --session-file <path>  protected browser/CLI session file",
	}, "\n")
}

func ParseExitCode(value string) (int, error) {
	code, err := strconv.Atoi(value)
	if err != nil || code < 0 || code > 255 {
		return 0, errors.New("exit code must be 0-255")
	}
	return code, nil
}
