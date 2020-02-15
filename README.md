# cachePool

#### 工作中经常需要一个 key value 的 缓存，并且需要经常修改value的值，所以value必须是一个指针，如果按正常的做法
就是map[key]*struct{xxx},每次make一个value对象，如果这个map比较大，
1. 造成内存碎片化
2. 造成gc扫描的时间过长，
3. map 读写过多，锁竞争过大，效率低。
#### 为了避免以上这几个问题，要做到以下:
1. map 的key 和 value都不包含指针，避免gc 扫描， bigcache 就是map[int]int方式避免gc扫描。
	验证[slice 和 map 数据类型不同，gc 扫描时间也不同](https://github.com/jursonmo/articles/blob/master/record/go/performent/slice_map_gc.md)
    如果value包含了指针, 用 uintptr 类型代替，并且保证指针指向的内存在cachepool运行期间不会被gc 回收
2. map的操作需要读写锁的保护，但是频繁读写map，竞争过大，效率过低，可以使用shardMap 方式减小锁的竞争
3. 预先分配一个大的对象池，每次分配对象时，不用make，直接从对象池里去，用完再放回去。
   关键是要实现一个效率高的、可伸缩的对象池；
    3.1 slots 快速找到一个可用的对象
	3.2 spinLock 自旋锁(需要时间的验证和考验,并且小心使用)
	3.3 或者用 环形缓存区ring 来记录对象位置。
    3.4 如果内存不够，自动新建一个pool

all: no pointer in key or value, or value's pointer will not be gc when cachePool is working
     1. slots or ringslots record the free buffer position
     2. map[key]positionID ,positionID indicate the buffer position, so can get the value
