package main

import (
	"cmp"
	"slices"
	"strconv"
	"strings"

	"github.com/fireball-lang/bindgen"
	"github.com/fireball-lang/bindgen/fb"
)

type enumBuilder struct {
	decl *fb.Enum

	ctx *GenContext

	raw     string
	bitmask bool
	prefix  string

	cases    map[string]*fb.Case
	rawCases map[string]string
	list     []*fb.Case
}

func (ctx *GenContext) extensionCase(ext string) string {
	return bindgen.SnakeToPascalCase(strings.TrimPrefix(ext, "VK_"))
}

func (ctx *GenContext) WriteExtensionEnum(w fb.Writer) {
	w.Write("\n")
	w.Write("pub enum Extension {\n")

	for _, ext := range ctx.extensions {
		w.Write("    %s,\n", ctx.extensionCase(ext))
	}

	w.Write("}\n")

	w.Write("\n")
	w.Write("pub func extension_name(ext: Extension) StringView {\n")

	for _, ext := range ctx.extensions {
		w.Write("    if (ext == Extension::%s) return \"%s\";\n", ctx.extensionCase(ext), ext)
	}

	w.Write("\n")
	w.Write("    return \"\";\n")
	w.Write("}\n")
}

func (ctx *GenContext) CreateEnums() {
	builders := make(map[string]*enumBuilder)

	// All canonical enum names, to distinguish excluded enums from unknown ones
	ctx.enumNames = make(map[string]bool)
	for i := range ctx.reg.Enums {
		enums := &ctx.reg.Enums[i]
		if enums.Type == "enum" || enums.Type == "bitmask" {
			ctx.enumNames[enums.Name] = true
		}
	}

	for i := range ctx.reg.Enums {
		enums := &ctx.reg.Enums[i]

		bitmask := enums.Type == "bitmask"
		if enums.Type != "enum" && !bitmask {
			continue
		}

		if !ctx.included[enums.Name] {
			continue
		}

		decl := &fb.Enum{
			OutputIndex:   EnumsOutput,
			Documentation: cleanComment(enums.Comment),
			Name:          ctx.enumDeclName(enums.Name, bitmask),
			Bitfield:      bitmask,
		}

		if bitmask {
			text := "u32"
			if enums.BitWidth == 64 {
				text = "u64"
			}

			decl.Type = &fb.SimpleType{Text: text}
		}

		builder := &enumBuilder{
			decl:     decl,
			ctx:      ctx,
			raw:      enums.Name,
			bitmask:  bitmask,
			prefix:   ctx.enumCasePrefix(enums.Name),
			cases:    make(map[string]*fb.Case),
			rawCases: make(map[string]string),
		}

		builders[enums.Name] = builder
		ctx.enums[enums.Name] = decl
	}

	// Canonical cases
	for i := range ctx.reg.Enums {
		enums := &ctx.reg.Enums[i]

		builder, ok := builders[enums.Name]
		if !ok {
			continue
		}

		for j := range enums.Cases {
			cas := &enums.Cases[j]

			if cas.Alias != "" {
				builder.addAlias(cas.Name, cas.Alias)
				continue
			}

			value := cas.Value
			if value == "" {
				if cas.BitPos == nil {
					panic("no value for enum case: " + cas.Name)
				}

				value = strconv.FormatUint(uint64(1)<<uint(*cas.BitPos), 10)
			}

			builder.add(cas.Name, value, cas.Comment)
		}
	}

	// Cases contributed by core versions
	for _, feature := range ctx.reg.Features {
		if !slices.Contains(feature.Apis, "vulkan") {
			continue
		}

		for _, require := range feature.Requires {
			if !apiSupported(require.Api) {
				continue
			}

			for i := range require.Enums {
				ctx.mergeEnumCase(builders, &require.Enums[i], 0)
			}
		}

		for _, remove := range feature.Removes {
			if !apiSupported(remove.Api) {
				continue
			}

			for _, e := range remove.Enums {
				if e.Extends == "" {
					continue
				}

				builder, ok := builders[ctx.resolveAlias(e.Extends)]
				if !ok {
					continue
				}

				builder.remove(e.Name)
			}
		}
	}

	// Cases contributed by cross-vendor extensions
	for _, ext := range ctx.reg.Extensions {
		if !slices.Contains(ext.Supported, "vulkan") || !crossVendor(ext.Name) {
			continue
		}

		number := 0
		if value, err := strconv.Atoi(ext.Number); err == nil {
			number = value
		}

		for _, require := range ext.Requires {
			if !apiSupported(require.Api) {
				continue
			}

			for i := range require.Enums {
				ctx.mergeEnumCase(builders, &require.Enums[i], number)
			}
		}
	}

	for _, builder := range builders {
		builder.finalize()
	}
}

func (b *enumBuilder) add(raw string, value string, comment string) {
	name := CheckName(b.ctx.caseName(b.raw, raw, b.bitmask, true))

	if existing, ok := b.cases[name]; ok {
		if sameValue(existing.Value, value) {
			return
		}

		// Names that only differ by a vendor tag can carry different values,
		// fall back to keeping the tag (e.g. VK_VENDOR_ID_VIV)
		name = CheckName(b.ctx.caseName(b.raw, raw, b.bitmask, false))

		if existing, ok := b.cases[name]; ok {
			if !sameValue(existing.Value, value) {
				panic("conflicting value for case " + name + ": " + existing.Value + " vs " + value)
			}

			return
		}
	}

	cas := &fb.Case{
		Documentation: comment,
		Name:          name,
		Value:         value,
	}

	b.cases[name] = cas
	b.rawCases[raw] = name
	b.list = append(b.list, cas)

	if b.raw == "VkStructureType" {
		if value, ok := ParseInt(cas.Value); ok {
			b.ctx.structureTypes[raw] = value
		}
	}
}

func (b *enumBuilder) addAlias(raw string, alias string) {
	if _, ok := b.rawCases[alias]; !ok {
		panic("unknown alias " + alias + " for case " + raw)
	}

	if b.raw == "VkStructureType" {
		target := b.cases[b.rawCases[alias]]

		if value, ok := ParseInt(target.Value); ok {
			b.ctx.structureTypes[raw] = value
		}
	}

	// The canonical case is always kept
}

func (b *enumBuilder) remove(raw string) {
	name, ok := b.rawCases[raw]
	if !ok {
		return
	}

	cas, ok := b.cases[name]
	if !ok {
		return
	}

	delete(b.cases, name)
	delete(b.rawCases, raw)

	idx := slices.Index(b.list, cas)
	b.list = slices.Delete(b.list, idx, idx+1)
}

func (ctx *GenContext) mergeEnumCase(builders map[string]*enumBuilder, e *ExtensionEnum, extNumber int) {
	if e.Extends == "" {
		return
	}

	if e.ExtNumber != nil {
		extNumber = *e.ExtNumber
	}

	extends := ctx.resolveAlias(e.Extends)

	builder, ok := builders[extends]
	if !ok {
		if ctx.enumNames[extends] {
			return // The target enum is not included
		}

		panic("unknown extends target: " + e.Extends)
	}

	// Compatibility aliases of promoted cases are dropped
	if e.Alias != "" {
		return
	}

	value := ""
	switch {
	case e.Value != "":
		value = e.Value

	case e.Offset != nil:
		if extNumber == 0 {
			panic("missing extnumber for offset case: " + e.Name)
		}

		merged := int64(1e9) + int64(extNumber-1)*1000 + int64(*e.Offset)
		if e.Dir == "-" {
			merged = -merged
		}

		value = strconv.FormatInt(merged, 10)

	case e.BitPos != nil:
		value = strconv.FormatUint(uint64(1)<<uint(*e.BitPos), 10)

	default:
		panic("no value for merged case: " + e.Name)
	}

	builder.add(e.Name, value, "")
}

func (b *enumBuilder) finalize() {
	slices.SortFunc(b.list, func(a, c *fb.Case) int {
		aNeg, aValue := valueKey(a.Value)
		cNeg, cValue := valueKey(c.Value)

		if aNeg != cNeg {
			if aNeg {
				return -1
			}

			return 1
		}

		return cmp.Compare(aValue, cValue)
	})

	b.decl.Cases = b.list

	// Underlying type for non-bitmask enums
	if !b.bitmask {
		negative := false
		for _, cas := range b.list {
			if neg, _ := valueKey(cas.Value); neg {
				negative = true
				break
			}
		}

		text := "u32"
		if negative {
			text = "i32"
		}

		b.decl.Type = &fb.SimpleType{Text: text}
	}
}

func valueKey(value string) (negative bool, magnitude uint64) {
	str := strings.Trim(strings.TrimSpace(value), "()")

	if rest, ok := strings.CutPrefix(str, "-"); ok {
		negative = true
		str = rest
	}

	base := 10
	if rest, ok := strings.CutPrefix(str, "0x"); ok {
		base = 16
		str = rest
	}

	magnitude, _ = strconv.ParseUint(str, base, 64)

	return negative, magnitude
}

func sameValue(a string, c string) bool {
	aNeg, aValue := valueKey(a)
	cNeg, cValue := valueKey(c)

	return aNeg == cNeg && aValue == cValue
}

func (ctx *GenContext) enumDeclName(raw string, bitmask bool) string {
	base := strings.TrimPrefix(raw, "Vk")

	if bitmask {
		if idx := strings.Index(base, "FlagBits"); idx >= 0 {
			base = base[:idx] + "Flags" + base[idx+len("FlagBits"):]
		}
	}

	return CheckName(base)
}

func (ctx *GenContext) enumCasePrefix(raw string) string {
	base := strings.TrimPrefix(raw, "Vk")
	base = stripTag(base, ctx.tags)

	if idx := strings.Index(base, "FlagBits"); idx >= 0 {
		rest := base[idx+len("FlagBits"):]
		digits := strings.TrimLeft(rest, "0123456789")
		number := rest[:len(rest)-len(digits)]

		base = base[:idx]

		if number != "" {
			return screaming(base) + "_" + number + "_"
		}
	}

	return screaming(base) + "_"
}

func screaming(str string) string {
	return strings.ToUpper(bindgen.CamelToSnakeCase(str))
}

func stripTag(str string, tags []string) string {
	for {
		stripped := false

		for _, tag := range tags {
			if rest, ok := strings.CutSuffix(str, tag); ok && rest != "" {
				str = strings.TrimSuffix(rest, "_")
				stripped = true
				break
			}
		}

		if !stripped {
			return str
		}
	}
}

func (ctx *GenContext) caseName(enumRaw string, raw string, bitmask bool, strip bool) string {
	name := raw

	if rest, ok := strings.CutPrefix(name, "VK_"); ok {
		name = rest
	}

	if prefix := ctx.enumCasePrefix(enumRaw); strings.HasPrefix(name, prefix) {
		name = name[len(prefix):]
	}

	if strip {
		name = stripTag(name, ctx.tags)
	}

	if bitmask {
		if rest, ok := strings.CutSuffix(name, "_BIT"); ok {
			name = rest
		}
	}

	name = bindgen.SnakeToPascalCase(name)

	if name == "" {
		panic("empty case name for: " + raw)
	}

	if name[0] >= '0' && name[0] <= '9' {
		name = stripTag(strings.TrimPrefix(enumRaw, "Vk"), ctx.tags) + name
	}

	return name
}
