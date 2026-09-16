package persistent

import (
	"os"
	"reflect"
)

type mmapDriver struct {
	mmapBytes         func(file *os.File, size int) (bool, []byte, error)
	closeMapped       func(data []byte) error
	flushMapped       func(data []byte) error
	closeMappedByAddr func(addr uintptr, size int) error
	flushMappedByAddr func(addr uintptr, size int) error
}

var defaultMmapOps = mmapDriver{
	mmapBytes:         mmapBytes,
	closeMapped:       closeMapped,
	flushMapped:       flushMapped,
	closeMappedByAddr: closeMappedByAddr,
	flushMappedByAddr: flushMappedByAddr,
}

var mmapOps = defaultMmapOps

func slicePtr(data []byte) uintptr {
	if len(data) == 0 {
		return 0
	}
	return reflect.ValueOf(data).Pointer()
}
