// Package projectfs coordinates project-directory readers and writers inside
// one Cronova process. Uploads swap complete directory trees while schedulers
// hold a read lock during per-attempt staging.
package projectfs

import (
	"path/filepath"
	"sync"
)

var roots sync.Map // canonical root path -> *sync.RWMutex

// Lock returns the process-wide lock for root.
func Lock(root string) *sync.RWMutex {
	key, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		key = filepath.Clean(root)
	}
	v, _ := roots.LoadOrStore(key, &sync.RWMutex{})
	return v.(*sync.RWMutex)
}
