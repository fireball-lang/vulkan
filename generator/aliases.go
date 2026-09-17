package main

import (
	"cmp"
	"slices"
	"strconv"
	"strings"

	"github.com/fireball-lang/bindgen"
	"github.com/fireball-lang/bindgen/fb"
)

func (ctx *GenContext) CreateAliases() []*fb.Alias {
	aliases := make(map[string]*fb.Alias)
	pending := make(map[*fb.Alias]string)
	ctx.commandLevels = make(map[string]commandLevel)

	var list []*fb.Alias

	for i := range ctx.reg.Commands {
		command := &ctx.reg.Commands[i]

		if !apiSupported(command.Api) {
			continue
		}

		name := command.Name
		if name == "" {
			name = command.Proto.Name
		}

		if name == "" {
			continue
		}

		if !ctx.commandSet[name] {
			continue
		}

		display := "Vk" + strings.TrimPrefix(name, "vk")

		if _, ok := aliases[display]; ok {
			continue
		}

		alias := &fb.Alias{
			OutputIndex: AliasesOutput,
			Name:        display,
		}

		if command.Alias != "" {
			pending[alias] = command.Alias
		} else {
			ctx.commandLevels[display] = ctx.commandLevel(command)

			params := make([]*fb.Param, 0, len(command.Params))

			for j, param := range command.Params {
				if !apiSupported(param.Api) {
					continue
				}

				paramName := memberName(param.Name)
				if paramName == "" {
					paramName = "arg" + strconv.Itoa(j)
				}

				params = append(params, &fb.Param{
					Name: paramName,
					Type: ctx.buildType(&param.typeParts),
				})
			}

			alias.Type = &fb.FuncType{
				Params:  params,
				Returns: ctx.buildType(&command.Proto.Returns),
			}
		}

		aliases[display] = alias
		list = append(list, alias)
	}

	// Resolve aliased commands against their targets
	for _, alias := range list {
		target, ok := pending[alias]
		if !ok {
			continue
		}

		seen := make(map[string]bool)

		for {
			resolved, ok := aliases["Vk"+strings.TrimPrefix(target, "vk")]
			if !ok {
				panic("unknown aliased command: " + target)
			}

			if resolved.Type != nil {
				alias.Type = resolved.Type
				ctx.commandLevels[alias.Name] = ctx.commandLevels[resolved.Name]
				break
			}

			if seen[resolved.Name] {
				panic("circular command alias: " + target)
			}

			seen[resolved.Name] = true

			next, ok := pending[resolved]
			if !ok {
				panic("aliased command without signature: " + resolved.Name)
			}

			target = next
		}
	}

	slices.SortFunc(list, func(a, b *fb.Alias) int {
		return cmp.Compare(a.Name, b.Name)
	})

	return list
}

func WriteAliases(w fb.Writer, aliases []*fb.Alias) {
	for _, alias := range aliases {
		w.Write("\n")
		alias.Write(w)
	}
}

func WriteStubs(w fb.Writer, aliases []*fb.Alias) {
	for _, alias := range aliases {
		w.Write("\n")

		// Signature
		typ := alias.Type.(*fb.FuncType)
		name := "stub_" + bindgen.CamelToSnakeCase(strings.TrimPrefix(alias.Name, "Vk"))

		params := make([]*fb.Param, len(typ.Params))

		for i, param := range typ.Params {
			params[i] = &fb.Param{
				Name: "_" + param.Name,
				Type: param.Type,
			}
		}

		w.Write("pub ")
		returns := fb.WriteSignature(w, name, fb.None, params, typ.Returns)

		// Body
		w.Write(" {\n    panic(\"Vulkan function not found\");\n")

		if returns {
			switch typ := typ.Returns.(type) {
			case *fb.SimpleType:
				if typ.Text == "bool" {
					w.Write("    return false;\n")
				} else {
					w.Write("    return 0;\n")
				}

			case *fb.PointerType:
				w.Write("    return null;\n")

			case *fb.DeclType:
				w.Write("    return 0 as ")
				typ.Write(w)
				w.Write(";\n")
			}
		}

		w.Write("}\n")
	}
}
