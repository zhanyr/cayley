package graph

import (
	"encoding/json"
	"fmt"

	"github.com/cayleygraph/cayley/clog"
	"github.com/cayleygraph/cayley/quad"
	"golang.org/x/net/context"
)

// IterateChain is a chain-enabled helper to setup iterator execution.
type IterateChain struct {
	ctx context.Context
	it  Iterator
	qs  QuadStore

	paths    bool
	optimize bool

	limit int
	n     int
}

// Iterate is a set of helpers for iteration. Context may be used to cancel execution.
// Iterator will be optimized and closed after execution.
//
// By default, iteration has no limit and includes sub-paths.
func Iterate(ctx context.Context, it Iterator) *IterateChain {
	if ctx == nil {
		ctx = context.Background()
	}
	return &IterateChain{
		ctx: ctx, it: it,
		limit: -1, paths: true,
		optimize: true,
	}
}
func (c *IterateChain) next() bool {
	select {
	case <-c.ctx.Done():
		return false
	default:
	}
	ok := (c.limit < 0 || c.n < c.limit) && c.it.Next()
	if ok {
		c.n++
	}
	return ok
}
func (c *IterateChain) nextPath() bool {
	select {
	case <-c.ctx.Done():
		return false
	default:
	}
	ok := c.paths && (c.limit < 0 || c.n < c.limit) && c.it.NextPath()
	if ok {
		c.n++
	}
	return ok
}
func (c *IterateChain) start() {
	if c.optimize {
		c.it, _ = c.it.Optimize()
		if c.qs != nil {
			c.it, _ = c.qs.OptimizeIterator(c.it)
		}
	}
	if !clog.V(2) {
		return
	}
	if b, err := json.MarshalIndent(c.it.Describe(), "", "  "); err != nil {
		clog.Infof("failed to format description: %v", err)
	} else {
		clog.Infof("%s", b)
	}
}
func (c *IterateChain) end() {
	c.it.Close()
	if !clog.V(2) {
		return
	}
	if b, err := json.MarshalIndent(DumpStats(c.it), "", "  "); err != nil {
		clog.Infof("failed to format stats: %v", err)
	} else {
		clog.Infof("%s", b)
	}
}

// Limit limits a total number of results returned.
func (c *IterateChain) Limit(n int) *IterateChain {
	c.limit = n
	return c
}

// Paths switches iteration over sub-paths (with it.NextPath).
// Defaults to true.
func (c *IterateChain) Paths(enable bool) *IterateChain {
	c.paths = enable
	return c
}

// On sets a default quad store for iteration. If qs was set, it may be omitted in other functions.
func (c *IterateChain) On(qs QuadStore) *IterateChain {
	c.qs = qs
	return c
}

// UnOptimized disables iterator optimization.
func (c *IterateChain) UnOptimized() *IterateChain {
	c.optimize = false
	return c
}

// Each will run a provided callback for each result of the iterator.
func (c *IterateChain) Each(fnc func(Value)) error {
	c.start()
	defer c.end()
	done := c.ctx.Done()

	for c.next() {
		select {
		case <-done:
			return c.ctx.Err()
		default:
		}
		fnc(c.it.Result())
		for c.nextPath() {
			select {
			case <-done:
				return c.ctx.Err()
			default:
			}
			fnc(c.it.Result())
		}
	}
	return c.it.Err()
}

// All will return all results of an iterator.
func (c *IterateChain) Count() (int64, error) {
	c.start()
	defer c.end()
	if err := c.it.Err(); err != nil {
		return 0, err
	}
	if size, exact := c.it.Size(); exact {
		return size, nil
	}
	done := c.ctx.Done()
	var cnt int64
iteration:
	for c.next() {
		select {
		case <-done:
			break iteration
		default:
		}
		cnt++
		for c.nextPath() {
			select {
			case <-done:
				break iteration
			default:
			}
			cnt++
		}
	}
	return cnt, c.it.Err()
}

// All will return all results of an iterator.
func (c *IterateChain) All() ([]Value, error) {
	c.start()
	defer c.end()
	done := c.ctx.Done()
	var out []Value
iteration:
	for c.next() {
		select {
		case <-done:
			break iteration
		default:
		}
		out = append(out, c.it.Result())
		for c.nextPath() {
			select {
			case <-done:
				break iteration
			default:
			}
			out = append(out, c.it.Result())
		}
	}
	return out, c.it.Err()
}

// First will return a first result of an iterator. It returns nil if iterator is empty.
func (c *IterateChain) First() (Value, error) {
	c.start()
	defer c.end()
	if !c.next() {
		return nil, c.it.Err()
	}
	return c.it.Result(), nil
}

// Send will send each result of the iterator to the provided channel.
//
// Channel will NOT be closed when function returns.
func (c *IterateChain) Send(out chan<- Value) error {
	c.start()
	defer c.end()
	done := c.ctx.Done()
	for c.next() {
		select {
		case <-done:
			return c.ctx.Err()
		case out <- c.it.Result():
		}
		for c.nextPath() {
			select {
			case <-done:
				return c.ctx.Err()
			case out <- c.it.Result():
			}
		}
	}
	return c.it.Err()
}

// TagEach will run a provided tag map callback for each result of the iterator.
func (c *IterateChain) TagEach(fnc func(map[string]Value)) error {
	c.start()
	defer c.end()
	done := c.ctx.Done()

	for c.next() {
		select {
		case <-done:
			return c.ctx.Err()
		default:
		}
		tags := make(map[string]Value)
		c.it.TagResults(tags)
		fnc(tags)
		for c.nextPath() {
			select {
			case <-done:
				return c.ctx.Err()
			default:
			}
			tags := make(map[string]Value)
			c.it.TagResults(tags)
			fnc(tags)
		}
	}
	return c.it.Err()
}

var errNoQuadStore = fmt.Errorf("no quad store in Iterate")

// EachValue is an analog of Each, but it will additionally call NameOf
// for each graph.Value before passing it to a callback.
func (c *IterateChain) EachValue(qs QuadStore, fnc func(quad.Value)) error {
	if qs != nil {
		c.qs = qs
	}
	if c.qs == nil {
		return errNoQuadStore
	}
	// TODO(dennwc): batch NameOf?
	return c.Each(func(v Value) {
		if nv := c.qs.NameOf(v); nv != nil {
			fnc(nv)
		}
	})
}

// EachValuePair is an analog of Each, but it will additionally call NameOf
// for each graph.Value before passing it to a callback. Original value will be passed as well.
func (c *IterateChain) EachValuePair(qs QuadStore, fnc func(Value, quad.Value)) error {
	if qs != nil {
		c.qs = qs
	}
	if c.qs == nil {
		return errNoQuadStore
	}
	// TODO(dennwc): batch NameOf?
	return c.Each(func(v Value) {
		if nv := c.qs.NameOf(v); nv != nil {
			fnc(v, nv)
		}
	})
}

// AllValues is an analog of All, but it will additionally call NameOf
// for each graph.Value before returning the results slice.
func (c *IterateChain) AllValues(qs QuadStore) ([]quad.Value, error) {
	var out []quad.Value
	err := c.EachValue(qs, func(v quad.Value) {
		out = append(out, v)
	})
	return out, err
}

// FirstValue is an analog of First, but it does lookup of a value in QuadStore.
func (c *IterateChain) FirstValue(qs QuadStore) (quad.Value, error) {
	if qs != nil {
		c.qs = qs
	}
	if c.qs == nil {
		return nil, errNoQuadStore
	}
	v, err := c.First()
	if err != nil || v == nil {
		return nil, err
	}
	// TODO: return an error from NameOf once we have it exposed
	return c.qs.NameOf(v), nil
}

// SendValues is an analog of Send, but it will additionally call NameOf
// for each graph.Value before sending it to a channel.
func (c *IterateChain) SendValues(qs QuadStore, out chan<- quad.Value) error {
	if qs != nil {
		c.qs = qs
	}
	if c.qs == nil {
		return errNoQuadStore
	}
	c.start()
	defer c.end()
	done := c.ctx.Done()
	send := func(v Value) error {
		nv := c.qs.NameOf(c.it.Result())
		if nv == nil {
			return nil
		}
		select {
		case <-done:
			return c.ctx.Err()
		case out <- c.qs.NameOf(c.it.Result()):
		}
		return nil
	}
	for c.next() {
		if err := send(c.it.Result()); err != nil {
			return err
		}
		for c.nextPath() {
			if err := send(c.it.Result()); err != nil {
				return err
			}
		}
	}
	return c.it.Err()
}

// TagValues is an analog of TagEach, but it will additionally call NameOf
// for each graph.Value before passing the map to a callback.
func (c *IterateChain) TagValues(qs QuadStore, fnc func(map[string]quad.Value)) error {
	if qs != nil {
		c.qs = qs
	}
	if c.qs == nil {
		return errNoQuadStore
	}
	return c.TagEach(func(m map[string]Value) {
		vm := make(map[string]quad.Value, len(m))
		for k, v := range m {
			vm[k] = c.qs.NameOf(v) // TODO(dennwc): batch NameOf?
		}
		fnc(vm)
	})
}
