package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/fireball-lang/bindgen"
	"github.com/fireball-lang/bindgen/fb"
)

func main() {
	reg, err := LoadRegistry()
	if err != nil {
		panic(err.Error())
	}

	ctx := NewContext(reg)
	ctx.ComputeIncluded()
	ctx.CreateEnums()
	ctx.CreateHandles()
	ctx.CreateStructs()
	aliases := ctx.CreateAliases()
	ctx.Validate()

	outputs := []bindgen.File{
		{Path: "../src/enums.fb", Module: "vulkan"},
		{Path: "../src/constants.fb", Module: "vulkan"},
		{Path: "../src/handles.fb", Module: "vulkan"},
		{Path: "../src/structs.fb", Module: "vulkan"},
		{Path: "../src/aliases.fb", Module: "vulkan"},
		{Path: "../src/stubs.fb", Module: "vulkan"},
		{Path: "../src/api.fb", Module: "vulkan"},
		{Path: "../src/wrappers.fb", Module: "vulkan"},
		{Path: "../src/api_load_instance.fb", Module: "vulkan"},
		{Path: "../src/api_load_device.fb", Module: "vulkan"},
	}

	// Constants
	{
		w, err := bindgen.NewFileWriter(outputs, ConstantsOutput)
		if err != nil {
			panic(err.Error())
		}

		for _, c := range ctx.constList {
			w.Write("\n")
			(&fb.Const{
				OutputIndex: ConstantsOutput,
				Name:        c.name,
				Type:        &fb.SimpleType{Text: c.kind},
				Value:       c.value,
			}).Write(w)
		}

		_ = w.Close()
	}

	// Enums
	{
		w, err := bindgen.NewFileWriter(outputs, EnumsOutput)
		if err != nil {
			panic(err.Error())
		}

		ctx.WriteExtensionEnum(w)

		for _, decl := range ctx.SortedEnums() {
			w.Write("\n")
			decl.Write(w)
		}

		_ = w.Close()
	}

	// Handles
	{
		w, err := bindgen.NewFileWriter(outputs, HandlesOutput)
		if err != nil {
			panic(err.Error())
		}

		WriteHandleInterface(w)

		for _, decl := range ctx.SortedHandles() {
			w.Write("\n")
			decl.Write(w)
			WriteHandleImpls(w, decl)
		}

		_ = w.Close()
	}

	// Structs
	{
		w, err := bindgen.NewFileWriter(outputs, StructsOutput)
		if err != nil {
			panic(err.Error())
		}

		for _, decl := range ctx.SortedStructs() {
			w.Write("\n")
			decl.Write(w)

			if structureType, ok := ctx.structDefaults[decl.Name]; ok {
				parts := []string{fmt.Sprintf("s_type: %d as vulkan::StructureType", structureType)}

				for _, field := range decl.Fields {
					if slices.Contains(field.Attributes, "required") || ctx.zeroable(field.Type) {
						continue
					}

					parts = append(parts, fmt.Sprintf("%s: %s", field.Name, ctx.defaultExpr(field.Type, 0)))
				}

				w.Write("\n")
				w.Write("impl %s {\n", decl.Name)
				w.Write("    const DEFAULT: Self = Self { %s };\n", strings.Join(parts, ", "))
				w.Write("}\n")
			}
		}

		_ = w.Close()
	}

	// Aliases
	{
		w, err := bindgen.NewFileWriter(outputs, AliasesOutput)
		if err != nil {
			panic(err.Error())
		}

		WriteAliases(w, aliases)
		_ = w.Close()
	}

	// Stubs
	{
		w, err := bindgen.NewFileWriter(outputs, StubsOutput)
		if err != nil {
			panic(err.Error())
		}

		WriteStubs(w, aliases)
		_ = w.Close()
	}

	// Api
	{
		w, err := bindgen.NewFileWriter(outputs, ApiOutput)
		if err != nil {
			panic(err.Error())
		}

		ctx.WriteApi(w, aliases)
		_ = w.Close()
	}

	// Api method wrappers
	{
		w, err := bindgen.NewFileWriter(outputs, WrappersOutput)
		if err != nil {
			panic(err.Error())
		}

		ctx.WriteApiMethodWrappers(w, aliases)
		_ = w.Close()
	}

	// Api instance command loading
	{
		w, err := bindgen.NewFileWriter(outputs, LoadInstanceOutput)
		if err != nil {
			panic(err.Error())
		}

		ctx.WriteApiStubCommands(w, levelInstance, "stub_instance_commands", aliases)
		ctx.WriteApiLoadInstance(w, aliases)
		_ = w.Close()
	}

	// Api device command loading
	{
		w, err := bindgen.NewFileWriter(outputs, LoadDeviceOutput)
		if err != nil {
			panic(err.Error())
		}

		ctx.WriteApiStubCommands(w, levelDevice, "stub_device_commands", aliases)
		ctx.WriteApiLoadDevice(w, aliases)
		_ = w.Close()
	}
}
