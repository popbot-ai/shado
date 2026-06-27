// shado - instant copy-on-write workspaces for very large projects.
//
// Project-level: create / recache / restore. Per-shadow: clone {create,reset,
// park,resume,rm}. Inspection: ls / json / doctor / version. shado never warms;
// you hand it a warm folder, it does the copy-on-write.
package main

import (
	"fmt"
	"os"
)

func usage() {
	fmt.Print(`shado - instant copy-on-write workspaces for very large projects

Usage:
  shado create <warm-folder> --name N [--count C] [--size-gb G] [--no-main]
  shado recache --name N [--force]
  shado restore --name N [--to <folder>] [--force]
  shado clone create  --name N --slot S [--mount PATH] [--hook CMD]
  shado clone reset   --name N --slot S
  shado clone park    --name N --slot S
  shado clone resume  --name N --slot S
  shado clone rm      --name N --slot S [--force]
  shado du [--name N]            disk usage: base + main clone + slot clones
  shado ls | json | doctor | version

Privileged operations need an elevated shell. Run 'shado doctor' first.
`)
}

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		usage()
		return
	}

	// global --verbose: echo child (powershell/robocopy/hook) output inline.
	if os.Getenv("SHADO_VERBOSE") == "1" {
		verbose = true
	}
	for _, a := range args {
		if a == "--verbose" {
			verbose = true
		}
	}

	switch args[0] {
	case "doctor":
		cmdDoctor()
	case "version", "--version", "-v":
		cmdVersion()
	case "help", "--help", "-h":
		usage()
	case "ls":
		cmdLs()
	case "du", "usage":
		f, _ := parseFlags(args[1:])
		cmdDu(f)
	case "json":
		cmdJSON()
	case "create":
		f, pos := parseFlags(args[1:])
		cmdCreate(f, pos)
	case "recache":
		f, _ := parseFlags(args[1:])
		cmdRecache(f)
	case "restore":
		f, _ := parseFlags(args[1:])
		cmdRestore(f)
	case "remount":
		f, _ := parseFlags(args[1:])
		cmdRemount(f)
	case "clone":
		if len(args) < 2 {
			fail("usage: shado clone <create|reset|park|resume|rm> --slot S")
		}
		f, _ := parseFlags(args[2:])
		switch args[1] {
		case "create":
			cmdCloneCreate(f)
		case "reset":
			cmdCloneReset(f)
		case "park":
			cmdClonePark(f)
		case "resume":
			cmdCloneResume(f)
		case "rm", "remove":
			cmdCloneRm(f)
		default:
			fail("unknown clone action %q (create|reset|park|resume|rm)", args[1])
		}
	default:
		fail("unknown command %q (try 'shado help')", args[0])
	}
}
