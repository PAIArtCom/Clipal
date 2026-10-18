package integration

import "github.com/PAIArtCom/Clipal/internal/config"

// Kilo CLI shares OpenCode's provider and model grammar, with its own config
// discovery. Verified against Kilo-Org/kilocode revision
// 2a1903f29186e25adec0bace14f6ee589095f7eb, packages/opencode/src/config/config.ts
// and packages/opencode/src/kilocode/skills/kilo-config.md.
func (m Manager) planKilo(cfg *config.Config) ([]takeoverFile, error) {
	return m.planOpenCodeCompatible(ProductKiloCLI, cfg)
}
