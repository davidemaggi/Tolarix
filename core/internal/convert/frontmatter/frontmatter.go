// Package frontmatter converts YAML front matter to and from canonical
// metadata, keeping key order. Shared by the Markdown-file connectors.
package frontmatter

import (
	"bytes"
	"fmt"

	"go.yaml.in/yaml/v3"

	"github.com/davidemaggi/tolarix/core/internal/model"
)

// Parse decodes YAML front matter (without the --- fences).
func Parse(src string) ([]model.MetaItem, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 {
		return nil, nil
	}
	m := doc.Content[0]
	if m.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("expected key: value pairs")
	}
	items := make([]model.MetaItem, 0, len(m.Content)/2)
	for i := 0; i+1 < len(m.Content); i += 2 {
		var v any
		if err := m.Content[i+1].Decode(&v); err != nil {
			return nil, err
		}
		items = append(items, model.MetaItem{Key: m.Content[i].Value, Value: normalize(v)})
	}
	return items, nil
}

// normalize converts map[string]any trees so they JSON-encode cleanly.
func normalize(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			t[k] = normalize(x)
		}
		return t
	case []any:
		for i, x := range t {
			t[i] = normalize(x)
		}
		return t
	default:
		return v
	}
}

// Encode renders items as YAML (without the --- fences).
func Encode(items []model.MetaItem) ([]byte, error) {
	m := &yaml.Node{Kind: yaml.MappingNode}
	for _, it := range items {
		var v yaml.Node
		if err := v.Encode(it.Value); err != nil {
			return nil, err
		}
		m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: it.Key}, &v)
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(m); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Block renders items as a complete front matter block ("---\n…---\n"), or
// nothing when there are none.
func Block(items []model.MetaItem) ([]byte, error) {
	if len(items) == 0 {
		return nil, nil
	}
	fm, err := Encode(items)
	if err != nil {
		return nil, err
	}
	return append(append([]byte("---\n"), fm...), "---\n"...), nil
}
