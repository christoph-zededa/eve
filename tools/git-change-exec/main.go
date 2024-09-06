// Copyright (c) 2024 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/storer"
)

var dryRun = true

type gitChangeExec struct {
	actionDos map[string]action
	gitPath   string
	g         *git.Repository
}

func newGitChangeExec() gitChangeExec {
	return gitChangeExec{
		actionDos: map[string]action{},
	}
}

func main() {
	var err error

	dryRunFlag := flag.Bool("d", false, "dry-run")
	flag.Parse()

	dryRun = *dryRunFlag

	if len(os.Args) < 2 || os.Args[1] == "" {
		execName, err := os.Executable()
		if err != nil {
			panic(err)
		}
		fmt.Printf("Usage: %s <path to scripts>\n", execName)
		os.Exit(0)
	}
	loadActions(os.Args[1])

	gce := newGitChangeExec()

	gce.g, err = git.PlainOpenWithOptions("./", &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		log.Fatalf("open git path %s failed: %v", gce.gitPath, err)
	}

	wt, err := gce.g.Worktree()
	if err != nil {
		log.Fatalf("could not determine worktree: %v", err)
	}
	gce.gitPath = wt.Filesystem.Root()
	gce.fetchOrigin()

	gce.collectActionsGitTree()
	gce.collectDirtyGitTree()

	gce.runActionDos()
}

func (gce *gitChangeExec) fetchOrigin() {
	err := gce.g.Fetch(&git.FetchOptions{
		RemoteName: "origin",
		Tags:       git.AllTags,
	})
	if err != nil {
		log.Printf("fetching from origin failed: %v", err)
	}
}

func (gce *gitChangeExec) collectActionsGitTree() {

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

	err = logIter.ForEach(func(c *object.Commit) error {
		for _, cb := range commonBase {
			if c.Hash == cb.Hash {
				return storer.ErrStop
			}
		}

		commitStats, err := c.Stats()
		if err != nil {
			log.Fatalf("getting commit stats failed: %v", err)
		}

		for _, st := range commitStats {
			gce.addActionByPath(st.Name)
		}

		// all actions are already marked for run
		if len(actions) == len(gce.actionDos) {
			return storer.ErrStop
		}

		return nil
	})
	logIter.Close()

	if err != nil {
		log.Fatalf("iterating over commits failed: %v", err)
	}
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
			log.Printf("finding merge base failed: %v", err)
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

func (gce *gitChangeExec) addActionByPath(path string) {
	if path == "" {
		return
	}
	for _, a := range actions {
		if a.match(path) {
			actionName := id(a)
			gce.actionDos[actionName] = a
		}
	}
}

func (gce *gitChangeExec) runActionDos() {
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

	err = os.Chdir(gce.gitPath)
	if err != nil {
		log.Fatalf("could not change to %s: %v", gce.gitPath, err)
	}
	failed := false
	for _, a := range gce.actionDos {
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
		// all actions are already marked for run
		if len(actions) == len(gce.actionDos) {
			break
		}

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
		gce.addActionByPath(file)
	}

}
