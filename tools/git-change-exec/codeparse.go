package main

import (
	"context"
	"path/filepath"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/bash"
	"github.com/smacker/go-tree-sitter/cpp"
	"github.com/smacker/go-tree-sitter/dockerfile"
	"github.com/smacker/go-tree-sitter/golang"
)

func parse(path string, content string) lineTypes {
	var lang *sitter.Language

	ext := filepath.Ext(path)
	switch ext {
	case ".go":
		lang = golang.GetLanguage()
	case ".sh":
		lang = bash.GetLanguage()
	case ".cpp":
		lang = cpp.GetLanguage()
	}

	if filepath.Base(path) == "Dockerfile" {
		lang = dockerfile.GetLanguage()
	}

	if lang == nil {
		return nil
	}

	lt := parseWithLang(lang, []byte(content))

	return lt
}

type lineType uint8
type lineTypes map[uint32]lineType

const (
	undecided lineType = iota
	notComment
	isComment
)

type parser struct {
	sourceCode []byte
	comments   lineTypes
}

func (p *parser) notComment(line uint32) {
	//	fmt.Printf(">>> notComment %d\n", line)
	p.comments[line] = notComment
}

func (p *parser) setComment(line uint32) {
	//	fmt.Printf(">>> setComment %d\n", line)
	state, ok := p.comments[line]

	if !ok || state == undecided {
		p.comments[line] = isComment
	}
}

func parseWithLang(lang *sitter.Language, sourceCode []byte) lineTypes {
	p := parser{
		sourceCode: sourceCode,
		comments:   lineTypes{},
	}
	parser := sitter.NewParser()
	parser.SetLanguage(lang)
	tree, err := parser.ParseCtx(context.Background(), nil, sourceCode)
	if err != nil {
		panic(err)
	}

	n := tree.RootNode()

	p.walk(n)

	return p.comments
}

func (p *parser) walk(n *sitter.Node) {
	for i := 0; i < int(n.ChildCount()); i++ {
		child := n.NamedChild(i)
		if child == nil {
			continue
		}

		from := child.StartPoint().Row + 1
		to := child.EndPoint().Row + 1
		if child.Type() == "comment" {
			for i := from; i <= to; i++ {
				p.setComment(i)
			}
		} else {
			/*
				for i := from; i <= to; i++ {
					fmt.Printf("not comments %d-%d, is %s\n", from, to, child.Type())
					p.notComment(i)
				}
			*/
			if from == to {
				p.notComment(from)

			}
		}
		/*
			content := child.Content(p.sourceCode)
			content = strings.TrimSpace(content)
			_ = content
			fmt.Printf(">>> %s %d-%d: %s\n", child.Type(), from, to, content)
		*/

		/*
			if child.Type() == "identifier" &&
				(child.Parent().Type() == "function_declarator" || child.Parent().Type() == "function_declaration") {
				fmt.Printf("\t%v parent: %v\n", child.Content(p.sourceCode), child.Parent().Type())
			}
			if child.Type() == "word" && child.Parent().Type() == "function_definition" {
				fmt.Printf("\t%v parent: %v\n", child.Content(p.sourceCode), child.Parent().Type())
			}
		*/

		p.walk(child)
	}
}
