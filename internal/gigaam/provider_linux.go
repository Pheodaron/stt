//go:build linux

package gigaam

/*
#cgo LDFLAGS: -lonnxruntime -ldl
#include "provider_linux.h"
*/
import "C"

import (
	"fmt"
	"unsafe"
)

func checkCUDA() error {
	var message [1024]byte
	buffer := (*C.char)(unsafe.Pointer(&message[0]))

	if C.stt_check_cuda(buffer, C.size_t(len(message))) != 0 {
		return fmt.Errorf("CUDA requested: %s", C.GoString(buffer))
	}

	return nil
}
