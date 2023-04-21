package main

import (
	"strings"

	"gopkg.in/yaml.v3"
)

type YamlSet struct {
	File  string
	Path  string
	Value string
}

func (set YamlSet) Apply(in []byte) (out []byte, err error) {
	v := &yaml.Node{}

	err = yaml.Unmarshal(in, v)
	if err != nil {
		return
	}

	e := v

	if e.Kind == yaml.DocumentNode {
		if len(e.Content) == 0 {
			e = &yaml.Node{Kind: yaml.MappingNode}
			v = &yaml.Node{
				Kind:    yaml.DocumentNode,
				Content: []*yaml.Node{e},
			}
		} else {
			e = e.Content[0]
		}
	}

	for _, property := range strings.Split(set.Path, "/") {
		if e.Kind != yaml.MappingNode {
			*e = yaml.Node{
				Kind: yaml.MappingNode,
			}
		}

		var next *yaml.Node

		for i := 0; i+1 < len(e.Content); i += 2 {
			nameNode := e.Content[i]
			valueNode := e.Content[i+1]

			if nameNode.Value == property {
				next = valueNode
				break
			}
		}

		if next == nil {
			nameNode := &yaml.Node{}
			nameNode.SetString(property)

			next = &yaml.Node{Kind: yaml.MappingNode}

			e.Content = append(e.Content,
				nameNode,
				next,
			)
		}

		e = next
	}

	e.SetString(set.Value)

	return yaml.Marshal(v)
}
