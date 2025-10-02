// Copyright (c) 2025 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package pkg

import (
	"fmt"
	"log"
	"os"
	"strings"

	lua "github.com/yuin/gopher-lua"
	luar "layeh.com/gopher-luar"
)

type runMode uint8

const (
	baseRunMode = iota
	extendedRunMode
)

type LuaAction struct {
	state *lua.LState
	rm    runMode
	id    string
}

func LuaLoad(name string, script string) *LuaAction {
	la := LuaAction{
		rm: baseRunMode,
	}

	la.state = lua.NewState(lua.Options{SkipOpenLibs: true, IncludeGoStackTrace: true})
	la.luaLoadBaseFunctions()

	if err := la.state.DoString(script); err != nil {
		panic(err)
	}

	la.id = name

	return &la
}

func (la *LuaAction) Close() {
	la.state.Close()
}

func (la *LuaAction) Id() string {
	return la.id
}

func (la *LuaAction) action() error {
	la.luaLoadExtendedFunctions()

	err := la.state.CallByParam(lua.P{
		Fn:      la.state.GetGlobal("exec"),
		NRet:    1,
		Protect: true,
	})
	if err != nil {
		return fmt.Errorf("running exec failed: %w", err)
	}
	ret := la.state.Get(-1) // returned value
	defer la.state.Pop(1)   // remove received value
	switch val := ret.(type) {
	case lua.LBool:
		if !val {
			return fmt.Errorf("action failed")
		}
	case lua.LNumber:
		if val != 0 {
			return fmt.Errorf("action failed, return %d", val)
		}
	}

	return nil
}

type luaFile struct {
	path  string
	lines []string
}

func (lf luaFile) Path() string {
	return lf.path
}

func (lf luaFile) Lines() []string {
	return lf.lines
}

func (la *LuaAction) match(path string, ld LineDiff) bool {
	if la.rm != baseRunMode {
		panic("baseRunMode expected")
	}
	bs, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("could not read file %s: %v", path, err)
	}

	lines := strings.Split(string(bs), "\n")
	lf := luaFile{
		path:  path,
		lines: lines,
	}

	if err := la.state.CallByParam(lua.P{
		Fn:      la.state.GetGlobal("match"),
		NRet:    1,
		Protect: true,
	}, luar.New(la.state, lf), luar.New(la.state, ld)); err != nil {
		log.Fatalf("could not call 'match': %v", err)
	}
	ret := la.state.Get(-1) // returned value
	retBool, ok := ret.(lua.LBool)
	if !ok {
		log.Fatalf("match: unknown return type %T", ret)
	}
	la.state.Pop(1) // remove received value

	return bool(retBool)
}

func (la *LuaAction) luaLoadExtendedFunctions() {
	if la.rm == extendedRunMode {
		panic("extended mode is already loaded")
	}
	la.rm = extendedRunMode
	lua.OpenOs(la.state)
	lua.OpenIo(la.state)
	lua.OpenPackage(la.state)
	lua.OpenChannel(la.state)
	lua.OpenCoroutine(la.state)
}

func (la *LuaAction) luaLoadBaseFunctions() {
	if la.rm == extendedRunMode {
		panic("extended mode is loaded")
	}
	lua.OpenBase(la.state)
	lua.OpenString(la.state)
	lua.OpenMath(la.state)
}

func (la *LuaAction) Do() error {
	return la.action()
}

func (la *LuaAction) MatchDiff(path string, ld LineDiff) bool {
	return la.match(path, ld)
}
