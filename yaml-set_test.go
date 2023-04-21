package main

import (
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"
)

func ExampleYamlSet() {
	op := YamlSet{
		Path:  "a/b/c",
		Value: `new value`,
	}

	in := []byte(`# comment x
x: 1
# comment a
a:
  # comment a/x
  x: 1
  # comment a/b
  b: "old-value"
`)

	fmt.Println("--- in ---")
	os.Stdout.Write(in)

	out, err := op.Apply(in)
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	fmt.Println("")
	fmt.Println("--- out --")
	os.Stdout.Write(out)

	// Output:
	// --- in ---
	// # comment x
	// x: 1
	// # comment a
	// a:
	//   # comment a/x
	//   x: 1
	//   # comment a/b
	//   b: "old-value"
	//
	// --- out --
	// # comment x
	// x: 1
	// # comment a
	// a:
	//     # comment a/x
	//     x: 1
	//     # comment a/b
	//     b:
	//         c: new value
}

func printNode(w io.Writer, prefix string, v *yaml.Node) {
	kind := "?"

	switch v.Kind {
	case yaml.DocumentNode:
		kind = "DocumentNode"
	case yaml.SequenceNode:
		kind = "SequenceNode"
	case yaml.MappingNode:
		kind = "MappingNode "
	case yaml.ScalarNode:
		kind = "ScalarNode  "
	case yaml.AliasNode:
		kind = "AliasNode   "
	}

	fmt.Fprintf(w, "%s%s %q\n", prefix, kind, v.Value)
	for _, e := range v.Content {
		printNode(w, prefix+"  ", e)
	}
}
