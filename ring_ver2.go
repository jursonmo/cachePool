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

func (r *ringEntryPosition2) PutEntryHeader(eh *EntryHeader) bool {
	cap := uint32(len(r.ring) - 1)
	for {
		tail := atomic.LoadUint32(&r.tail) //maybe something happend between acquire tail and head
		head := atomic.LoadUint32(&r.head)

		/*由于acquire tail 和 head 是两个操作，所以无法确定现在ring 是否满的，即n==cap有两种情况
		1. ring 真的满了，这时应该return
		2. ring 没满，只是在读tail 后，其他线程递增了head 和 tail，这时本线程再读head时，这个head是一个最新值
		   ，而 tail 是一个旧的值，所以 head-tail 会比实际的要大，即这时n==cap 的情况实际没满，需要try again

		all: 当n>=cap时，无法确定是return 还是 try again

		这里的应用跟disruptor 不一样，disruptor消费者 线程只要读不到就一直循环读，不会退出，生产者写入数据后，消费者
		可以立即拿到数据并处理完，处理完后又一直循环读，即为了生产者的数据得到快速、低延迟的处理。
		而这里的需求是内存池的需求，即内存池里对象可以用就一定要用上，而不能保证别的线程把内存对象放到这个池里，也就是当
		发现内存池没有对象可用时，不能一直循环等内存池有对象为止。
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
