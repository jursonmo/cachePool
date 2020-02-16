package cachePool

/*
#### 工作中经常需要一个 key value 的 缓存，并且需要经常修改value的值，所以value必须是一个指针，如果按正常的做法
就是map[key]*struct{xxx},每次make一个value对象，如果这个map比较大，
1. 造成内存碎片化
2. 造成gc扫描的时间过长，
3. map 读写过多，锁竞争过大，效率低。
#### 为了避免以上这几个问题，要做到以下:
1. map 的key 和 value都不包含指针，避免gc 扫描， bigcache 就是map[int]int方式避免gc扫描。
	验证[slice 和 map 数据类型不同，gc 扫描时间也不同](https://github.com/jursonmo/articles/blob/master/record/go/performent/slice_map_gc.md)
2. map的操作需要读写锁的保护，但是频繁读写map，竞争过大，效率过低，可以使用shardMap 方式减小锁的竞争
3. 预先分配一个大的对象池，每次分配对象时，不用make，直接从对象池里去，用完再放回去。
   关键是要实现一个效率高的、可伸缩的对象池；
    3.1 slots 快速找到一个可用的对象
	3.2 spinLock 自旋锁(需要时间的验证和考验,并且小心使用)
	3.3 或者用 环形缓存区ring 来记录对象位置。

all: no pointer in key or value, or value's pointer will not be gc when cachePool is working
     1. slots or ringslots record the free buffer position
     2. map[key]positionID ,positionID indicate the buffer position, so can get the value
*/
import (
	"flag"
	"fmt"
	"sync"
	"unsafe"
)

const (
	MaxPoolSize = 1<<31 - 1
	IdMask      = 1<<31 - 1
	HigestBit   = 1 << 31 //entry used flag bit, idleSlot init value is HigestBit, int is
	UsedFlag    = HigestBit
	Invalid     = HigestBit
)

var InvalidEntryHeader EntryHeader

var useSlots bool

func init() {
	InvalidEntryHeader = EntryHeader{entryPosition: entryPosition{entryId: Invalid}}
	flag.BoolVar(&useSlots, "useSlots", true, "use slots or use ring for position")
}

type entryPosition struct {
	poolId  uint32 //uint8,后面才想到需要这个poolid, 其实可以把using flag 放到这里
	entryId uint32 //pool buffer index
}

type EntryHeader struct {
	//p        *Pool
	entryPosition
	/*
		1. nextFree's hihgest bit means using flag
		2. slotsPosition: buffer's EntryHeader's nextFree correspond slot index,
			but in slots[]EntryHeader,EntryHeader's nextFree means next free slot'index
		3. ringPosition: buffer's EntryHeader's nextFree only use hihgest bit ( means entry is used)
		   , ring EntryHeader's nextFree is for checking if available
	*/
	nextFree uint32 //golang have no union feature
}

//like list_head,  put the list_head on the first position of entry node
type Entry struct {
	EntryHeader //first member
	//user data Value
	Value
}

type Value struct {
	A, B, C int //如果包含指针，保证指针指向的对象不会被gc 回收
}

//Key must implement Hash() for shardMap
type Key struct {
	A, B, C int //尽量不要有指针，避免扫描
}

func (k *Key) Hash() int {
	return k.A
}

type CachePoolConf struct {
	poolNum    int //init pool number
	poolCap    int
	autoExtend bool
	maxPool    int
	shardSize  int
}

type cachePool struct {
	pools []*Pool
	sm    *poolShardMap
	sync.Mutex
	CachePoolConf
}

type EntryPositioner interface {
	String() string
	InitPosition(buffer []byte, poolIndex, cap, entrySize int) error
	PutEntry(*EntryHeader) bool
	GetEntryHeader() EntryHeader
}

type Pool struct {
	//sync.RWMutex
	index    int
	size     uint32 //entry num
	buffer   []byte
	useSlots bool

	positioner EntryPositioner
	//use slots for pool
	//slotsPosition

	//use ring for pool
	//ringEntryPosition
}

type poolShardMap struct {
	sync.RWMutex
	origSize  int
	shardSize int
	shardMask int
	maps      []map[Key]uint64
}

var offset uintptr
var entrySize int

func init() {
	e := Entry{}
	entrySize = int(unsafe.Sizeof(e))
	offset = unsafe.Offsetof(e.Value)
}

//for buffer' EntryHeader
func (e *Entry) String() string {
	return fmt.Sprintf("entry:pid=%d, entryId=%d, entry th=%d, used=%v", e.poolId, e.entryId, e.nextFree&IdMask, e.isUsed())
}
func (e *EntryHeader) isUsed() bool {
	return e.nextFree&UsedFlag != 0
}
func (e *EntryHeader) String() string {
	return fmt.Sprintf("entryheader:pid=%d, entryId=%d, nexfree slot=%d, valid=%v", e.poolId, e.entryId, e.nextFree&IdMask, !e.invalid())
}
func (e *EntryHeader) invalid() bool {
	return e.nextFree&Invalid != 0
}

type Option func(*CachePoolConf)

func OptionWithAutoExtend(b bool) Option {
	return func(c *CachePoolConf) {
		c.autoExtend = b
	}
}

func OptionWithMaxPool(n int) Option {
	return func(c *CachePoolConf) {
		c.maxPool = n
	}
}

func OptionWithShardSize(n int) Option {
	return func(c *CachePoolConf) {
		c.shardSize = n
	}
}

func (c *CachePoolConf) Check() error {
	if c.poolNum == 0 || c.poolCap == 0 || c.shardSize == 0 {
		return fmt.Errorf("poolNum or poolCap or shardSize eq 0")
	}
	return nil
}

func NewCachePool(poolNum, poolCap int, opts ...Option) (cp *cachePool, err error) {
	cp = new(cachePool)
	cp.poolCap = poolCap
	cp.poolNum = poolNum

	cp.autoExtend = true //default
	if cp.shardSize == 0 {
		cp.shardSize = cp.poolNum //default shardSize is eq init poolNum
	}

	for _, opt := range opts {
		opt(&cp.CachePoolConf)
	}
	err = cp.Check()
	if err != nil {
		return
	}

	cp.pools = make([]*Pool, cp.poolNum)
	for i := 0; i < len(cp.pools); i++ {
		cp.pools[i], err = NewPool(i, cp.poolCap)
		if err != nil {
			return
		}
	}

	cp.sm, err = NewShardMap(cp.shardSize)
	return
}

func (cp *cachePool) String() string {
	return fmt.Sprintf("poolNum:%d, poolcap:%d, shardMap size:%d", cp.GetPoolNum(), cp.poolCap, cp.sm.shardSize)
}

func (cp *cachePool) GetPoolNum() int {
	return len(cp.pools)
}

func (cp *cachePool) Capacity() int {
	capSum := 0
	for _, pool := range cp.pools {
		if pool != nil {
			capSum += int(pool.Cap())
		}
	}
	return capSum
}

func (cp *cachePool) GetPoolPositioner(i int) EntryPositioner {
	return cp.pools[i].positioner
}

func NewPool(index, cap int) (*Pool, error) {
	var err error
	if index < 0 || cap < 0 {
		return nil, fmt.Errorf("pool index or cap invalid")
	}
	if cap > MaxPoolSize {
		return nil, fmt.Errorf("MaxPoolSize is %d", MaxPoolSize)
	}

	p := &Pool{}
	p.index = index
	p.size = uint32(cap)
	p.buffer = make([]byte, cap*entrySize)
	p.useSlots = useSlots
	if p.useSlots {
		//p.positioner = &p.slotsPosition
		p.positioner = new(slotsPosition)
	} else {
		//p.positioner = &p.ringEntryPosition
		p.positioner = new(ringEntryPosition)
	}
	err = p.positioner.InitPosition(p.buffer, p.index, cap, entrySize)
	fmt.Println(p)
	p.showEntrys()
	return p, err
}

func (p *Pool) Cap() uint32 {
	return p.size
}

func (p *Pool) invalid(id uint32) bool {
	if uint32(len(p.buffer)) > id {
		return false
	}
	return true
}

func (p *Pool) String() string {
	return fmt.Sprintf("pool:index=%d, size=%d, entrySize=%d, bufferSize=%d, useSlots=%v", p.index, p.size, entrySize, len(p.buffer), p.useSlots)
}

func (p *Pool) showEntrys() {
	for i := 0; i < int(p.size); i++ {
		e := (*EntryHeader)(unsafe.Pointer(&p.buffer[i*entrySize]))
		fmt.Printf("i:=%d, %s\n", i, e)
	}
}

func GetEntryFromElem(v *Value) *Entry {
	return (*Entry)(unsafe.Pointer(uintptr(unsafe.Pointer(v)) - offset))
}

func GetElemID(v *Value) uint64 {
	e := GetEntryFromElem(v)
	return *(*uint64)(unsafe.Pointer(&e.EntryHeader))
}

func (cp *cachePool) PutValue(v *Value) {
	if v == nil {
		return
	}
	e := GetEntryFromElem(v)
	cp.PutEntry(e)
}

//here Entry is pool buffer'Entry
func (cp *cachePool) PutEntry(e *Entry) bool {
	index := int(e.poolId)
	if index >= len(cp.pools) {
		return false
	}
	//todo: 如果使用率过少，可以不用put 回去，当这个pool 使用率为0时，可以清除pool，让gc 回收

	//clean UsedFlag even if put fail
	e.nextFree &= (UsedFlag - 1)
	return cp.pools[index].PutEntry(e)
}

//Delete Value --> buffer entry --> putEntry()
func (p *Pool) PutEntry(e *Entry) bool {
	return p.positioner.PutEntry(&e.EntryHeader)
}

func (cp *cachePool) GetValue() *Value {
	var p *Pool
	poolNum := 0
	start := getPid()
	for {
		poolNum = len(cp.pools)
		for n := 0; n < poolNum; start++ {
			n++
			p = cp.pools[start%poolNum]
			if p == nil {
				continue
			}
			entry := p.GetEntry()
			fmt.Printf("process id:%d\n", start)
			if entry != nil {
				//return entry
				return &entry.Value
			}
		}
		if !cp.autoExtend {
			return nil
		}
		var err error
		cp.Lock()
		if poolNum < len(cp.pools) { //have apppend new pool
			start = len(cp.pools) - 1 //so start from last pool
			cp.Unlock()
			continue
		}
		if cp.maxPool != 0 && len(cp.pools) >= cp.maxPool {
			//log
			fmt.Printf("")
			cp.Unlock()
			return nil
		}
		p, err = NewPool(len(cp.pools), cp.poolCap)
		if err != nil {
			cp.Unlock()
			return nil
		}
		entry := p.GetEntry()
		cp.pools = append(cp.pools, p)
		cp.Unlock()
		//log
		fmt.Printf("add new pool,now cp:%s\n", cp)
		if entry == nil {
			panic("new pool, get entry must be successfull")
		}
		return &entry.Value
	}
	return nil
}

func (p *Pool) GetEntry() *Entry {
	eh := p.positioner.GetEntryHeader()
	if p.invalid(eh.entryId) {
		//log
		return nil
	}
	entry := (*Entry)(unsafe.Pointer(&p.buffer[int(eh.entryId)]))
	fmt.Println("GetValue:", entry)
	if entry.isUsed() {
		panic("GetEntry: entry have been used?")
	}
	entry.nextFree |= UsedFlag //means this entry of buffer has been used
	return entry
}

func NewShardMap(n int) (*poolShardMap, error) {
	if n == 0 {
		return nil, fmt.Errorf("Shards number must be > 0 ")
	}
	// if !IsPowerOfTwo(n) {
	// 	return nil, fmt.Errorf("Shards number must be power of two")
	// }
	sm := &poolShardMap{}
	sm.origSize = n
	sm.shardSize = CeilToPowerOfTwo(n)
	sm.shardMask = sm.shardSize - 1
	sm.maps = make([]map[Key]uint64, sm.shardSize)
	for i, _ := range sm.maps {
		sm.maps[i] = make(map[Key]uint64)
	}
	return sm, nil
}

func (cp *cachePool) Store(key Key, v *Value) {
	hash := key.Hash()
	m := cp.sm.maps[hash&cp.sm.shardMask]
	elemID := GetElemID(v)
	cp.sm.Lock()
	m[key] = elemID
	cp.sm.Unlock()
}

func (cp *cachePool) Load(t Key) *Value {
	hash := t.Hash()
	m := cp.sm.maps[hash&cp.sm.shardMask]
	cp.sm.RLock()
	elemID, ok := m[t]
	cp.sm.RUnlock()
	if !ok {
		return nil
	}
	return cp.getValueFromElemID(elemID)
}

func (cp *cachePool) Delete(key Key) {
	hash := key.Hash()
	m := cp.sm.maps[hash&cp.sm.shardMask]
	cp.sm.Lock()
	delete(m, key)
	cp.sm.Unlock()
	return

}

func (cp *cachePool) DeleteAndFreeValue(key Key) bool {
	hash := key.Hash()
	m := cp.sm.maps[hash&cp.sm.shardMask]
	cp.sm.Lock()
	elemID, ok := m[key]
	if ok {
		delete(m, key)
	}
	cp.sm.Unlock()
	if !ok {
		return false
	}
	e := cp.getEntryFromElemID(elemID)
	if e == nil {
		return false
	}
	return cp.PutEntry(e)
}

func (cp *cachePool) getValueFromElemID(elemID uint64) *Value {
	e := cp.getEntryFromElemID(elemID)
	if e == nil {
		return nil
	}
	return &e.Value
}

func (cp *cachePool) getEntryFromElemID(elemID uint64) *Entry {
	entryh := (*EntryHeader)(unsafe.Pointer(&elemID))
	if int(entryh.poolId) >= len(cp.pools) {
		// log
		return nil
	}
	pbuf := cp.pools[entryh.poolId].buffer
	if int(entryh.entryId) >= len(pbuf) {
		// log
		return nil
	}
	return (*Entry)(unsafe.Pointer(&pbuf[entryh.entryId]))
}

/*
func main() {
	flag.Parse()
	cp, err := NewCachePool(2, 4)
	if err != nil {
		return
	}
	key := Key{1, 2, 3}
	v := cp.GetValue()
	if v == nil {
		panic("v is nil")
	}
	e := GetEntryFromElem(v)
	fmt.Println(e)
	//dosomthing with v
	v.A = 3
	v.B = 2
	v.C = 1

	cp.Store(key, v)
	newv := cp.Load(key)
	if v != newv {
		fmt.Println(v, newv)
		panic("v != newv ")
	}

	fmt.Println("=========showing================")
	fmt.Println(cp.pools[0].positioner)
	ok := cp.DeleteAndFreeValue(key)
	if !ok {
		panic("DeleteAndFreeValue fail")
	}
	ok = cp.DeleteAndFreeValue(key)
	if ok {
		panic("DeleteAndFreeValue double delete fail")
	}

	fmt.Println(cp.pools[0].positioner)

	//测试缓存池自动扩展
	testExtend()
}

func testExtend() {
	fmt.Println("------ testExtend----------------")
	poolNum := 2
	poolCap := 2
	cp, err := NewCachePool(poolNum, poolCap)
	if err != nil {
		return
	}
	for i := 0; i < poolCap*poolNum; i++ {
		v := cp.GetValue()
		if v == nil {
			panic("v==nil")
		}
	}

	v := cp.GetValue() //make cp extend pool
	if v == nil {
		panic("v==nil")
	}
	n := cp.GetPoolNum()
	if n != poolNum+1 {
		panic("")
	}
	fmt.Println(cp)
}
*/
