// Package regis exposes static assets embedded in the binary.
package regis

import _ "embed"

// RTFContext is the full regis reference — schema, CLI, and concepts — useful as AI context.
//
//go:embed docs/regis-ai-context.md
var RTFContext []byte

// SchemaDoc is the dense annotated YAML schema reference for regis.yml.
//
//go:embed docs/regis-schema.yaml
var SchemaDoc []byte
