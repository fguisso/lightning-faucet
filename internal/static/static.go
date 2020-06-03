//go:generate go run generator.go

package static

type storage struct {
	assets, templates map[string][]byte
}

// newEmbedFiles creates a new storage for embed files.
func newStorage() *storage {
	return &storage{
		assets:    make(map[string][]byte),
		templates: make(map[string][]byte),
	}
}

// Add adds a file to storage.
func (s *storage) Add(filetype string, filepath string, content []byte) {
	if filetype == "static" {
		s.assets[filepath] = content
	} else {
		s.templates[filepath] = content
	}
}

// Assets returns the assets map.
func (s *storage) Assets() map[string][]byte {
	return s.assets
}

// Templates returns the tamplates map.
func (s *storage) Templates() map[string][]byte {
	return s.templates
}

// Expose the embed files.
var s = newStorage()

// Add adds file content in memory.
func Add(filetype string, filepath string, content []byte) {
	s.Add(filetype, filepath, content)
}

// Assets returns the assets map.
func Assets() map[string][]byte {
	return s.Assets()
}

// Templates returns the tamplates map.
func Templates() map[string][]byte {
	return s.Templates()
}
