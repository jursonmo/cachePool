package cachePool

import (
	"fmt"
	"sync/atomic"
	"unsafe"
)

type slotsPosition struct {
	SpinLock
	//idleCount uint32
	idleSlot uint32
	slots    []EntryHeader //entryheader have no pointer, no scan
}

func (s *slotsPosition) InitPosition(buffer []byte, poolIndex, cap, entrySize int) error {
	s.slots = make([]EntryHeader, cap)
	s.idleSlot = Invalid
	/*
		for i := 0; i < len(p.slots); i++ {
			p.Put(uint32(i))
			p.slots[i].poolId = uint32(p.index)
			p.slots[i].entryId = uint32(i * entrySize)
			e := (*Entry)(unsafe.Pointer(&p.buffer[i*entrySize]))
			e.poolId = p.slots[i].poolId
			e.entryId = p.slots[i].entryId
			e.nextFree = uint32(i) //buffer's EntryHeader's nextFree correspond slot index
		}
	*/
	for i := len(s.slots) - 1; i >= 0; i-- {
		if ok := s.Put(uint32(i)); !ok {
			return fmt.Errorf("put id:%d fail", i)
		}
		s.slots[i].poolId = uint32(poolIndex)
		s.slots[i].entryId = uint32(i * entrySize)
		e := (*Entry)(unsafe.Pointer(&buffer[i*entrySize]))
		e.poolId = s.slots[i].poolId
		e.entryId = s.slots[i].entryId
		e.nextFree = uint32(i) //buffer's EntryHeader's nextFree correspond slot index
	}

	fmt.Println(s)
	return nil
}

func (s *slotsPosition) String() string {
	ss := fmt.Sprintf("----Slots, idleSlot=%d-----\n", s.idleSlot)
	for i, _ := range s.slots {
		ss += fmt.Sprintf("%s\n", &s.slots[i])
	}
	return ss
}

func (s *slotsPosition) invalid(id uint32) bool {
	return id&Invalid != 0 || id >= uint32(len(s.slots))
}

//put the free slot in p.slots
func (s *slotsPosition) Put(id uint32) bool {
	if s.invalid(id) {
		return false
	}

	s.Lock()
	//here is a problem , atomic not in happen-before, so atomic does't mean p.idleCount read the newest value
	// if p.idleCount == p.size {
	// 	p.Unlock()
	// 	return false
	// }

	//p.slots[id].nextFree = p.idleSlot //i can't sure about that it get the newest vlaue,可能从寄存器里读
	//atomic.StoreUint32(&p.slots[id].nextFree, p.idleSlot)
	atomic.StoreUint32(&s.slots[id].nextFree, atomic.LoadUint32(&s.idleSlot))
	//p.idleSlot = id
	atomic.StoreUint32(&s.idleSlot, id)
	// p.idleCount++
	s.Unlock()
	return true
}

func (s *slotsPosition) Get() uint32 {
	s.Lock()
	// if p.idleCount == 0 {
	// 	p.Unlock()
	// 	return Invalid
	// }

	//id := p.idleSlot
	id := atomic.LoadUint32(&s.idleSlot)
	if s.invalid(id) {
		s.Unlock()
		return id
	}

	//p.idleSlot = p.slots[id].nextFree
	//atomic.StoreUint32(&p.idleSlot, p.slots[id].nextFree)
	atomic.StoreUint32(&s.idleSlot, atomic.LoadUint32(&s.slots[id].nextFree))
	// p.idleCount--
	s.Unlock()
	return id
}

//here Entry is pool buffer'Entry
func (s *slotsPosition) PutEntryHeader(e *EntryHeader) bool {
	e.nextFree &= IdMask
	return s.Put(e.nextFree)
}

func (s *slotsPosition) GetEntryHeader() EntryHeader {
	id := s.Get()
	//fmt.Println("id===========", id)
	if s.invalid(id) {
		fmt.Printf("slot index=%d invalid\n", id)
		return InvalidEntryHeader
	}
	return s.slots[int(id)]
}

// func (p *Pool) GetEntry() *Entry {
// 	id := p.Get()
// 	if p.invalid(id) {
// 		return nil
// 	}
// 	eid := int(p.slots[int(id)].entryId)
// 	if eid >= len(p.buffer) {
// 		return nil
// 	}
// 	return (*Entry)(unsafe.Pointer(&p.buffer[eid]))
// }
