package presets

import (
	"embed"
	"io/fs"
)

//go:embed all:data
var seed embed.FS

// Embedded returns the built-in preset tree, rooted at data/.
func Embedded() fs.FS {
	sub, err := fs.Sub(seed, "data")
	if err != nil {
		panic(err)
	}
	return sub
}
