# Vulkan
[Fireball](https://github.com/fireball-lang/fireball) library and generator for Vulkan.

## Features
- Generates an `Api` struct with function references instead of global state, loaded in three stages (`load` -> `set_instance` -> `set_device`).
- Parses enums into strongly typed, distinct enums instead of untyped integers (`VK_IMAGE_LAYOUT_GENERAL` -> `ImageLayout::General`).
- Handles are typed structs sharing a `Handle` interface (`VkDevice` -> `Device`).
- Struct fields and command parameters are converted to snake case with prefixes stripped (`pNext` -> `next`, `pAllocateInfo` -> `allocate_info`).
- Every struct gets a `DEFAULT` constant with the required `s_type` field filled in (`ApplicationInfo::DEFAULT`).
- Remaining constants are generated as top-level typed constants (`VK_WHOLE_SIZE` -> `WHOLE_SIZE: u64`).
- Only includes core and cross-vendor extensions (`KHR` and `EXT`).
- Every command starts as a panic stub and is swapped out when its level is loaded, so missing commands fail loudly.

## Usage
```fireball
var vk = vulkan::Api::load().expect("failed to load libvulkan");

// create the instance
var instance_info = vulkan::InstanceCreateInfo::DEFAULT with { ... };
var instance: vulkan::Instance;

vk.create_instance(&instance_info, null, &instance);
vk.set_instance(instance);

// create the device
var device_info = vulkan::DeviceCreateInfo::DEFAULT with { ... };
var device: vulkan::Device;

vk.create_device(physical_device, &device_info, null, &device);
vk.set_device(device);
```
