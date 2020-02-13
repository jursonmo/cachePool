// +build ver2

package cachePool

import (
	"fmt"
	"sync/atomic"
	"unsafe"
)

type ringEntryPosition2 struct {
	putRace uint64 //should use percpu var
	_       CachePad
	getRace uint64
	_       CachePad
	head    uint32
	_       CachePad
	tail    uint32
	putPos  []uint32
	getPos  []uint32
	ring    []EntryHeader //entryheader have no pointer, no scan
}

func (r *ringEntryPosition2) InitPosition(buffer []byte, poolIndex, cap, entrySize int) error {
	if !IsPowerOfTwo(cap) {
		return fmt.Errorf("cap must IsPowerOfTwo")
	}
	r.ring = make([]EntryHeader, cap)
	for i := 0; i < cap; i++ {
		eh := (*EntryHeader)(unsafe.Pointer(&buffer[i*entrySize]))
		eh.poolId = uint32(poolIndex)
		eh.entryId = uint32(i * entrySize)
		// in buffer , eh.nextFree is unused, but in ring entryheader, eh.nextFree is used to available or unavailable
		r.PutEntry(eh)
	}
	fmt.Printf("poolId:%d, initRingPosition:%s", poolIndex, r)
	return nil
}

func (r *ringEntryPosition2) PutEntry(eh *EntryHeader) bool {
	cap := uint32(len(r.ring) - 1)
	for {
		tail := atomic.LoadUint32(&r.tail) //maybe something happend between acquire tail and head
		head := atomic.LoadUint32(&r.head)

		/*由于acquire tail 和 head 是两个操作，所以无法确定现在ring 是否满的，即n==cap有两种情况
		1. ring 真的满了，这时应该return
		2. ring 没满，只是在读tail 后，其他线程递增了head 和 tail，这时本线程再读head时，这个head是一个最新值
		   ，而 tail 是一个旧的值，所以 head-tail 会比实际的要大，即这时n==cap 的情况实际没满，需要try again

		all: 当n>=cap时，无法确定是return 还是 try again
		*/
		n := head - tail
		if n >= cap {
			continue
		}
		if !atomic.CompareAndSwapUint32(&r.head, head, head+1) {
			r.incPutRace()
			continue
		}
		index := int(head & (cap - 1))
		for {
			if atomic.LoadUint32(&r.ring[index].nextFree) == Unavailable {
				eh.nextFree = Unavailable
				atomic.StoreUint32(&r.ring[index].nextFree, Available) // get goroutine will check it is available
				return true
			}
			r.incPutRace()
		}
	}
	return false
}

func (r *ringEntryPosition2) GetEntryHeader() EntryHeader {
	ret := InvalidEntryHeader
	// var eh *EntryHeader
	// ret := InvalidEntryHeader
	// cap := uint32(len(r.ring))
	// for {
	// 	head := atomic.LoadUint32(&r.head)
	// 	tail := atomic.LoadUint32(&r.tail)

	// }
	return ret
}

func (r *ringEntryPosition2) incPutRace() {
	atomic.AddUint64(&r.putRace, 1)
}
func (r *ringEntryPosition2) incGetRace() {
	atomic.AddUint64(&r.getRace, 1)
}
