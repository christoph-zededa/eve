package main

import (
	"github.com/spf13/cobra"
	"go.podman.io/buildah"
)

func main() {
	if buildah.InitReexec() {
		return
	}

	rootCmd := &cobra.Command{}

	proxyCmd := &cobra.Command{
		Use: "proxy",
	}

	forceBuild := false
	buildCmd := &cobra.Command{
		Use:  "build <pkg dir>",
		Args: cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			buildMulti(args, forceBuild)
		},
	}

	buildCmd.PersistentFlags().BoolVarP(&forceBuild, "force", "f", false, "set force build")

	rootCmd.AddCommand(proxyCmd, buildCmd)

	rootCmd.Execute()
}
