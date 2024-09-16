package main

import (
	"context"
	"fmt"
	"path/filepath"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/bash"
	"github.com/smacker/go-tree-sitter/cpp"
	"github.com/smacker/go-tree-sitter/dockerfile"
	"github.com/smacker/go-tree-sitter/golang"
)

/*
	func main() {
		fmt.Println("dockerfile")
		runDockerfile()

		fmt.Println("------------")
		fmt.Println("go")
		runGo()

		fmt.Println("------------")
		fmt.Println("cpp")
		runCpp()

		fmt.Println("------------")
		fmt.Println("bash")
		runBash()
	}
*/
func parse(path string, content string) {
	var lang *sitter.Language

	fmt.Printf(">>>> %s (%d)\n", path, len(content))
	ext := filepath.Ext(path)
	switch ext {
	case ".go":
		lang = golang.GetLanguage()
	case ".cpp":
		lang = cpp.GetLanguage()
	}

	if filepath.Base(path) == "Dockerfile" {
		lang = dockerfile.GetLanguage()
	}

	if lang == nil {
		//fmt.Printf("lang = nil\n")
		return
	}
	parseWithLang(lang, []byte(content))
}

func runBash() {
	lang := bash.GetLanguage()

	sourceCode := []byte(`
	#!/usr/bin/env bash

	f() {
	    # comment
	    echo "Hello world" # hello world
	}
	`)

	parseWithLang(lang, sourceCode)
}

func runCpp() {
	lang := cpp.GetLanguage()

	sourceCode := []byte(`
	#include <iostream>

	void foo() {
		
	}
	int main() {
		/*
		 * Print Hello World
		 */
		std::cout << "Hello World" << std::endl; // print something

		foo();
		
		return 0;
	}
	`)

	parseWithLang(lang, sourceCode)
}
func runGo() {
	lang := golang.GetLanguage()
	sourceCode := []byte(`
		package main

		import "fmt"

		func main() {
			f := func(){ fmt.Println("lambda")}
			// do nothing

			fmt.Println("hello world") // partial comment
		}
	`)

	parseWithLang(lang, sourceCode)
}
func runDockerfile() {
	lang := dockerfile.GetLanguage()

	sourceCode := []byte(`
	FROM ubuntu:latest

	# Update repository
	RUN apt-get update
	
	# Update packages
	RUN apt-get -y dist-upgrade
		
	`)

	parseWithLang(lang, sourceCode)
}

type parser struct {
	sourceCode []byte
}

func parseWithLang(lang *sitter.Language, sourceCode []byte) {
	p := parser{
		sourceCode: sourceCode,
	}
	parser := sitter.NewParser()
	parser.SetLanguage(lang)
	tree, err := parser.ParseCtx(context.Background(), nil, sourceCode)
	if err != nil {
		panic(err)
	}

	n := tree.RootNode()

	fmt.Println(n)

	p.walk(n)
}

func (p parser) walk(n *sitter.Node) {
	for i := 0; i < int(n.ChildCount()); i++ {
		child := n.NamedChild(i)
		if child == nil {
			continue
		}
		fmt.Printf("%d - %d: %s\n", child.StartPoint().Row, child.EndPoint().Row, child.Type())

		/*
			if strings.HasPrefix(child.Type(), "function_") {
				fmt.Printf("\t%v\n", child.Content(p.sourceCode))
			}
		*/

		if child.Type() == "identifier" &&
			(child.Parent().Type() == "function_declarator" || child.Parent().Type() == "function_declaration") {
			fmt.Printf("\t%v parent: %v\n", child.Content(p.sourceCode), child.Parent().Type())
		}
		if child.Type() == "word" && child.Parent().Type() == "function_definition" {
			fmt.Printf("\t%v parent: %v\n", child.Content(p.sourceCode), child.Parent().Type())
		}

		p.walk(child)
	}
}
