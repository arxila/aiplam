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
	"strings"

	"arxila.io/aiplam/internal/config"
)

const setsUsage = `Usage: aiplam sets

Lists the configuration sets (the files ~/.aiplam/<set>.yaml), with their targets and number of playbooks. Each set
is an independent configuration; commands act on the 'default' set unless given --set SET, and 'aiplam init --set SET'
creates a new one.
`

func (a *App) cmdSets(args []string) error {
	p, err := parseArgs(args, nil, nil)
	if err != nil {
		return err
	}
	if len(p.positional) > 0 {
		return usagef("unexpected argument: %s", p.positional[0])
	}
	sets, err := config.Sets(a.Home)
	if err != nil {
		return err
	}
	if len(sets) == 0 {
		a.printf("No sets exist yet: create the default one with 'aiplam init'.\n")
		return nil
	}
	for _, set := range sets {
		label := set
		if set == config.DefaultSet {
			label += " (default)"
		}
		file := a.pretty(config.File(a.Home, set))
		cfg, err := config.Load(a.Home, set)
		if err != nil {
			a.printf("%-20s %s: cannot be read (%s); see 'aiplam check --set %s'\n", label, file, err, set)
			continue
		}
		var targets []string
		for _, t := range cfg.TargetDirs(a.Home) {
			targets = append(targets, a.pretty(t))
		}
		a.printf("%-20s %d playbook(s), targets: %s\n", label, len(cfg.Playbooks), strings.Join(targets, ", "))
	}
	return nil
}
