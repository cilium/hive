package cell

import (
	"sync"
)

type SimpleHealth struct {
	sync.Mutex

	Scope  string
	Level  Level
	Status string
	Error  error

	Children map[string]*SimpleHealth
}

// NewScope implements cell.Health.
func (h *SimpleHealth) NewScope(name string) Health {
	h.Lock()
	defer h.Unlock()
	h2 := &SimpleHealth{
		Scope:    name,
		Children: make(map[string]*SimpleHealth),
	}
	h.Children[name] = h2
	return h2
}

func (h *SimpleHealth) GetChild(names ...string) *SimpleHealth {
	for _, name := range names {
		h.Lock()
		h2, ok := h.Children[name]
		h.Unlock()
		h = h2
		ok = ok && h.Scope == name
		if !ok {
			return nil
		}
	}
	return h

}

// Degraded implements cell.Health.
func (h *SimpleHealth) Degraded(reason string, err error) {
	h.Lock()
	defer h.Unlock()

	h.Level = StatusDegraded
	h.Status = reason
	h.Error = err
}

// OK implements cell.Health.
func (h *SimpleHealth) OK(status string) {
	h.Lock()
	defer h.Unlock()

	h.Level = StatusOK
	h.Status = status
	h.Error = nil
}

// Stopped implements cell.Health.
func (h *SimpleHealth) Stopped(reason string) {
	h.Lock()
	defer h.Unlock()

	h.Level = StatusStopped
	h.Status = reason
	h.Error = nil
}

func NewSimpleHealth() (Health, *SimpleHealth) {
	h := &SimpleHealth{
		Children: make(map[string]*SimpleHealth),
	}
	return h, h
}

var _ Health = &SimpleHealth{}

var SimpleHealthCell = Provide(NewSimpleHealth)
