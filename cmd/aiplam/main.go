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

// Command aiplam is the AI Playbook Manager: it registers AI playbooks (git checkouts of shared agent artifacts) and
// links their artifacts from the agents configuration folders that coding agents read.
package main

import (
	"fmt"
	"os"

	"arxila.io/aiplam/internal/app"
)

func main() {
	a, err := app.New()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %s\n", err)
		os.Exit(app.ExitFailure)
	}
	os.Exit(a.Run(os.Args[1:]))
}
