package cell

import (
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/cilium/hive/internal"
)

// Describe searches for given object type in the cells and
// prints out a summary of where it is provided from and used at.
// If an object is decorated this shows both where it is provided
// and where it's decorated and does not understand which one wins.
func Describe(w io.Writer, searchRegexp string, cells ...Cell) {
	re, err := regexp.Compile(searchRegexp)
	if err != nil {
		fmt.Fprintf(w, "%q is invalid: %s\n", searchRegexp, err)
	}
	s := describeState{
		matches: map[string][]describeRef{},
		re:      re,
	}
	for _, c := range cells {
		s.search(c, nil)
	}
	fmt.Fprintf(w, "Results for %q:\n", re)
	for typ, refs := range s.matches {
		fmt.Fprintf(w, "  %s:\n", typ)
		for _, ref := range refs {
			if ref.isProvide {
				fmt.Fprintf(w, "    Provided from '%s' by %s\n", ref.moduleID, ref.fn)
			} else {
				fmt.Fprintf(w, "    Used in '%s' by %s\n", ref.moduleID, ref.fn)
			}
		}
	}
}

type describeRef struct {
	typ       string
	moduleID  FullModuleID
	fn        string
	isProvide bool
}

type describeState struct {
	matches map[string][]describeRef
	re      *regexp.Regexp
}

func (s *describeState) search(c Cell, moduleID FullModuleID) {
	switch c := c.(type) {
	case *module:
		for _, child := range c.cells {
			s.search(child, c.fullModuleID(moduleID))
		}
	case group:
		for _, child := range c {
			s.search(child, moduleID)
		}
	case *provider:
		for i, info := range c.infos {
			for _, in := range info.Inputs {
				typ := extractTypeFromInfoString(in.String())
				if s.re.MatchString(typ) {
					s.matches[typ] = append(s.matches[typ], describeRef{
						typ:      typ,
						moduleID: moduleID,
						fn:       internal.FuncNameAndLocation(c.ctors[i]),
					})
				}
			}
			for _, out := range info.Outputs {
				typ := extractTypeFromInfoString(out.String())
				if s.re.MatchString(typ) {
					s.matches[typ] = append(s.matches[typ], describeRef{
						typ:       typ,
						moduleID:  moduleID,
						fn:        internal.FuncNameAndLocation(c.ctors[i]),
						isProvide: true,
					})
				}
			}
		}

	case *decorator:
		for _, in := range c.info.Inputs {
			typ := extractTypeFromInfoString(in.String())
			if s.re.MatchString(typ) {
				s.matches[typ] = append(s.matches[typ], describeRef{
					typ:      typ,
					moduleID: moduleID,
					fn:       internal.FuncNameAndLocation(c.decorator),
				})
			}
		}
		for _, out := range c.info.Outputs {
			typ := extractTypeFromInfoString(out.String())
			if s.re.MatchString(typ) {
				s.matches[typ] = append(s.matches[typ], describeRef{
					typ:       typ,
					moduleID:  moduleID,
					fn:        internal.FuncNameAndLocation(c.decorator),
					isProvide: true,
				})
			}
		}
		for _, child := range c.cells {
			s.search(child, moduleID)
		}

	case *invoker:
		for i := range c.funcs {
			fn := &c.funcs[i]
			fn.infoMu.Lock()
			if fn.info != nil {
				for _, in := range fn.info.Inputs {
					typ := extractTypeFromInfoString(in.String())
					if s.re.MatchString(typ) {
						s.matches[typ] = append(s.matches[typ], describeRef{
							typ:      typ,
							moduleID: moduleID,
							fn:       fn.name,
						})
					}
				}
			}
			fn.infoMu.Unlock()
		}

	case configInfoer:
		typ, flagsFn := c.configInfo()
		if s.re.MatchString(typ) {
			s.matches[typ] = append(s.matches[typ], describeRef{
				typ:       typ,
				moduleID:  moduleID,
				fn:        internal.FuncNameAndLocation(flagsFn),
				isProvide: true,
			})
		}

	default:
		panic(fmt.Sprintf("unexpected cell.Cell: %#v", c))
	}
}

func extractTypeFromInfoString(s string) string {
	// Unfortunately the Input and Output types in uber/dig don't
	// give access to the pure type name, so we need to just cut out
	// the extras returned from String().
	if idx := strings.LastIndexByte(s, '['); idx > 0 {
		s = s[:idx]
	}
	return s
}
