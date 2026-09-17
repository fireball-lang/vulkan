package main

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/fireball-lang/bindgen"
	"github.com/fireball-lang/bindgen/fb"
)

const (
	EnumsOutput     = 0
	ConstantsOutput = 1
	HandlesOutput   = 2
	StructsOutput   = 3
	AliasesOutput   = 4
	StubsOutput     = 5
	ApiOutput       = 6

	WrappersOutput     = 7
	LoadInstanceOutput = 8
	LoadDeviceOutput   = 9
)

type commandLevel uint8

const (
	levelGlobal commandLevel = iota
	levelInstance
	levelDevice
)

// commandLevel classifies a command into the stage that is able to load it.
// Global commands are exported by the loader itself and can be queried with a
// null instance, instance commands are queried through vkGetInstanceProcAddr
// with an instance, and device commands are queried through vkGetDeviceProcAddr
// with a device.
func (ctx *GenContext) commandLevel(command *Command) commandLevel {
	name := command.Name
	if name == "" {
		name = command.Proto.Name
	}

	switch name {
	case "vkGetInstanceProcAddr":
		// Loaded directly from the library
		return levelGlobal
	case "vkGetDeviceProcAddr":
		// Queried through vkGetInstanceProcAddr despite taking a device
		return levelInstance
	}

	for _, param := range command.Params {
		if !apiSupported(param.Api) {
			continue
		}

		switch param.Base {
		case "VkInstance", "VkPhysicalDevice":
			return levelInstance
		case "VkDevice", "VkQueue", "VkCommandBuffer":
			return levelDevice

		default:
			// Commands that do not take a dispatchable handle are global
			// (e.g. vkCreateInstance)
			return levelGlobal
		}
	}

	return levelGlobal
}

var voidPointer = &fb.PointerType{Mutable: true, Pointee: &fb.SimpleType{Text: "void"}}

type GenContext struct {
	reg Registry

	enums   map[string]*fb.Enum
	structs map[string]*fb.Struct
	handles map[string]*fb.Struct

	flagBits   map[string]string
	flagSimple map[string]fb.Type
	basetypes  map[string]fb.Type
	externals  map[string]fb.Type
	aliases    map[string]string

	included   map[string]bool
	commandSet map[string]bool
	enumNames  map[string]bool

	extensions []string

	commandLevels map[string]commandLevel

	structureTypes map[string]int64
	structDefaults map[string]int64
	constList      []constant

	constants map[string]int64
	tags      []string
}

// constant is a standalone registry constant that is emitted as a top level
// constant.
type constant struct {
	name  string
	kind  string
	value string
}

func NewContext(reg Registry) *GenContext {
	ctx := &GenContext{
		reg:            reg,
		enums:          make(map[string]*fb.Enum),
		structs:        make(map[string]*fb.Struct),
		handles:        make(map[string]*fb.Struct),
		flagBits:       make(map[string]string),
		flagSimple:     make(map[string]fb.Type),
		basetypes:      make(map[string]fb.Type),
		externals:      make(map[string]fb.Type),
		aliases:        make(map[string]string),
		constants:      make(map[string]int64),
		structureTypes: make(map[string]int64),
		structDefaults: make(map[string]int64),
	}

	for _, tag := range reg.Tags {
		ctx.tags = append(ctx.tags, tag.Name)
	}

	ctx.collectTypes()
	ctx.collectConstants()

	return ctx
}

func typeName(t *Type) string {
	if t.AttrName != "" {
		return t.AttrName
	}

	return t.NameElem
}

func apiSupported(api StringSlice) bool {
	if len(api) == 0 {
		return true
	}

	return slices.Contains(api, "vulkan")
}

func crossVendor(name string) bool {
	return strings.HasPrefix(name, "VK_KHR_") || strings.HasPrefix(name, "VK_EXT_")
}

func (ctx *GenContext) findCommand(name string) *Command {
	for i := range ctx.reg.Commands {
		command := &ctx.reg.Commands[i]

		if !apiSupported(command.Api) {
			continue
		}

		if command.Name == name || command.Proto.Name == name {
			return command
		}
	}

	return nil
}

// ComputeIncluded computes the set of types and commands provided by the core
// versions and the cross-vendor extensions (KHR and EXT). Types referenced by
// included commands and structs are pulled in transitively.
func (ctx *GenContext) ComputeIncluded() {
	ctx.included = make(map[string]bool)
	ctx.commandSet = make(map[string]bool)

	var queue []string

	push := func(raw string) {
		if raw == "" {
			return
		}

		raw = ctx.resolveAlias(raw)

		if bits, ok := ctx.flagBits[raw]; ok {
			raw = bits
		}

		if ctx.included[raw] {
			return
		}

		ctx.included[raw] = true
		queue = append(queue, raw)
	}

	// References made by struct and union fields
	structRefs := make(map[string][]string)

	for i := range ctx.reg.Types {
		t := &ctx.reg.Types[i]

		if (t.Category != "struct" && t.Category != "union") || !apiSupported(t.Api) || t.Alias != "" {
			continue
		}

		var refs []string

		for _, m := range t.Fields {
			if !apiSupported(m.Api) || m.Base == "" {
				continue
			}

			name := ctx.resolveAlias(m.Base)
			if bits, ok := ctx.flagBits[name]; ok {
				name = bits
			}

			refs = append(refs, name)
		}

		structRefs[typeName(t)] = refs
	}

	walkCommand := func(command *Command) {
		for _, param := range command.Params {
			if !apiSupported(param.Api) {
				continue
			}

			push(param.Base)
		}

		push(command.Proto.Returns.Base)
	}

	addRequires := func(requires []RefList) {
		for _, require := range requires {
			if !apiSupported(require.Api) {
				continue
			}

			for _, t := range require.Types {
				push(t.Name)
			}

			for _, c := range require.Commands {
				ctx.commandSet[c.Name] = true

				command := ctx.findCommand(c.Name)
				if command == nil {
					continue
				}

				if command.Alias != "" {
					ctx.commandSet[command.Alias] = true

					if target := ctx.findCommand(command.Alias); target != nil {
						walkCommand(target)
					}

					continue
				}

				walkCommand(command)
			}
		}
	}

	for _, feature := range ctx.reg.Features {
		if !slices.Contains(feature.Apis, "vulkan") {
			continue
		}

		addRequires(feature.Requires)
	}

	for _, ext := range ctx.reg.Extensions {
		if !slices.Contains(ext.Supported, "vulkan") || !crossVendor(ext.Name) {
			continue
		}

		ctx.extensions = append(ctx.extensions, ext.Name)

		addRequires(ext.Requires)
	}

	slices.Sort(ctx.extensions)

	// Transitive struct references
	for len(queue) > 0 {
		raw := queue[0]
		queue = queue[1:]

		for _, ref := range structRefs[raw] {
			push(ref)
		}
	}
}

func (ctx *GenContext) collectTypes() {
	for i := range ctx.reg.Types {
		t := &ctx.reg.Types[i]

		if !apiSupported(t.Api) {
			continue
		}

		name := typeName(t)

		if t.Alias != "" {
			ctx.aliases[name] = t.Alias
			continue
		}

		switch t.Category {
		case "bitmask":
			bits := t.BitValues
			if bits == "" {
				bits = t.Requires
			}

			if bits != "" {
				ctx.flagBits[name] = bits
			} else if t.TypeElem == "VkFlags64" {
				ctx.flagSimple[name] = &fb.SimpleType{Text: "u64"}
			} else {
				ctx.flagSimple[name] = &fb.SimpleType{Text: "u32"}
			}

		case "handle":
			// Handled in CreateHandles

		case "basetype":
			ctx.basetypes[name] = mapBaseType(t.TypeElem)

		default:
			if t.Category == "" && t.Requires != "" && (strings.Contains(t.Requires, "/") || strings.HasSuffix(t.Requires, ".h")) {
				ctx.externals[name] = mapExternalType(name)
			}
		}
	}
}

func mapBaseType(elem string) fb.Type {
	switch elem {
	case "uint8_t":
		return &fb.SimpleType{Text: "u8"}
	case "uint16_t":
		return &fb.SimpleType{Text: "u16"}
	case "uint32_t":
		return &fb.SimpleType{Text: "u32"}
	case "int32_t":
		return &fb.SimpleType{Text: "i32"}
	case "uint64_t":
		return &fb.SimpleType{Text: "u64"}
	case "int64_t":
		return &fb.SimpleType{Text: "i64"}
	case "float":
		return &fb.SimpleType{Text: "f32"}
	case "double":
		return &fb.SimpleType{Text: "f64"}

	default:
		return voidPointer
	}
}

func mapExternalType(name string) fb.Type {
	switch name {
	case "Window", "VisualID", "RROutput":
		return &fb.SimpleType{Text: "u64"}
	case "DWORD", "GgpStreamDescriptor", "GgpFrameToken", "xcb_visualid_t", "xcb_window_t":
		return &fb.SimpleType{Text: "u32"}
	case "zx_handle_t":
		return &fb.SimpleType{Text: "i32"}
	case "LPCWSTR":
		return &fb.PointerType{Pointee: &fb.SimpleType{Text: "u16"}}
	case "bool":
		return &fb.SimpleType{Text: "bool"}

	default:
		return voidPointer
	}
}

type constEntry struct {
	name     string
	alias    string
	value    string
	typ      string
	caseLike bool
	keep     bool
	source   string
}

func (ctx *GenContext) collectConstants() {
	var entries []constEntry

	// Standalone constants (e.g. the API Constants block)
	for i := range ctx.reg.Enums {
		enums := &ctx.reg.Enums[i]
		if enums.Type != "constants" {
			continue
		}

		for _, cas := range enums.Cases {
			entries = append(entries, constEntry{
				name:   cas.Name,
				alias:  cas.Alias,
				value:  cas.Value,
				typ:    cas.Type,
				keep:   ctx.constantVendorTag(cas.Name) == "",
				source: "api constants",
			})
		}
	}

	// Core versions
	for _, feature := range ctx.reg.Features {
		if !slices.Contains(feature.Apis, "vulkan") {
			continue
		}

		for _, require := range feature.Requires {
			if !apiSupported(require.Api) {
				continue
			}

			for _, e := range require.Enums {
				entries = append(entries, constEntry{
					name:     e.Name,
					alias:    e.Alias,
					value:    e.Value,
					typ:      e.Type,
					caseLike: e.Extends != "" || e.Offset != nil || e.BitPos != nil,
					keep:     true,
					source:   "core",
				})
			}
		}
	}

	// Cross-vendor extensions
	for _, ext := range ctx.reg.Extensions {
		if !slices.Contains(ext.Supported, "vulkan") || !crossVendor(ext.Name) {
			continue
		}

		for _, require := range ext.Requires {
			if !apiSupported(require.Api) {
				continue
			}

			for _, e := range require.Enums {
				entries = append(entries, constEntry{
					name:     e.Name,
					alias:    e.Alias,
					value:    e.Value,
					typ:      e.Type,
					caseLike: e.Extends != "" || e.Offset != nil || e.BitPos != nil,
					keep:     true,
					source:   ext.Name,
				})
			}
		}
	}

	// Integer lookup for array dimensions (unfiltered, vendor included)
	for _, entry := range entries {
		if entry.value == "" {
			continue
		}

		if value, ok := ParseInt(entry.value); ok {
			ctx.constants[entry.name] = value
		}
	}

	for {
		changed := false

		for _, entry := range entries {
			if entry.alias == "" {
				continue
			}

			if _, ok := ctx.constants[entry.name]; ok {
				continue
			}

			if value, ok := ctx.constants[entry.alias]; ok {
				ctx.constants[entry.name] = value
				changed = true
			}
		}

		if !changed {
			break
		}
	}

	// Emission list
	kept := make(map[string]constEntry, len(entries))

	for _, entry := range entries {
		if !entry.keep || entry.caseLike || constantSkipName(entry.name) || constantIsString(entry.value) {
			continue
		}

		if entry.value == "" && entry.alias == "" {
			continue
		}

		if _, ok := kept[entry.name]; !ok {
			kept[entry.name] = entry
		}
	}

	// Resolve aliases against the kept entries
	for {
		changed := false

		for name, entry := range kept {
			if entry.alias == "" {
				continue
			}

			target, ok := kept[entry.alias]
			if !ok {
				panic("unknown constant alias " + entry.alias + " for " + name + " (from " + entry.source + ")")
			}

			entry.value = target.value
			entry.typ = target.typ
			entry.alias = ""
			kept[name] = entry
			changed = true
		}

		if !changed {
			break
		}
	}

	for name, entry := range kept {
		if entry.typ == "" {
			panic("constant " + name + " (" + entry.source + ") has no type")
		}

		kind := constantKind(entry.typ, name)

		ctx.constList = append(ctx.constList, constant{
			name:  CheckName(strings.TrimPrefix(name, "VK_")),
			kind:  kind,
			value: constantValue(kind, entry.value),
		})
	}

	slices.SortFunc(ctx.constList, func(a, c constant) int {
		return strings.Compare(a.name, c.name)
	})

}

// constantVendorTag returns the vendor tag a constant name ends with, if it is
// neither KHR nor EXT.
func (ctx *GenContext) constantVendorTag(name string) string {
	for _, tag := range ctx.tags {
		if tag == "KHR" || tag == "EXT" {
			continue
		}

		if strings.HasSuffix(name, "_"+tag) {
			return tag
		}
	}

	return ""
}

func constantSkipName(name string) bool {
	return strings.HasSuffix(name, "SPEC_VERSION") || strings.HasSuffix(name, "EXTENSION_NAME")
}

func constantIsString(value string) bool {
	return strings.HasPrefix(value, "\"")
}

func constantKind(typ string, name string) string {
	switch typ {
	case "uint32_t":
		return "u32"
	case "uint64_t":
		return "u64"
	case "float":
		return "f32"

	default:
		panic("unknown constant type " + typ + " for " + name)
	}
}

// constantValue evaluates the raw value of a constant into a fireball literal.
func constantValue(kind string, raw string) string {
	value := strings.TrimSpace(raw)

	if strings.HasPrefix(value, "(") && strings.HasSuffix(value, ")") {
		value = value[1 : len(value)-1]
	}

	// Bitwise not expressions (e.g. (~0U))
	if strings.HasPrefix(value, "~") {
		operand := strings.TrimRight(value[1:], "UL")

		parsed, err := strconv.ParseUint(operand, 0, 64)
		if err != nil {
			panic("invalid constant expression: " + raw)
		}

		if kind == "u32" {
			return fmt.Sprintf("0x%X", ^parsed&0xFFFFFFFF)
		}

		return fmt.Sprintf("0x%X", ^parsed)
	}

	if kind == "f32" {
		value = strings.TrimSuffix(value, "F")
		value = strings.TrimSuffix(value, "f")

		return value + "f"
	}

	return value
}

func (ctx *GenContext) resolveAlias(name string) string {
	for range 32 {
		alias, ok := ctx.aliases[name]
		if !ok {
			break
		}

		name = alias
	}

	return name
}

func (ctx *GenContext) resolveType(word string) fb.Type {
	word = ctx.resolveAlias(word)

	switch word {
	case "void":
		return &fb.SimpleType{Text: "void"}
	case "char", "int8_t", "uint8_t":
		return &fb.SimpleType{Text: "u8"}
	case "int16_t", "uint16_t":
		return &fb.SimpleType{Text: "u16"}
	case "int", "int32_t":
		return &fb.SimpleType{Text: "i32"}
	case "uint32_t":
		return &fb.SimpleType{Text: "u32"}
	case "int64_t":
		return &fb.SimpleType{Text: "i64"}
	case "size_t", "uint64_t":
		return &fb.SimpleType{Text: "u64"}
	case "float":
		return &fb.SimpleType{Text: "f32"}
	case "double":
		return &fb.SimpleType{Text: "f64"}
	case "bool":
		return &fb.SimpleType{Text: "bool"}
	}

	if strings.HasPrefix(word, "PFN_") {
		return voidPointer
	}

	if decl, ok := ctx.enums[word]; ok {
		return &fb.DeclType{Decl: decl}
	}

	if decl, ok := ctx.structs[word]; ok {
		return &fb.DeclType{Decl: decl}
	}

	if bits, ok := ctx.flagBits[word]; ok {
		decl, ok := ctx.enums[bits]
		if !ok {
			panic("missing bits enum for flags type: " + word)
		}

		return &fb.DeclType{Decl: decl}
	}

	if typ, ok := ctx.flagSimple[word]; ok {
		return typ
	}

	// Dispatchable and non-dispatchable handles are single-field structs
	if decl, ok := ctx.handles[word]; ok {
		return &fb.DeclType{Decl: decl}
	}

	if typ, ok := ctx.basetypes[word]; ok {
		return typ
	}

	if typ, ok := ctx.externals[word]; ok {
		return typ
	}

	panic("failed to resolve type: " + word)
}

func (ctx *GenContext) buildType(parts *typeParts) fb.Type {
	typ := ctx.resolveType(parts.Base)

	for i := 0; i < parts.Pointer; i++ {
		typ = &fb.PointerType{Mutable: !parts.Const, Pointee: typ}
	}

	for _, v := range slices.Backward(parts.Dims) {
		typ = &fb.ArrayType{Size: ctx.dimSize(v), Element: typ}
	}

	return typ
}

func (ctx *GenContext) dimSize(dim string) uint32 {
	if value, err := strconv.ParseUint(dim, 10, 32); err == nil {
		return uint32(value)
	}

	value, ok := ctx.constants[dim]
	if !ok || value < 0 {
		panic("unknown array dimension: " + dim)
	}

	return uint32(value)
}

func ParseInt(str string) (int64, bool) {
	str = strings.Trim(strings.TrimSpace(str), "()")

	negative := false
	if rest, ok := strings.CutPrefix(str, "-"); ok {
		negative = true
		str = rest
	}

	base := 10
	if rest, ok := strings.CutPrefix(strings.ToLower(str), "0x"); ok {
		base = 16
		str = rest
	}

	value, err := strconv.ParseInt(str, base, 64)
	if err != nil {
		return 0, false
	}

	if negative {
		value = -value
	}

	return value, true
}

func CheckName(name string) string {
	if slices.Contains(bindgen.KEYWORDS, name) {
		return name + "_"
	}

	return name
}

func cleanComment(comment string) string {
	comment = strings.TrimSpace(comment)

	if rest, ok := strings.CutPrefix(comment, "// "); ok {
		comment = strings.TrimSpace(rest)
	}

	return comment
}
