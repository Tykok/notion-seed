// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"os"

	"github.com/tykok/notion-seed/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		os.Exit(1)
	}
}
