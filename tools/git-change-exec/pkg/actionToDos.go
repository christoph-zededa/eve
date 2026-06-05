// Copyright (c) 2026 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package pkg

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
)

type ActionToDos struct {
	Actions map[string][]ActionToDo
}

type ActionToDo struct {
	Path string
	Ld   *LineDiff
}

func (atd *ActionToDos) dumpActionToDos(w io.Writer) {
	bs, err := json.MarshalIndent(atd, "", "\t")
	if err != nil {
		log.Fatalf("json marshalling failed: %v", err)
	}

	fmt.Fprintf(w, "%s\n", string(bs))
}

func (atd *ActionToDos) addActionToDo(a Action, path string, ld *LineDiff) {
	if atd.Actions[Id(a)] == nil {
		atd.Actions[Id(a)] = make([]ActionToDo, 0)
	}
	atd.Actions[Id(a)] = append(atd.Actions[Id(a)], ActionToDo{
		Path: path,
		Ld:   ld,
	})
}
