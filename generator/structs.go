package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/fireball-lang/bindgen"
	"github.com/fireball-lang/bindgen/fb"
)

// memberName converts a C member/parameter name to snake case and strips the
// leading hungarian p/s prefixes (pNext, sType, ppGeometries, ...). The prefix
// is kept when stripping would produce a keyword (sType -> s_type instead of
// type_).
func memberName(name string) string {
	snake := bindgen.CamelToSnakeCase(name)
	segments := strings.Split(snake, "_")

	i := 0
	for i < len(segments)-1 && (segments[i] == "p" || segments[i] == "s") {
		i++
	}

	stripped := strings.Join(segments[i:], "_")

	if i > 0 && slices.Contains(bindgen.KEYWORDS, stripped) {
		return CheckName(snake)
	}

	return CheckName(stripped)
}

func (ctx *GenContext) CreateStructs() {
	// Create declarations first so struct references (including self references) resolve
	for i := range ctx.reg.Types {
		t := &ctx.reg.Types[i]

		if t.Category != "struct" && t.Category != "union" {
			continue
		}

		if !apiSupported(t.Api) || t.Alias != "" {
			continue
		}

		raw := typeName(t)
		if raw == "" {
			panic("unnamed struct type")
		}

		if !ctx.included[raw] {
			continue
		}

		layout := fb.C
		if t.Category == "union" {
			layout = fb.Union
		}

		ctx.structs[raw] = &fb.Struct{
			OutputIndex:   StructsOutput,
			Documentation: cleanComment(t.Comment),
			Name:          CheckName(strings.TrimPrefix(raw, "Vk")),
			Layout:        layout,
		}
	}

	for i := range ctx.reg.Types {
		t := &ctx.reg.Types[i]

		if t.Category != "struct" && t.Category != "union" {
			continue
		}

		if !apiSupported(t.Api) || t.Alias != "" {
			continue
		}

		raw := typeName(t)
		if !ctx.included[raw] {
			continue
		}

		decl := ctx.structs[raw]

		for j := 0; j < len(t.Fields); {
			m := &t.Fields[j]

			if !apiSupported(m.Api) {
				j++
				continue
			}

			// Consecutive bitfields pack into shared 32-bit words
			if m.Bits > 0 {
				group := []*Member{m}
				width := m.Bits
				j++

				for j < len(t.Fields) {
					next := &t.Fields[j]

					if !apiSupported(next.Api) {
						j++
						continue
					}

					if next.Bits == 0 || width+next.Bits > 32 {
						break
					}

					group = append(group, next)
					width += next.Bits
					j++
				}

				decl.Fields = append(decl.Fields, bitfieldField(raw, group))
				continue
			}

			if m.Name == "" || m.Base == "" {
				panic("incomplete member in struct " + raw)
			}

			field := &fb.Field{
				Public: true,
				Name:   memberName(m.Name),
				Type:   ctx.buildType(&m.typeParts),
			}

			// The structure type member must be set before the struct is used
			if m.Name == "sType" {
				field.Attributes = []string{"required"}

				// Structs whose structure type belongs to an excluded
				// extension do not get a default
				if len(m.Values) > 0 {
					value := strings.TrimSpace(m.Values[0])

					if structureType, ok := ctx.structureTypes[value]; ok {
						ctx.structDefaults[decl.Name] = structureType
					}
				}
			}

			decl.Fields = append(decl.Fields, field)

			j++
		}
	}
}

var packed24_8 = &fb.SimpleType{Text: "vulkan::Packed24_8"}

func bitfieldField(structName string, group []*Member) *fb.Field {
	if len(group) == 2 && group[0].Bits == 24 && group[1].Bits == 8 {
		name := memberName(group[0].Name) + "_and_" + memberName(group[1].Name)

		return &fb.Field{
			Public: true,
			Name:   CheckName(name),
			Type:   packed24_8,
		}
	}

	panic("unsupported bitfield layout in struct " + structName)
}

// CreateHandles turns handles into single-field newtype structs. Both the
// dispatchable (pointer) and non-dispatchable (integer) handles are backed by
// a raw u64, which keeps the 8 byte scalar ABI on 64 bit targets while giving
// handle values a named type.
func (ctx *GenContext) CreateHandles() {
	for i := range ctx.reg.Types {
		t := &ctx.reg.Types[i]

		if t.Category != "handle" || t.Alias != "" || !apiSupported(t.Api) {
			continue
		}

		raw := typeName(t)

		if !ctx.included[raw] {
			continue
		}

		ctx.handles[raw] = &fb.Struct{
			OutputIndex: HandlesOutput,
			Name:        CheckName(strings.TrimPrefix(raw, "Vk")),
			Layout:      fb.C,
			Fields: []*fb.Field{
				{Public: true, Name: "raw", Type: &fb.SimpleType{Text: "u64"}},
			},
		}
	}
}

// WriteHandleInterface writes the shared interface implemented by every
// handle type.
func WriteHandleInterface(w fb.Writer) {
	w.Write("\n")
	w.Write("pub interface Handle {\n")
	w.Write("    func from_raw(raw: u64) Self;\n")
	w.Write("    func as_raw(self) u64;\n")
	w.Write("    func valid(self) bool;\n")
	w.Write("}\n")
}

// WriteHandleImpls writes the helper methods and trait implementations of a
// handle type.
func WriteHandleImpls(w fb.Writer, decl *fb.Struct) {
	w.Write("\n")
	w.Write("impl %s : Handle {\n", decl.Name)
	w.Write("    pub func from_raw(raw: u64) Self {\n")
	w.Write("        return Self { raw: raw };\n")
	w.Write("    }\n")
	w.Write("\n")
	w.Write("    pub func as_raw(self) u64 {\n")
	w.Write("        return self.raw;\n")
	w.Write("    }\n")
	w.Write("\n")
	w.Write("    pub func valid(self) bool {\n")
	w.Write("        return self.raw != 0;\n")
	w.Write("    }\n")
	w.Write("}\n")

	w.Write("\n")
	w.Write("impl %s : Eq[%s] {\n", decl.Name, decl.Name)
	w.Write("    pub func eq(self, rhs: %s) bool {\n", decl.Name)
	w.Write("        return self.raw == rhs.raw;\n")
	w.Write("    }\n")
	w.Write("}\n")

	w.Write("\n")
	w.Write("impl %s : Ord[%s] {\n", decl.Name, decl.Name)
	w.Write("    pub func ord(self, rhs: %s) Ordering {\n", decl.Name)
	w.Write("        if (self.raw < rhs.raw) return Ordering::Less;\n")
	w.Write("        if (self.raw > rhs.raw) return Ordering::Greater;\n")
	w.Write("        return Ordering::Equal;\n")
	w.Write("    }\n")
	w.Write("}\n")
}

// zeroable reports whether the comptime evaluator can zero-initialize a value
// of the given type, mirroring the compiler's Zeroable conformance rules.
func (ctx *GenContext) zeroable(typ fb.Type) bool {
	return ctx.zeroableType(typ, make(map[fb.Decl]bool))
}

func (ctx *GenContext) zeroableType(typ fb.Type, visiting map[fb.Decl]bool) bool {
	switch t := typ.(type) {
	case *fb.SimpleType:
		return true
	case *fb.PointerType:
		return true
	case *fb.ArrayType:
		return ctx.zeroableType(t.Element, visiting)
	case *fb.FuncType:
		return false
	case *fb.DeclType:
		switch d := t.Decl.(type) {
		case *fb.Enum:
			return true
		case *fb.Struct:
			if visiting[d] {
				return true // Break impossible by-value cycles
			}

			visiting[d] = true
			defer delete(visiting, d)

			for _, field := range d.Fields {
				if slices.Contains(field.Attributes, "required") {
					return false
				}

				if !ctx.zeroableType(field.Type, visiting) {
					return false
				}
			}

			return true
		}
	}

	panic(fmt.Sprintf("zeroableType: unhandled type %T", typ))
}

// defaultExpr returns an initializer expression for a field of a non-zeroable
// type. Unions pick their first member and structs without a default constant
// fall back to inline literals.
func (ctx *GenContext) defaultExpr(typ fb.Type, depth int) string {
	if ctx.zeroable(typ) {
		panic("defaultExpr called with a zeroable type")
	}

	switch t := typ.(type) {
	case *fb.PointerType:
		return "null"

	case *fb.DeclType:
		s, ok := t.Decl.(*fb.Struct)
		if !ok {
			panic("defaultExpr: unexpected declaration")
		}

		// Unions initialize their first member
		if s.Layout == fb.Union && len(s.Fields) > 0 {
			first := s.Fields[0]
			return fmt.Sprintf("%s { %s: %s }", s.Name, first.Name, ctx.defaultExpr(first.Type, depth+1))
		}

		// Structs with a default constant reference it
		if _, ok := ctx.structDefaults[s.Name]; ok {
			return s.Name + "::DEFAULT"
		}

		// Inline literal for structs without a default constant
		if depth > 32 {
			panic("defaultExpr: recursion limit exceeded")
		}

		parts := make([]string, 0, len(s.Fields))

		for _, field := range s.Fields {
			if slices.Contains(field.Attributes, "required") || !ctx.zeroable(field.Type) {
				parts = append(parts, fmt.Sprintf("%s: %s", field.Name, ctx.defaultExpr(field.Type, depth+1)))
			}
		}

		return fmt.Sprintf("%s { %s }", s.Name, strings.Join(parts, ", "))
	}

	panic(fmt.Sprintf("defaultExpr: unhandled type %T", typ))
}

func (ctx *GenContext) SortedEnums() []*fb.Enum {
	list := make([]*fb.Enum, 0, len(ctx.enums))

	for _, decl := range ctx.enums {
		list = append(list, decl)
	}

	slices.SortFunc(list, func(a, c *fb.Enum) int {
		return strings.Compare(a.Name, c.Name)
	})

	return list
}

func (ctx *GenContext) SortedStructs() []*fb.Struct {
	list := make([]*fb.Struct, 0, len(ctx.structs))

	for _, decl := range ctx.structs {
		list = append(list, decl)
	}

	slices.SortFunc(list, func(a, c *fb.Struct) int {
		return strings.Compare(a.Name, c.Name)
	})

	return list
}

func (ctx *GenContext) SortedHandles() []*fb.Struct {
	list := make([]*fb.Struct, 0, len(ctx.handles))

	for _, decl := range ctx.handles {
		list = append(list, decl)
	}

	slices.SortFunc(list, func(a, c *fb.Struct) int {
		return strings.Compare(a.Name, c.Name)
	})

	return list
}

func (ctx *GenContext) Validate() {
	names := make(map[string]string)

	for _, decl := range ctx.enums {
		if existing, ok := names[decl.Name]; ok {
			panic("duplicate decl name " + decl.Name + " (" + existing + ")")
		}

		names[decl.Name] = "enum"
	}

	for _, decl := range ctx.structs {
		if existing, ok := names[decl.Name]; ok {
			panic("duplicate decl name " + decl.Name + " (" + existing + ")")
		}

		names[decl.Name] = "struct"
	}

	for _, decl := range ctx.handles {
		if existing, ok := names[decl.Name]; ok {
			panic("duplicate decl name " + decl.Name + " (" + existing + ")")
		}

		names[decl.Name] = "handle"
	}

	for _, c := range ctx.constList {
		if existing, ok := names[c.name]; ok {
			panic("duplicate decl name " + c.name + " (" + existing + ")")
		}

		names[c.name] = "constant"
	}
}
