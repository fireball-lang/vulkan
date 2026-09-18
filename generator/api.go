package main

import (
	"strings"

	"github.com/fireball-lang/bindgen"
	"github.com/fireball-lang/bindgen/fb"
)

func aliasFuncName(alias *fb.Alias) string {
	return CheckName(bindgen.CamelToSnakeCase(strings.TrimPrefix(alias.Name, "Vk")))
}

// aliasLinkName returns the C symbol name of a command (e.g. VkCreateInstance
// -> vkCreateInstance).
func aliasLinkName(alias *fb.Alias) string {
	return "vk" + strings.TrimPrefix(alias.Name, "Vk")
}

func (ctx *GenContext) WriteApi(w fb.Writer, aliases []*fb.Alias) {
	// Api
	fields := make([]*fb.Field, 0, 2+len(aliases))

	fields = append(fields, &fb.Field{
		Name: "instance",
		Type: ctx.resolveType("VkInstance"),
	})

	fields = append(fields, &fb.Field{
		Name: "device",
		Type: ctx.resolveType("VkDevice"),
	})

	for _, alias := range aliases {
		fields = append(fields, &fb.Field{
			Public: true,
			Name:   "ref_" + aliasFuncName(alias),
			Type:   &fb.DeclType{Decl: alias},
		})
	}

	api := &fb.Struct{
		OutputIndex: ApiOutput,
		Name:        "Api",
		Fields:      fields,
		Layout:      fb.Fireball,
	}

	w.Write("\n")
	w.Write("import std::c;\n")
	w.Write("import std::os;\n")

	w.Write("\n")
	api.Write(w)

	ctx.WriteApiAccessors(w)
	ctx.WriteApiOpenLibrary(w)
	ctx.WriteApiStubCommands(w, levelGlobal, "stub_global_commands", aliases)
	ctx.WriteApiLoad(w, aliases)
	ctx.WriteApiSetInstance(w)
	ctx.WriteApiSetDevice(w)
}

// WriteApiAccessors writes the getters of the private Api handle fields.
func (ctx *GenContext) WriteApiAccessors(w fb.Writer) {
	w.Write("\n")
	w.Write("impl vulkan::Api {\n")
	w.Write("    pub func instance(self) vulkan::Instance {\n")
	w.Write("        return self.instance;\n")
	w.Write("    }\n")
	w.Write("\n")
	w.Write("    pub func device(self) vulkan::Device {\n")
	w.Write("        return self.device;\n")
	w.Write("    }\n")
	w.Write("}\n")
}

func (ctx *GenContext) WriteApiOpenLibrary(w fb.Writer) {
	// Windows
	w.Write("\n")
	w.Write("#[cfg(target_os = \"windows\")]\n")
	w.Write("func open_library() ?os::Library {\n")
	w.Write("    return os::Library::open(\"vulkan-1\");\n")
	w.Write("}\n")

	// Linux
	w.Write("\n")
	w.Write("#[cfg(target_os = \"linux\")]\n")
	w.Write("func open_library() ?os::Library {\n")
	w.Write("    var library = os::Library::open(\"libvulkan.so.1\");\n")
	w.Write("\n")
	w.Write("    if (library.is_none()) {\n")
	w.Write("        library = os::Library::open(\"libvulkan.so\");\n")
	w.Write("    }\n")
	w.Write("\n")
	w.Write("    return library;\n")
	w.Write("}\n")

	// macOS
	w.Write("\n")
	w.Write("#[cfg(target_os = \"macos\")]\n")
	w.Write("func open_library() ?os::Library {\n")
	w.Write("    var library = os::Library::open(\"libvulkan.dylib\");\n")
	w.Write("\n")
	w.Write("    if (library.is_none()) {\n")
	w.Write("        library = os::Library::open(\"libMoltenVK.dylib\");\n")
	w.Write("    }\n")
	w.Write("\n")
	w.Write("    return library;\n")
	w.Write("}\n")

	// Fallback for other platforms
	w.Write("\n")
	w.Write("#[cfg(not(any(target_os = \"windows\", target_os = \"linux\", target_os = \"macos\")))]\n")
	w.Write("func open_library() ?os::Library {\n")
	w.Write("    return os::Library::open(\"vulkan\");\n")
	w.Write("}\n")
}

// WriteApiStubCommands writes the function that reverts every command of a
// level back to its panic stub.
func (ctx *GenContext) WriteApiStubCommands(w fb.Writer, level commandLevel, name string, aliases []*fb.Alias) {
	w.Write("\n")
	w.Write("impl vulkan::Api {\n")
	w.Write("    func %s(mut self) {\n", name)

	for _, alias := range aliases {
		if ctx.commandLevels[alias.Name] != level {
			continue
		}

		funcName := aliasFuncName(alias)
		w.Write("        self.ref_%s = vulkan::stub_%s;\n", funcName, funcName)
	}

	w.Write("    }\n")
	w.Write("}\n")
}

func (ctx *GenContext) WriteApiLoad(w fb.Writer, aliases []*fb.Alias) {
	w.Write("\n")
	w.Write("impl vulkan::Api {\n")
	w.Write("    /// Loads the Vulkan library and returns a new Api with the global\n")
	w.Write("    /// level commands resolved. Returns none if no library could be\n")
	w.Write("    /// opened or the loader does not provide vkGetInstanceProcAddr.\n")
	w.Write("    ///\n")
	w.Write("    /// Instance and device level commands are resolved later through\n")
	w.Write("    /// set_instance and set_device.\n")
	w.Write("    pub func load() ?&Api {\n")
	w.Write("        var api = c::malloc(sizeof(Self)) as mut &Self;\n")
	w.Write("\n")
	w.Write("        api.instance = vulkan::Instance::from_raw(0);\n")
	w.Write("        api.device = vulkan::Device::from_raw(0);\n")
	w.Write("\n")
	w.Write("        api.stub_global_commands();\n")
	w.Write("        api.stub_instance_commands();\n")
	w.Write("        api.stub_device_commands();\n")
	w.Write("\n")
	w.Write("        var library = open_library()?;\n")
	w.Write("\n")
	w.Write("        var symbol = library.get(\"vkGetInstanceProcAddr\")?;\n")
	w.Write("        api.ref_get_instance_proc_addr = symbol as vulkan::GetInstanceProcAddr;\n")
	w.Write("\n")
	w.Write("        var ptr: mut *void;\n")

	for _, alias := range aliases {
		if ctx.commandLevels[alias.Name] != levelGlobal {
			continue
		}

		name := aliasFuncName(alias)

		w.Write("\n")
		w.Write("        ptr = api.ref_get_instance_proc_addr(vulkan::Instance::from_raw(0), \"%s\".ptr);\n", aliasLinkName(alias))
		w.Write("        if (ptr != null) api.ref_%s = ptr as vulkan::%s;\n", name, alias.Name)
	}

	w.Write("\n")
	w.Write("        return api;\n")
	w.Write("    }\n")
	w.Write("}\n")
}

func (ctx *GenContext) WriteApiSetInstance(w fb.Writer) {
	w.Write("\n")
	w.Write("impl vulkan::Api {\n")
	w.Write("    /// Sets the instance and (re)loads all instance level commands.\n")
	w.Write("    /// A previously set device is cleared and its commands revert to\n")
	w.Write("    /// stubs.\n")
	w.Write("    pub func set_instance(mut self, instance: vulkan::Instance) {\n")
	w.Write("        if (!instance.valid()) panic(\"cannot set an invalid instance\");\n")
	w.Write("        if (self.instance == instance) return;\n")
	w.Write("\n")
	w.Write("        if (self.device.valid()) {\n")
	w.Write("            self.device = vulkan::Device::from_raw(0);\n")
	w.Write("            self.stub_device_commands();\n")
	w.Write("        }\n")
	w.Write("\n")
	w.Write("        if (self.instance.valid()) self.stub_instance_commands();\n")
	w.Write("\n")
	w.Write("        self.instance = instance;\n")
	w.Write("        self.load_instance_commands(instance);\n")
	w.Write("    }\n")
	w.Write("}\n")
}

func (ctx *GenContext) WriteApiSetDevice(w fb.Writer) {
	w.Write("\n")
	w.Write("impl vulkan::Api {\n")
	w.Write("    /// Sets the device and (re)loads all device level commands.\n")
	w.Write("    pub func set_device(mut self, device: vulkan::Device) {\n")
	w.Write("        if (!device.valid()) panic(\"cannot set an invalid device\");\n")
	w.Write("        if (!self.instance.valid()) panic(\"set_instance must be called before set_device\");\n")
	w.Write("        if (self.device == device) return;\n")
	w.Write("\n")
	w.Write("        if (self.device.valid()) self.stub_device_commands();\n")
	w.Write("\n")
	w.Write("        self.device = device;\n")
	w.Write("        self.load_device_commands(device);\n")
	w.Write("    }\n")
	w.Write("}\n")
}

func (ctx *GenContext) WriteApiMethodWrappers(w fb.Writer, aliases []*fb.Alias) {
	w.Write("\n")
	w.Write("impl vulkan::Api {\n")

	i := 0

	for _, alias := range aliases {
		if i > 0 {
			w.Write("\n")
		}

		i++

		// Signature
		typ := alias.Type.(*fb.FuncType)
		name := aliasFuncName(alias)

		w.Write("    pub ")
		returns := fb.WriteSignature(w, name, fb.Immutable, typ.Params, typ.Returns)

		// Body
		w.Write(" {\n")

		if returns {
			w.Write("        return ")
		} else {
			w.Write("        ")
		}

		w.Write("self.ref_%s(", name)

		for i, param := range typ.Params {
			if i > 0 {
				w.Write(", ")
			}

			w.Write("%s", param.Name)
		}

		w.Write(");\n")
		w.Write("    }\n")
	}

	w.Write("}\n")
}

func (ctx *GenContext) WriteApiLoadInstance(w fb.Writer, aliases []*fb.Alias) {
	w.Write("\n")
	w.Write("impl vulkan::Api {\n")
	w.Write("    func load_instance_commands(mut self, instance: vulkan::Instance) {\n")
	w.Write("        var ptr: mut *void;\n")

	for _, alias := range aliases {
		if ctx.commandLevels[alias.Name] != levelInstance {
			continue
		}

		name := aliasFuncName(alias)

		w.Write("\n")
		w.Write("        ptr = self.ref_get_instance_proc_addr(instance, \"%s\".ptr);\n", aliasLinkName(alias))
		w.Write("        if (ptr != null) self.ref_%s = ptr as vulkan::%s;\n", name, alias.Name)
	}

	w.Write("    }\n")
	w.Write("}\n")
}

func (ctx *GenContext) WriteApiLoadDevice(w fb.Writer, aliases []*fb.Alias) {
	w.Write("\n")
	w.Write("impl vulkan::Api {\n")
	w.Write("    func load_device_commands(mut self, device: vulkan::Device) {\n")
	w.Write("        var ptr: mut *void;\n")

	for _, alias := range aliases {
		if ctx.commandLevels[alias.Name] != levelDevice {
			continue
		}

		name := aliasFuncName(alias)

		w.Write("\n")
		w.Write("        ptr = self.ref_get_device_proc_addr(device, \"%s\".ptr);\n", aliasLinkName(alias))
		w.Write("        if (ptr == null) ptr = self.ref_get_instance_proc_addr(self.instance, \"%s\".ptr);\n", aliasLinkName(alias))
		w.Write("        if (ptr != null) self.ref_%s = ptr as vulkan::%s;\n", name, alias.Name)
	}

	w.Write("    }\n")
	w.Write("}\n")
}
