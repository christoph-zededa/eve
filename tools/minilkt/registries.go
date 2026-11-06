package main

import (
	"os"
	"path/filepath"

	"go.podman.io/image/v5/types"
)

// registriesConf writes a registries.conf under dataDir that resolves short
// names against docker.io only, and returns a SystemContext that makes buildah
// use it instead of the host's /etc/containers/registries.conf and its drop-in
// directory. Without it, a short name that slips past AdditionalBuildContexts
// (buildah matches FROM before ARG expansion, so FROM ${ARG} never matches) is
// resolved against the host's unqualified-search-registries, which on most
// distros means an interactive registry prompt.
func registriesConf() (*types.SystemContext, error) {
	dropIns := filepath.Join(dataDir, "buildah-conf", "registries.conf.d")
	if err := os.MkdirAll(dropIns, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dataDir, "buildah-conf", "registries.conf")
	conf := "unqualified-search-registries = [\"docker.io\"]\nshort-name-mode = \"enforcing\"\n"
	if err := os.WriteFile(path, []byte(conf), 0o600); err != nil {
		return nil, err
	}
	return &types.SystemContext{
		SystemRegistriesConfPath:    path,
		SystemRegistriesConfDirPath: dropIns,
	}, nil
}
