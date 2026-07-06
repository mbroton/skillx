// Command skillx manages AI agent skills from a private git repository.
package main

import (
	"os"

	"github.com/mbroton/skillx/internal/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
