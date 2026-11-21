// =========================================================================
//
//   Copyright (c) 2026 Arxila OSS (https://arxila.io)
//
//   Licensed under the Apache License, Version 2.0 (the "License");
//   you may not use this file except in compliance with the License.
//   You may obtain a copy of the License at
//
//       http://www.apache.org/licenses/LICENSE-2.0
//
//   Unless required by applicable law or agreed to in writing, software
//   distributed under the License is distributed on an "AS IS" BASIS,
//   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or
//   implied. See the License for the specific language governing
//   permissions and limitations under the License.
//
// =========================================================================

package app

import (
	"fmt"
	"strings"
)

// usageError is an error in the command line; it makes aiplam exit with code 2.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

func usagef(format string, args ...any) error {
	return &usageError{msg: fmt.Sprintf(format, args...)}
}

// parsedArgs holds the result of parsing a command's arguments.
type parsedArgs struct {
	positional []string
	values     map[string][]string
	bools      map[string]bool
}

func (p parsedArgs) value(name string) string {
	if v := p.values[name]; len(v) > 0 {
		return v[len(v)-1]
	}
	return ""
}

// shortOptions maps the short forms of options ("-t", "-sp") to their long names. "-h" (help) is handled apart, and
// "-s" (set) is removed by Run before commands parse their arguments.
var shortOptions = map[string]string{
	"t":  "target",
	"sp": "skills-path",
}

// parseArgs parses GNU-style options ("--name value", "--name=value", boolean "--name", and the short forms in
// shortOptions, as "-t value" or "-t=value") mixed with positional arguments; "--" ends the options. valueFlags and
// boolFlags list the accepted option names, without dashes.
func parseArgs(args []string, valueFlags, boolFlags []string) (parsedArgs, error) {
	isValue := map[string]bool{}
	for _, f := range valueFlags {
		isValue[f] = true
	}
	isBool := map[string]bool{"help": true}
	for _, f := range boolFlags {
		isBool[f] = true
	}
	p := parsedArgs{values: map[string][]string{}, bools: map[string]bool{}}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			p.positional = append(p.positional, args[i+1:]...)
			break
		}
		if arg == "-h" {
			p.bools["help"] = true
			continue
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			p.positional = append(p.positional, arg)
			continue
		}
		var name, value string
		var hasValue bool
		shown := arg // the option as written, for messages
		if strings.HasPrefix(arg, "--") {
			name, value, hasValue = strings.Cut(arg[2:], "=")
		} else {
			var short string
			short, value, hasValue = strings.Cut(arg[1:], "=")
			long, ok := shortOptions[short]
			if !ok || !(isValue[long] || isBool[long]) {
				return p, usagef("unknown option: -%s (to give an argument starting with '-', put it after '--')", short)
			}
			name = long
			shown = "-" + short
		}
		if hasValue {
			shown, _, _ = strings.Cut(shown, "=")
		}
		switch {
		case isValue[name]:
			if !hasValue {
				if i+1 >= len(args) {
					return p, usagef("option %s needs a value", shown)
				}
				i++
				value = args[i]
			}
			p.values[name] = append(p.values[name], value)
		case isBool[name]:
			if hasValue {
				return p, usagef("option %s takes no value", shown)
			}
			p.bools[name] = true
		default:
			return p, usagef("unknown option: --%s", name)
		}
	}
	return p, nil
}
