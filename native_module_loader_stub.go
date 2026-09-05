//go:build !cgo

package kvlite

func openNativeModuleDriver(path string, module Module, options DriverOptions) (Engine, error) {
	_ = path
	_ = module
	_ = options
	return nil, ErrDriverNotLoaded
}
