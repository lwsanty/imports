package csharp

import (
	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/csharp"
	"github.com/src-d/imports/languages/tsitter"
)

const (
	query = `
		(using_directive (identifier_name) @name)
		(using_directive (qualified_name) @name)
	`
)

var (
	q *sitter.Query
)

func init() {
	tsitter.RegisterLanguage(language{})

	var err error
	if q, err = sitter.NewQuery([]byte(query), csharp.GetLanguage()); err != nil {
		panic(err)
	}
}

type language struct{}

func (language) Aliases() []string {
	return []string{"C#"}
}

func (l language) GetLanguage() *sitter.Language {
	return csharp.GetLanguage()
}

func (l language) Imports(content []byte, root *sitter.Node) ([]string, error) {
	var out []string

	c := sitter.NewQueryCursor()
	c.Exec(q, root)
	for {
		m, ok := c.NextMatch()
		if !ok {
			break
		}

		for _, cap := range m.Captures {
			out = append(out, filterOut(content, cap.Node))
		}
	}

	return out, nil
}

func filterOut(content []byte, node *sitter.Node) string {
	var (
		filters = map[string]struct{}{
			"type_argument_list": struct{}{},
		}

		out string

		fn func(n *sitter.Node)
	)

	fn = func(n *sitter.Node) {
		if _, ok := filters[n.Type()]; ok {
			return
		}

		if cnt := int(n.ChildCount()); cnt == 0 {
			out += string(content[n.StartByte():n.EndByte()])
		} else {
			for i := 0; i < cnt; i++ {
				fn(n.Child(i))
			}
		}
	}

	fn(node)
	return out
}
