package main

import (
	"os"

	c "github.com/gookit/color"
	"github.com/katbyte/go-kt/clog"
	"github.com/katbyte/prawn/cli"
	"github.com/katbyte/prawn/cli/close"
	"github.com/katbyte/prawn/cli/label"
)

func main() {
	// the log level comes from PRAWN_LOG; read it once here, before anything logs
	clog.SetLevelFromEnv("PRAWN_LOG")

	// the command groups are wired here rather than in a package: prawn builds
	// the root, and each group hangs off it
	cmd, err := cli.Make()
	if err != nil {
		clog.Log.Error(c.Sprintf("<red>prawn: building cmd</> %v", err))
		os.Exit(1)
	}
	cmd.AddCommand(close.Command(), label.Command())

	if err := cmd.Execute(); err != nil {
		clog.Log.Error(c.Sprintf("<red>prawn:</> %v", err))
		os.Exit(1)
	}

	os.Exit(0)
}
