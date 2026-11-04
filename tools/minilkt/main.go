package main

import (
	"bufio"
	"fmt"
	"minilkt/dockerproxy"
	"os"

	"github.com/spf13/cobra"
	"go.podman.io/buildah"
	"go.podman.io/storage/pkg/unshare"
)

func deleteImage(id string) error {
	unshare.MaybeReexecUsingUserNamespace(false)

	store, err := openStore()
	if err != nil {
		return err
	}
	defer func() { _, _ = store.Shutdown(false) }()

	return store.Delete(id)
}

func prune() error {
	unshare.MaybeReexecUsingUserNamespace(false)

	store, err := openStore()
	if err != nil {
		return err
	}
	defer func() { _, _ = store.Shutdown(false) }()

	store.Wipe()

	return nil
}

func main() {
	if buildah.InitReexec() {
		return
	}

	rootCmd := &cobra.Command{}

	proxyCmd := &cobra.Command{
		Use: "proxy",
	}
	imageCmd := &cobra.Command{
		Use:     "image",
		Aliases: []string{"i", "img"},
	}

	proxyStartCmd := &cobra.Command{
		Use:  "start",
		Args: cobra.MaximumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			dir := "proxy"
			if len(args) == 1 {
				dir = os.Args[0]
			}
			p, err := dockerproxy.New(dir)
			os.Mkdir("proxy", 0o700)
			p.Verbose = true
			httpsProxyPort, err := getFreePort()
			if err != nil {
				panic(err)
			}
			if err := p.Start(uint16(httpsProxyPort)); err != nil {
				panic(err)
			}

			fmt.Printf("port: %d capath: %s\n", httpsProxyPort, p.CertPEM)
			fmt.Printf("example usage:\n")
			fmt.Printf("curl: curl -x localhost:%d --cacert %s <url>\n", httpsProxyPort, p.CertPEM)
			fmt.Printf("go program: SSL_CERT_FILE=%s https_proxy=localhost:%d go run client.go\n", p.CertPEM, httpsProxyPort)
			fmt.Printf(`podman build: https_proxy=http://127.0.0.1:%d http_proxy=http://127.0.0.1:%d SSL_CERT_FILE=%s \
							podman build --network=pasta:--splice-only,-T,%d -v %s:/etc/ssl/cert.pem:ro,z \
							-v %s:/usr/local/share/ca-certificates/proxy-ca.crt:ro,z \
							--no-cache -t test-podman-build .`, httpsProxyPort, httpsProxyPort, p.CertPEM, httpsProxyPort, p.CertPEM, p.CertPEM)
			fmt.Println()
			fmt.Printf(`podman run: podman run --network=pasta:--splice-only,-T,%d -e http_proxy=http://127.0.0.1:%d -e https_proxy=http://127.0.0.1:%d \
							-v %s:/etc/ssl/cert.pem:ro,z \
							-v %s:/usr/local/share/ca-certificates/proxy-ca.crt:ro,z \
							-it test-podman-build `, httpsProxyPort, httpsProxyPort, httpsProxyPort, p.CertPEM, p.CertPEM)
			fmt.Println()
			fmt.Println("press [RETURN] to exit")
			input := bufio.NewScanner(os.Stdin)
			input.Scan()
		},
	}

	proxyCmd.AddCommand(proxyStartCmd)

	forceBuild := false
	dryBuild := false
	keepBuild := false
	buildCmd := &cobra.Command{
		Use:  "build <pkg dir>",
		Args: cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			buildMulti(args, forceBuild, dryBuild, keepBuild)
		},
	}

	buildCmd.PersistentFlags().BoolVarP(&forceBuild, "force", "f", false, "set force build")
	buildCmd.PersistentFlags().BoolVarP(&dryBuild, "dry", "d", false, "set dry build")
	buildCmd.PersistentFlags().BoolVarP(&keepBuild, "keep-going", "k", false, "keep going building")

	listCmd := &cobra.Command{
		Use:          "list",
		Aliases:      []string{"ls"},
		Short:        "List the images in the minilkt store",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return listImages(os.Stdout)
		},
	}

	pruneCmd := &cobra.Command{
		Use:   "prune",
		Short: "prune all images containers and layers",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			prune()
		},
	}
	deleteCmd := &cobra.Command{
		Use:     "delete",
		Short:   "delete image",
		Aliases: []string{"del", "rm"},
		Args:    cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			err := deleteImage(args[0])
			if err != nil {
				fmt.Printf("failed: %+v\n", err)
			}
		},
	}
	export2LktCacheCmd := &cobra.Command{
		Use:     "export-to-linuxkit",
		Short:   "export images to the linuxkit cache",
		Aliases: []string{"export2linukit", "e2l", "e2lk", "e2lkt", "export2lkt"},
		Args:    cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			err := exportAllToLinuxkitCache()
			if err != nil {
				panic(err)
			}
		},
	}

	imageCmd.AddCommand(pruneCmd, listCmd, deleteCmd, export2LktCacheCmd)

	rootCmd.AddCommand(proxyCmd, buildCmd, imageCmd)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
