// Copyright (c) 2024 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package pkg

import (
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/storer"
)

var debug = false

type LineOp uint8

const (
	LineAdd = iota
	LineDel
	LineNop
)

func (o LineOp) String() string {
	if o == LineAdd {
		return "+"
	}
	if o == LineDel {
		return "-"
	}
	if o == LineNop {
		return "="
	}

	return " "
}

type LineDiff struct {
	Operation  LineOp
	Line       string
	LineNumber uint64
	TypeOfLine LineProperty
}

func (ld LineDiff) String() string {
	return fmt.Sprintf("%s %d: %s\n\t%s\n", ld.Operation.String(), ld.LineNumber, ld.Line, ld.TypeOfLine.String())
}

func (ld LineDiff) startCol() int {
	var i int
	var r rune

	for i, r = range []rune(ld.Line) {
		if !unicode.IsSpace(r) {
			break
		}
	}

	return i
}

type CommentType uint8

func (c CommentType) String() string {
	if c == Undecided {
		return "undecided"
	}
	if c == NotComment {
		return "not a comment"
	}
	if c == IsComment {
		return "comment"
	} else {
		panic("what?")
	}

}

const (
	Undecided CommentType = iota
	NotComment
	IsComment
)

// IsCommentString returns a stringified IsComment()
func (ld LineDiff) IsCommentString() string {
	return ld.IsComment().String()
}

func (ld LineDiff) IsComment() CommentType {
	// did not parse, so we don't know
	if len(ld.TypeOfLine) == 0 {
		return Undecided
	}
	cs, found := ld.TypeOfLine["comment"]
	if !found {
		return NotComment
	}

	for _, c := range cs {
		if (c.ColFrom == 0 || c.ColFrom <= uint32(ld.startCol())) &&
			(c.ColTo == uint32(math.MaxUint32) || c.ColTo >= uint32(len(ld.Line))) {
			return IsComment
		}
	}

	return Undecided
}

type GitChangeExec struct {
	ActionsToCheck []Action
	ActionDos      map[Action]struct{}
	GitPath        string
	G              *git.Repository
	relPaths       map[string]struct{}
	baseCommit     *object.Commit
	originPath     string
}

func debugLog(fmt string, args ...any) {
	if debug {
		log.Printf(fmt, args...)
	}
}

func NewGitChangeExec() GitChangeExec {
	return GitChangeExec{
		ActionDos:      map[Action]struct{}{},
		ActionsToCheck: []Action{},
		relPaths:       map[string]struct{}{},
	}
}

func (gce *GitChangeExec) GoToGitRootDir() {
	var err error

	gce.originPath, err = os.Getwd()
	if err != nil {
		log.Fatalf("getting current working directory: %v", err)
	}

	wt, err := gce.G.Worktree()
	if err != nil {
		log.Fatalf("could not determine worktree: %v", err)
	}
	gce.GitPath = wt.Filesystem.Root()

	err = os.Chdir(gce.GitPath)
	if err != nil {
		log.Fatalf("could not change to %s: %v", gce.GitPath, err)
	}
}

func (gce *GitChangeExec) ChangeBackDir() {
	if gce.originPath == "" {
		return
	}

	err := os.Chdir(gce.originPath)
	if err != nil {
		log.Fatalf("changing back to %s failed: %v", gce.originPath, err)
	}
}

func (gce *GitChangeExec) FetchOrigin() {
	err := gce.G.Fetch(&git.FetchOptions{
		RemoteName: "origin",
		Tags:       git.AllTags,
	})
	if err != nil {
		debugLog("fetching from origin failed: %v", err)
	}
}

func (gce *GitChangeExec) Diff() {
	//fmt.Printf(">>> diff %+q\n", gce.relPaths)
	for path := range gce.relPaths {
		//fmt.Printf(">>> relpath %s\n", path)
		gce.diffPath(path)
	}
}

func (gce *GitChangeExec) diffPath(path string) {
	var oldContent string

	file, err := gce.baseCommit.File(path)
	if err == nil {
		oldContent, err = file.Contents()
		if err != nil {
			log.Fatalf("could not get file contents of %s: %v", path, err)
		}
	}

	///
	linesFrom := Parse(path, oldContent)
	//printLines(linesOld, oldContent)

	bs, err := os.ReadFile(path)
	if err != nil {
		// log.Printf("could not slurp '%s': %v", path, err)
		return
	}
	linesTo := Parse(path, string(bs))

	fromLines := strings.Split(oldContent, "\n")
	toLines := strings.Split(string(bs), "\n")

	dfs := Diff(fromLines, toLines)
	for i := range dfs {
		if dfs[i].Operation == LineDel {
			dfs[i].TypeOfLine = linesFrom[uint32(dfs[i].LineNumber)]
		}
		if dfs[i].Operation == LineAdd {
			dfs[i].TypeOfLine = linesTo[uint32(dfs[i].LineNumber)]
		}
	}

	allEqual := true
	for _, df := range dfs {
		if df.Operation != LineNop {
			allEqual = false
		}
		gce.addActionByLineDiff(path, df)
	}
	if allEqual {
		return
	}
	gce.addActionByPath(path)

}

func (gce *GitChangeExec) CalculateBaseCommit() {
	logIter, err := gce.G.Log(&git.LogOptions{})
	if err != nil {
		log.Fatalf("getting log failed: %v", err)
	}

	branchHead, err := logIter.Next()
	if err != nil {
		log.Fatalf("getting log.Next failed: %v", err)
	}

	commonBase := gce.findCommonBase(branchHead)

	logIter, err = gce.G.Log(&git.LogOptions{})
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

func (gce *GitChangeExec) BaseCommit() *object.Commit {
	return gce.baseCommit
}

func (gce *GitChangeExec) CollectActionsGitTree() {
	logIter, err := gce.G.Log(&git.LogOptions{})
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

func (gce *GitChangeExec) findCommonBase(branchHead *object.Commit) []*object.Commit {
	var commonBase []*object.Commit
	masterRef := gce.retrieveMasterRef()

	refs := masterRef
	refs = append(refs, gce.retrieveLtsRefs()...)

	for _, ref := range refs {
		commit, err := gce.G.CommitObject(ref.Hash())
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

func (gce *GitChangeExec) retrieveLtsRefs() []*plumbing.Reference {
	ret := []*plumbing.Reference{}

	refs, err := gce.G.References()
	if err != nil {
		log.Printf("retrieve refs failed: %v", err)
	}

	err = refs.ForEach(func(r *plumbing.Reference) error {
		if strings.HasPrefix(r.Name().String(), "refs/remotes/origin") &&
			// f.e. refs/remotes/origin/10.4-stable
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

func (gce *GitChangeExec) retrieveMasterRef() []*plumbing.Reference {
	masterRefs := []*plumbing.Reference{}

	for _, nameOfMaster := range []string{
		"refs/heads/master",
		"refs/remotes/origin/master",
		"refs/heads/main",
		"refs/remotes/origin/main",
	} {
		var err error

		masterRef, err := gce.G.Reference(plumbing.ReferenceName(nameOfMaster), true)
		if err == nil {
			masterRefs = append(masterRefs, masterRef)
		}
	}

	if len(masterRefs) == 0 {
		log.Fatalf("could not find a base commit - check your main branch name")
	}
	return masterRefs
}

func (gce *GitChangeExec) storePath(path string) {
	gce.relPaths[path] = struct{}{}
}

func (gce *GitChangeExec) addActionByLineDiff(path string, ld LineDiff) {
	for _, a := range gce.ActionsToCheck {
		ad, ok := a.(ActionDiff)
		if ok && ad.MatchDiff(path, ld) {
			gce.ActionDos[a] = struct{}{}
		}
	}
}

func (gce *GitChangeExec) addActionByPath(path string) {
	for _, a := range gce.ActionsToCheck {
		ap, ok := a.(ActionPath)
		if ok && ap.MatchPath(path) {
			gce.ActionDos[a] = struct{}{}
		}
	}
}

func (gce *GitChangeExec) ForceRunActionDos() {
	var err error
	for _, a := range gce.ActionsToCheck {
		err = a.Do()
		if err != nil {
			log.Printf("%s failed with: %v", Id(a), err)
		}
	}

	if err != nil {
		os.Exit(1)
	}
}

func (gce *GitChangeExec) RunActionDos(dryRun bool) {
	failed := false
	for _, a := range gce.ActionsToCheck {
		_, found := gce.ActionDos[a]
		if !found {
			continue
		}
		var err error
		if !dryRun {
			log.Printf("--- running %s ...", Id(a))
			err = a.Do()
			log.Printf("--- running %s done", Id(a))
		} else {
			log.Printf("would run %s, but running dry ...", Id(a))
		}
		if err != nil {
			log.Printf("%s failed with: %v", Id(a), err)
			failed = true
		}
	}

	if failed {
		os.Exit(1)
	}
}

func (gce *GitChangeExec) CollectDirtyGitTree() {
	ignoredStatusCodes := map[git.StatusCode]struct{}{
		git.Unmodified: {},
		git.Untracked:  {},
	}
	worktree, err := gce.G.Worktree()
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

		fp := filepath.Join(gce.GitPath, file)
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
