package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// fixture is a throwaway git repository with one package directory under pkg/.
type fixture struct {
	t    *testing.T
	root string
	pkg  string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	f := &fixture{t: t, root: root, pkg: filepath.Join(root, "pkg", "probe")}
	f.git("init", "-q", "-b", "main")
	f.write("pkg/probe/build.yml", "image: probe\n")
	f.write("pkg/probe/Dockerfile", "FROM scratch\nCOPY a /a\n")
	f.write("pkg/probe/a", "alpha\n")
	f.write("pkg/probe/sub/b", "bravo\n")
	f.write("pkg/other/build.yml", "image: other\n")
	f.write("pkg/other/c", "charlie\n")
	f.write("top.txt", "top\n")
	f.commit()
	return f
}

func (f *fixture) git(args ...string) string {
	f.t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
	cmd.Dir = f.root
	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (f *fixture) write(rel, content string) {
	f.t.Helper()
	p := filepath.Join(f.root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) commit() {
	f.t.Helper()
	f.git("add", "-A")
	f.git("commit", "-q", "-m", "fixture")
}

func (f *fixture) hash() string {
	f.t.Helper()
	h, err := lktPkgHash(f.pkg)
	if err != nil {
		f.t.Fatalf("PkgHash(%s): %v", f.pkg, err)
	}
	return h
}

var dirtyRe = regexp.MustCompile(`^[0-9a-f]{40}-dirty-[0-9a-f]{7}$`)

func (f *fixture) wantClean() {
	f.t.Helper()
	want := f.git("rev-parse", "HEAD:pkg/probe")
	if got := f.hash(); got != want {
		f.t.Fatalf("hash = %q, want clean tree hash %q", got, want)
	}
}

func (f *fixture) wantDirty() string {
	f.t.Helper()
	got := f.hash()
	tree := f.git("rev-parse", "HEAD:pkg/probe")
	if !dirtyRe.MatchString(got) || !strings.HasPrefix(got, tree+"-dirty-") {
		f.t.Fatalf("hash = %q, want %s-dirty-<7 hex>", got, tree)
	}
	return got
}

// linuxkitHash runs the real linuxkit binary as the reference and returns the
// {{.Hash}} part of the tag it prints, or skips the test when no binary exists.
func linuxkitHash(t *testing.T, pkgDir string) string {
	t.Helper()
	out, err := exec.Command(linuxkitBin(t), "pkg", "show-tag", pkgDir).CombinedOutput()
	if err != nil {
		t.Fatalf("linuxkit pkg show-tag %s: %v\n%s", pkgDir, err, out)
	}
	tag := strings.TrimSpace(string(out))
	return tag[strings.LastIndex(tag, ":")+1:]
}

func isExecutable(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular() && fi.Mode()&0o111 != 0
}

func TestPkgHashCleanTreeIsSubtreeHash(t *testing.T) {
	newFixture(t).wantClean()
}

func TestPkgHashRepositoryRootUsesCommitTree(t *testing.T) {
	f := newFixture(t)
	want := f.git("rev-parse", "HEAD^{tree}")
	got, err := lktPkgHash(f.root)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("hash = %q, want commit tree %q", got, want)
	}
}

func TestPkgHashOutsideGitFails(t *testing.T) {
	if _, err := lktPkgHash(t.TempDir()); err == nil {
		t.Fatal("expected an error for a directory outside any git repository")
	}
}

func TestPkgHashUncommittedDirectoryFails(t *testing.T) {
	f := newFixture(t)
	f.write("pkg/fresh/build.yml", "image: fresh\n")
	if _, err := lktPkgHash(filepath.Join(f.root, "pkg", "fresh")); err == nil {
		t.Fatal("expected an error for a directory that is not in HEAD")
	}
}

func TestPkgHashModifiedTrackedFileIsDirty(t *testing.T) {
	f := newFixture(t)
	f.write("pkg/probe/a", "changed\n")
	f.wantDirty()
}

func TestPkgHashUntrackedFileIsDirty(t *testing.T) {
	f := newFixture(t)
	f.write("pkg/probe/new.txt", "new\n")
	f.wantDirty()
}

func TestPkgHashIgnoredUntrackedFileIsClean(t *testing.T) {
	f := newFixture(t)
	f.write(".gitignore", "*.log\n")
	f.write("pkg/probe/.gitignore", "generated/\n")
	f.commit()
	f.write("pkg/probe/build.log", "noise\n")
	f.write("pkg/probe/generated/out", "noise\n")
	f.wantClean()
}

func TestPkgHashInfoExcludeIsHonoured(t *testing.T) {
	f := newFixture(t)
	f.write(".git/info/exclude", "scratch\n")
	f.write("pkg/probe/scratch", "noise\n")
	f.wantClean()
}

func TestPkgHashStagedDeletionIsDirty(t *testing.T) {
	f := newFixture(t)
	f.git("rm", "-q", "--cached", "pkg/probe/a")
	f.wantDirty()
}

func TestPkgHashDeletedWorktreeFileIsDirty(t *testing.T) {
	f := newFixture(t)
	if err := os.Remove(filepath.Join(f.pkg, "a")); err != nil {
		t.Fatal(err)
	}
	f.wantDirty()
}

func TestPkgHashModeChangeIsDirty(t *testing.T) {
	f := newFixture(t)
	if err := os.Chmod(filepath.Join(f.pkg, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	f.wantDirty()
}

func TestPkgHashSymlinkRetargetIsDirty(t *testing.T) {
	f := newFixture(t)
	link := filepath.Join(f.pkg, "link")
	if err := os.Symlink("a", link); err != nil {
		t.Fatal(err)
	}
	f.commit()
	f.wantClean()
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("sub/b", link); err != nil {
		t.Fatal(err)
	}
	f.wantDirty()
}

func TestPkgHashTouchedUnchangedFileIsClean(t *testing.T) {
	f := newFixture(t)
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(f.pkg, "a"), past, past); err != nil {
		t.Fatal(err)
	}
	f.wantClean()
}

func TestPkgHashSiblingPackageChangeIsClean(t *testing.T) {
	f := newFixture(t)
	f.write("pkg/other/c", "changed\n")
	f.write("pkg/other/new", "new\n")
	f.write("top.txt", "changed\n")
	f.wantClean()
}

func TestPkgHashDirtySuffixMatchesLinuxkit(t *testing.T) {
	f := newFixture(t)
	f.write("pkg/probe/a", "changed\n")
	f.write("pkg/probe/zz-untracked", "untracked\n")
	f.write("pkg/probe/sub/new", "new\n")
	f.write("pkg/probe/sub-dash", "sorts before sub/ in byte order\n")
	f.write("pkg/probe/ignored.log", "noise\n")
	f.write(".gitignore", "*.log\n")
	f.write("pkg/probe/café", "git quotes this name, so linuxkit never hashes it\n")
	if err := os.Symlink("a", filepath.Join(f.pkg, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(f.pkg, "Dockerfile")); err != nil {
		t.Fatal(err)
	}
	want := linuxkitHash(t, f.pkg)
	if got := f.wantDirty(); got != want {
		t.Fatalf("hash = %q, linuxkit says %q", got, want)
	}
}

func TestPkgHashExtraSourcesMatchLinuxkit(t *testing.T) {
	f := newFixture(t)
	f.write("extra/one/x", "x\n")
	f.write("extra/two/y", "y\n")
	f.write("pkg/probe/build.yml", "image: probe\nextra-sources:\n  - ../../extra/one:/one\n  - ../../extra/two:/two\n")
	f.commit()
	want := linuxkitHash(t, f.pkg)
	got, err := lktPkgHash(f.pkg, filepath.Join(f.root, "extra", "one"), filepath.Join(f.root, "extra", "two"))
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("hash = %q, linuxkit says %q", got, want)
	}
	if len(got) != 40 || strings.Contains(got, "-dirty-") {
		t.Fatalf("hash = %q, want a plain sha1 of the combined tree hashes", got)
	}
}

// TestPkgHashMatchesLinuxkitForEVEPackages compares against the real build
// tool for every package of the enclosing EVE checkout, whatever its state.
func TestPkgHashMatchesLinuxkitForEVEPackages(t *testing.T) {
	ymls, err := filepath.Glob("../../pkg/*/build.yml")
	if err != nil || len(ymls) == 0 {
		t.Skip("not inside an EVE checkout")
	}
	start := time.Now()
	for _, yml := range ymls {
		dir, err := filepath.Abs(filepath.Dir(yml))
		if err != nil {
			t.Fatal(err)
		}
		got, err := lktPkgHash(dir)
		if err != nil {
			t.Errorf("%s: %v", dir, err)
			continue
		}
		if want := linuxkitHash(t, dir); !strings.HasPrefix(want, got) {
			t.Errorf("%s: hash %q, linuxkit says %q", filepath.Base(dir), got, want)
		}
	}
	t.Logf("%d packages hashed twice (PkgHash + linuxkit) in %v", len(ymls), time.Since(start))
}

func BenchmarkPkgHashPillar(b *testing.B) {
	dir, err := filepath.Abs("../../pkg/pillar")
	if err != nil {
		b.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "build.yml")); err != nil {
		b.Skip("not inside an EVE checkout")
	}
	for b.Loop() {
		if _, err := lktPkgHash(dir); err != nil {
			b.Fatal(err)
		}
	}
}
