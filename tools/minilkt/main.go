package main

import (
	"bufio"
	"fmt"
	"minilkt/dockerproxy"
	"os"
	"path/filepath"
	"strings"

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

func wipe() error {
	unshare.MaybeReexecUsingUserNamespace(false)

	store, err := openStore()
	if err != nil {
		return err
	}
	defer func() { _, _ = store.Shutdown(false) }()

	err = store.Wipe()
	if err != nil {
		panic(err)
	}

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

	proxyStartCmdCacheDir := filepath.Join(dataDir, "proxy")
	proxyStartNetns := false
	proxyStartCmd := &cobra.Command{
		Use:  "start",
		Args: cobra.MaximumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			if len(args) == 1 {
				proxyStartCmdCacheDir = os.Args[0]
			}
			if proxyStartNetns && os.Geteuid() != 0 {
				fmt.Fprintf(os.Stderr, "--netns must run inside the user namespace of the containers that will join it, e.g.:\n  podman unshare -- %s\n", strings.Join(os.Args, " "))
				os.Exit(1)
			}
			p, err := dockerproxy.New(proxyStartCmdCacheDir)
			if err != nil {
				panic(err)
			}
			os.Mkdir("proxy", 0o700)
			p.Verbose = true
			httpsProxyPort, err := getFreePort()
			if err != nil {
				panic(err)
			}
			netnsPath := ""
			if proxyStartNetns {
				ns, err := newPrivateNetns()
				if err != nil {
					panic(err)
				}
				listeners, err := listenBothSides(ns, uint16(httpsProxyPort))
				if err != nil {
					panic(err)
				}
				if err := p.StartWith(listeners); err != nil {
					panic(err)
				}
				netnsPath = ns.Path
			} else if err := p.Start(uint16(httpsProxyPort)); err != nil {
				panic(err)
			}

			fmt.Printf("port: %d capath: %s\n", httpsProxyPort, p.CertPEM)
			if netnsPath != "" {
				fmt.Printf("netns: %s\n", netnsPath)
				fmt.Printf(`podman run: podman run --network=ns:%s -e http_proxy=http://127.0.0.1:%d -e https_proxy=http://127.0.0.1:%d \
							-v %s:/etc/ssl/cert.pem:ro,z -v %s:/usr/local/share/ca-certificates/proxy-ca.crt:ro,z -it <image>`,
					netnsPath, httpsProxyPort, httpsProxyPort, p.CertPEM, p.CertPEM)
				fmt.Println()
				fmt.Printf(`podman build: http_proxy=http://127.0.0.1:%d https_proxy=http://127.0.0.1:%d podman build --network=ns:%s \
							-v %s:/etc/ssl/cert.pem:ro,z -v %s:/usr/local/share/ca-certificates/proxy-ca.crt:ro,z -t <tag> .`,
					httpsProxyPort, httpsProxyPort, netnsPath, p.CertPEM, p.CertPEM)
				fmt.Println()
			}
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
	proxyStartCmd.PersistentFlags().StringVarP(&proxyStartCmdCacheDir, "cache", "c", proxyStartCmdCacheDir, "set cache dir")
	proxyStartCmd.PersistentFlags().BoolVar(&proxyStartNetns, "netns", false, "also listen in a private loopback-only network namespace for containers to join (run under podman unshare for podman)")

	proxyWipeCmd := &cobra.Command{
		Use:  "wipe",
		Args: cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			dir := filepath.Join(dataDir, "proxy")
			p, err := dockerproxy.New(dir)
			if err != nil {
				panic(err)
			}
			err = p.WipeCache()
			if err != nil {
				panic(err)
			}

		},
	}

	proxyExportCacheCmd := &cobra.Command{
		Use:  "export",
		Args: cobra.MaximumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			dir := filepath.Join(dataDir, "proxy", "cache")
			imageName := cacheImageName
			if len(args) > 0 {
				imageName = args[0]
			}
			img, err := exportCache(dir, imageName)
			if err != nil {
				panic(err)
			}

			fmt.Printf("image: %+v\n", img)
		},
	}
	proxyImportCacheCmd := &cobra.Command{
		Use:  "import",
		Args: cobra.RangeArgs(1, 2),
		Run: func(cmd *cobra.Command, args []string) {
			dir := filepath.Join(dataDir, "proxy", "cache")
			if len(args) > 1 {
				dir = args[1]
			}
			err := importCache(args[0], dir)
			if err != nil {
				panic(err)
			}
		},
	}

	proxyCmd.AddCommand(proxyStartCmd, proxyWipeCmd, proxyExportCacheCmd, proxyImportCacheCmd)

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

	listImageCmd := &cobra.Command{
		Use:          "list",
		Aliases:      []string{"ls"},
		Short:        "List the images in the minilkt store",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return listImages(os.Stdout)
		},
	}
	pushImageCmd := &cobra.Command{
		Use:          "push <id> <org> <name>",
		Aliases:      []string{},
		Short:        "push image to docker hub",
		Args:         cobra.ExactArgs(3),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return uploadImage(args[0], args[1], args[2])
		},
	}
	pullImageCmd := &cobra.Command{
		Use:          "pull <org> <name>",
		Aliases:      []string{},
		Short:        "push image to docker hub",
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return downloadImage(args[0], args[1])
		},
	}

	wipeImageCmd := &cobra.Command{
		Use:   "wipe",
		Short: "wipe all images containers and layers",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			wipe()
		},
	}
	deleteImageCmd := &cobra.Command{
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

	imageCmd.AddCommand(wipeImageCmd, listImageCmd, deleteImageCmd, export2LktCacheCmd, pushImageCmd, pullImageCmd)

	rootCmd.AddCommand(proxyCmd, buildCmd, imageCmd)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
