// Copyright (c) 2025 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"git-change-exec/pkg"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/spf13/cobra"
)

var dryRun = false
var forceRun = false

func main() {
	lintSpdx := &pkg.LintSpdx{}
	internalActions := map[string]pkg.Action{
		lintSpdx.Id(): lintSpdx,
	}

	rootCmd := cobra.Command{}
	baseCommitCmd := cobra.Command{
		Args: cobra.MinimumNArgs(0),
		Use:  "base-commit",
		Run: func(cmd *cobra.Command, args []string) {
			var err error
			gce := pkg.NewGitChangeExec()

			gce.G, err = git.PlainOpenWithOptions("./", &git.PlainOpenOptions{DetectDotGit: true})
			if err != nil {
				log.Fatalf("open git path %s failed: %v", gce.GitPath, err)
			}
			gce.GoToGitRootDir()
			defer gce.ChangeBackDir()

			gce.FetchOrigin()

			gce.CalculateBaseCommit()

			fmt.Printf("baseCommit: %s\n", gce.BaseCommit())
		},
	}
	runCmd := cobra.Command{
		Args: cobra.MinimumNArgs(1),
		Use:  "run <path>",
		Run: func(_ *cobra.Command, args []string) {
			var err error

			gce := pkg.NewGitChangeExec()

			gce.G, err = git.PlainOpenWithOptions("./", &git.PlainOpenOptions{DetectDotGit: true})
			if err != nil {
				log.Fatalf("open git path %s failed: %v", gce.GitPath, err)
			}
			luaFiles := make([]string, 0)
			for _, path := range args {
				var luaPath string

				if strings.HasPrefix(path, "lua:") {
					luaPath = strings.TrimPrefix(path, "lua:")
				}
				if !strings.Contains(path, ":") {
					_, err := os.Stat(path)
					if err == nil {
						luaPath = path
					}
				}

				if luaPath != "" {
					actionsPath, err := filepath.Abs(luaPath)
					if err != nil {
						log.Fatalf("could not get absolute path of %s: %v", path, err)
					}
					luaFiles = append(luaFiles, pkg.ListLuaActions(actionsPath)...)
				}
				if strings.HasPrefix(path, "gce:") {
					name := strings.TrimPrefix(path, "gce:")
					action, found := internalActions[name]
					if !found {
						log.Fatalf("could not find action '%s'", path)
					}
					gce.ActionsToCheck = append(gce.ActionsToCheck, action)
				}
			}
			for _, luaFile := range luaFiles {
				content, err := os.ReadFile(luaFile)
				if err != nil {
					log.Fatalf("could not read file %s: %v", luaFile, err)
				}
				log.Printf("Loading %s ...\n", luaFile)
				la := pkg.LuaLoad(luaFile, string(content))
				gce.ActionsToCheck = append(gce.ActionsToCheck, la)
			}

			gce.GoToGitRootDir()
			defer gce.ChangeBackDir()

			if len(gce.ActionsToCheck) == 0 {
				fmt.Printf("no actions to check\n")
				os.Exit(0)
			}

			log.Printf("Running ...")
			if forceRun {
				gce.ForceRunActionDos()
				return
			}

			gce.FetchOrigin()

			gce.CalculateBaseCommit()
			gce.CollectActionsGitTree()
			gce.CollectDirtyGitTree()

			gce.Diff()
			if dryRun {
				gce.DryRunActionDos()
				return
			}

			gce.RunActionDos()

		},
	}
	parseCmd := cobra.Command{
		Args: cobra.ExactArgs(1),
		Use:  "parse <path>",
		Run: func(_ *cobra.Command, args []string) {
			content, err := os.ReadFile(args[0])

			if err != nil {
				log.Fatalf("could not read %s: %v", args[0], err)
			}

			lts := pkg.Parse(args[0], string(content))

			lines := strings.Split(string(content), "\n")

			for i, line := range lines {
				lineNr := i
				lt := lts[uint32(lineNr)]

				fmt.Printf("%d (%+v): %s\n", lineNr, lt, line)
			}
		},
	}
	luaCmd := cobra.Command{
		Args:  cobra.ExactArgs(1),
		Use:   "lua-load <action.lua>",
		Short: "load LUA file to do syntax check",
		Run: func(cmd *cobra.Command, args []string) {
			script, err := os.ReadFile(args[0])
			if err != nil {
				panic(err)
			}
			pkg.LuaLoad(args[0], string(script))
		},
	}
	listCmd := cobra.Command{
		Args: cobra.MinimumNArgs(0),
		Use:  "list <dir>",
		Run: func(cmd *cobra.Command, args []string) {
			actionLuaFiles := []string{}
			for _, arg := range args {
				actionLuaFiles = append(actionLuaFiles, pkg.ListLuaActions(arg)...)
			}
			for _, path := range actionLuaFiles {
				fmt.Printf("- lua:%s\n", path)
			}
			for name := range internalActions {
				fmt.Printf("- gce:%s\n", name)
			}
		},
	}

	runCmd.PersistentFlags().BoolVarP(&dryRun, "dry-run", "d", false, "")
	runCmd.PersistentFlags().BoolVarP(&forceRun, "force", "f", false, "run all tests without check if necessary")

	rootCmd.AddCommand(&runCmd, &parseCmd, &baseCommitCmd, &luaCmd, &listCmd)
	err := rootCmd.Execute()
	if err != nil {
		log.Fatalf("corba failed with: %v", err)
	}

}
