package main

import (
	"bytes"
	"fmt"
	"os"

	"github.com/openshift/imagebuilder"
	"github.com/openshift/imagebuilder/dockerfile/parser"
)

func parse(dockerfilePath string) {

	bs, err := os.ReadFile(dockerfilePath)
	if err != nil {
		panic(err)
	}
	mainNode, err := imagebuilder.ParseDockerfile(bytes.NewReader(bs))
	// fmt.Printf("main node: %s\n", mainNode.Dump())
	dump(mainNode, 0)
}

func dump(n *parser.Node, depth int) {
	for i := 0; i < depth; i++ {
		fmt.Printf(" ")
	}

	fmt.Printf("val: %s\n", n.Value)

	for _, c := range n.Children {
		dump(c, depth+20)
		fmt.Println("-------------")
	}

	next := n.Next
	for next != nil {
		dump(next, depth+1)
		next = next.Next
	}

}
