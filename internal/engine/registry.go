package engine

import "fmt"

// Registry maps engine names to their implementations.
type Registry struct {
	engines map[string]Engine
}

// NewRegistry creates an engine registry with the provided engines.
func NewRegistry(engines ...Engine) *Registry {
	r := &Registry{engines: make(map[string]Engine, len(engines))}
	for _, e := range engines {
		r.engines[e.Name()] = e
	}
	return r
}

// Get returns the engine for the given name, or an error if not found.
func (r *Registry) Get(name string) (Engine, error) {
	e, ok := r.engines[name]
	if !ok {
		return nil, fmt.Errorf("unsupported engine: %s", name)
	}
	return e, nil
}

// Names returns all registered engine names.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.engines))
	for k := range r.engines {
		names = append(names, k)
	}
	return names
}
