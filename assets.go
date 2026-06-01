// Package regis exposes static assets embedded in the binary.
package regis

import _ "embed"

// AIContext is the condensed regis schema reference for AI-assisted authoring.
//
//go:embed docs/regis-ai-context.md
var AIContext []byte

// SchemaDoc is the dense annotated YAML schema reference for regis.yml.
//
//go:embed docs/regis-schema.yaml
var SchemaDoc []byte
