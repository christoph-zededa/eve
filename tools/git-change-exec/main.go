// Copyright (c) 2024 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/storer"
	udiff "github.com/go-git/go-git/v5/utils/diff"
	"github.com/sergi/go-diff/diffmatchpatch"
	"github.com/spf13/cobra"
)

var dryRun = true
var debug = false

type lineOp uint8

const (
	lineAdd = iota
	lineDel
)

func (o lineOp) String() string {
	if o == lineAdd {
		return "+"
	}
	if o == lineDel {
		return "-"
	}

	return " "
}

type lineDiff struct {
	op         lineOp
	line       string
	lineNumber uint64
}

type gitChangeExec struct {
	actionsToCheck []action
	actionDos      map[action]struct{}
	gitPath        string
	g              *git.Repository
	relPaths       map[string]struct{}
	baseCommit     *object.Commit
}

func debugLog(fmt string, args ...any) {
	if debug {
		log.Printf(fmt, args...)
	}
}

func newGitChangeExec() gitChangeExec {
	return gitChangeExec{
		actionDos:      map[action]struct{}{},
		actionsToCheck: []action{},
		relPaths:       map[string]struct{}{},
	}
}

func main() {

	actionCategories := []string{}

	for c := range actions {
		actionCategories = append(actionCategories, c)
	}

	rootCmd := cobra.Command{
		Args: cobra.MinimumNArgs(1),
		Use:  strings.Join(actionCategories, "|"),
		Run: func(cmd *cobra.Command, args []string) {
			gce := newGitChangeExec()

			for _, category := range args {
				if len(actions[category]) == 0 {
					log.Fatalf("could not find actions for %s", category)
				}

				gce.actionsToCheck = append(gce.actionsToCheck, actions[category]...)
			}

			currentPath, err := os.Getwd()
			if err != nil {
				log.Fatalf("getting current working directory: %v", err)
			}

			defer func() {
				err := os.Chdir(currentPath)
				if err != nil {
					log.Printf("could not change back to previous dir %s: %v", currentPath, err)
				}
			}()

			gce.g, err = git.PlainOpenWithOptions("./", &git.PlainOpenOptions{DetectDotGit: true})
			if err != nil {
				log.Fatalf("open git path %s failed: %v", gce.gitPath, err)
			}

			wt, err := gce.g.Worktree()
			if err != nil {
				log.Fatalf("could not determine worktree: %v", err)
			}
			gce.gitPath = wt.Filesystem.Root()

			err = os.Chdir(gce.gitPath)
			if err != nil {
				log.Fatalf("could not change to %s: %v", gce.gitPath, err)
			}

			gce.fetchOrigin()

			gce.calculateBaseCommit()
			gce.collectActionsGitTree()
			gce.collectDirtyGitTree()

			gce.diff()
			gce.runActionDos()

		},
	}

	rootCmd.PersistentFlags().BoolVarP(&dryRun, "dry-run", "d", false, "")
	err := rootCmd.Execute()
	if err != nil {
		log.Fatalf("corba failed with: %v", err)
	}

}

func (gce *gitChangeExec) fetchOrigin() {
	err := gce.g.Fetch(&git.FetchOptions{
		RemoteName: "origin",
		Tags:       git.AllTags,
	})
	if err != nil {
		debugLog("fetching from origin failed: %v", err)
	}
}

var splitLinesRegexp = regexp.MustCompile(`[^\n]*(\n|$)`)

func lineInFile(path string, line int) string {
	fp, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer fp.Close()

	scanner := bufio.NewScanner(fp)
	i := 0
	for scanner.Scan() {
		i++
		if line == i {
			return scanner.Text()
		}
	}

	return ""
}

/*
func handleFilePatch(fp diff.FilePatch) {
	fromFile, toFile := fp.Files()

	fromPath := ""
	if fromFile != nil {
		fromPath = fromFile.Path()
	}
	toPath := ""
	if toFile != nil {
		toPath = toFile.Path()
	}
	fmt.Printf(">> %s --> %s\n", fromPath, toPath)

	if fp.IsBinary() {
		return
	}

	nlines := 1
	for _, ch := range fp.Chunks() {
		lines := splitLinesRegexp.FindAllString(ch.Content(), -1)
		var op string
		if ch.Type() == diff.Equal {
			nlines += len(lines)
			continue
		}
		if ch.Type() == diff.Add {
			op = "+"
		}
		if ch.Type() == diff.Delete {
			op = "-"
		}

		//content := strings.TrimSpace(ch.Content())
		for i, line := range lines {
			index := nlines + i

			line = strings.TrimSuffix(line, "\n")
			var origLine string
			if ch.Type() == diff.Add {
				origLine = lineInFile(toPath, index)
				if origLine != line && len(line) > 1 {
					log.Fatalf("!!!! '%s' <-> '%s'\n", line, origLine)
				}
			}
			fmt.Printf("%s %d %s ||| %s\n", op, index, line, origLine)
		}
		if ch.Type() == diff.Add {
			nlines += len(lines)
		}
		if ch.Type() == diff.Delete {
			//	nlines -= len(lines)
		}
	}
}

func handleDiff(df diffmatchpatch.Diff) {
	var op string

	switch df.Type {
	case diffmatchpatch.DiffInsert:
		op = "+"
	case diffmatchpatch.DiffDelete:
		op = "-"
	default:
		return
	}

	fmt.Printf("%s %s\n", op, df.Text)
}
*/

func (gce *gitChangeExec) diff() {
	//	wg := sync.WaitGroup{}
	for path := range gce.relPaths {
		//		wg.Add(1)
		//		go func(path string) {
		gce.diffPath(path)
		//		wg.Done()
		//		}(path)
	}

	// wg.Wait()
}

func (gce *gitChangeExec) diffPath(path string) {
	var oldContent string

	file, err := gce.baseCommit.File(path)
	if err == nil {
		oldContent, err = file.Contents()
		if err != nil {
			log.Fatalf("could not get file contents of %s: %v", path, err)
		}
	}

	///
	parse(path, oldContent)

	bs, err := os.ReadFile(path)
	if err != nil {
		log.Printf("could slurp '%s': %v", path, err)
	}
	dfs := udiff.Do(oldContent, string(bs))
	parse(path, string(bs))

	allEqual := true
	for _, df := range dfs {
		if df.Type != diffmatchpatch.DiffEqual {
			allEqual = false
		}
	}
	if allEqual {
		return
	}
	gce.addActionByPath(path)

	//		fmt.Printf(">>> len(oldContent): %d <-> len(newContent): %d\n", len(oldContent), len(bs))
	nlines := 1
	for _, df := range dfs {
		//handleDiff(df)
		lines := splitLinesRegexp.FindAllString(df.Text, -1)
		if df.Type == diffmatchpatch.DiffEqual {
			nlines += len(lines)
			continue
		}
		var op lineOp
		if df.Type == diffmatchpatch.DiffInsert {
			op = lineAdd
		}
		if df.Type == diffmatchpatch.DiffDelete {
			op = lineDel
		}
		for i, line := range lines {
			index := nlines + i
			line = strings.TrimSuffix(line, "\n")
			ld := lineDiff{
				op:         op,
				line:       line,
				lineNumber: uint64(index),
			}
			//fmt.Printf("%d: %s %s\n", index, op, line)
			gce.addActionByLineDiff(path, ld)
		}
		if df.Type == diffmatchpatch.DiffInsert {
			nlines += len(lines)
		}
	}
}

func (gce *gitChangeExec) calculateBaseCommit() {
	logIter, err := gce.g.Log(&git.LogOptions{})
	if err != nil {
		log.Fatalf("getting log failed: %v", err)
	}

	branchHead, err := logIter.Next()
	if err != nil {
		log.Fatalf("getting log.Next failed: %v", err)
	}

	commonBase := gce.findCommonBase(branchHead)

	logIter, err = gce.g.Log(&git.LogOptions{})
	if err != nil {
		log.Fatalf("getting log for iteration failed: %v", err)
	}

	var baseCommit *object.Commit
	err = logIter.ForEach(func(c *object.Commit) error {
		for _, cb := range commonBase {
			if c.Hash == cb.Hash {
				baseCommit = c
				return storer.ErrStop
			}
		}
		return nil
	})
	if err != nil {
		log.Fatalf("iterating over commits failed: %v", err)
	}
	logIter.Close()

	gce.baseCommit = baseCommit
}

func (gce *gitChangeExec) collectActionsGitTree() {
	logIter, err := gce.g.Log(&git.LogOptions{})
	if err != nil {
		log.Fatalf("getting log for iteration failed: %v", err)
	}

	err = logIter.ForEach(func(c *object.Commit) error {
		if c.Hash == gce.baseCommit.Hash {
			return storer.ErrStop
		}

		commitStats, err := c.Stats()
		if err != nil {
			log.Fatalf("getting commit stats failed: %v", err)
		}

		for _, st := range commitStats {
			//gce.addActionByPath(st.Name)
			gce.storePath(st.Name)
		}

		return nil
	})
	if err != nil {
		log.Fatalf("iterating over commits failed: %v", err)
	}
	logIter.Close()

	/*
		patch, err := gce.baseCommit.Patch(branchHead)
		if err == nil {
			for _, fp := range patch.FilePatches() {
				handleFilePatch(fp)
			}
		}
	*/

}

func (gce *gitChangeExec) findCommonBase(branchHead *object.Commit) []*object.Commit {
	var commonBase []*object.Commit
	masterRef := gce.retrieveMasterRef()

	refs := masterRef
	refs = append(refs, gce.retrieveLtsRefs()...)

	for _, ref := range refs {
		commit, err := gce.g.CommitObject(ref.Hash())
		if err != nil {
			log.Printf("retrieve commit object from ref %v failed: %v", ref, err)
			continue
		}
		commonBase = append(commonBase, commit)
		addBase, err := branchHead.MergeBase(commit)
		if err != nil {
			debugLog("finding merge base failed: %v", err)
		}
		commonBase = append(commonBase, addBase...)
	}
	return commonBase
}

func (gce *gitChangeExec) retrieveLtsRefs() []*plumbing.Reference {
	ret := []*plumbing.Reference{}

	refs, err := gce.g.References()
	if err != nil {
		log.Printf("retrieve refs failed: %v", err)
	}

	err = refs.ForEach(func(r *plumbing.Reference) error {
		// f.e. refs/remotes/origin/10.4-stable
		if strings.HasPrefix(r.Name().String(), "refs/remotes/origin") &&
			strings.HasSuffix(r.Name().String(), "-stable") {
			ret = append(ret, r)
		}
		return nil
	})

	if err != nil {
		log.Printf("iterating over refs failed: %v", err)
	}

	return ret
}

func (gce *gitChangeExec) retrieveMasterRef() []*plumbing.Reference {
	masterRefs := []*plumbing.Reference{}

	for _, nameOfMaster := range []string{
		"refs/heads/master",
		"refs/remotes/origin/master",
	} {
		var err error

		masterRef, err := gce.g.Reference(plumbing.ReferenceName(nameOfMaster), true)
		if err == nil {
			masterRefs = append(masterRefs, masterRef)
		}
	}
	return masterRefs
}

func (gce *gitChangeExec) storePath(path string) {
	gce.relPaths[path] = struct{}{}
}

func (gce *gitChangeExec) addActionByLineDiff(path string, ld lineDiff) {
	for _, a := range gce.actionsToCheck {
		ad, ok := a.(actionDiff)
		if ok && ad.matchDiff(path, ld) {
			gce.actionDos[a] = struct{}{}
		}
	}
}

func (gce *gitChangeExec) addActionByPath(path string) {
	for _, a := range gce.actionsToCheck {
		ap, ok := a.(actionPath)
		if ok && ap.matchPath(path) {
			gce.actionDos[a] = struct{}{}
		}
	}
}

func (gce *gitChangeExec) runActionDos() {
	failed := false
	for _, a := range gce.actionsToCheck {
		_, found := gce.actionDos[a]
		if !found {
			continue
		}
		var err error
		if !dryRun {
			log.Printf("--- running %s ...", id(a))
			err = a.do()
			log.Printf("--- running %s done", id(a))
		} else {
			log.Printf("would run %s, but running dry ...", id(a))
		}
		if err != nil {
			log.Printf("%s failed with: %v", id(a), err)
			failed = true
		}
	}

	if failed {
		os.Exit(1)
	}
}

func (gce *gitChangeExec) collectDirtyGitTree() {
	ignoredStatusCodes := map[git.StatusCode]struct{}{
		git.Unmodified: struct{}{},
		git.Untracked:  struct{}{},
	}
	worktree, err := gce.g.Worktree()
	if err != nil {
		log.Fatalf("getting current worktree: %v", err)
	}

	stats, err := worktree.Status()
	if err != nil {
		log.Fatalf("getting current worktree status: %v", err)
	}

	for file, gitSt := range stats {
		_, foundStaging := ignoredStatusCodes[gitSt.Staging]
		_, foundWorkTree := ignoredStatusCodes[gitSt.Worktree]

		if foundStaging && foundWorkTree {
			continue
		}

		fp := filepath.Join(gce.gitPath, file)
		st, err := os.Stat(fp)
		if err != nil {
			continue
		}

		// git-go has some bug with links, so only consider files
		if !st.Mode().IsRegular() {
			continue
		}
		//gce.addActionByPath(file)
		gce.storePath(file)
	}
}
