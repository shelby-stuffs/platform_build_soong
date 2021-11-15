// Copyright 2021 Google Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package android

import (
	"android/soong/bazel"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"strings"

	"github.com/google/blueprint"
	"github.com/google/blueprint/proptools"
)

// Properties contains common module properties for Bazel migration purposes.
type properties struct {
	// In USE_BAZEL_ANALYSIS=1 mode, this represents the Bazel target replacing
	// this Soong module.
	Bazel_module bazel.BazelModuleProperties
}

type namespacedVariableProperties map[string]interface{}

// BazelModuleBase contains the property structs with metadata for modules which can be converted to
// Bazel.
type BazelModuleBase struct {
	bazelProperties properties

	// namespacedVariableProperties is used for soong_config_module_type support
	// in bp2build. Soong config modules allow users to set module properties
	// based on custom product variables defined in Android.bp files. These
	// variables are namespaced to prevent clobbering, especially when set from
	// Makefiles.
	namespacedVariableProperties namespacedVariableProperties

	// baseModuleType is set when this module was created from a module type
	// defined by a soong_config_module_type. Every soong_config_module_type
	// "wraps" another module type, e.g. a soong_config_module_type can wrap a
	// cc_defaults to a custom_cc_defaults, or cc_binary to a custom_cc_binary.
	// This baseModuleType is set to the wrapped module type.
	baseModuleType string
}

// Bazelable is specifies the interface for modules that can be converted to Bazel.
type Bazelable interface {
	bazelProps() *properties
	HasHandcraftedLabel() bool
	HandcraftedLabel() string
	GetBazelLabel(ctx BazelConversionPathContext, module blueprint.Module) string
	ConvertWithBp2build(ctx BazelConversionContext) bool
	convertWithBp2build(ctx BazelConversionContext, module blueprint.Module) bool
	GetBazelBuildFileContents(c Config, path, name string) (string, error)

	// For namespaced config variable support
	namespacedVariableProps() namespacedVariableProperties
	setNamespacedVariableProps(props namespacedVariableProperties)
	BaseModuleType() string
	SetBaseModuleType(string)
}

// BazelModule is a lightweight wrapper interface around Module for Bazel-convertible modules.
type BazelModule interface {
	Module
	Bazelable
}

// InitBazelModule is a wrapper function that decorates a BazelModule with Bazel-conversion
// properties.
func InitBazelModule(module BazelModule) {
	module.AddProperties(module.bazelProps())
}

// bazelProps returns the Bazel properties for the given BazelModuleBase.
func (b *BazelModuleBase) bazelProps() *properties {
	return &b.bazelProperties
}

func (b *BazelModuleBase) namespacedVariableProps() namespacedVariableProperties {
	return b.namespacedVariableProperties
}

func (b *BazelModuleBase) setNamespacedVariableProps(props namespacedVariableProperties) {
	b.namespacedVariableProperties = props
}

func (b *BazelModuleBase) BaseModuleType() string {
	return b.baseModuleType
}

func (b *BazelModuleBase) SetBaseModuleType(baseModuleType string) {
	b.baseModuleType = baseModuleType
}

// HasHandcraftedLabel returns whether this module has a handcrafted Bazel label.
func (b *BazelModuleBase) HasHandcraftedLabel() bool {
	return b.bazelProperties.Bazel_module.Label != nil
}

// HandcraftedLabel returns the handcrafted label for this module, or empty string if there is none
func (b *BazelModuleBase) HandcraftedLabel() string {
	return proptools.String(b.bazelProperties.Bazel_module.Label)
}

// GetBazelLabel returns the Bazel label for the given BazelModuleBase.
func (b *BazelModuleBase) GetBazelLabel(ctx BazelConversionPathContext, module blueprint.Module) string {
	if b.HasHandcraftedLabel() {
		return b.HandcraftedLabel()
	}
	if b.ConvertWithBp2build(ctx) {
		return bp2buildModuleLabel(ctx, module)
	}
	return "" // no label for unconverted module
}

// Configuration to decide if modules in a directory should default to true/false for bp2build_available
type Bp2BuildConfig map[string]BazelConversionConfigEntry
type BazelConversionConfigEntry int

const (
	// A sentinel value to be used as a key in Bp2BuildConfig for modules with
	// no package path. This is also the module dir for top level Android.bp
	// modules.
	BP2BUILD_TOPLEVEL = "."

	// iota + 1 ensures that the int value is not 0 when used in the Bp2buildAllowlist map,
	// which can also mean that the key doesn't exist in a lookup.

	// all modules in this package and subpackages default to bp2build_available: true.
	// allows modules to opt-out.
	Bp2BuildDefaultTrueRecursively BazelConversionConfigEntry = iota + 1

	// all modules in this package (not recursively) default to bp2build_available: true.
	// allows modules to opt-out.
	Bp2BuildDefaultTrue

	// all modules in this package (not recursively) default to bp2build_available: false.
	// allows modules to opt-in.
	Bp2BuildDefaultFalse
)

var (
	// Keep any existing BUILD files (and do not generate new BUILD files) for these directories
	// in the synthetic Bazel workspace.
	bp2buildKeepExistingBuildFile = map[string]bool{
		// This is actually build/bazel/build.BAZEL symlinked to ./BUILD
		".":/*recursive = */ false,

		// build/bazel/examples/apex/... BUILD files should be generated, so
		// build/bazel is not recursive. Instead list each subdirectory under
		// build/bazel explicitly.
		"build/bazel":/* recursive = */ false,
		"build/bazel/examples/android_app":/* recursive = */ true,
		"build/bazel/examples/java":/* recursive = */ true,
		"build/bazel/bazel_skylib":/* recursive = */ true,
		"build/bazel/rules":/* recursive = */ true,
		"build/bazel/rules_cc":/* recursive = */ true,
		"build/bazel/scripts":/* recursive = */ true,
		"build/bazel/tests":/* recursive = */ true,
		"build/bazel/platforms":/* recursive = */ true,
		"build/bazel/product_variables":/* recursive = */ true,
		"build/bazel_common_rules":/* recursive = */ true,
		"build/make/tools":/* recursive = */ true,
		"build/pesto":/* recursive = */ true,

		// external/bazelbuild-rules_android/... is needed by mixed builds, otherwise mixed builds analysis fails
		// e.g. ERROR: Analysis of target '@soong_injection//mixed_builds:buildroot' failed
		"external/bazelbuild-rules_android":/* recursive = */ true,
		"external/bazel-skylib":/* recursive = */ true,
		"external/guava":/* recursive = */ true,
		"external/error_prone":/* recursive = */ true,
		"external/jsr305":/* recursive = */ true,
		"frameworks/ex/common":/* recursive = */ true,

		"packages/apps/Music":/* recursive = */ true,
		"packages/apps/QuickSearchBox":/* recursive = */ true,
		"packages/apps/WallpaperPicker":/* recursive = */ false,

		"prebuilts/gcc":/* recursive = */ true,
		"prebuilts/sdk":/* recursive = */ false,
		"prebuilts/sdk/current/extras/app-toolkit":/* recursive = */ false,
		"prebuilts/sdk/current/support":/* recursive = */ false,
		"prebuilts/sdk/tools":/* recursive = */ false,
		"prebuilts/r8":/* recursive = */ false,
	}

	// Configure modules in these directories to enable bp2build_available: true or false by default.
	bp2buildDefaultConfig = Bp2BuildConfig{
		"bionic":                                             Bp2BuildDefaultTrueRecursively,
		"build/bazel/examples/apex/minimal":                  Bp2BuildDefaultTrueRecursively,
		"build/soong/cc/libbuildversion":                     Bp2BuildDefaultTrue, // Skip tests subdir
		"development/sdk":                                    Bp2BuildDefaultTrueRecursively,
		"external/arm-optimized-routines":                    Bp2BuildDefaultTrueRecursively,
		"external/boringssl":                                 Bp2BuildDefaultTrueRecursively,
		"external/brotli":                                    Bp2BuildDefaultTrue,
		"external/fmtlib":                                    Bp2BuildDefaultTrueRecursively,
		"external/google-benchmark":                          Bp2BuildDefaultTrueRecursively,
		"external/googletest/googletest":                     Bp2BuildDefaultTrueRecursively,
		"external/gwp_asan":                                  Bp2BuildDefaultTrueRecursively,
		"external/jemalloc_new":                              Bp2BuildDefaultTrueRecursively,
		"external/jsoncpp":                                   Bp2BuildDefaultTrueRecursively,
		"external/libcap":                                    Bp2BuildDefaultTrueRecursively,
		"external/libcxx":                                    Bp2BuildDefaultTrueRecursively,
		"external/libcxxabi":                                 Bp2BuildDefaultTrueRecursively,
		"external/lz4/lib":                                   Bp2BuildDefaultTrue,
		"external/mdnsresponder":                             Bp2BuildDefaultTrueRecursively,
		"external/minijail":                                  Bp2BuildDefaultTrueRecursively,
		"external/pcre":                                      Bp2BuildDefaultTrueRecursively,
		"external/protobuf":                                  Bp2BuildDefaultTrueRecursively,
		"external/python/six":                                Bp2BuildDefaultTrueRecursively,
		"external/scudo":                                     Bp2BuildDefaultTrueRecursively,
		"external/selinux/libselinux":                        Bp2BuildDefaultTrueRecursively,
		"external/zlib":                                      Bp2BuildDefaultTrueRecursively,
		"external/zstd":                                      Bp2BuildDefaultTrueRecursively,
		"frameworks/native/libs/adbd_auth":                   Bp2BuildDefaultTrueRecursively,
		"packages/modules/adb":                               Bp2BuildDefaultTrue,
		"packages/modules/adb/crypto":                        Bp2BuildDefaultTrueRecursively,
		"packages/modules/adb/libs":                          Bp2BuildDefaultTrueRecursively,
		"packages/modules/adb/pairing_auth":                  Bp2BuildDefaultTrueRecursively,
		"packages/modules/adb/pairing_connection":            Bp2BuildDefaultTrueRecursively,
		"packages/modules/adb/proto":                         Bp2BuildDefaultTrueRecursively,
		"packages/modules/adb/tls":                           Bp2BuildDefaultTrueRecursively,
		"prebuilts/clang/host/linux-x86":                     Bp2BuildDefaultTrueRecursively,
		"system/core/diagnose_usb":                           Bp2BuildDefaultTrueRecursively,
		"system/core/libasyncio":                             Bp2BuildDefaultTrue,
		"system/core/libcrypto_utils":                        Bp2BuildDefaultTrueRecursively,
		"system/core/libcutils":                              Bp2BuildDefaultTrueRecursively,
		"system/core/libpackagelistparser":                   Bp2BuildDefaultTrueRecursively,
		"system/core/libprocessgroup":                        Bp2BuildDefaultTrue,
		"system/core/libprocessgroup/cgrouprc":               Bp2BuildDefaultTrue,
		"system/core/libprocessgroup/cgrouprc_format":        Bp2BuildDefaultTrue,
		"system/core/property_service/libpropertyinfoparser": Bp2BuildDefaultTrueRecursively,
		"system/libbase":                                     Bp2BuildDefaultTrueRecursively,
		"system/libziparchive":                               Bp2BuildDefaultTrueRecursively,
		"system/logging/liblog":                              Bp2BuildDefaultTrueRecursively,
		"system/sepolicy/apex":                               Bp2BuildDefaultTrueRecursively,
		"system/timezone/apex":                               Bp2BuildDefaultTrueRecursively,
		"system/timezone/output_data":                        Bp2BuildDefaultTrueRecursively,
	}

	// Per-module denylist to always opt modules out of both bp2build and mixed builds.
	bp2buildModuleDoNotConvertList = []string{
		"libprotobuf-cpp-full", "libprotobuf-cpp-lite", // Unsupported product&vendor suffix. b/204811222 and b/204810610.

		"libc_malloc_debug", // depends on libunwindstack, which depends on unsupported module art_cc_library_statics

		"libbase_ndk", // http://b/186826477, fails to link libctscamera2_jni for device (required for CtsCameraTestCases)

		"libprotobuf-python",               // contains .proto sources
		"libprotobuf-internal-protos",      // we don't handle path property for fileegroups
		"libprotobuf-internal-python-srcs", // we don't handle path property for fileegroups

		"libseccomp_policy", // b/201094425: depends on func_to_syscall_nrs, which depends on py_binary, which is unsupported in mixed builds.
		"libfdtrack",        // depends on libunwindstack, which depends on unsupported module art_cc_library_statics

		"gwp_asan_crash_handler", // cc_library, ld.lld: error: undefined symbol: memset

		"brotli-fuzzer-corpus", // b/202015218: outputs are in location incompatible with bazel genrule handling.

		// b/203369847: multiple genrules in the same package creating the same file
		// //development/sdk/...
		"platform_tools_properties",
		"build_tools_source_properties",

		"libminijail", // b/202491296: Uses unsupported c_std property.
		"minijail0",   // depends on unconverted modules: libminijail
		"drop_privs",  // depends on unconverted modules: libminijail

		// Tests. Handle later.
		"libbionic_tests_headers_posix", // http://b/186024507, cc_library_static, sched.h, time.h not found
		"libjemalloc5_integrationtest",
		"libjemalloc5_stresstestlib",
		"libjemalloc5_unittest",

		// APEX support
		"com.android.runtime", // http://b/194746715, apex, depends on 'libc_malloc_debug'

		"libadb_crypto",                    // Depends on libadb_protos
		"libadb_crypto_static",             // Depends on libadb_protos_static
		"libadb_pairing_connection",        // Depends on libadb_protos
		"libadb_pairing_connection_static", // Depends on libadb_protos_static
		"libadb_pairing_server",            // Depends on libadb_protos
		"libadb_pairing_server_static",     // Depends on libadb_protos_static
		"libadbd",                          // Depends on libadbd_core
		"libadbd_core",                     // Depends on libadb_protos
		"libadbd_services",                 // Depends on libadb_protos

		"libadb_protos_static",         // b/200601772: Requires cc_library proto support
		"libadb_protos",                // b/200601772: Requires cc_library proto support
		"libapp_processes_protos_lite", // b/200601772: Requires cc_library proto support

		"libgtest_ndk_c++",      // b/201816222: Requires sdk_version support.
		"libgtest_main_ndk_c++", // b/201816222: Requires sdk_version support.

		"abb",                     // depends on unconverted modules: libadbd_core, libadbd_services, libcmd, libbinder, libutils, libselinux
		"adb",                     // depends on unconverted modules: bin2c_fastdeployagent, libadb_crypto, libadb_host, libadb_pairing_connection, libadb_protos, libandroidfw, libapp_processes_protos_full, libfastdeploy_host, libmdnssd, libopenscreen-discovery, libopenscreen-platform-impl, libusb, libutils, libziparchive, libzstd, AdbWinApi
		"adbd",                    // depends on unconverted modules: libadb_crypto, libadb_pairing_connection, libadb_protos, libadbd, libadbd_core, libapp_processes_protos_lite, libmdnssd, libzstd, libadbd_services, libcap, libminijail, libselinux
		"bionic_tests_zipalign",   // depends on unconverted modules: libziparchive, libutils
		"linker",                  // depends on unconverted modules: liblinker_debuggerd_stub, libdebuggerd_handler_fallback, libziparchive, liblinker_main, liblinker_malloc
		"linker_reloc_bench_main", // depends on unconverted modules: liblinker_reloc_bench_*
		"sefcontext_compile",      // depends on unconverted modules: libsepol
		"versioner",               // depends on unconverted modules: libclang_cxx_host, libLLVM_host

		"linkerconfig", // http://b/202876379 has arch-variant static_executable
		"mdnsd",        // http://b/202876379 has arch-variant static_executable

		"acvp_modulewrapper", // disabled for android x86/x86_64
	}

	// Per-module denylist of cc_library modules to only generate the static
	// variant if their shared variant isn't ready or buildable by Bazel.
	bp2buildCcLibraryStaticOnlyList = []string{
		"libjemalloc5", // http://b/188503688, cc_library, `target: { android: { enabled: false } }` for android targets.
	}

	// Per-module denylist to opt modules out of mixed builds. Such modules will
	// still be generated via bp2build.
	mixedBuildsDisabledList = []string{
		"libbrotli",                            // http://b/198585397, ld.lld: error: bionic/libc/arch-arm64/generic/bionic/memmove.S:95:(.text+0x10): relocation R_AARCH64_CONDBR19 out of range: -1404176 is not in [-1048576, 1048575]; references __memcpy
		"func_to_syscall_nrs",                  // http://b/200899432, bazel-built cc_genrule does not work in mixed build when it is a dependency of another soong module.
		"libseccomp_policy_app_zygote_sources", // http://b/200899432, bazel-built cc_genrule does not work in mixed build when it is a dependency of another soong module.
		"libseccomp_policy_app_sources",        // http://b/200899432, bazel-built cc_genrule does not work in mixed build when it is a dependency of another soong module.
		"libseccomp_policy_system_sources",     // http://b/200899432, bazel-built cc_genrule does not work in mixed build when it is a dependency of another soong module.
		"minijail_constants_json",              // http://b/200899432, bazel-built cc_genrule does not work in mixed build when it is a dependency of another soong module.
	}

	// Used for quicker lookups
	bp2buildModuleDoNotConvert  = map[string]bool{}
	bp2buildCcLibraryStaticOnly = map[string]bool{}
	mixedBuildsDisabled         = map[string]bool{}
)

func init() {
	for _, moduleName := range bp2buildModuleDoNotConvertList {
		bp2buildModuleDoNotConvert[moduleName] = true
	}

	for _, moduleName := range bp2buildCcLibraryStaticOnlyList {
		bp2buildCcLibraryStaticOnly[moduleName] = true
	}

	for _, moduleName := range mixedBuildsDisabledList {
		mixedBuildsDisabled[moduleName] = true
	}
}

func GenerateCcLibraryStaticOnly(moduleName string) bool {
	return bp2buildCcLibraryStaticOnly[moduleName]
}

func ShouldKeepExistingBuildFileForDir(dir string) bool {
	if _, ok := bp2buildKeepExistingBuildFile[dir]; ok {
		// Exact dir match
		return true
	}
	// Check if subtree match
	for prefix, recursive := range bp2buildKeepExistingBuildFile {
		if recursive {
			if strings.HasPrefix(dir, prefix+"/") {
				return true
			}
		}
	}
	// Default
	return false
}

// MixedBuildsEnabled checks that a module is ready to be replaced by a
// converted or handcrafted Bazel target.
func (b *BazelModuleBase) MixedBuildsEnabled(ctx ModuleContext) bool {
	if ctx.Os() == Windows {
		// Windows toolchains are not currently supported.
		return false
	}
	if !ctx.Config().BazelContext.BazelEnabled() {
		return false
	}
	if !convertedToBazel(ctx, ctx.Module()) {
		return false
	}

	if GenerateCcLibraryStaticOnly(ctx.Module().Name()) {
		// Don't use partially-converted cc_library targets in mixed builds,
		// since mixed builds would generally rely on both static and shared
		// variants of a cc_library.
		return false
	}
	return !mixedBuildsDisabled[ctx.Module().Name()]
}

// ConvertedToBazel returns whether this module has been converted (with bp2build or manually) to Bazel.
func convertedToBazel(ctx BazelConversionContext, module blueprint.Module) bool {
	b, ok := module.(Bazelable)
	if !ok {
		return false
	}
	return b.convertWithBp2build(ctx, module) || b.HasHandcraftedLabel()
}

// ConvertWithBp2build returns whether the given BazelModuleBase should be converted with bp2build.
func (b *BazelModuleBase) ConvertWithBp2build(ctx BazelConversionContext) bool {
	return b.convertWithBp2build(ctx, ctx.Module())
}

func (b *BazelModuleBase) convertWithBp2build(ctx BazelConversionContext, module blueprint.Module) bool {
	if bp2buildModuleDoNotConvert[module.Name()] {
		return false
	}

	// Ensure that the module type of this module has a bp2build converter. This
	// prevents mixed builds from using auto-converted modules just by matching
	// the package dir; it also has to have a bp2build mutator as well.
	if ctx.Config().bp2buildModuleTypeConfig[ctx.OtherModuleType(module)] == false {
		if b, ok := module.(Bazelable); ok && b.BaseModuleType() != "" {
			// For modules with custom types from soong_config_module_types,
			// check that their _base module type_ has a bp2build mutator.
			if ctx.Config().bp2buildModuleTypeConfig[b.BaseModuleType()] == false {
				return false
			}
		} else {
			return false
		}
	}

	packagePath := ctx.OtherModuleDir(module)
	config := ctx.Config().bp2buildPackageConfig

	// This is a tristate value: true, false, or unset.
	propValue := b.bazelProperties.Bazel_module.Bp2build_available
	if bp2buildDefaultTrueRecursively(packagePath, config) {
		// Allow modules to explicitly opt-out.
		return proptools.BoolDefault(propValue, true)
	}

	// Allow modules to explicitly opt-in.
	return proptools.BoolDefault(propValue, false)
}

// bp2buildDefaultTrueRecursively checks that the package contains a prefix from the
// set of package prefixes where all modules must be converted. That is, if the
// package is x/y/z, and the list contains either x, x/y, or x/y/z, this function will
// return true.
//
// However, if the package is x/y, and it matches a Bp2BuildDefaultFalse "x/y" entry
// exactly, this module will return false early.
//
// This function will also return false if the package doesn't match anything in
// the config.
func bp2buildDefaultTrueRecursively(packagePath string, config Bp2BuildConfig) bool {
	ret := false

	// Check if the package path has an exact match in the config.
	if config[packagePath] == Bp2BuildDefaultTrue || config[packagePath] == Bp2BuildDefaultTrueRecursively {
		return true
	} else if config[packagePath] == Bp2BuildDefaultFalse {
		return false
	}

	// If not, check for the config recursively.
	packagePrefix := ""
	// e.g. for x/y/z, iterate over x, x/y, then x/y/z, taking the final value from the allowlist.
	for _, part := range strings.Split(packagePath, "/") {
		packagePrefix += part
		if config[packagePrefix] == Bp2BuildDefaultTrueRecursively {
			// package contains this prefix and this prefix should convert all modules
			return true
		}
		// Continue to the next part of the package dir.
		packagePrefix += "/"
	}

	return ret
}

// GetBazelBuildFileContents returns the file contents of a hand-crafted BUILD file if available or
// an error if there are errors reading the file.
// TODO(b/181575318): currently we append the whole BUILD file, let's change that to do
// something more targeted based on the rule type and target.
func (b *BazelModuleBase) GetBazelBuildFileContents(c Config, path, name string) (string, error) {
	if !strings.Contains(b.HandcraftedLabel(), path) {
		return "", fmt.Errorf("%q not found in bazel_module.label %q", path, b.HandcraftedLabel())
	}
	name = filepath.Join(path, name)
	f, err := c.fs.Open(name)
	if err != nil {
		return "", err
	}
	defer f.Close()

	data, err := ioutil.ReadAll(f)
	if err != nil {
		return "", err
	}
	return string(data[:]), nil
}
