// +build !ver2

package main

import (
	"fmt"
	"sync/atomic"
	"unsafe"
)

const (
	Available   = Invalid //ring entry
	Unavailable = 0
	mask        = 1<<32 - 1
)

type ringEntryPosition struct {
	putRace  uint64 //should use percpu var
	_        CachePad
	getRace  uint64
	_        CachePad
	headtail uint64
	ring     []EntryHeader //entryheader have no pointer, no scan
}

func (r *ringEntryPosition) InitPosition(buffer []byte, poolIndex, cap, entrySize int) error {
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

func unpack(headtail uint64) (uint32, uint32) {
	//return uint32((headtail >> 32)&mask), uint32(headtail&mask)
	return uint32(headtail >> 32), uint32(headtail)
}

func (r *ringEntryPosition) String() string {
	//b := bytes.NewBuffer(make([]byte, 128))//todo: use strings.Builder
	headtail := atomic.LoadUint64(&r.headtail)
	head, tail := unpack(headtail)
	n := head - tail
	s := fmt.Sprintf("ring, headtail:%d, tail:%d, head:%d, n:%d, ringSize:%d, putRace:%d, getRace:%d\n",
		headtail, tail, head, n, len(r.ring), atomic.LoadUint64(&r.putRace), atomic.LoadUint64(&r.getRace))
	if n > uint32(len(r.ring)) {
		return s + "fail\n"
	}
	mask := uint32(len(r.ring) - 1)
	for i := uint32(0); i < n; i++ {
		index := (tail + i) & mask
		s += fmt.Sprintf("i:%d, poolId:%d, entryId:%d, a:%v\n", i,
			r.ring[index].poolId, r.ring[index].entryId, r.ring[index].nextFree == Available)
	}
	return s
}

/*

一般head 和 tail 分开读写，这样更好读写线程的冲突，效率更好，但是要小心：
```
head 和 tail 都是uint32
put 线程1：								    put 线程2：
 atomic get head
					--------->          atomic increase head
									    atomic increase tail

 atomic get tail
 这时读到tail已经走在head前面去了

 all： 也就是tail比head大，有两种情况：
	  1. 先读head再读tail，
	  2. head 到了最大环绕了
所以，go-queue库的 put 线程先读了tail 再读head， get 线程先读head再读tail
```
*/

//把head 和 tail 放在一个atomic 操作里，可保证没有问题，但是性能会差些, 因为读写线程都需要操作一个uint64 headtail
//把head 和 tail 放在一个atomic 操作里，可满足从head pop 即head值减小的情况，比如 1.13 sync.pool getSlow()的实现
func (r *ringEntryPosition) PutEntry(eh *EntryHeader) bool {
	cap := uint32(len(r.ring))
	n, index := uint32(0), uint32(0)
	// in for loop, shouldn't be sched
	procPin()
	defer procUnpin()
	for {
		headtail := atomic.LoadUint64(&r.headtail)
		head, tail := unpack(headtail)

		n = head - tail
		if n == cap {
			return false
		}
		if n > cap {
			fmt.Printf("headtail=%d, head=%d, tail=%d, n=%d, cap=%d\n", headtail, head, tail, n, cap)
			panic("int(n) > cap")
		}
		newheadtail := uint64(head+1)<<32 | uint64(tail)
		if !atomic.CompareAndSwapUint64(&r.headtail, headtail, newheadtail) {
			r.incPutRace()
			continue
		}

		index = head & (cap - 1)
		for {
			if atomic.LoadUint32(&r.ring[index].nextFree) == Unavailable {
				eh.nextFree = Unavailable //set eh to unavailable. 如果没有设置它，可能
				r.ring[index] = *eh       //eh.nextFree == 0（Unavailable）, so get goroutine still can't get it
				//如果没有设置eh.nextFree = 0 ，可能此时get goroutine 就可以读取到entryHeader，并且设置nextFree=0,
				//然后继续这里的流程执行atomic.StoreUint32(&p.ring[index].nextFree, head+1)，此slot 将永远不可Put
				//要保证此put函数执行完，get goroutine 才能看到此slot 可用
				atomic.StoreUint32(&r.ring[index].nextFree, Available) // get goroutine will check it is available
				return true
			}
			r.incPutRace()
		}
	}
}

func (r *ringEntryPosition) GetEntryHeader() EntryHeader {
	var eh *EntryHeader
	ret := InvalidEntryHeader
	cap := uint32(len(r.ring))
	for {
		headtail := atomic.LoadUint64(&r.headtail)
		head, tail := unpack(headtail)

		if head == tail {
			return ret
		}
		newheadtail := uint64(head)<<32 | uint64(tail+1)
		if atomic.CompareAndSwapUint64(&r.headtail, headtail, newheadtail) {
			eh = &r.ring[tail&(cap-1)]
			for {
				if atomic.LoadUint32(&eh.nextFree) == Available {
					ret = *eh
					atomic.StoreUint32(&eh.nextFree, Unavailable)
					return ret
				}
				r.incGetRace()
			}
		}
		r.incGetRace()
	}
	return ret
}

func (r *ringEntryPosition) incPutRace() {
	atomic.AddUint64(&r.putRace, 1)
}
func (r *ringEntryPosition) incGetRace() {
	atomic.AddUint64(&r.getRace, 1)
}
