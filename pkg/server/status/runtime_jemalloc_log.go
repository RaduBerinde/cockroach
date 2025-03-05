//go:build !stdmalloc

package status

import "runtime/cgo"

// #include <stdlib.h>
import "C"

//export goPrintJemallocLog
func goPrintJemallocLog(handle C.uintptr_t, str *C.char) {
	h := cgo.Handle(handle)
	printFn := h.Value().(func(string))
	printFn(C.GoString(str))
}
