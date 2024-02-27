package cas

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteKubevirtMountpointsFile(t *testing.T) {
	cas := containerdCAS{}
	mountPoints := map[string]struct{}{
		"/media/floppy": struct{}{},
		"/media/cdrom":  struct{}{},
	}

	dir, err := ioutil.TempDir("", "prefix")
	if err != nil {
		t.Fatal(err)
	}

	cas.writeKubevirtMountpointsFile(mountPoints, dir)

	contentBytes, err := ioutil.ReadFile(filepath.Join(dir, "mountPoints"))

	content := string(contentBytes)

	for mountPoint := range mountPoints {
		if !strings.Contains(content, mountPoint) {
			t.Fatalf("mountPoint %s is missing", mountPoint)
		}
	}

	os.RemoveAll(dir)
}
