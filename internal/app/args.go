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
	"errors"
	"fmt"
	"strings"
	"unicode"

	"arxila.io/aiplam/internal/config"
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

// shortOptions maps the short forms of the commands' options to their long names. "-h" (help) is handled apart, and
// "-p" (--profile) and "-u" (--user) are removed by Run before commands parse their arguments.
var shortOptions = map[string]string{
	"a": "agent",
	"f": "folder",
}

// parseArgs parses GNU-style options ("--name value", "--name=value", boolean "--name", and the short forms in
// shortOptions, as "-a value" or "-a=value") mixed with positional arguments; "--" ends the options. valueFlags and
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

// splitQuoted splits s at every sep found outside double or single quotes. With keepQuotes, the quotes are kept in
// the parts (so that they can be split again); otherwise they are removed.
func splitQuoted(s string, sep rune, keepQuotes bool) ([]string, error) {
	var parts []string
	var cur strings.Builder
	var quote rune
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
				if keepQuotes {
					cur.WriteRune(r)
				}
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote = r
			if keepQuotes {
				cur.WriteRune(r)
			}
		case r == sep:
			parts = append(parts, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quote in '%s'", s)
	}
	return append(parts, cur.String()), nil
}

// folderItems splits the values of --folder options, each a comma-separated list (commas inside quotes do not
// separate), into their items, each split at its colons (colons inside quotes do not separate), with quotes removed.
func folderItems(values []string) ([][]string, error) {
	var items [][]string
	for _, v := range values {
		list, err := splitQuoted(v, ',', true)
		if err != nil {
			return nil, usagef("--folder: %s", err)
		}
		for _, item := range list {
			if strings.TrimSpace(item) == "" {
				return nil, usagef("--folder '%s' has an empty item", v)
			}
			parts, err := splitQuoted(strings.TrimSpace(item), ':', false)
			if err != nil {
				return nil, usagef("--folder: %s", err)
			}
			items = append(items, parts)
		}
	}
	return items, nil
}

// parseTargets parses the --folder options of init and profile add: "<kind>:<targetfolder>" items, for the user
// managed folder when user is set, on the operating system goos (%APPDATA% is only allowed on Windows).
func parseTargets(values []string, user bool, goos string) (config.Targets, error) {
	items, err := folderItems(values)
	if err != nil {
		return nil, err
	}
	var targets config.Targets
	for _, parts := range items {
		if path, ok := drivePath(parts); ok {
			return nil, usagef("--folder %s: target folder '%s' must be a relative path", parts[0], path)
		}
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return nil, usagef("--folder '%s' must be <kind>:<targetfolder>, as in 'skills:.agents/skills'",
				strings.Join(parts, ":"))
		}
		if first, _, _ := strings.Cut(strings.ReplaceAll(parts[1], `\`, "/"), "/"); first == config.AppData &&
			goos != "windows" {
			return nil, usagef("--folder: target folder '%s': %s only exists on Windows", parts[1], config.AppData)
		}
		targets = append(targets, config.Target{Kind: parts[0], Folder: parts[1]})
	}
	if errs := config.CheckTargets(targets, user); len(errs) > 0 {
		return nil, usagef("--folder: %s", errors.Join(errs...))
	}
	for i := range targets {
		targets[i].Folder, _ = config.CleanTarget(targets[i].Folder, user)
	}
	return targets, nil
}

// parseMappings parses the --folder options of add: "<kind>:<subfolder>[:<format>]" items.
func parseMappings(values []string) (map[string]config.Mapping, error) {
	items, err := folderItems(values)
	if err != nil {
		return nil, err
	}
	mappings := map[string]config.Mapping{}
	for _, parts := range items {
		if path, ok := drivePath(parts); ok {
			return nil, usagef("--folder %s: playbook subfolder '%s' must be a relative path", parts[0], path)
		}
		if len(parts) < 2 || len(parts) > 3 || parts[0] == "" {
			return nil, usagef("--folder '%s' must be <kind>:<subfolder>[:<format>], as in 'skills:.' or "+
				"'subagents:agents:*.md'", strings.Join(parts, ":"))
		}
		kind := parts[0]
		if !config.ValidKind(kind) {
			return nil, usagef("--folder: invalid kind '%s'", kind)
		}
		if _, dup := mappings[kind]; dup {
			return nil, usagef("--folder: kind '%s' is given more than once", kind)
		}
		folder, err := config.CleanSubfolder(parts[1])
		if err != nil {
			return nil, usagef("--folder %s: %s", kind, err)
		}
		m := config.Mapping{Folder: folder}
		if len(parts) == 3 {
			if err := config.CheckFormat(parts[2]); err != nil {
				return nil, usagef("--folder %s: %s", kind, err)
			}
			m.Format = parts[2]
		}
		mappings[kind] = m
	}
	return mappings, nil
}

// drivePath reports whether the parts of a --folder item, after the kind, are an absolute Windows path, split at its
// drive letter's colon ("skills:C:\x" gives "skills", "C" and "\x"), and returns it whole.
func drivePath(parts []string) (string, bool) {
	if len(parts) < 3 || len(parts[1]) != 1 || !unicode.IsLetter(rune(parts[1][0])) ||
		!strings.HasPrefix(parts[2], `\`) && !strings.HasPrefix(parts[2], "/") {
		return "", false
	}
	return strings.Join(parts[1:], ":"), true
}

// profileNameList parses profile names given as arguments, each possibly a comma-separated list, dropping repetitions.
func profileNameList(args []string) ([]string, error) {
	var names []string
	for _, arg := range args {
		for _, name := range strings.Split(arg, ",") {
			name = strings.TrimSpace(name)
			if !config.ValidProfileName(name) {
				return nil, invalidProfileName(name)
			}
			dup := false
			for _, n := range names {
				dup = dup || strings.EqualFold(n, name)
			}
			if !dup {
				names = append(names, name)
			}
		}
	}
	return names, nil
}

func invalidProfileName(name string) error {
	return usagef("invalid profile name '%s': use letters, digits, '_' and '-', starting with a letter or digit ('%s' is "+
		"reserved)", name, config.ConfigName)
}
