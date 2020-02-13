package cachePool

import (
	"sync/atomic"
	_ "unsafe"
)

/*
//this is from allenxuxu
func (l *SpinLock) Lock() {
	for !atomic.CompareAndSwapUintptr(&l.lock, 0, 1) {
		runtime.Gosched()
	}
}

func (l *SpinLock) Unlock() {
	atomic.StoreUintptr(&l.lock, 0)
}
*/

type SpinLock struct {
	lock uint32
}

//go:linkname procPin runtime.procPin
func procPin() int

//go:linkname procUnpin runtime.procUnpin
func procUnpin()

func NewSpinLock() *SpinLock {
	return &SpinLock{}
}

/*
if current goroutine Lock() and then schedule away, next goroutine may be loop
dead in Lock(), until the first goroutine running on another P and Unlock(), so
1. make sure between Lokc() and Unlock(), current goroutine don't be scheduled out
	by using procPin(). eg. check in sync/atomic/value.go
2. like allenxuxu spinlock, use runtime.Gosched()
*/
func (l *SpinLock) Lock() {
	procPin()
	for {
		//procPin()
		if atomic.CompareAndSwapUint32(&l.lock, 0, 1) {
			return
		}
		//procUnpin()
	}
}

//Unlock() must be after Lock(), or will painc
func (l *SpinLock) Unlock() {
	//n := 0
	for {
		if atomic.CompareAndSwapUint32(&l.lock, 1, 0) {
			procUnpin()
			return
		}
		panic("spinlock unlock fail")

		// n++
		// if n > 100 {
		// 	panic("spinlock unlock fail")
		// }
	}
}
